package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/retry"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestCluster(t *testing.T) {
	kubectlOptions := k8s.NewKubectlOptions("", "", "test")

	namespace := "test"

	defer k8s.DeleteNamespace(t, kubectlOptions, namespace)
	k8s.CreateNamespace(t, kubectlOptions, namespace)

	releaseName := "test-pet-name"

	defer helm.Delete(t, &helm.Options{
		KubectlOptions: kubectlOptions,
	}, releaseName, true)

	helm.Install(t, &helm.Options{
		ValuesFiles:    []string{"values/cluster-test.yaml"},
		KubectlOptions: kubectlOptions,
		ExtraArgs: map[string][]string{
			"install": []string{"--wait"},
		},
	}, "../charts/cluster", releaseName)

	// This takes the kubeconfig path from the KUBECONFIG env var which means
	// before running this test you need to set KUBECONFIG with the path to the kubeconfig.
	kubeconfigPath, err := k8s.GetKubeConfigPathE(t)

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		t.Fatalf("failed to load kubeconfig: %v", err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatalf("failed to create dynamic client: %v", err)
	}

	// Check cluster status.infrastructureReady.
	clusterResource, err := dynClient.Resource(schema.GroupVersionResource{
		Resource: "clusters",
		Group:    "cluster.x-k8s.io",
		Version:  "v1beta1",
	}).Namespace(namespace).Get(context.TODO(), fmt.Sprintf("%s-cluster", releaseName), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get custom resource: %v", err)
	}

	infraReady, found, err := unstructured.NestedBool(clusterResource.Object, "status", "infrastructureReady")

	if err != nil {
		t.Fatalf("error extracting infrastructureReady: %v", err)
	}
	if !found {
		t.Fatalf("error extracting infrastructureReady: not found")
	}
	if !infraReady {
		t.Fatalf("infra is not ready")
	}

	// Expect the cluster to be fully ready within 30 minutes.
	// Because for some reason this takes ages now???
	retry.DoWithRetry(t, "Check all argo applications are ready", 6*30, 10*time.Second, func() (string, error) {
		// TODO Change label to instance because part-of is not unique.
		apps, err := dynClient.Resource(schema.GroupVersionResource{
			Resource: "applications",
			Group:    "argoproj.io",
			Version:  "v1alpha1",
		}).Namespace("argocd").List(context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("app.kubernetes.io/part-of=%s-%s-cluster", namespace, releaseName)})

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
				return "", fmt.Errorf("App %s not synced", app.GetName())
			}

			health, found, err := unstructured.NestedString(app.Object, "status", "health", "status")
			if err != nil {
				return "", err
			}
			if !found {
				return "", fmt.Errorf("error extracting application health status: not found")
			}
			if health != "Healthy" {
				return "", fmt.Errorf("App %s must be healthy", app.GetName())
			}
		}

		return "", err
	})
}

func checkArgoApplicationIsSynced(t *testing.T, client *dynamic.DynamicClient, appName string, timeout time.Duration) {
	start := time.Now()

	for time.Since(start) < timeout {
		app, err := client.Resource(schema.GroupVersionResource{
			Resource: "applications",
			Group:    "argoproj.io",
			Version:  "v1alpha1",
		}).Namespace("argocd").Get(context.TODO(), appName, metav1.GetOptions{})

		if err != nil {
			t.Fatalf("failed to get argo application: %v", err)
		}

		synced, found, err := unstructured.NestedString(app.Object, "status", "sync", "status")

		if err != nil {
			t.Fatalf("error extracting application status: %v", err)
		}
		if !found {
			t.Fatalf("error extracting application status: not found")
		}

		if synced == "Synced" {
			return
		}
	}

	t.Fatalf("Argo application %v failed to sync after %v", appName, timeout.String())
}
