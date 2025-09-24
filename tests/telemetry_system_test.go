package tests

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/random"
)

func TestTelemetrySystem(t *testing.T) {
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
			defer k8s.DeleteNamespace(t, ko, namespace)
		}
		k8s.CreateNamespace(t, ko, namespace)
	}

	uninstallCluster := installCluster(t, ko, "test")
	if !skipDeletion {
		defer uninstallCluster()
	}

	// Wait until cluster is ready.
	checkAllSubApplicationsAreSynced(t, ko, dyn, namespace, "test", 6*30, 10*time.Second)
}
