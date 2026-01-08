package tests

import (
	"context"
	"errors"
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
)

func TestUpgrade(t *testing.T) {
	t.Parallel()

	_, skipDeletion := os.LookupEnv("TEST_SKIP_DELETION")
	ko, dyn := createKubectlOptionsAndDynamicClient(t, "cluster-test", !skipDeletion)

	if getClusterVersion(t, ko, dyn, "test-cluster") != "v1.35.0" {
		installCluster(t, ko, "test", false)
		checkAllSubApplicationsAreSynced(t, ko, dyn, ko.Namespace, "test", 6*30, 10*time.Second)
	}

	installClusterWithValues(t, ko, "test", []string{"values/cluster-upgrade.yaml"}, map[string]string{}, !skipDeletion)
	waitUntilClusterIsProvisioned(t, ko, dyn, "test-cluster", 6*10, 10*time.Second)
	checkAllSubApplicationsAreSynced(t, ko, dyn, ko.Namespace, "test", 6*30, 10*time.Second)

	tko, _, closer := createTenantKubectlOptionsAndDynamicClient(t, ko, "test-cluster")
	t.Cleanup(closer)
	tko.Namespace = "default"
	waitUntilNodesAreUpgraded(t, tko, "v1.35.0", 2, 2*15, 30*time.Second)
}

func getClusterVersion(t *testing.T, ko *k8s.KubectlOptions, dyn *dynamic.DynamicClient, clusterName string) string {
	backup, err := dyn.Resource(schema.GroupVersionResource{
		Resource: "kubeadmcontrolplanes",
		Group:    "controlplane.cluster.x-k8s.io",
		Version:  "v1beta2",
	}).Namespace(ko.Namespace).Get(context.Background(), clusterName, metav1.GetOptions{})
	if err != nil {
		return ""
	}

	version, found, err := unstructured.NestedString(backup.Object, "spec", "version")
	if err != nil {
		t.Fatalf("error getting spec.version: %v", err)
	}
	if !found {
		t.Fatal("error extracting spec.version: not found")
	}
	return version

}

func waitUntilClusterIsProvisioned(t *testing.T, ko *k8s.KubectlOptions, dyn *dynamic.DynamicClient, clusterName string, maxRetries int, sleepBetweenRetries time.Duration) {
	retry.DoWithRetry(t, "", maxRetries, sleepBetweenRetries, func() (string, error) {
		cluster, err := dyn.Resource(schema.GroupVersionResource{
			Resource: "clusters",
			Group:    "cluster.x-k8s.io",
			Version:  "v1beta2",
		}).Namespace(ko.Namespace).Get(context.Background(), clusterName, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("error getting cluster: %w", err)
		}

		phase, found, err := unstructured.NestedString(cluster.Object, "status", "phase")
		if err != nil {
			return "", fmt.Errorf("error getting status.phase: %w", err)
		}
		if !found {
			return "", fmt.Errorf("error extracting backup status: not found")
		}

		if phase != "Provisioned" {
			return "", fmt.Errorf("cluster %s is not 'Provisioned', use 'kubectl describe cluster %s' to debug", cluster.GetName(), cluster.GetName())
		}
		return "", nil
	})
}

func waitUntilNodesAreUpgraded(t *testing.T, ko *k8s.KubectlOptions, expectedVersion string, expectedNodeCount int, maxRetries int, sleepBetweenRetries time.Duration) {

	retry.DoWithRetry(t, "Wait until all nodes are upgraded", maxRetries, sleepBetweenRetries, func() (string, error) {
		nodes := k8s.GetNodes(t, ko)
		errs := make([]error, len(nodes))
		for i, node := range nodes {
			if node.Status.NodeInfo.KubeletVersion != expectedVersion {
				errs[i] = fmt.Errorf("expected node '%s' to have status.nodeInfo.kubeletVersion='%s', got %s", node.Name, expectedVersion, node.Status.NodeInfo.KubeletVersion)
			}
		}
		err := errors.Join(errs...)
		if err != nil {
			// See for explanation https://pkg.go.dev/errors#Join
			nonNilErrs := err.(interface{ Unwrap() []error }).Unwrap()
			return "", fmt.Errorf("expected all nodes to have status.nodeInfo.kubeletVersion='%s', got %d/%d: %v", expectedVersion, len(errs)-len(nonNilErrs), len(errs), err)
		}

		if len(nodes) != expectedNodeCount {
			return "", fmt.Errorf("expected cluster to have %d nodes, got %d", expectedNodeCount, len(nodes))
		}

		return "", nil
	})
}
