package reconciler

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"knative.dev/pkg/logging"

	// These are the clients for Kueue's API types
	kueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

// The label key and value to look for on Secrets that should be synced.
const (
	syncLabelKey   = "app.kubernetes.io/managed-by"
	syncLabelValue = "pipelinesascode.tekton.dev"
)

// SimpleReconciler is a standalone reconciler that doesn't use Knative injection
type SimpleReconciler struct {
	HubKubeClient  kubernetes.Interface
	HubKueueClient kueueclient.Interface
}

// Reconcile implements the controller.Reconciler interface
func (r *SimpleReconciler) Reconcile(ctx context.Context, key string) error {
	// Parse the key to get namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return fmt.Errorf("invalid resource key: %s", key)
	}

	logger := logging.FromContext(ctx).With("namespace", namespace, "name", name)

	// Get the secret from the API server
	secret, err := r.HubKubeClient.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// Secret was deleted from hub cluster
			logger.Info("Secret was deleted from hub cluster")
			return nil
		}
		return fmt.Errorf("failed to get secret: %w", err)
	}

	// Check if the secret has our sync label.
	labelValue, ok := secret.Labels[syncLabelKey]
	if !ok || labelValue != syncLabelValue {
		logger.Info("Secret does not have sync label, skipping")
		return nil // Not an error, just an event we don't care about.
	}

	logger.Info("Found secret with sync label, processing...")

	// Check if the secret is being deleted
	if IsBeingDeleted(secret) {
		logger.Info("Secret is being deleted, processing finalizer")

		// Only process deletion if our finalizer is present
		if HasSyncFinalizer(secret) {
			// Clean up mirrored secrets from spoke clusters
			if err := r.ProcessSecretDeletion(ctx, secret); err != nil {
				logger.Errorf("Failed to clean up mirrored secrets: %v", err)
				return err // This will requeue the event to retry later
			}

			// Remove our finalizer so the secret can be deleted
			if err := r.RemoveFinalizerFromSecret(ctx, secret); err != nil {
				logger.Errorf("Failed to remove finalizer: %v", err)
				return err
			}
		}

		return nil // Deletion processing complete
	}

	// Find the MultiKueueConfig to get the list of spoke clusters.
	mkConfigs, err := r.HubKueueClient.KueueV1beta1().MultiKueueConfigs().List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Errorf("Failed to list MultiKueueConfigs: %v", err)
		return err // Return the error to requeue the event.
	}
	if len(mkConfigs.Items) == 0 {
		logger.Info("No MultiKueueConfigs found, nothing to do.")
		return nil
	}
	targetClusters := mkConfigs.Items[0].Spec.Clusters

	// Sync the secret to each target cluster.
	for _, clusterName := range targetClusters {
		if err := r.syncSecretToCluster(ctx, secret, clusterName); err != nil {
			logger.Errorf("Failed to sync secret to cluster %s: %v", clusterName, err)
			// Returning an error will cause this secret to be re-processed later.
			return err
		}
	}

	// Add finalizer to the secret so we can clean up mirrored secrets when it's deleted
	if !HasSyncFinalizer(secret) {
		if err := r.AddFinalizerToSecret(ctx, secret); err != nil {
			logger.Errorf("Failed to add finalizer to secret: %v", err)
			// Don't return error here as the sync was successful
		}
	}

	return nil // Successfully processed.
}

// syncSecretToCluster connects to a spoke cluster and creates/updates the secret.
func (r *SimpleReconciler) syncSecretToCluster(ctx context.Context, secret *corev1.Secret, clusterName string) error {
	logger := logging.FromContext(ctx).With("cluster", clusterName, "secret", secret.Name)

	// 1. Get the spoke cluster's kubeconfig.
	spokeConfig, err := r.getSpokeClusterConfig(ctx, clusterName)
	if err != nil {
		return err
	}

	spokeClientset, err := kubernetes.NewForConfig(spokeConfig)
	if err != nil {
		return fmt.Errorf("could not create client for cluster %s: %w", clusterName, err)
	}

	// 3. Prepare the new secret object.
	secretToSync := secret.DeepCopy()
	secretToSync.ObjectMeta.UID = ""
	secretToSync.ObjectMeta.ResourceVersion = ""
	secretToSync.ObjectMeta.ManagedFields = nil
	secretToSync.ObjectMeta.OwnerReferences = nil
	secretToSync.ObjectMeta.DeletionTimestamp = nil

	// 4. Create or update the secret on the spoke.
	_, err = spokeClientset.CoreV1().Secrets(secret.Namespace).Get(ctx, secret.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Secret not found on spoke, creating...")
			createdSecret, createErr := spokeClientset.CoreV1().Secrets(secret.Namespace).Create(ctx, secretToSync, metav1.CreateOptions{})
			if createErr != nil {
				return fmt.Errorf("failed to create secret on spoke: %w", createErr)
			}
			logger.Infof("Successfully created secret %s/%s on spoke cluster %s", secret.Namespace, secret.Name, clusterName)
			logger.Infof("Created secret OwnerReferences: %+v", createdSecret.OwnerReferences)
			return nil
		} else {
			return fmt.Errorf("failed to get secret from spoke: %w", err)
		}
	}

	return nil
}

// getSpokeClusterConfig retrieves the REST config for a spoke cluster.
func (r *SimpleReconciler) getSpokeClusterConfig(ctx context.Context, clusterName string) (*rest.Config, error) {
	mkCluster, err := r.HubKueueClient.KueueV1beta1().MultiKueueClusters().Get(ctx, clusterName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not find MultiKueueCluster %s: %w", clusterName, err)
	}

	// The MultiKueueCluster API uses KubeConfig
	kubeConfig := mkCluster.Spec.KubeConfig

	// Handle different location types
	switch kubeConfig.LocationType {
	case "Secret":
		// Get the kubeconfig from a secret
		kubeconfigSecret, err := r.HubKubeClient.CoreV1().Secrets("kueue-system").Get(ctx, kubeConfig.Location, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("could not get kubeconfig secret %s/%s: %w", "kueue-system", kubeConfig.Location, err)
		}

		kubeconfigBytes, ok := kubeconfigSecret.Data["kubeconfig"]
		if !ok {
			return nil, fmt.Errorf("kubeconfig secret %s/%s is missing 'kubeconfig' data key", "kueue-system", kubeConfig.Location)
		}

		return clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	case "Path":
		// Load kubeconfig from file path
		return clientcmd.BuildConfigFromFlags("", kubeConfig.Location)
	default:
		return nil, fmt.Errorf("unsupported kubeconfig location type: %s", kubeConfig.LocationType)
	}
}

// SetupSecretInformer creates a secret informer without injection
func SetupSecretInformer(cfg *rest.Config, enqueueFunc func(interface{})) (informers.SharedInformerFactory, cache.SharedIndexInformer, error) {
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}

	informerFactory := informers.NewSharedInformerFactory(kubeClient, 0)
	secretInformer := informerFactory.Core().V1().Secrets().Informer()

	secretInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    enqueueFunc,
		UpdateFunc: func(old, new interface{}) { enqueueFunc(new) },
		DeleteFunc: enqueueFunc,
	})

	return informerFactory, secretInformer, nil
}
