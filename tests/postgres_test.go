package tests

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/retry"
	"github.com/jackc/pgx/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const fqdn = "example.linuxdweller.com"

func TestPostgres(t *testing.T) {
	t.Parallel()

	_, skipDeletion := os.LookupEnv("TEST_SKIP_DELETION")
	ko, dyn := createKubectlOptionsAndDynamicClient(t, "postgres-test", !skipDeletion)

	releaseName := "postgres"
	hostname := fmt.Sprintf("%s.pg.%s", uuid.New().String(), fqdn)

	svc, err := k8s.GetServiceE(t, ko, releaseName)
	if err != nil {
		t.Logf("Didn't find existing service, generated %s", hostname)
	} else {
		hostname = svc.Annotations["external-dns.alpha.kubernetes.io/hostname"]
		t.Logf("Found existing service for %s", hostname)
	}

	helm.Upgrade(t, &helm.Options{
		// ValuesFiles: []string{"values/cluster.yaml"},
		KubectlOptions: ko,
		SetValues: map[string]string{
			"hostname":     hostname,
			"service.type": "LoadBalancer",
		},
		ExtraArgs: map[string][]string{
			"upgrade": []string{"--install", "--wait"},
		},
	}, "../charts/postgres", releaseName)
	if !skipDeletion {
		t.Cleanup(func() {
			helm.Delete(t, &helm.Options{
				KubectlOptions: ko,
			}, releaseName, true)
		})
	}

	appCredentialsSecret := k8s.GetSecret(t, ko, fmt.Sprintf("%s-app", releaseName))
	user := string(appCredentialsSecret.Data["user"])
	db := string(appCredentialsSecret.Data["dbname"])
	connectionString := fmt.Sprintf("postgresql://%s@%s:5432/%s?sslmode=verify-full", user, hostname, db)
	t.Logf("Connection string: %s", connectionString)

	k8s.WaitUntilPodAvailable(t, ko, fmt.Sprintf("%s-1", releaseName), 12, 10*time.Second)

	retry.DoWithRetry(t, "Connect to Postgres", 12, 10*time.Second, func() (string, error) {
		conn, err := postgresConnection(t, ko, releaseName, connectionString)
		if err != nil {
			return "", fmt.Errorf("error connecting to Postgres: %w", err)
		}

		_, err = conn.Exec(context.Background(), "CREATE TABLE IF NOT EXISTS messages (id TEXT PRIMARY KEY, message TEXT NOT NULL)")
		if err != nil {
			return "", fmt.Errorf("error during table creation: %w", err)
		}

		messageID := uuid.New().String()
		_, err = conn.Exec(context.Background(), "INSERT INTO messages (id, message) VALUES ($1, $2)", messageID, "sample")
		if err != nil {
			return "", fmt.Errorf("error during insert: %w", err)
		}

		row := conn.QueryRow(context.Background(), "SELECT id, message FROM messages WHERE id=$1", messageID)
		var resultID string
		var resultMessage string
		err = row.Scan(&resultID, &resultMessage)
		if err != nil {
			return "", fmt.Errorf("error during row scan: %w", err)
		}
		if resultID != messageID {
			return "", fmt.Errorf("expected row id to be '%s', got '%s'", messageID, resultID)
		}
		if resultMessage != "sample" {
			return "", fmt.Errorf("expected row message to be 'sample', got '%s'", resultMessage)
		}
		return "", nil
	})

	retry.DoWithRetry(t, "Create backup and restore from it", 12, 10*time.Second, func() (string, error) {
		// TODO Clean up backup.
		backupName := "test-backup"
		backupYAML := getPostgresBackupYAML(backupName)
		k8s.KubectlApplyFromString(t, ko, backupYAML)
		t.Cleanup(func() {
			k8s.KubectlDeleteFromString(t, ko, backupYAML)
		})

		_, err := retry.DoWithRetryE(t, "Wait for backup to complete", 12, 10*time.Second, func() (string, error) {
			backup, err := dyn.Resource(schema.GroupVersionResource{
				Resource: "backups",
				Group:    "postgresql.cnpg.io",
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

			if phase != "completed" {
				return "", fmt.Errorf("backup %s is not completed, use 'kubectl describe backup %s' to debug", backup.GetName(), backup.GetName())
			}

			return phase, nil
		})
		if err != nil {
			return "", err
		}

		restoreHostname := fmt.Sprintf("restore-%s", hostname)
		helm.Upgrade(t, &helm.Options{
			ValuesFiles:    []string{"values/postgres-restore.yaml"},
			KubectlOptions: ko,
			SetValues: map[string]string{
				"hostname":                       restoreHostname,
				"service.type":                   "LoadBalancer",
				"bootstrap.recovery.backup.name": backupName,
			},
			ExtraArgs: map[string][]string{
				"upgrade": []string{"--install", "--wait"},
			},
		}, "../charts/postgres", fmt.Sprintf("%s-restore", releaseName))
		if !skipDeletion {
			t.Cleanup(func() {
				helm.Delete(t, &helm.Options{
					KubectlOptions: ko,
				}, fmt.Sprintf("%s-restore", releaseName), true)
			})
		}

		k8s.WaitUntilPodAvailable(t, ko, fmt.Sprintf("%s-restore-1", releaseName), 12, 10*time.Second)

		restoreConnectionString := fmt.Sprintf("postgresql://%s@%s:5432/%s?sslmode=verify-full", user, restoreHostname, db)
		conn, err := postgresConnection(t, ko, fmt.Sprintf("%s-restore", releaseName), restoreConnectionString)
		if err != nil {
			return "", fmt.Errorf("error connecting to Postgres: %w", err)
		}

		row := conn.QueryRow(context.Background(), "SELECT id, message FROM messages LIMIT 1")
		var resultID string
		var resultMessage string
		err = row.Scan(&resultID, &resultMessage)
		if err != nil {
			return "", fmt.Errorf("error during row scan: %w", err)
		}
		if resultMessage != "sample" {
			return "", fmt.Errorf("expected row message to be 'sample', got '%s'", resultMessage)
		}
		return "", nil
	})
}

func fetchAndDecodeCAAndClientCert(t *testing.T, ko *k8s.KubectlOptions, secretName string) (*x509.CertPool, *tls.Certificate, error) {
	secret, err := k8s.GetSecretE(t, ko, secretName)
	if err != nil {
		return nil, nil, err
	}
	caPEMBytes := secret.Data["ca.crt"]

	cas := x509.NewCertPool()
	for block, rest := pem.Decode(caPEMBytes); block != nil; block, rest = pem.Decode(rest) {
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, nil, err
		}
		cas.AddCert(cert)
	}

	certPEMBytes := secret.Data["tls.crt"]
	keyPEMBytes := secret.Data["tls.key"]
	cert, err := tls.X509KeyPair(certPEMBytes, keyPEMBytes)
	if err != nil {
		return nil, nil, err
	}
	return cas, &cert, nil
}

func getPostgresBackupYAML(name string) string {
	return fmt.Sprintf(`apiVersion: postgresql.cnpg.io/v1
kind: Backup
metadata:
  name: %s
spec:
  cluster:
    name: postgres
  method: plugin
  pluginConfiguration:
    name: barman-cloud.cloudnative-pg.io`, name)
}

func postgresConnection(t *testing.T, ko *k8s.KubectlOptions, releaseName string, connectionString string) (*pgx.Conn, error) {
	cas, cert, err := fetchAndDecodeCAAndClientCert(t, ko, fmt.Sprintf("%s-tls-app", releaseName))
	if err != nil {
		return nil, err
	}

	connConfig, err := pgx.ParseConfig(connectionString)
	if err != nil {
		return nil, err
	}
	connConfig.TLSConfig.RootCAs = cas
	connConfig.TLSConfig.Certificates = []tls.Certificate{*cert}

	conn, err := pgx.ConnectConfig(context.Background(), connConfig)
	if err != nil {
		return nil, fmt.Errorf("error connecting to Postgres: %w", err)
	}
	return conn, nil
}
