package reconciler

import (
	"context"
	"fmt"
	"strings"
	"testing"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonfake "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"knative.dev/pkg/apis"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	kueuefake "sigs.k8s.io/kueue/client-go/clientset/versioned/fake"
)

const (
	testKueueNamespace = "kueue-system"
	testClusterName    = "test-cluster"
	testSecretName     = "test-kubeconfig-secret"
)

// validKubeConfigData returns a minimal valid kubeconfig for testing
func validKubeConfigData() []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://test-cluster.example.com:6443
    insecure-skip-tls-verify: true
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token
`)
}

// TestGetSpokeClusterConfig tests the getSpokeClusterConfig function with various scenarios
func TestGetSpokeClusterConfig(t *testing.T) {
	tests := []struct {
		name               string
		clusterName        string
		multiKueueClusters []runtime.Object
		secrets            []runtime.Object
		expectError        bool
		errorContains      string
		exactErrorMessage  string
		validateConfig     func(*testing.T, *rest.Config)
	}{
		{
			name:        "success with secret location type",
			clusterName: testClusterName,
			multiKueueClusters: []runtime.Object{
				&kueuev1beta1.MultiKueueCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: testClusterName,
					},
					Spec: kueuev1beta1.MultiKueueClusterSpec{
						KubeConfig: kueuev1beta1.KubeConfig{
							LocationType: kueuev1beta1.SecretLocationType,
							Location:     testSecretName,
						},
					},
				},
			},
			secrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testSecretName,
						Namespace: testKueueNamespace,
					},
					Data: map[string][]byte{
						"kubeconfig": validKubeConfigData(),
					},
				},
			},
			expectError: false,
			validateConfig: func(t *testing.T, config *rest.Config) {
				if config == nil {
					t.Fatal("expected non-nil config")
				}
				if config.Host != "https://test-cluster.example.com:6443" {
					t.Errorf("expected host 'https://test-cluster.example.com:6443', got: %s", config.Host)
				}
			},
		},
		{
			name:        "fail with path location type and no file",
			clusterName: testClusterName,
			multiKueueClusters: []runtime.Object{
				&kueuev1beta1.MultiKueueCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: testClusterName,
					},
					Spec: kueuev1beta1.MultiKueueClusterSpec{
						KubeConfig: kueuev1beta1.KubeConfig{
							LocationType: kueuev1beta1.PathLocationType,
							Location:     "", // Empty path will fail in test environment
						},
					},
				},
			},
			secrets:     []runtime.Object{},
			expectError: true, // Expected to fail without a real kubeconfig file
		},
		{
			name:               "fail when cluster not found",
			clusterName:        testClusterName,
			multiKueueClusters: []runtime.Object{},
			secrets:            []runtime.Object{},
			expectError:        true,
			errorContains:      fmt.Sprintf("could not find MultiKueueCluster %s:", testClusterName),
		},
		{
			name:        "fail when secret not found",
			clusterName: testClusterName,
			multiKueueClusters: []runtime.Object{
				&kueuev1beta1.MultiKueueCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: testClusterName,
					},
					Spec: kueuev1beta1.MultiKueueClusterSpec{
						KubeConfig: kueuev1beta1.KubeConfig{
							LocationType: kueuev1beta1.SecretLocationType,
							Location:     testSecretName,
						},
					},
				},
			},
			secrets:       []runtime.Object{},
			expectError:   true,
			errorContains: fmt.Sprintf("could not get kubeconfig secret %s/%s:", testKueueNamespace, testSecretName),
		},
		{
			name:        "fail when secret missing kubeconfig key",
			clusterName: testClusterName,
			multiKueueClusters: []runtime.Object{
				&kueuev1beta1.MultiKueueCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: testClusterName,
					},
					Spec: kueuev1beta1.MultiKueueClusterSpec{
						KubeConfig: kueuev1beta1.KubeConfig{
							LocationType: kueuev1beta1.SecretLocationType,
							Location:     testSecretName,
						},
					},
				},
			},
			secrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testSecretName,
						Namespace: testKueueNamespace,
					},
					Data: map[string][]byte{
						"wrong-key": []byte("some-data"),
					},
				},
			},
			expectError:       true,
			exactErrorMessage: fmt.Sprintf("kubeconfig secret %s/%s is missing 'kubeconfig' data key", testKueueNamespace, testSecretName),
		},
		{
			name:        "fail with unsupported location type",
			clusterName: testClusterName,
			multiKueueClusters: []runtime.Object{
				&kueuev1beta1.MultiKueueCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: testClusterName,
					},
					Spec: kueuev1beta1.MultiKueueClusterSpec{
						KubeConfig: kueuev1beta1.KubeConfig{
							LocationType: "UnsupportedType",
							Location:     "some-location",
						},
					},
				},
			},
			secrets:           []runtime.Object{},
			expectError:       true,
			exactErrorMessage: "unsupported kubeconfig location type: UnsupportedType",
		},
		{
			name:        "fail with invalid kubeconfig data",
			clusterName: testClusterName,
			multiKueueClusters: []runtime.Object{
				&kueuev1beta1.MultiKueueCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: testClusterName,
					},
					Spec: kueuev1beta1.MultiKueueClusterSpec{
						KubeConfig: kueuev1beta1.KubeConfig{
							LocationType: kueuev1beta1.SecretLocationType,
							Location:     testSecretName,
						},
					},
				},
			},
			secrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testSecretName,
						Namespace: testKueueNamespace,
					},
					Data: map[string][]byte{
						"kubeconfig": []byte("invalid-kubeconfig-data"),
					},
				},
			},
			expectError: true,
		},
		{
			name:        "success with multiple clusters - first cluster",
			clusterName: "cluster-1",
			multiKueueClusters: []runtime.Object{
				&kueuev1beta1.MultiKueueCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster-1",
					},
					Spec: kueuev1beta1.MultiKueueClusterSpec{
						KubeConfig: kueuev1beta1.KubeConfig{
							LocationType: kueuev1beta1.SecretLocationType,
							Location:     "kubeconfig-1",
						},
					},
				},
				&kueuev1beta1.MultiKueueCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster-2",
					},
					Spec: kueuev1beta1.MultiKueueClusterSpec{
						KubeConfig: kueuev1beta1.KubeConfig{
							LocationType: kueuev1beta1.SecretLocationType,
							Location:     "kubeconfig-2",
						},
					},
				},
			},
			secrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubeconfig-1",
						Namespace: testKueueNamespace,
					},
					Data: map[string][]byte{
						"kubeconfig": validKubeConfigData(),
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubeconfig-2",
						Namespace: testKueueNamespace,
					},
					Data: map[string][]byte{
						"kubeconfig": validKubeConfigData(),
					},
				},
			},
			expectError: false,
			validateConfig: func(t *testing.T, config *rest.Config) {
				if config == nil {
					t.Fatal("expected non-nil config")
				}
			},
		},
		{
			name:        "success with multiple clusters - second cluster",
			clusterName: "cluster-2",
			multiKueueClusters: []runtime.Object{
				&kueuev1beta1.MultiKueueCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster-1",
					},
					Spec: kueuev1beta1.MultiKueueClusterSpec{
						KubeConfig: kueuev1beta1.KubeConfig{
							LocationType: kueuev1beta1.SecretLocationType,
							Location:     "kubeconfig-1",
						},
					},
				},
				&kueuev1beta1.MultiKueueCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster-2",
					},
					Spec: kueuev1beta1.MultiKueueClusterSpec{
						KubeConfig: kueuev1beta1.KubeConfig{
							LocationType: kueuev1beta1.SecretLocationType,
							Location:     "kubeconfig-2",
						},
					},
				},
			},
			secrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubeconfig-1",
						Namespace: testKueueNamespace,
					},
					Data: map[string][]byte{
						"kubeconfig": validKubeConfigData(),
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubeconfig-2",
						Namespace: testKueueNamespace,
					},
					Data: map[string][]byte{
						"kubeconfig": validKubeConfigData(),
					},
				},
			},
			expectError: false,
			validateConfig: func(t *testing.T, config *rest.Config) {
				if config == nil {
					t.Fatal("expected non-nil config")
				}
			},
		},
		{
			name:        "fail with multiple clusters - non-existent cluster",
			clusterName: "non-existent-cluster",
			multiKueueClusters: []runtime.Object{
				&kueuev1beta1.MultiKueueCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster-1",
					},
					Spec: kueuev1beta1.MultiKueueClusterSpec{
						KubeConfig: kueuev1beta1.KubeConfig{
							LocationType: kueuev1beta1.SecretLocationType,
							Location:     "kubeconfig-1",
						},
					},
				},
			},
			secrets: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubeconfig-1",
						Namespace: testKueueNamespace,
					},
					Data: map[string][]byte{
						"kubeconfig": validKubeConfigData(),
					},
				},
			},
			expectError:   true,
			errorContains: "could not find MultiKueueCluster non-existent-cluster:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create fake clients with the objects
			fakeKueueClient := kueuefake.NewSimpleClientset(tt.multiKueueClusters...)
			fakeKubeClient := fake.NewSimpleClientset(tt.secrets...)

			// Create reconciler
			reconciler := &Reconciler{
				logger:         zap.NewNop().Sugar(),
				hubKubeClient:  fakeKubeClient,
				kueueClient:    fakeKueueClient,
				kueueNamespace: testKueueNamespace,
			}

			// Test getSpokeClusterConfig
			config, err := reconciler.getSpokeClusterConfig(ctx, tt.clusterName)

			// Validate error expectations
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.exactErrorMessage != "" {
					if err.Error() != tt.exactErrorMessage {
						t.Errorf("expected error %q, got: %v", tt.exactErrorMessage, err)
					}
				} else if tt.errorContains != "" {
					if !strings.Contains(err.Error(), tt.errorContains) {
						t.Errorf("expected error to contain %q, got: %v", tt.errorContains, err)
					}
				}
			} else if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}

			// Validate config if validation function is provided
			if tt.validateConfig != nil && config != nil {
				tt.validateConfig(t, config)
			}
		})
	}
}

func TestValidatePLRAndGetSecretName(t *testing.T) {
	pipelineRunName := "test-pipeline-run"
	pipelineRunNamespace := "test-namespace"
	secretName := "test-secret"
	_ = &v1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pipelineRunName,
			Namespace: pipelineRunNamespace,
			Annotations: map[string]string{
				gitAuthSecret: secretName,
			},
		},
	}
	tests := []struct {
		name                string
		plrName             string
		plrNamespace        string
		pipelineRun         *v1.PipelineRun
		isPrDone            bool
		expectedErrorString string
		expectedLogSnippets []string
		expectedSecretName  string
		expectedPLRName     string
	}{
		{
			name:                "pipeline run not found",
			plrName:             "test-pipeline-run",
			plrNamespace:        pipelineRunNamespace,
			expectedLogSnippets: []string{"PipelineRun test-namespace/test-pipeline-run is not created yet on spoke cluster test-cluster"},
		},
		{
			name:                "getting error when retrieving pipeline run",
			plrName:             "test-pipeline-run",
			plrNamespace:        pipelineRunNamespace,
			expectedErrorString: "pipelineruns.tekton.dev \"test-pipeline-run\" is forbidden: I don't want to return pipeline run, I am in bad mood",
		},
		{
			name:                "pipeline run is done",
			plrName:             "test-pipeline-run",
			plrNamespace:        pipelineRunNamespace,
			pipelineRun:         &v1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pipelineRunName,
					Namespace: pipelineRunNamespace,
				},
			},
			isPrDone:            true,
			expectedLogSnippets: []string{
				"retrieved PipelineRun test-namespace/test-pipeline-run successfully from spoke cluster test-cluster",
				"PipelineRun test-namespace/test-pipeline-run is done on spoke cluster test-cluster",
			},
		},
		{
			name:                "pipeline run doesn't have git auth secret annotation",
			plrName:             "test-pipeline-run",
			plrNamespace:        pipelineRunNamespace,
			pipelineRun:         &v1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pipelineRunName,
					Namespace: pipelineRunNamespace,
				},
			},
			expectedLogSnippets: []string{
				"retrieved PipelineRun test-namespace/test-pipeline-run successfully from spoke cluster test-cluster",
				"git auth secret not found for PipelineRun test-namespace/test-pipeline-run on spoke cluster test-cluster",
			},
		},
		{
			name:                "pipeline is good",
			plrName:             "test-pipeline-run",
			plrNamespace:        pipelineRunNamespace,
			pipelineRun:         &v1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pipelineRunName,
					Namespace: pipelineRunNamespace,
					Annotations: map[string]string{
						gitAuthSecret: secretName,
					},
				},
			},
			expectedLogSnippets: []string{
				"retrieved PipelineRun test-namespace/test-pipeline-run successfully from spoke cluster test-cluster",
				"PipelineRun test-namespace/test-pipeline-run has git auth secret test-secret",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			fakeKubeClient := fake.NewSimpleClientset()
			fakeKueueClient := kueuefake.NewSimpleClientset()
			var spokeTektonClient *tektonfake.Clientset
			
			if tt.pipelineRun != nil && tt.isPrDone {
				foo := &apis.Condition{
					Type:   apis.ConditionSucceeded,
					Status: corev1.ConditionTrue,
				}
				tt.pipelineRun.Status.SetCondition(foo)
			}

			if tt.pipelineRun != nil {
				spokeTektonClient = tektonfake.NewSimpleClientset(tt.pipelineRun)
			} else {
				spokeTektonClient = tektonfake.NewSimpleClientset()
			}
			observer, log := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			r := &Reconciler{
				logger:         logger,
				hubKubeClient:  fakeKubeClient,
				kueueClient:    fakeKueueClient,
				kueueNamespace: testKueueNamespace,
			}

			if tt.pipelineRun == nil && tt.expectedErrorString != "" {
				spokeTektonClient.PrependReactor("get", "pipelineruns", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					forbiddenError := errors.NewForbidden(schema.GroupResource{Group: "tekton.dev", Resource: "pipelineruns"}, tt.plrName, fmt.Errorf("I don't want to return pipeline run, I am in bad mood"))
					return true, nil, forbiddenError
				})
			}

			secretName, _, err := r.validatePLRAndGetSecretName(ctx, spokeTektonClient, tt.plrName, tt.plrNamespace, testClusterName)
			if tt.expectedErrorString != "" {
				assert.ErrorContains(t, err, tt.expectedErrorString)
				return
			}
		
			for _, logmsg := range tt.expectedLogSnippets {
				logmsg := log.FilterMessageSnippet(logmsg).TakeAll()
				assert.Assert(t, len(logmsg) > 0, "log messages", logmsg, log)
			}
			if tt.expectedSecretName != "" {
				assert.Equal(t, tt.expectedSecretName, secretName)
			}
			if tt.expectedPLRName != "" {
				assert.Equal(t, tt.expectedPLRName, pipelineRunName)
			}
		})
	}
}
