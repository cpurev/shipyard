// Package config is the operator's local Shipyard settings.
// The default region is in the EU.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// DefaultRegion is the Cloud Run location used when the operator has not
	// chosen one. It is in the EU (Belgium). Vercel maps this to arn1.
	DefaultRegion = "europe-west1"

	// DefaultProvider is fake so a fresh install never calls a cloud API.
	DefaultProvider = "fake"

	// CredentialFileBackend stores tokens in a file mode 0600.
	CredentialFileBackend = "file"

	// CredentialKeychainBackend selects the OS keychain stub.
	CredentialKeychainBackend = "keychain"

	configFile = "config.json"
)

// Config is the on-disk settings file.
type Config struct {
	// Region is the default Cloud Run location. EU by default.
	Region string `json:"region"`
	// DefaultProvider is fake, vercel, or gcp.
	DefaultProvider string `json:"defaultProvider"`
	// OrgDomain is the company email domain reserved for the login gate.
	OrgDomain string `json:"orgDomain,omitempty"`
	// CredentialBackend is "file" or "keychain".
	CredentialBackend string `json:"credentialBackend,omitempty"`
}

// Default returns the settings used when no file and no environment are set.
func Default() Config {
	return Config{
		Region:            DefaultRegion,
		DefaultProvider:   DefaultProvider,
		CredentialBackend: CredentialFileBackend,
	}
}

// Dir is the configuration directory. SHIPYARD_HOME overrides the
// OS user config directory.
func Dir() (string, error) {
	if v := os.Getenv("SHIPYARD_HOME"); v != "" {
		return v, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(base, "shipyard"), nil
}

// Load reads config.json from dir, then applies environment overrides.
// A missing file yields Default.
func Load(dir string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(filepath.Join(dir, configFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err == nil && len(data) > 0 {
		var overlay Config
		if err := json.Unmarshal(data, &overlay); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
		if overlay.Region != "" {
			cfg.Region = overlay.Region
		}
		if overlay.DefaultProvider != "" {
			cfg.DefaultProvider = overlay.DefaultProvider
		}
		if overlay.OrgDomain != "" {
			cfg.OrgDomain = overlay.OrgDomain
		}
		if overlay.CredentialBackend != "" {
			cfg.CredentialBackend = overlay.CredentialBackend
		}
	}
	if v := os.Getenv("SHIPYARD_REGION"); v != "" {
		cfg.Region = v
	}
	if v := os.Getenv("SHIPYARD_PROVIDER"); v != "" {
		cfg.DefaultProvider = v
	}
	if v := os.Getenv("SHIPYARD_ORG_DOMAIN"); v != "" {
		cfg.OrgDomain = v
	}
	if v := os.Getenv("SHIPYARD_CREDENTIAL_BACKEND"); v != "" {
		cfg.CredentialBackend = v
	}
	if cfg.Region == "" {
		cfg.Region = DefaultRegion
	}
	if cfg.DefaultProvider == "" {
		cfg.DefaultProvider = DefaultProvider
	}
	if cfg.CredentialBackend == "" {
		cfg.CredentialBackend = CredentialFileBackend
	}
	return cfg, nil
}
