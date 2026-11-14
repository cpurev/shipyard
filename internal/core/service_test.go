package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cpurev/shipyard/internal/audit"
	"github.com/cpurev/shipyard/internal/config"
	"github.com/cpurev/shipyard/internal/providers"
	"github.com/cpurev/shipyard/internal/providers/fake"
	"github.com/cpurev/shipyard/internal/secrets"
)

func TestDeployScanAndAudit(t *testing.T) {
	svc := newFakeService(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"express":"4.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	aws := "AKIA" + "IOSFODNN7EXAMPLE"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("AWS_ACCESS_KEY_ID="+aws+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Deploy(ctx, DeployOptions{Name: "hello", Dir: dir})
	var scanErr *secrets.ScanError
	if !errors.As(err, &scanErr) {
		t.Fatalf("err = %v", err)
	}
	apps, err := svc.ListApps(ctx, "fake")
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 0 {
		t.Fatalf("app was recorded despite the scan: %+v", apps)
	}

	dep, err := svc.Deploy(ctx, DeployOptions{Name: "hello", Dir: dir, AllowSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	if dep.Framework != "node" || dep.Region != config.DefaultRegion {
		t.Fatalf("deployment = %+v", dep)
	}
	if !strings.Contains(dep.Message, "allow-secrets") {
		t.Fatalf("message = %s", dep.Message)
	}
	events, err := svc.Audit.List(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != "deploy" {
		t.Fatalf("events = %+v", events)
	}
	if err := svc.SetSecret(ctx, "", "hello", "API_KEY", "test-secret-value"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Status(ctx, "vercel", "hello"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("vercel status = %v", err)
	}
}

func TestInvalidName(t *testing.T) {
	svc := newFakeService(t)
	if _, err := svc.Deploy(context.Background(), DeployOptions{Name: "Hello", Dir: t.TempDir()}); err == nil {
		t.Fatal("expected invalid name")
	}
}

func newFakeService(t *testing.T) *Service {
	t.Helper()
	p := fake.New(filepath.Join(t.TempDir(), "fake.json"))
	if err := p.Connect(context.Background(), providers.Credentials{Token: "local"}); err != nil {
		t.Fatal(err)
	}
	return &Service{
		Config:    config.Default(),
		Providers: map[string]providers.Provider{"fake": p},
		Audit:     audit.NewMemory(),
	}
}
