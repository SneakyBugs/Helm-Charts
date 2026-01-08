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

func TestBackup(t *testing.T) {
	t.Parallel()

	_, skipDeletion := os.LookupEnv("TEST_SKIP_DELETION")
	ko, dyn := createKubectlOptionsAndDynamicClient(t, "cluster-test", !skipDeletion)

	installClusterWithSetValues(t, ko, "test", map[string]string{
		"features.backups": "true",
	}, !skipDeletion)

	objectStoreUserName := fmt.Sprintf("%s-test-cluster-backup-velero", ko.Namespace)
	bucketClaimName := fmt.Sprintf("%s-test-cluster-backup", ko.Namespace)
	radosNamespaceName := fmt.Sprintf("%s-test-cluster", ko.Namespace)
	if !skipDeletion {
		t.Cleanup(func() {
			radosNamespaceErr := dyn.Resource(schema.GroupVersionResource{
				Resource: "cephblockpoolradosnamespaces",
				Group:    "ceph.rook.io",
				Version:  "v1",
			}).Namespace("rook-ceph").Delete(context.Background(), radosNamespaceName, metav1.DeleteOptions{})
			bucketClaimErr := dyn.Resource(schema.GroupVersionResource{
				Resource: "objectbucketclaims",
				Group:    "objectbucket.io",
				Version:  "v1alpha1",
			}).Namespace("rook-ceph").Delete(context.Background(), bucketClaimName, metav1.DeleteOptions{})
			objectStoreUserErr := dyn.Resource(schema.GroupVersionResource{
				Resource: "cephobjectstoreusers",
				Group:    "ceph.rook.io",
				Version:  "v1",
			}).Namespace("rook-ceph").Delete(context.Background(), objectStoreUserName, metav1.DeleteOptions{})

			joinedErr := errors.Join(radosNamespaceErr, bucketClaimErr, objectStoreUserErr)
			if joinedErr != nil {
				t.Errorf("Failed to clean kept resources from chart installation: %v", joinedErr)
			}
		})
	}

	// Wait until cluster is ready.
	checkAllSubApplicationsAreSynced(t, ko, dyn, ko.Namespace, "test", 6*30, 10*time.Second)

	tko, tdyn, closer := createTenantKubectlOptionsAndDynamicClient(t, ko, "test-cluster")
	t.Cleanup(closer)
	tko.Namespace = "default"

	// Create PVC and job that writes a file to the PVC.
	k8s.KubectlApply(t, tko, "manifests/pvc-write-job.yaml")
	k8s.WaitUntilJobSucceed(t, tko, "test-volume-backup-writer", 12, time.Second*10)

	backupName := "test"
	backupYAML := getBackupYAML(backupName)
	t.Log(backupYAML)

	tko.Namespace = "velero"
	k8s.KubectlApplyFromString(t, tko, backupYAML)
	waitUntilBackupFinished(t, tko, tdyn, backupName, 12, 10*time.Second)
	tko.Namespace = "default"

	setValues := map[string]string{
		"features.backups":                                "true",
		"config.cephCSIRBD.existingRadosNamespace":        radosNamespaceName,
		"config.velero.storage.prefix":                    fmt.Sprintf("%s:test-cluster", ko.Namespace),
		"config.velero.storage.existingObjectBucketClaim": bucketClaimName,
		"config.velero.storage.existingObjectBucketUser":  objectStoreUserName,
	}
	installClusterWithSetValues(t, ko, "restore", setValues, !skipDeletion)
	checkAllSubApplicationsAreSynced(t, ko, dyn, ko.Namespace, "restore", 6*30, 10*time.Second)

	rtko, rtdyn, closer := createTenantKubectlOptionsAndDynamicClient(t, ko, "restore-cluster")
	t.Cleanup(closer)

	restoreYAML := getRestoreYAML(backupName)
	t.Log(restoreYAML)
	rtko.Namespace = "velero"
	k8s.KubectlApplyFromString(t, rtko, restoreYAML)
	waitUntilRestoreFinished(t, rtko, rtdyn, backupName, 12, 10*time.Second)
	rtko.Namespace = "default"

	k8s.KubectlApply(t, rtko, "manifests/pvc-read-job.yaml")
	k8s.WaitUntilJobSucceed(t, rtko, "test-volume-backup-reader", 12, time.Second*10)
}

func getBackupYAML(name string) string {
	return fmt.Sprintf(`apiVersion: velero.io/v1
kind: Backup
metadata:
  name: %s
  namespace: velero
spec:
  storageLocation: default
  ttl: 24h0m0s`, name)
}

func waitUntilBackupFinished(t *testing.T, ko *k8s.KubectlOptions, dyn *dynamic.DynamicClient, backupName string, maxRetries int, sleepBetweenRetries time.Duration) {
	retry.DoWithRetry(t, "Wait for backup to finish", maxRetries, sleepBetweenRetries, func() (string, error) {
		backup, err := dyn.Resource(schema.GroupVersionResource{
			Resource: "backups",
			Group:    "velero.io",
			Version:  "v1",
		}).Namespace(ko.Namespace).Get(context.Background(), backupName, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("error getting backup: %w", err)
		}

		phase, found, err := unstructured.NestedString(backup.Object, "status", "phase")
		if err != nil {
			return "", fmt.Errorf("error getting status.phase: %w", err)
		}
		if !found {
			return "", fmt.Errorf("error extracting backup status: not found")
		}

		if phase != "Completed" {
			return "", fmt.Errorf("backup %s is not 'Completed', use 'kubectl describe backup %s' to debug", backup.GetName(), backup.GetName())
		}
		return "", nil
	})
}

func getRestoreYAML(name string) string {
	return fmt.Sprintf(`apiVersion: velero.io/v1
kind: Restore
metadata:
  name: %s
spec:
  backupName: %s`, name, name)
}

func waitUntilRestoreFinished(t *testing.T, ko *k8s.KubectlOptions, dyn *dynamic.DynamicClient, restoreName string, maxRetries int, sleepBetweenRetries time.Duration) {
	retry.DoWithRetry(t, "Wait for restore to finish", maxRetries, sleepBetweenRetries, func() (string, error) {
		backup, err := dyn.Resource(schema.GroupVersionResource{
			Resource: "restores",
			Group:    "velero.io",
			Version:  "v1",
		}).Namespace(ko.Namespace).Get(context.Background(), restoreName, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("error getting restore: %w", err)
		}

		phase, found, err := unstructured.NestedString(backup.Object, "status", "phase")
		if err != nil {
			return "", fmt.Errorf("error getting status.phase: %w", err)
		}
		if !found {
			return "", fmt.Errorf("error extracting backup status: not found")
		}

		if phase != "Completed" {
			return "", fmt.Errorf("restore %s is not 'Completed', use 'kubectl describe restore %s' to debug", backup.GetName(), backup.GetName())
		}
		return "", nil
	})
}
