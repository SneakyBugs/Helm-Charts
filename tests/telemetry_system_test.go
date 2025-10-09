package tests

import (
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/gruntwork-io/terratest/modules/retry"
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

	uninstallTelemetrySystem := installTelemetrySystem(t, ko, "test", "test-telemetry-system")
	if !skipDeletion {
		defer uninstallTelemetrySystem()
	}

	checkAllSubApplicationsAreSynced(t, ko, dyn, namespace, "test-telemetry-system", 0, 10*time.Second)

	tko, _, closer := createTenantKubectlOptionsAndDynamicClient(t, ko, "test-cluster")
	t.Cleanup(closer)

	httpClient := getHTTPClientWithStagingCAs(t)

	t.Run("Grafana data sources are present", func(t *testing.T) {
		t.Parallel()
		retry.DoWithRetry(t, "Grafana data sources are present", 6*20, 10*time.Second, func() (string, error) {
			return "", testGrafanaDatasources(t, httpClient, tko)
		})
	})

	t.Run("Prometheus responds to query through Grafana API", func(t *testing.T) {
		t.Parallel()
		retry.DoWithRetry(t, "Prometheus responds to query through Grafana API", 6*20, 10*time.Second, func() (string, error) {
			return "", testPrometheusDataSourceQuery(t, httpClient, tko)
		})
	})

	t.Run("ClickHouse responds to query through Grafana API", func(t *testing.T) {
		t.Parallel()
		retry.DoWithRetry(t, "ClickHouse responds to query through Grafana API", 6*20, 10*time.Second, func() (string, error) {
			return "", testClickHouseDataSourceQuery(t, httpClient, tko)
		})
	})

	t.Run("Prometheus contains metrics from the exporter", func(t *testing.T) {
		t.Parallel()
		retry.DoWithRetry(t, "Prometheus contains metrics from the exporter", 6*20, 10*time.Second, func() (string, error) {
			return "", testPrometheusMetricsAreReceived(t, httpClient, tko)
		})
	})

	t.Run("ClickHouse contains logs from the exporter", func(t *testing.T) {
		t.Parallel()
		retry.DoWithRetry(t, "ClickHouse contains logs from the exporter", 6*20, 10*time.Second, func() (string, error) {
			return "", testClickHouseLogsAreReceived(t, httpClient, tko)
		})
	})

	t.Run("Prometheus contains metrics from kube-scheduler exporter", func(t *testing.T) {
		t.Parallel()
		retry.DoWithRetry(t, "Prometheus contains metrics from kube-scheduler exporter", 6*20, 10*time.Second, func() (string, error) {
			return "", testPrometheusContainsKubeSchedulerMetrics(t, httpClient, tko)
		})
	})

	t.Run("Prometheus contains metrics from kube-controller-manager exporter", func(t *testing.T) {
		t.Parallel()
		retry.DoWithRetry(t, "Prometheus contains metrics kube-controller-manager exporter", 6*20, 10*time.Second, func() (string, error) {
			return "", testPrometheusContainsKubeControllerManagerMetrics(t, httpClient, tko)
		})
	})

	t.Run("Prometheus contains metrics from kubelet exporter", func(t *testing.T) {
		t.Parallel()
		retry.DoWithRetry(t, "Prometheus contains metrics kubelet exporter", 6*20, 10*time.Second, func() (string, error) {
			return "", testPrometheusContainsKubeletMetrics(t, httpClient, tko)
		})
	})

	t.Run("Clickhouse contains traces from kube-apiserver", func(t *testing.T) {
		t.Parallel()
		retry.DoWithRetry(t, "Clickhouse contains traces from kube-apiserver", 6*20, 10*time.Second, func() (string, error) {
			return "", testClickHouseContainsAPIServerTraces(t, httpClient, tko)
		})
	})
}

func testGrafanaDatasources(t *testing.T, c *http.Client, tenantKubectlOptions *k8s.KubectlOptions) error {
	req, err := http.NewRequest(http.MethodGet, "https://grafana.infra.sneakybugs.com/api/datasources", http.NoBody)
	if err != nil {
		return fmt.Errorf("expected no error creating datasources request, got %v", err)
	}
	username, password, err := fetchGrafanaCredentials(t, tenantKubectlOptions)
	if err != nil {
		return err
	}
	req.SetBasicAuth(username, password)

	datasources := []GrafanaDataSource{}
	err = httpDoAndUnmarshal(t, c, req, &datasources)
	if err != nil {
		return err
	}

	if len(datasources) != 2 {
		return fmt.Errorf("expected data sources list length to be 2, got %d", len(datasources))
	}
	promtheusDataSourceFound := false
	clickhouseDataSourceFound := false
	for _, datasource := range datasources {
		if datasource.Type == "prometheus" {
			promtheusDataSourceFound = true
		}
		if datasource.Type == "grafana-clickhouse-datasource" {
			clickhouseDataSourceFound = true
		}
	}

	if !promtheusDataSourceFound {
		return fmt.Errorf("expected Grafana data source of type 'prometheus' to be present, got [%s, %s]", datasources[0].Type, datasources[1].Type)
	}

	if !clickhouseDataSourceFound {
		return fmt.Errorf("expected Grafana data source of type 'grafana-clickhouse-datasource' to be present, got [%s, %s]", datasources[0].Type, datasources[1].Type)
	}

	return nil
}

func testPrometheusDataSourceQuery(t *testing.T, c *http.Client, tenantKubectlOptions *k8s.KubectlOptions) error {
	datasourceUID, err := getGrafanaDataSourceID(t, c, tenantKubectlOptions, "Prometheus")
	if err != nil {
		return err
	}

	queryResponse, err := queryGrafanaDataSource(t, c, tenantKubectlOptions, GrafanaDataSourceQueryBody{
		To:   "now",
		From: "now-1s",
		Queries: []any{
			GrafanaDataSourceQuery{
				Datasource: GrafanaDataSource{
					UID: datasourceUID,
				},
				Expresion: "time()",
				RefID:     "A",
			},
		},
	})
	if err != nil {
		return err
	}

	result, ok := queryResponse.Results["A"]
	if !ok {
		return fmt.Errorf("expected query response to contain result with ref 'A'")
	}

	if result.Status != 200 {
		return fmt.Errorf("expected query status to be 200, got %d", result.Status)
	}

	if len(result.Frames) != 1 {
		return fmt.Errorf("expected query result frames to be of length 1, got %d", len(result.Frames))
	}

	v, ok := result.Frames[0].Data.Values[0][0].(float64)
	if !ok {
		return fmt.Errorf("expected result values to be of type float64, got %s", reflect.TypeOf(result.Frames[0].Data.Values[0][0]))
	}

	vt := time.UnixMilli(int64(v))
	now := time.Now()

	if vt.Before(now.Add(-1*time.Minute)) || vt.After(now.Add(time.Minute)) {
		return fmt.Errorf("expected result to be within 1 minute of current time, got %v with offset %v", vt, now.Sub(vt))
	}

	return nil
}

func testClickHouseDataSourceQuery(t *testing.T, c *http.Client, tenantKubectlOptions *k8s.KubectlOptions) error {
	datasourceUID, err := getGrafanaDataSourceID(t, c, tenantKubectlOptions, "ClickHouse")
	if err != nil {
		return err
	}

	queryResponse, err := queryGrafanaDataSource(t, c, tenantKubectlOptions, GrafanaDataSourceQueryBody{
		To:   "now",
		From: "now-1h",
		Queries: []any{
			ClickHouseDataSourceQuery{
				Datasource: GrafanaDataSource{
					UID: datasourceUID,
				},
				Format: 1,
				RawSQL: "SELECT 1",
				RefID:  "A",
			},
		},
	})
	if err != nil {
		return err
	}

	result, ok := queryResponse.Results["A"]
	if !ok {
		return fmt.Errorf("expected query response to contain result with ref 'A'")
	}

	if result.Status != 200 {
		return fmt.Errorf("expected query status to be 200, got %d", result.Status)
	}

	if len(result.Frames) != 1 {
		return fmt.Errorf("expected query result frames to be of length 1, got %d", len(result.Frames))
	}

	v, ok := result.Frames[0].Data.Values[0][0].(float64)
	if !ok {
		return fmt.Errorf("expected result values to be of type float64, got %s", reflect.TypeOf(result.Frames[0].Data.Values[0][0]))
	}

	if v != 1 {
		return fmt.Errorf("expected result value to be [1.0], got [%f]", v)
	}

	return nil
}

func testPrometheusMetricsAreReceived(t *testing.T, c *http.Client, tenantKubectlOptions *k8s.KubectlOptions) error {
	datasourceUID, err := getGrafanaDataSourceID(t, c, tenantKubectlOptions, "Prometheus")
	if err != nil {
		return fmt.Errorf("unexpected error getting datasource: %v", err)
	}

	queryResponse, err := queryGrafanaDataSource(t, c, tenantKubectlOptions, GrafanaDataSourceQueryBody{
		To:   "now",
		From: "now-1s",
		Queries: []any{
			GrafanaDataSourceQuery{
				Datasource: GrafanaDataSource{
					UID: datasourceUID,
				},
				Expresion: "sum(kube_pod_status_ready)",
				RefID:     "A",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("unexpected error querying datasource: %v", err)
	}

	result, ok := queryResponse.Results["A"]
	if !ok {
		return fmt.Errorf("expected query response to contain result with ref 'A'")
	}

	if result.Status != 200 {
		return fmt.Errorf("expected query status to be 200, got %d", result.Status)
	}

	if len(result.Frames) != 1 {
		return fmt.Errorf("expected query result frames to be of length 1, got %d", len(result.Frames))
	}

	v, ok := result.Frames[0].Data.Values[0][0].(float64)
	if !ok {
		return fmt.Errorf("expected result values to be of type float64, got %s", reflect.TypeOf(result.Frames[0].Data.Values[0][0]))
	}

	if v < 20 {
		return fmt.Errorf("expected at least 20 ready Pods, got %f", v)
	}

	return nil
}

func testClickHouseLogsAreReceived(t *testing.T, c *http.Client, tenantKubectlOptions *k8s.KubectlOptions) error {
	datasourceUID, err := getGrafanaDataSourceID(t, c, tenantKubectlOptions, "ClickHouse")
	if err != nil {
		return err
	}

	queryResponse, err := queryGrafanaDataSource(t, c, tenantKubectlOptions, GrafanaDataSourceQueryBody{
		To:   "now",
		From: "now-12h",
		Queries: []any{
			ClickHouseDataSourceQuery{
				Datasource: GrafanaDataSource{
					UID: datasourceUID,
				},
				Format: 1,
				RawSQL: "SELECT Body FROM otel.otel_logs WHERE ResourceAttributes['k8s.namespace.name'] = 'kube-system' ORDER BY Timestamp DESC LIMIT 10",
				RefID:  "A",
			},
		},
	})
	if err != nil {
		return err
	}

	result, ok := queryResponse.Results["A"]
	if !ok {
		return fmt.Errorf("expected query response to contain result with ref 'A'")
	}

	if result.Status != 200 {
		return fmt.Errorf("expected query status to be 200, got %d", result.Status)
	}

	if len(result.Frames) != 1 {
		return fmt.Errorf("expected query result frames to be of length 1, got %d", len(result.Frames))
	}

	if len(result.Frames[0].Data.Values[0]) <= 1 {
		return fmt.Errorf("expected result length to be at least 1, got %d", len(result.Frames[0].Data.Values[0]))
	}

	return nil
}

func testPrometheusContainsKubeSchedulerMetrics(t *testing.T, c *http.Client, tenantKubectlOptions *k8s.KubectlOptions) error {
	datasourceUID, err := getGrafanaDataSourceID(t, c, tenantKubectlOptions, "Prometheus")
	if err != nil {
		return fmt.Errorf("unexpected error getting datasource: %v", err)
	}

	queryResponse, err := queryGrafanaDataSource(t, c, tenantKubectlOptions, GrafanaDataSourceQueryBody{
		To:   "now",
		From: "now-1s",
		Queries: []any{
			GrafanaDataSourceQuery{
				Datasource: GrafanaDataSource{
					UID: datasourceUID,
				},
				Expresion: "sum(scheduler_pending_pods)",
				RefID:     "A",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("unexpected error querying datasource: %v", err)
	}

	result, ok := queryResponse.Results["A"]
	if !ok {
		return fmt.Errorf("expected query response to contain result with ref 'A'")
	}

	if result.Status != 200 {
		return fmt.Errorf("expected query status to be 200, got %d", result.Status)
	}

	if len(result.Frames) != 1 {
		return fmt.Errorf("expected query result frames to be of length 1, got %d", len(result.Frames))
	}

	v, ok := result.Frames[0].Data.Values[1][0].(float64)
	if !ok {
		return fmt.Errorf("expected result values to be of type float64, got %s", reflect.TypeOf(result.Frames[0].Data.Values[0][0]))
	}

	if 10 < v {
		return fmt.Errorf("expected less than 10 pending pods, got %f", v)
	}

	return nil
}

func testPrometheusContainsKubeControllerManagerMetrics(t *testing.T, c *http.Client, tenantKubectlOptions *k8s.KubectlOptions) error {
	datasourceUID, err := getGrafanaDataSourceID(t, c, tenantKubectlOptions, "Prometheus")
	if err != nil {
		return fmt.Errorf("unexpected error getting datasource: %v", err)
	}

	queryResponse, err := queryGrafanaDataSource(t, c, tenantKubectlOptions, GrafanaDataSourceQueryBody{
		To:   "now",
		From: "now-1s",
		Queries: []any{
			GrafanaDataSourceQuery{
				Datasource: GrafanaDataSource{
					UID: datasourceUID,
				},
				Expresion: `sum(workqueue_adds_total{name="replicaset"})`,
				RefID:     "A",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("unexpected error querying datasource: %v", err)
	}

	result, ok := queryResponse.Results["A"]
	if !ok {
		return fmt.Errorf("expected query response to contain result with ref 'A'")
	}

	if result.Status != 200 {
		return fmt.Errorf("expected query status to be 200, got %d", result.Status)
	}

	if len(result.Frames) != 1 {
		return fmt.Errorf("expected query result frames to be of length 1, got %d", len(result.Frames))
	}

	v, ok := result.Frames[0].Data.Values[1][0].(float64)
	if !ok {
		return fmt.Errorf("expected result values to be of type float64, got %s", reflect.TypeOf(result.Frames[0].Data.Values[0][0]))
	}

	if v < 10 {
		return fmt.Errorf(`expected 10<sum(workqueue_adds_total{name="replicaset"}), got %f`, v)
	}

	return nil
}

func testPrometheusContainsKubeletMetrics(t *testing.T, c *http.Client, tenantKubectlOptions *k8s.KubectlOptions) error {
	datasourceUID, err := getGrafanaDataSourceID(t, c, tenantKubectlOptions, "Prometheus")
	if err != nil {
		return fmt.Errorf("unexpected error getting datasource: %v", err)
	}

	queryResponse, err := queryGrafanaDataSource(t, c, tenantKubectlOptions, GrafanaDataSourceQueryBody{
		To:   "now",
		From: "now-1s",
		Queries: []any{
			GrafanaDataSourceQuery{
				Datasource: GrafanaDataSource{
					UID: datasourceUID,
				},
				Expresion: "sum(kubelet_active_pods)",
				RefID:     "A",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("unexpected error querying datasource: %v", err)
	}

	result, ok := queryResponse.Results["A"]
	if !ok {
		return fmt.Errorf("expected query response to contain result with ref 'A'")
	}

	if result.Status != 200 {
		return fmt.Errorf("expected query status to be 200, got %d", result.Status)
	}

	if len(result.Frames) != 1 {
		return fmt.Errorf("expected query result frames to be of length 1, got %d", len(result.Frames))
	}

	v, ok := result.Frames[0].Data.Values[1][0].(float64)
	if !ok {
		return fmt.Errorf("expected result values to be of type float64, got %s", reflect.TypeOf(result.Frames[0].Data.Values[0][0]))
	}

	if v < 10 {
		return fmt.Errorf(`expected 10<sum(kubelet_active_pods), got %f`, v)
	}

	return nil
}

func testClickHouseContainsAPIServerTraces(t *testing.T, c *http.Client, tenantKubectlOptions *k8s.KubectlOptions) error {
	datasourceUID, err := getGrafanaDataSourceID(t, c, tenantKubectlOptions, "ClickHouse")
	if err != nil {
		return err
	}

	queryResponse, err := queryGrafanaDataSource(t, c, tenantKubectlOptions, GrafanaDataSourceQueryBody{
		To:   "now",
		From: "now-12h",
		Queries: []any{
			ClickHouseDataSourceQuery{
				Datasource: GrafanaDataSource{
					UID: datasourceUID,
				},
				Format: 1,
				RawSQL: "SELECT COUNT(*) FROM otel.otel_traces WHERE ServiceName='apiserver'",
				RefID:  "A",
			},
		},
	})
	if err != nil {
		return err
	}

	result, ok := queryResponse.Results["A"]
	if !ok {
		return fmt.Errorf("expected query response to contain result with ref 'A'")
	}

	if result.Status != 200 {
		return fmt.Errorf("expected query status to be 200, got %d", result.Status)
	}

	if len(result.Frames) == 2 {
		return fmt.Errorf("expected query result frames to be of length 2, got %d", len(result.Frames))
	}

	if len(result.Frames[0].Data.Values[0]) != 1 {
		return fmt.Errorf("expected result length to be 1, got %d", len(result.Frames[0].Data.Values[0]))
	}

	v, ok := result.Frames[0].Data.Values[0][0].(float64)
	if !ok {
		return fmt.Errorf("expected result values to be of type float64, got %s", reflect.TypeOf(result.Frames[0].Data.Values[0][0]))
	}

	if v < 1 {
		return fmt.Errorf("Expected ClickHouse to contain at least 1 apiserver trace, got %f", v)
	}

	return nil
}

func installTelemetrySystem(t *testing.T, ko *k8s.KubectlOptions, clusterReleaseName string, releaseName string) func() {
	helm.Upgrade(t, &helm.Options{
		ValuesFiles:    []string{"values/telemetry-system.yaml"},
		KubectlOptions: ko,
		ExtraArgs: map[string][]string{
			"upgrade": []string{"--install", "--wait"},
		},
	}, "../charts/telemetry-system", releaseName)

	tko, _, closer := createTenantKubectlOptionsAndDynamicClient(t, ko, fmt.Sprintf("%s-cluster", clusterReleaseName))
	defer closer()

	tko.Namespace = "telemetry-system"
	retry.DoWithRetry(t, "attempt to install telemetry-system-components", 6*2, 10*time.Second, func() (string, error) {
		err := helm.UpgradeE(t, &helm.Options{
			ValuesFiles:    []string{"values/telemetry-system-components.yaml"},
			KubectlOptions: tko,
			ExtraArgs: map[string][]string{
				"upgrade": []string{"--install", "--wait", "--take-ownership"},
			},
		}, "../charts/telemetry-system-components", "telemetry-system-components")
		if err != nil {
			return "", err
		}
		return "", nil
	})

	return func() {
		defer helm.Delete(t, &helm.Options{
			KubectlOptions: ko,
		}, releaseName, true)
	}
}
