package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/gruntwork-io/terratest/modules/k8s"
)

func getGrafanaDataSourceID(t *testing.T, c *http.Client, tenantKubectlOptions *k8s.KubectlOptions, grafanaURL string, name string) (string, error) {
	grafanaUser, grafanaPassword, err := fetchGrafanaCredentials(t, tenantKubectlOptions)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/datasources/name/%s", grafanaURL, name), http.NoBody)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(grafanaUser, grafanaPassword)

	grafanaDatasource := GrafanaDataSource{}
	err = httpDoAndUnmarshal(t, c, req, &grafanaDatasource)
	if err != nil {
		return "", nil
	}
	return grafanaDatasource.UID, nil
}

func httpDoAndUnmarshal(t *testing.T, c *http.Client, req *http.Request, v any) error {
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	t.Logf("Response status: %d", resp.StatusCode)
	t.Logf("Response body:\n%s", string(bodyBytes))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("expected response status to be 200, got %d", resp.StatusCode)
	}
	err = json.Unmarshal(bodyBytes, v)
	if err != nil {
		return err
	}
	return nil
}

func fetchGrafanaCredentials(t *testing.T, tenantKubectlOptions *k8s.KubectlOptions) (string, string, error) {
	previousNamespace := tenantKubectlOptions.Namespace
	tenantKubectlOptions.Namespace = "telemetry-system"
	grafanaCredsSecret, err := k8s.GetSecretE(t, tenantKubectlOptions, "grafana-admin-credentials")
	if err != nil {
		return "", "", err
	}
	grafanaUser := string(grafanaCredsSecret.Data["GF_SECURITY_ADMIN_USER"])
	grafanaPassword := string(grafanaCredsSecret.Data["GF_SECURITY_ADMIN_PASSWORD"])
	tenantKubectlOptions.Namespace = previousNamespace
	return grafanaUser, grafanaPassword, nil

}

func queryGrafanaDataSource(t *testing.T, c *http.Client, tenantKubectlOptions *k8s.KubectlOptions, grafanaURL string, query GrafanaDataSourceQueryBody) (QueryResponse, error) {
	grafanaUser, grafanaPassword, err := fetchGrafanaCredentials(t, tenantKubectlOptions)
	if err != nil {
		return QueryResponse{}, err
	}
	bodyBytes, err := json.Marshal(query)
	if err != nil {
		return QueryResponse{}, fmt.Errorf("expected no error when marshaling body, got %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/ds/query", grafanaURL), bytes.NewReader(bodyBytes))
	if err != nil {
		return QueryResponse{}, fmt.Errorf("expected no error when creating new request, got %v", err)
	}
	req.SetBasicAuth(grafanaUser, grafanaPassword)

	queryResponse := QueryResponse{}
	err = httpDoAndUnmarshal(t, c, req, &queryResponse)
	return queryResponse, err
}

type GrafanaDataSourceQueryBody struct {
	Queries []any  `json:"queries"`
	From    string `json:"from"`
	To      string `json:"to"`
}

type GrafanaDataSourceQuery struct {
	RefID      string            `json:"refId,omitempty"`
	Expresion  string            `json:"expr,omitempty"`
	Format     string            `json:"format,omitempty"`
	Query      string            `json:"query,omitempty"`
	Datasource GrafanaDataSource `json:"datasource,omitempty"`
}

type ClickHouseDataSourceQuery struct {
	RefID      string            `json:"refId,omitempty"`
	Format     int               `json:"format,omitempty"`
	RawSQL     string            `json:"rawSql,omitempty"`
	Datasource GrafanaDataSource `json:"datasource,omitempty"`
}

type GrafanaDataSource struct {
	UID  string `json:"uid,omitempty"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
}

type QueryResponse struct {
	Results map[string]QueryResult `json:"results"`
}

type QueryResult struct {
	Status int     `json:"status"`
	Frames []Frame `json:"frames"`
}

type Frame struct {
	Schema Schema    `json:"schema"`
	Data   DataFrame `json:"data"`
}

type Schema struct {
	Name   string  `json:"name"`
	RefID  string  `json:"refId"`
	Fields []Field `json:"fields"`
}

type Field struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	TypeInfo TypeInfo `json:"typeInfo"`
}

type TypeInfo struct {
	Frame string `json:"frame"`
}

type DataFrame struct {
	Values [][]interface{} `json:"values"`
}
