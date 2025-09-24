package tests

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/retry"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

func createKubectlOptionsAndDynamicClient(t *testing.T, namespace string) (*k8s.KubectlOptions, *dynamic.DynamicClient) {
	kubectlOptions := k8s.NewKubectlOptions("", "", namespace)
	kubeconfigPath, err := k8s.GetKubeConfigPathE(t)
	if err != nil {
		t.Fatalf("failed to get kubeconfig path: %v", err)
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		t.Fatalf("failed to load kubeconfig: %v", err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatalf("failed to create dynamic client: %v", err)
	}
	return kubectlOptions, dynClient
}

func createTenantKubectlOptionsAndDynamicClient(t *testing.T, ko *k8s.KubectlOptions, clusterName string) (*k8s.KubectlOptions, *dynamic.DynamicClient, func()) {
	tenantKubeconfigSecret := k8s.GetSecret(t, ko, fmt.Sprintf("%s-kubeconfig", clusterName))

	kubeconfigFile, err := os.CreateTemp(os.TempDir(), "tenant.*.conf")
	if err != nil {
		t.Fatalf("Expected no error creating kubeconfig temp file, got %v", err)
	}

	if err != nil {
		t.Fatalf("Expected no error opening temp kubeconfig file, got %v", err)
	}
	_, err = kubeconfigFile.Write(tenantKubeconfigSecret.Data["value"])
	if err != nil {
		t.Fatalf("Expected no error writing temp kubeconfig file, got %v", err)
	}
	err = kubeconfigFile.Close()
	if err != nil {
		t.Fatalf("Expected no error closing temp kubeconfig file, got %v", err)
	}
	tenantKO := k8s.NewKubectlOptions("", kubeconfigFile.Name(), "")

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigFile.Name())
	if err != nil {
		t.Fatalf("failed to load kubeconfig: %v", err)
	}

	tenantDyn, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatalf("failed to create dynamic client: %v", err)
	}
	return tenantKO, tenantDyn, func() {
		err := os.Remove(kubeconfigFile.Name())
		if err != nil {
			t.Errorf("Expected no error deleting temp kubeconfig, got %v", err)
		}
	}
}

func checkAllSubApplicationsAreSynced(t *testing.T, ko *k8s.KubectlOptions, dyn *dynamic.DynamicClient, namespace string, appOfAppsReleaseName string, maxRetries int, sleepBetweenRetries time.Duration) {
	retry.DoWithRetry(t, "Check all argo applications are ready", maxRetries, sleepBetweenRetries, func() (string, error) {
		// TODO Change label to instance because part-of is not unique.
		apps, err := dyn.Resource(schema.GroupVersionResource{
			Resource: "applications",
			Group:    "argoproj.io",
			Version:  "v1alpha1",
		}).Namespace("argocd").List(
			context.TODO(),
			metav1.ListOptions{
				LabelSelector: fmt.Sprintf(
					"app.kubernetes.io/part-of=%s-%s-cluster",
					namespace,
					appOfAppsReleaseName,
				),
			})

		if err != nil {
			return "", err
		}

		for _, app := range apps.Items {
			synced, found, err := unstructured.NestedString(app.Object, "status", "sync", "status")

			if err != nil {
				return "", err
			}
			if !found {
				return "", fmt.Errorf("error extracting application status: not found")
			}

			if synced != "Synced" {
				return "", fmt.Errorf("app %s not synced", app.GetName())
			}

			health, found, err := unstructured.NestedString(app.Object, "status", "health", "status")
			if err != nil {
				return "", err
			}
			if !found {
				return "", fmt.Errorf("error extracting application health status: not found")
			}
			if health != "Healthy" {
				return "", fmt.Errorf("app %s must be healthy", app.GetName())
			}
		}

		return "", err
	})
}
