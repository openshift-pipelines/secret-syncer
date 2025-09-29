package reconciler

import (
	"context"
	"os"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/kmeta"
	"knative.dev/pkg/logging"
	kueueversioned "sigs.k8s.io/kueue/client-go/clientset/versioned"
	kueueinformers "sigs.k8s.io/kueue/client-go/informers/externalversions"
)

const controllerName = "kueue-workload-controller"

func NewController() func(context.Context, configmap.Watcher) *controller.Impl {
	return func(ctx context.Context, cmw configmap.Watcher) *controller.Impl {
		logger := logging.FromContext(ctx)

		hubKubeClient, cfg, err := getKubeClientAndConfig()
		if err != nil {
			logger.Fatalf("Failed to create Kubernetes client: %v", err)
		}

		kueueClient, err := kueueversioned.NewForConfig(cfg)
		if err != nil {
			logger.Fatalf("Failed to create Kueue client: %v", err)
		}

		kueueNamespace := os.Getenv("KUEUE_NAMESPACE")
		if kueueNamespace == "" {
			kueueNamespace = "kueue-system" // Default to standard Kueue namespace
		}
		logger.Infof("Using Kueue namespace: %s", kueueNamespace)

		kueueInformer := kueueinformers.NewSharedInformerFactory(kueueClient, 0)
		workloadInformer := kueueInformer.Kueue().V1beta1().Workloads()

		r := &Reconciler{
			logger:         logger,
			hubKubeClient:  hubKubeClient,
			workloadLister: workloadInformer.Lister(),
			kueueClient:    kueueClient,
			kueueNamespace: kueueNamespace,
		}

		impl := controller.NewContext(ctx, r, controller.ControllerOptions{
			Logger:        logger,
			WorkQueueName: controllerName,
		})

		if _, err := workloadInformer.Informer().AddEventHandler(controller.HandleAll(checkOwnerAndEnqueue(impl))); err != nil {
			logger.Panicf("Couldn't register Workload informer event handler: %v", err)
		}

		// Start the informer factory
		go kueueInformer.Start(ctx.Done())

		return impl
	}
}

// checkOwnerAndEnqueue only enqueues workloads which have OwnerReference kind as PipelineRun
func checkOwnerAndEnqueue(impl *controller.Impl) func(obj any) {
	return func(obj any) {
		object, err := kmeta.DeletionHandlingAccessor(obj)
		if err == nil {
			// Check if the workload has a PipelineRun owner reference
			for _, owner := range object.GetOwnerReferences() {
				if owner.Kind == "PipelineRun" {
					impl.EnqueueKey(types.NamespacedName{
						Namespace: object.GetNamespace(),
						Name:      object.GetName(),
					})
					return
				}
			}
		}
	}
}

func getKubeClientAndConfig() (kubernetes.Interface, *rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// Fallback to kubeconfig file for local development
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = os.Getenv("HOME") + "/.kube/config"
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, nil, err
		}
	}

	hubKubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}

	return hubKubeClient, cfg, nil
}
