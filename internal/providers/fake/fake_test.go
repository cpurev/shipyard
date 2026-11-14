package fake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shipyardhq/shipyard/internal/providers"
)

func TestFlow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake.json")
	p := New(path)
	ctx := context.Background()
	if _, err := p.Deploy(ctx, providers.DeployRequest{Name: "hello"}); !errors.Is(err, providers.ErrNotConnected) {
		t.Fatalf("deploy before connect: %v", err)
	}
	if err := p.Connect(ctx, providers.Credentials{Token: "local"}); err != nil {
		t.Fatal(err)
	}
	dep, err := p.Deploy(ctx, providers.DeployRequest{
		Name:      "hello",
		Region:    "europe-west1",
		Framework: "node",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dep.URL != "https://hello.fake.shipyard.local" || dep.Region != "europe-west1" || dep.Status != "READY" {
		t.Fatalf("deployment = %+v", dep)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o", info.Mode().Perm())
	}

	reloaded := New(path)
	if err := reloaded.Connect(ctx, providers.Credentials{Token: "local"}); err != nil {
		t.Fatal(err)
	}
	st, err := reloaded.Status(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if st.Region != "europe-west1" || st.URL != dep.URL {
		t.Fatalf("status = %+v", st)
	}
	secret := "super-secret-value"
	if err := reloaded.SetSecret(ctx, "hello", "API_KEY", secret); err != nil {
		t.Fatal(err)
	}
	lines, err := reloaded.Logs(ctx, "hello", providers.LogOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	blob := ""
	for _, line := range lines {
		blob += line.Message + "\n"
		if strings.Contains(line.Message, secret) {
			t.Fatalf("log line contains the secret: %s", line.Message)
		}
	}
	if !strings.Contains(blob, "updated secret API_KEY") {
		t.Fatalf("logs = %s", blob)
	}
	apps, err := reloaded.ListApps(ctx)
	if err != nil || len(apps) != 1 || apps[0].Name != "hello" {
		t.Fatalf("apps = %+v err=%v", apps, err)
	}
	if _, err := reloaded.ProvisionAccount(ctx, providers.ProvisionRequest{Email: "a@example.com"}); !errors.Is(err, providers.ErrNotSupported) {
		t.Fatalf("provision = %v", err)
	}
}
