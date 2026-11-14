package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultRegionIsEU(t *testing.T) {
	cfg := Default()
	if cfg.Region != "europe-west1" {
		t.Fatalf("region = %s", cfg.Region)
	}
	if cfg.DefaultProvider != "fake" {
		t.Fatalf("provider = %s", cfg.DefaultProvider)
	}
	if cfg.CredentialBackend != CredentialFileBackend {
		t.Fatalf("backend = %s", cfg.CredentialBackend)
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("SHIPYARD_REGION", "")
	t.Setenv("SHIPYARD_PROVIDER", "")
	t.Setenv("SHIPYARD_ORG_DOMAIN", "")
	t.Setenv("SHIPYARD_CREDENTIAL_BACKEND", "")
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != DefaultRegion {
		t.Fatalf("region = %s", cfg.Region)
	}
}

func TestLoadFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"region":"us-central1","defaultProvider":"vercel","orgDomain":"example.com","credentialBackend":"file"}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHIPYARD_REGION", "europe-north1")
	t.Setenv("SHIPYARD_PROVIDER", "")
	t.Setenv("SHIPYARD_ORG_DOMAIN", "")
	t.Setenv("SHIPYARD_CREDENTIAL_BACKEND", "")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "europe-north1" {
		t.Fatalf("env region = %s", cfg.Region)
	}
	if cfg.DefaultProvider != "vercel" {
		t.Fatalf("provider = %s", cfg.DefaultProvider)
	}
	if cfg.OrgDomain != "example.com" {
		t.Fatalf("domain = %s", cfg.OrgDomain)
	}
}
