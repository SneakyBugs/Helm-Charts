package tests

import (
	"fmt"
	"maps"
	"os"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/retry"
)

func TestCluster(t *testing.T) {
	t.Parallel()

	_, skipDeletion := os.LookupEnv("TEST_SKIP_DELETION")
	ko, dyn := createKubectlOptionsAndDynamicClient(t, "cluster-test", !skipDeletion)

	installCluster(t, ko, "test", !skipDeletion)

	// Wait until cluster is ready.
	checkAllSubApplicationsAreSynced(t, ko, dyn, ko.Namespace, "test", 6*30, 10*time.Second)
}

func installCluster(t *testing.T, ko *k8s.KubectlOptions, releaseName string, cleanup bool) {
	installClusterWithSetValues(t, ko, releaseName, map[string]string{}, cleanup)
}

func installClusterWithSetValues(t *testing.T, ko *k8s.KubectlOptions, releaseName string, setValues map[string]string, cleanup bool) {
	installClusterWithValues(t, ko, releaseName, []string{}, setValues, cleanup)
}

func installClusterWithValues(t *testing.T, ko *k8s.KubectlOptions, releaseName string, valuesFiles []string, setValues map[string]string, cleanup bool) {
	setValuesOverrides := map[string]string{
		"features.components":         "false",
		"features.exporterComponents": "false",
	}
	maps.Copy(setValuesOverrides, setValues)

	valuesFile := "values/cluster.yaml"
	helm.Upgrade(t, &helm.Options{
		ValuesFiles:    append(([]string{valuesFile}), valuesFiles...),
		SetValues:      setValuesOverrides,
		KubectlOptions: ko,
		ExtraArgs: map[string][]string{
			"upgrade": {"--install", "--wait"},
		},
	}, "../charts/cluster", releaseName)

	if cleanup {
		releaseNamespace := ko.Namespace
		t.Cleanup(func() {
			ko.Namespace = releaseNamespace
			helm.Delete(t, &helm.Options{
				KubectlOptions: ko,
			}, releaseName, true)
		})
	}

	tko, _, closer := createTenantKubectlOptionsAndDynamicClient(t, ko, fmt.Sprintf("%s-cluster", releaseName))
	defer closer()

	componentsValuesPath := templateApplicationValues(t, &helm.Options{
		ValuesFiles:    []string{valuesFile},
		SetValues:      setValues,
		KubectlOptions: ko,
		ExtraArgs: map[string][]string{
			"upgrade": {"--install", "--wait"},
		},
	},
		"../charts/cluster",
		releaseName,
		"templates/argo-applications/components-application.yml",
	)

	retry.DoWithRetry(t, "attempt to install cluster-components", 6*20, 10*time.Second, func() (string, error) {
		err := helm.UpgradeE(t, &helm.Options{
			ValuesFiles:    []string{componentsValuesPath},
			SetValues:      setValues,
			KubectlOptions: tko,
			ExtraArgs: map[string][]string{
				"upgrade": []string{"--install", "--wait", "--take-ownership"},
			},
		}, "../charts/cluster-components", "cluster-components")
		if err != nil {
			return "", err
		}
		return "", nil
	})

	exporterComponentsValuesPath := templateApplicationValues(t, &helm.Options{
		ValuesFiles:    []string{valuesFile},
		SetValues:      setValues,
		KubectlOptions: ko,
		ExtraArgs: map[string][]string{
			"upgrade": {"--install", "--wait"},
		},
	},
		"../charts/cluster",
		releaseName,
		"templates/argo-applications/exporter-components-application.yml",
	)

	retry.DoWithRetry(t, "attempt to install telemetry-exporter-components", 6*20, 10*time.Second, func() (string, error) {
		err := helm.UpgradeE(t, &helm.Options{
			KubectlOptions: tko,
			ValuesFiles:    []string{exporterComponentsValuesPath},
			SetValues:      setValues,
			ExtraArgs: map[string][]string{
				"upgrade": {"--install", "--wait", "--take-ownership"},
			},
		}, "../charts/telemetry-exporter-components", "telemetry-exporter-components")
		if err != nil {
			return "", err
		}
		return "", nil
	})
}
