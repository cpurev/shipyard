// Package core is the deploy flow shared by the CLI and the MCP server.
package core

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/cpurev/shipyard/internal/audit"
	"github.com/cpurev/shipyard/internal/config"
	"github.com/cpurev/shipyard/internal/credentials"
	"github.com/cpurev/shipyard/internal/detect"
	"github.com/cpurev/shipyard/internal/providers"
	"github.com/cpurev/shipyard/internal/secrets"
)

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const maxSecretBytes = 32 << 10

// Service runs detect, the secret scan, and one provider.
type Service struct {
	Config    config.Config
	Store     credentials.Store
	Providers map[string]providers.Provider
	Audit     audit.Logger
}

// DeployOptions is a deploy requested by the CLI or an agent.
type DeployOptions struct {
	Name         string
	Dir          string
	Provider     string
	Image        string
	AllowSecrets bool
}

// Open loads config and credentials and builds the providers that are connected.
// Building a client does not call the provider.
func Open(ctx context.Context) (*Service, error) {
	home, err := config.Dir()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(home)
	if err != nil {
		return nil, err
	}
	store, err := openStore(home, cfg)
	if err != nil {
		return nil, err
	}
	all, err := store.LoadAll(ctx)
	if err != nil {
		return nil, err
	}
	svc := &Service{
		Config:    cfg,
		Store:     store,
		Providers: map[string]providers.Provider{},
		Audit:     audit.NewMemory(),
	}
	if err := registerProviders(ctx, svc, home, all); err != nil {
		return nil, err
	}
	return svc, nil
}

// Connect stores credentials for an account the operator already owns.
func (s *Service) Connect(ctx context.Context, provider string, creds providers.Credentials) error {
	if s.Store == nil {
		return fmt.Errorf("credential store is not configured")
	}
	if !providers.CredentialsReady(provider, creds) {
		return fmt.Errorf("credentials for %s are incomplete", provider)
	}
	if err := s.Store.Save(ctx, provider, creds); err != nil {
		return err
	}
	return s.Audit.Record(ctx, audit.Event{
		Action:   "connect",
		Provider: provider,
		Message:  "stored local credentials",
	})
}

// Deploy detects the framework, scans for secrets, and calls the provider.
func (s *Service) Deploy(ctx context.Context, opts DeployOptions) (*providers.Deployment, error) {
	if err := providers.ValidateAppName(opts.Name); err != nil {
		return nil, err
	}
	name, provider, err := s.resolve(opts.Provider)
	if err != nil {
		return nil, err
	}
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	detected, err := detect.Detect(dir)
	if err != nil {
		return nil, err
	}
	var findings []secrets.Finding
	findings, err = secrets.Scan(dir)
	if err != nil {
		return nil, err
	}
	if len(findings) > 0 && !opts.AllowSecrets {
		return nil, &secrets.ScanError{Findings: findings}
	}
	region := s.Config.Region
	if region == "" {
		region = config.DefaultRegion
	}
	dep, err := provider.Deploy(ctx, providers.DeployRequest{
		Name:      opts.Name,
		Dir:       dir,
		Region:    region,
		Framework: string(detected.Framework),
		Image:     opts.Image,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if opts.AllowSecrets && len(findings) > 0 {
		note := fmt.Sprintf("secret scan found %d match(es); deployed anyway because allow-secrets was set", len(findings))
		if dep.Message != "" {
			dep.Message = dep.Message + "; " + note
		} else {
			dep.Message = note
		}
	}
	if err := s.Audit.Record(ctx, audit.Event{
		Action:   "deploy",
		Provider: name,
		App:      opts.Name,
		Message:  dep.Status,
	}); err != nil {
		return nil, err
	}
	return dep, nil
}

// Status returns the provider's view of one app.
func (s *Service) Status(ctx context.Context, providerName, app string) (*providers.AppStatus, error) {
	if err := providers.ValidateAppName(app); err != nil {
		return nil, err
	}
	name, provider, err := s.resolve(providerName)
	if err != nil {
		return nil, err
	}
	st, err := provider.Status(ctx, app)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return st, nil
}

// Logs returns provider log lines.
func (s *Service) Logs(ctx context.Context, providerName, app string, opts providers.LogOptions) ([]providers.LogLine, error) {
	if err := providers.ValidateAppName(app); err != nil {
		return nil, err
	}
	name, provider, err := s.resolve(providerName)
	if err != nil {
		return nil, err
	}
	lines, err := provider.Logs(ctx, app, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return lines, nil
}

// SetSecret stores one secret on the app. The value is not written to the audit log.
func (s *Service) SetSecret(ctx context.Context, providerName, app, key, value string) error {
	if err := providers.ValidateAppName(app); err != nil {
		return err
	}
	if !envKey.MatchString(key) || len(key) > 256 {
		return fmt.Errorf("secret key %q must be an environment variable name", key)
	}
	if value == "" {
		return fmt.Errorf("secret value is empty")
	}
	if len(value) > maxSecretBytes {
		return fmt.Errorf("secret value is longer than %d bytes", maxSecretBytes)
	}
	name, provider, err := s.resolve(providerName)
	if err != nil {
		return err
	}
	if err := provider.SetSecret(ctx, app, key, value); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return s.Audit.Record(ctx, audit.Event{
		Action:   "set_secret",
		Provider: name,
		App:      app,
		Message:  "updated " + key,
	})
}

// ListApps returns the apps on one provider, sorted by name.
func (s *Service) ListApps(ctx context.Context, providerName string) ([]providers.App, error) {
	name, provider, err := s.resolve(providerName)
	if err != nil {
		return nil, err
	}
	apps, err := provider.ListApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	return apps, nil
}

func (s *Service) resolve(name string) (string, providers.Provider, error) {
	if name == "" {
		name = s.Config.DefaultProvider
	}
	if name == "" {
		name = config.DefaultProvider
	}
	provider, ok := s.Providers[name]
	if !ok {
		return name, nil, fmt.Errorf("provider %q is not connected; run `shipyard connect %s`", name, name)
	}
	return name, provider, nil
}
