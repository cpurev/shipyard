package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFakeQuickstart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHIPYARD_HOME", home)
	t.Setenv("SHIPYARD_REGION", "")
	t.Setenv("SHIPYARD_PROVIDER", "")
	t.Setenv("SHIPYARD_CREDENTIAL_BACKEND", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"hello"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mustRun(t, "connect", "fake")
	out := mustRun(t, "deploy", "hello", "--dir", dir)
	if !strings.Contains(out, "hello deployed") || !strings.Contains(out, "europe-west1") || !strings.Contains(out, "fake.shipyard.local") {
		t.Fatalf("deploy output:\n%s", out)
	}
	if !strings.Contains(mustRun(t, "status", "hello"), "READY") {
		t.Fatal(mustRun(t, "status", "hello"))
	}
	secret := "test-secret-value"
	mustRun(t, "secrets", "set", "hello", "API_KEY", "--value", secret)
	logs := mustRun(t, "logs", "hello")
	if strings.Contains(logs, secret) || !strings.Contains(logs, "updated secret API_KEY") {
		t.Fatalf("logs:\n%s", logs)
	}
	apps := mustRun(t, "apps")
	if !strings.Contains(apps, "hello") {
		t.Fatalf("apps:\n%s", apps)
	}
	info, err := os.Stat(filepath.Join(home, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials mode = %o", info.Mode().Perm())
	}
	status := mustRun(t, "connect", "status")
	if !strings.Contains(status, "europe-west1") || !strings.Contains(status, "fake") || !strings.Contains(status, "connected") {
		t.Fatalf("status:\n%s", status)
	}
}

func TestSecretScanBlocksDeploy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHIPYARD_HOME", home)
	t.Setenv("SHIPYARD_REGION", "")
	t.Setenv("SHIPYARD_PROVIDER", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"hello"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("AWS_ACCESS_KEY_ID="+"AKIA"+"IOSFODNN7EXAMPLE"+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "connect", "fake")
	var stdout, stderr bytes.Buffer
	err := Execute([]string{"deploy", "hello", "--dir", dir}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "secret scan") {
		t.Fatalf("err=%v stdout=%s", err, stdout.String())
	}
	apps := mustRun(t, "apps")
	if strings.Contains(apps, "hello") {
		t.Fatalf("deploy was recorded:\n%s", apps)
	}
}

func TestConnectTokenStaysLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHIPYARD_HOME", home)
	t.Setenv("VERCEL_TOKEN", "")
	t.Setenv("VERCEL_OAUTH_CLIENT_ID", "client-id")
	out := mustRun(t, "connect", "vercel", "--oauth")
	if !strings.Contains(out, "https://vercel.com/oauth/authorize") || !strings.Contains(out, "client_id=client-id") {
		t.Fatalf("oauth url:\n%s", out)
	}
	mustRun(t, "connect", "vercel", "--token", "test-vercel-token", "--team", "team_1")
	data, err := os.ReadFile(filepath.Join(home, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "test-vercel-token") || !strings.Contains(string(data), "team_1") {
		t.Fatal("token was not stored")
	}
	info, err := os.Stat(filepath.Join(home, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	status := mustRun(t, "connect", "status")
	if !strings.Contains(status, "vercel") || !strings.Contains(status, "connected") {
		t.Fatalf("status:\n%s", status)
	}
	if strings.Contains(status, "test-vercel-token") {
		t.Fatal("status printed the token")
	}

	t.Setenv("GCP_OAUTH_CLIENT_ID", "gcp-client")
	gcpOut := mustRun(t, "connect", "gcp", "--project", "demo-project", "--oauth")
	if !strings.Contains(gcpOut, "client_id=gcp-client") || !strings.Contains(gcpOut, "demo-project") {
		t.Fatalf("gcp oauth:\n%s", gcpOut)
	}
	key := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(key, []byte(`{"type":"service_account","project_id":"demo-project"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "connect", "gcp", "--service-account", key)
	if !strings.Contains(mustRun(t, "connect", "status"), "gcp") {
		t.Fatal(mustRun(t, "connect", "status"))
	}
}

func TestKeychainStub(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHIPYARD_HOME", home)
	t.Setenv("SHIPYARD_CREDENTIAL_BACKEND", "keychain")
	var stdout, stderr bytes.Buffer
	err := Execute([]string{"connect", "fake"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "keychain") {
		t.Fatalf("err=%v", err)
	}
}

func TestHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Execute(nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "fake") {
		t.Fatalf("help:\n%s", stdout.String())
	}
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := Execute(args, &stdout, &stderr); err != nil {
		t.Fatalf("%s: %v\nstderr: %s\nstdout: %s", strings.Join(args, " "), err, stderr.String(), stdout.String())
	}
	return stdout.String()
}
