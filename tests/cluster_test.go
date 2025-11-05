package tests

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/gruntwork-io/terratest/modules/retry"
)

func TestCluster(t *testing.T) {
	t.Parallel()

	namespace := fmt.Sprintf("cluster-test-%s", strings.ToLower(random.UniqueId()))
	existingNamespace, useExistingNamespace := os.LookupEnv("TEST_USE_EXISTING_NAMESPACE")
	if useExistingNamespace {
		namespace = existingNamespace
	}
	ko, dyn := createKubectlOptionsAndDynamicClient(t, namespace)

	_, skipDeletion := os.LookupEnv("TEST_SKIP_DELETION")

	if !useExistingNamespace {
		if !skipDeletion {
			t.Cleanup(func() {
				k8s.DeleteNamespace(t, ko, namespace)
			})
		}
		k8s.CreateNamespace(t, ko, namespace)
	}

	installCluster(t, ko, "test", !skipDeletion)

	// Wait until cluster is ready.
	checkAllSubApplicationsAreSynced(t, ko, dyn, namespace, "test", 6*30, 10*time.Second)
}

func installCluster(t *testing.T, ko *k8s.KubectlOptions, releaseName string, cleanup bool) {
	helm.Upgrade(t, &helm.Options{
		ValuesFiles:    []string{"values/cluster.yaml"},
		KubectlOptions: ko,
		ExtraArgs: map[string][]string{
			"upgrade": []string{"--install", "--wait"},
		},
	}, "../charts/cluster", releaseName)
	if cleanup {
		t.Cleanup(func() {
			helm.Delete(t, &helm.Options{
				KubectlOptions: ko,
			}, releaseName, true)
		})
	}

	tko, _, closer := createTenantKubectlOptionsAndDynamicClient(t, ko, fmt.Sprintf("%s-cluster", releaseName))
	defer closer()

	retry.DoWithRetry(t, "attempt to install cluster-components", 6*20, 10*time.Second, func() (string, error) {
		err := helm.UpgradeE(t, &helm.Options{
			ValuesFiles:    []string{"values/cluster-components.yaml"},
			KubectlOptions: tko,
			ExtraArgs: map[string][]string{
				"upgrade": []string{"--install", "--wait", "--take-ownership"},
			},
			SetValues: map[string]string{
				"cephCSIRBD.nodeClientSecretRemoteKey":        fmt.Sprintf("rook-ceph-client-%s-%s-cluster-csi-rbd-node", ko.Namespace, releaseName),
				"cephCSIRBD.provisionerClientSecretRemoteKey": fmt.Sprintf("rook-ceph-client-%s-%s-cluster-csi-rbd-provisioner", ko.Namespace, releaseName),
			},
		}, "../charts/cluster-components", "cluster-components")
		if err != nil {
			return "", err
		}
		return "", nil
	})

	retry.DoWithRetry(t, "attempt to install telemetry-exporter-components", 6*20, 10*time.Second, func() (string, error) {
		err := helm.UpgradeE(t, &helm.Options{
			ValuesFiles:    []string{"values/telemetry-exporter-components.yaml"},
			KubectlOptions: tko,
			ExtraArgs: map[string][]string{
				"upgrade": []string{"--install", "--wait", "--take-ownership"},
			},
		}, "../charts/telemetry-exporter-components", "telemetry-exporter-components")
		if err != nil {
			return "", err
		}
		return "", nil
	})
}
