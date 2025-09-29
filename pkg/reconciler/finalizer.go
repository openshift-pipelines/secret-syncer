package reconciler

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"knative.dev/pkg/logging"
)

const (
	// SecretSyncFinalizer is the finalizer we add to secrets that are being synced
	SecretSyncFinalizer = "secret-syncer.zakisk.dev/finalizer"
)

// AddFinalizerToSecret adds our finalizer to the secret if it doesn't already exist
func (r *SimpleReconciler) AddFinalizerToSecret(ctx context.Context, secret *corev1.Secret) error {
	logger := logging.FromContext(ctx).With("namespace", secret.Namespace, "name", secret.Name)

	// Check if finalizer already exists
	for _, finalizer := range secret.Finalizers {
		if finalizer == SecretSyncFinalizer {
			logger.Debug("Finalizer already exists on secret")
			return nil
		}
	}

	// Add finalizer
	logger.Info("Adding finalizer to secret")
	secret.Finalizers = append(secret.Finalizers, SecretSyncFinalizer)

	_, err := r.HubKubeClient.CoreV1().Secrets(secret.Namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to add finalizer to secret: %w", err)
	}

	logger.Info("Successfully added finalizer to secret")
	return nil
}

// RemoveFinalizerFromSecret removes our finalizer from the secret
func (r *SimpleReconciler) RemoveFinalizerFromSecret(ctx context.Context, secret *corev1.Secret) error {
	logger := logging.FromContext(ctx).With("namespace", secret.Namespace, "name", secret.Name)

	// Find and remove our finalizer
	var newFinalizers []string
	found := false
	for _, finalizer := range secret.Finalizers {
		if finalizer == SecretSyncFinalizer {
			found = true
			continue // Skip adding this finalizer
		}
		newFinalizers = append(newFinalizers, finalizer)
	}

	if !found {
		logger.Debug("Finalizer not found on secret, nothing to remove")
		return nil
	}

	logger.Info("Removing finalizer from secret")
	secret.Finalizers = newFinalizers

	_, err := r.HubKubeClient.CoreV1().Secrets(secret.Namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to remove finalizer from secret: %w", err)
	}

	logger.Info("Successfully removed finalizer from secret")
	return nil
}

// ProcessSecretDeletion handles the cleanup of mirrored secrets from spoke clusters
func (r *SimpleReconciler) ProcessSecretDeletion(ctx context.Context, secret *corev1.Secret) error {
	logger := logging.FromContext(ctx).With("namespace", secret.Namespace, "name", secret.Name)
	logger.Info("Processing secret deletion - cleaning up mirrored secrets from spoke clusters")

	// Find the MultiKueueConfig to get the list of spoke clusters
	mkConfigs, err := r.HubKueueClient.KueueV1beta1().MultiKueueConfigs().List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Errorf("Failed to list MultiKueueConfigs during deletion: %v", err)
		return err
	}

	if len(mkConfigs.Items) == 0 {
		logger.Info("No MultiKueueConfigs found during deletion")
		return nil
	}

	targetClusters := mkConfigs.Items[0].Spec.Clusters

	// Delete the secret from each target cluster
	var lastError error
	deletedCount := 0
	for _, clusterName := range targetClusters {
		if err := r.deleteSecretFromCluster(ctx, secret, clusterName); err != nil {
			logger.Errorf("Failed to delete secret from cluster %s: %v", clusterName, err)
			lastError = err
			// Continue trying other clusters even if one fails
		} else {
			deletedCount++
		}
	}

	if lastError != nil {
		return fmt.Errorf("failed to delete secret from some clusters (deleted from %d/%d clusters): %w",
			deletedCount, len(targetClusters), lastError)
	}

	logger.Infof("Successfully deleted secret from all %d spoke clusters", len(targetClusters))
	return nil
}

// deleteSecretFromCluster deletes a secret from a specific spoke cluster
func (r *SimpleReconciler) deleteSecretFromCluster(ctx context.Context, secret *corev1.Secret, clusterName string) error {
	logger := logging.FromContext(ctx).With("cluster", clusterName, "secret", secret.Name)

	// Get the spoke cluster's kubeconfig
	spokeConfig, err := r.getSpokeClusterConfig(ctx, clusterName)
	if err != nil {
		return fmt.Errorf("failed to get spoke cluster config: %w", err)
	}

	spokeClientset, err := kubernetes.NewForConfig(spokeConfig)
	if err != nil {
		return fmt.Errorf("could not create client for cluster %s: %w", clusterName, err)
	}

	// Check if the secret exists on the spoke cluster
	_, err = spokeClientset.CoreV1().Secrets(secret.Namespace).Get(ctx, secret.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Secret not found on spoke cluster, already deleted or never existed")
			return nil
		}
		return fmt.Errorf("failed to check secret existence on spoke cluster: %w", err)
	}

	// Delete the secret from the spoke cluster
	logger.Info("Deleting secret from spoke cluster")
	err = spokeClientset.CoreV1().Secrets(secret.Namespace).Delete(ctx, secret.Name, metav1.DeleteOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Secret was already deleted from spoke cluster")
			return nil
		}
		return fmt.Errorf("failed to delete secret from spoke cluster: %w", err)
	}

	logger.Info("Successfully deleted secret from spoke cluster")
	return nil
}

// HasSyncFinalizer checks if the secret has our sync finalizer
func HasSyncFinalizer(secret *corev1.Secret) bool {
	for _, finalizer := range secret.Finalizers {
		if finalizer == SecretSyncFinalizer {
			return true
		}
	}
	return false
}

// IsBeingDeleted checks if the secret is being deleted (has DeletionTimestamp)
func IsBeingDeleted(secret *corev1.Secret) bool {
	return secret.DeletionTimestamp != nil
}
