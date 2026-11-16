package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenKubernetesFromEnvDoesNotDial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHIPYARD_HOME", home)
	t.Setenv("SHIPYARD_REGION", "")
	t.Setenv("SHIPYARD_PROVIDER", "")
	t.Setenv("SHIPYARD_ORG_DOMAIN", "")
	t.Setenv("SHIPYARD_CREDENTIAL_BACKEND", "")
	t.Setenv("SHIPYARD_K8S_IN_CLUSTER", "")
	t.Setenv("SHIPYARD_K8S_CONTEXT", "")
	t.Setenv("SHIPYARD_K8S_NAMESPACE", "")
	t.Setenv("SHIPYARD_K8S_DOMAIN", "")
	t.Setenv("SHIPYARD_K8S_INGRESS_CLASS", "")
	t.Setenv("SHIPYARD_K8S_ZONE", "")
	t.Setenv("SHIPYARD_K8S_TLS_ISSUER", "")
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(`apiVersion: v1
kind: Config
clusters:
- name: demo
  cluster:
    server: https://127.0.0.1:1
    insecure-skip-tls-verify: true
contexts:
- name: demo
  context:
    cluster: demo
    user: demo
current-context: demo
users:
- name: demo
  user:
    token: test-token
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHIPYARD_KUBECONFIG", path)
	svc, err := Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.Providers["kubernetes"]; !ok {
		t.Fatal("kubernetes provider was not registered")
	}
	if _, ok := svc.Providers["fake"]; !ok {
		t.Fatal("fake provider is no longer registered")
	}
	if svc.Config.DefaultProvider != "fake" {
		t.Fatalf("default provider = %s", svc.Config.DefaultProvider)
	}
}
