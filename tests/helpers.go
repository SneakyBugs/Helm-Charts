package tests

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/gruntwork-io/terratest/modules/retry"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

func createKubectlOptionsAndDynamicClient(t *testing.T, namespacePrefix string, cleanup bool) (*k8s.KubectlOptions, *dynamic.DynamicClient) {
	namespace := fmt.Sprintf("%s-%s", namespacePrefix, strings.ToLower(random.UniqueId()))
	existingNamespace, useExistingNamespace := os.LookupEnv("TEST_USE_EXISTING_NAMESPACE")
	if useExistingNamespace {
		namespace = existingNamespace
	}

	ko, dyn := createNamespacedKubectlOptionsAndDynamicClient(t, namespace)

	if !useExistingNamespace {
		if cleanup {
			t.Cleanup(func() {
				k8s.DeleteNamespace(t, ko, namespace)
			})
		}
		k8s.CreateNamespace(t, ko, namespace)
	}

	return ko, dyn
}
func createNamespacedKubectlOptionsAndDynamicClient(t *testing.T, namespace string) (*k8s.KubectlOptions, *dynamic.DynamicClient) {
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

		errs := make([]error, len(apps.Items))
		for i, app := range apps.Items {
			errs[i] = checkApplicationIsSynced(app)
		}
		err = errors.Join(errs...)
		if err != nil {
			// See for explanation https://pkg.go.dev/errors#Join
			nonNilErrs := err.(interface{ Unwrap() []error }).Unwrap()
			return "", fmt.Errorf("expected all Argo Applications to be synced and healthy, got %d/%d: %v", len(errs)-len(nonNilErrs), len(errs), err)
		}

		return "", nil
	})
}

func checkApplicationIsSynced(app unstructured.Unstructured) error {
	synced, found, err := unstructured.NestedString(app.Object, "status", "sync", "status")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("error extracting application status: not found")
	}

	if synced != "Synced" {
		return fmt.Errorf("app %s not synced", app.GetName())
	}

	health, found, err := unstructured.NestedString(app.Object, "status", "health", "status")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("error extracting application health status: not found")
	}
	if health != "Healthy" {
		return fmt.Errorf("app %s must be healthy", app.GetName())
	}
	return nil
}

func getHTTPClientWithStagingCAs(t *testing.T) *http.Client {
	cas := fetchLetsEncryptStagingCAs(t)
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: cas,
			},
		},
	}
}

func fetchLetsEncryptStagingCAs(t *testing.T) *x509.CertPool {
	cp := x509.NewCertPool()
	certs := fetchAndDecodePEM(t, "https://letsencrypt.org/certs/staging/letsencrypt-stg-root-x1.pem")
	certs = append(certs, fetchAndDecodePEM(t, "https://letsencrypt.org/certs/staging/letsencrypt-stg-root-x2.pem")...)
	for _, cert := range certs {
		cp.AddCert(cert)
	}
	return cp
}

func fetchAndDecodePEM(t *testing.T, url string) []*x509.Certificate {
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("Expected no error fetching cert from %s, got %v", url, err)
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			t.Errorf("Expected no error closing response body, got %v", err)
		}
	}()
	pemBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Expected no error reading response, got %v", err)
	}
	certs := []*x509.Certificate{}
	for block, rest := pem.Decode(pemBytes); block != nil; block, rest = pem.Decode(rest) {
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("Expected no error parsing x509 certificate, got %v", err)
		}
		certs = append(certs, cert)
	}
	return certs
}

func templateApplicationValues(t *testing.T, ho *helm.Options, chart string, releaseName string, template string) string {
	at := helm.RenderTemplate(t, ho, chart, releaseName, []string{template})
	application := &ArgoApplication{}
	err := yaml.Unmarshal([]byte(at), application)
	if err != nil {
		t.Fatalf("Expected no error unmarshaling Argo Application, got %v", err)
	}
	jsonValuesBytes, err := json.Marshal(application.Spec.Source.Helm.ValuesObject)
	if err != nil {
		t.Fatalf("Expected no error marshaling Argo Application, got %v", err)
	}

	valuesFile, err := os.CreateTemp(os.TempDir(), fmt.Sprintf("%s.values.*.yaml", releaseName))
	if err != nil {
		t.Fatalf("Expected no error creating values temp file, got %v", err)
	}

	_, err = valuesFile.Write(jsonValuesBytes)
	if err != nil {
		t.Fatalf("Expected no error writing temp values file, got %v", err)
	}
	err = valuesFile.Close()
	if err != nil {
		t.Fatalf("Expected no error closing temp values file, got %v", err)
	}

	t.Cleanup(func() {
		err := os.Remove(valuesFile.Name())
		if err != nil {
			t.Errorf("Expected no error deleting temp values, got %v", err)
		}
	})

	return valuesFile.Name()
}

type ArgoApplication struct {
	Spec ArgoApplicationSpec `yaml:"spec"`
}

type ArgoApplicationSpec struct {
	Source ArgoApplicationSpecSource `yaml:"source"`
}

type ArgoApplicationSpecSource struct {
	Helm ArgoApplicationSpecSourceHelm `yaml:"helm"`
}

type ArgoApplicationSpecSourceHelm struct {
	ValuesObject any `yaml:"valuesObject"`
}
