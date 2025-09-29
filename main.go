package main

import (
	"log"
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/signals"

	"github.com/openshift-pipelines/secret-syncer/pkg/reconciler"
	kueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

func main() {
	// Set up signal handling
	ctx := signals.NewContext()

	// Get the Kubernetes config
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// Fallback to kubeconfig file for local development
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = os.Getenv("HOME") + "/.kube/config"
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			log.Fatalf("Failed to get Kubernetes config: %v", err)
		}
	}

	// Set up basic logging without config dependency
	logLevel := "info"
	if envLevel := os.Getenv("LOG_LEVEL"); envLevel != "" {
		logLevel = envLevel
	}

	logger, _ := logging.NewLogger("", logLevel)
	ctx = logging.WithLogger(ctx, logger)

	// Create clients directly
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	kueueClient, err := kueueclient.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("Failed to create Kueue client: %v", err)
	}

	// Create a simple reconciler without injection
	r := &reconciler.SimpleReconciler{
		HubKubeClient:  kubeClient,
		HubKueueClient: kueueClient,
	}

	// Create controller
	impl := controller.NewContext(ctx, r, controller.ControllerOptions{
		WorkQueueName: "secret-sync",
		Logger:        logger,
	})

	// Set up secret informer manually
	informerFactory, _, err := reconciler.SetupSecretInformer(cfg, impl.Enqueue)
	if err != nil {
		log.Fatalf("Failed to setup secret informer: %v", err)
	}

	// Start informer factory
	logger.Info("Starting secret informer")
	informerFactory.Start(ctx.Done())

	// For stateless controller, we don't need to wait for full cache sync
	// We just start processing events as they come
	logger.Info("Starting secret controller")

	logger.Info("Starting controller")
	if err := impl.Run(ctx); err != nil {
		log.Fatalf("Controller failed: %v", err)
	}
}
