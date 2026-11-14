// Package fake is an in-memory provider for tests and local demos.
// It never contacts a network. State is a JSON file mode 0600 so the CLI
// can deploy and then read status in a later process.
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/shipyardhq/shipyard/internal/providers"
)

// Provider implements providers.Provider without hosting anything.
type Provider struct {
	path      string
	mu        sync.Mutex
	connected bool
	state     document
}

type document struct {
	Apps map[string]*app `json:"apps"`
}

type app struct {
	Name      string              `json:"name"`
	URL       string              `json:"url"`
	Status    string              `json:"status"`
	Region    string              `json:"region"`
	Framework string              `json:"framework"`
	Secrets   map[string]string   `json:"secrets,omitempty"`
	Logs      []providers.LogLine `json:"logs"`
	UpdatedAt time.Time           `json:"updatedAt"`
}

// New loads state from path. An empty path keeps state in memory only.
func New(path string) *Provider {
	p := &Provider{path: path, state: document{Apps: map[string]*app{}}}
	if path == "" {
		return p
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return p
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return p
	}
	if doc.Apps == nil {
		doc.Apps = map[string]*app{}
	}
	p.state = doc
	return p
}

func (p *Provider) Name() string { return "fake" }

// Connect marks the fake provider ready. Any non-empty token is accepted.
func (p *Provider) Connect(_ context.Context, creds providers.Credentials) error {
	if creds.Token == "" {
		return errors.New("fake: token is required")
	}
	p.mu.Lock()
	p.connected = true
	p.mu.Unlock()
	return nil
}

// Deploy records an app and a URL on the .fake.shipyard.local domain.
func (p *Provider) Deploy(_ context.Context, req providers.DeployRequest) (*providers.Deployment, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	p.mu.Lock()
	defer p.mu.Unlock()
	existing := p.state.Apps[req.Name]
	secrets := map[string]string{}
	var logs []providers.LogLine
	if existing != nil {
		secrets = existing.Secrets
		logs = existing.Logs
	}
	record := &app{
		Name:      req.Name,
		URL:       "https://" + req.Name + ".fake.shipyard.local",
		Status:    "READY",
		Region:    req.Region,
		Framework: req.Framework,
		Secrets:   secrets,
		Logs:      logs,
		UpdatedAt: now,
	}
	record.Logs = append(record.Logs, providers.LogLine{
		Time:    now,
		Level:   "info",
		Message: fmt.Sprintf("deployed %s (%s) to the fake provider in %s", req.Name, emptyFramework(req.Framework), req.Region),
	})
	if p.state.Apps == nil {
		p.state.Apps = map[string]*app{}
	}
	p.state.Apps[req.Name] = record
	if err := p.saveLocked(); err != nil {
		return nil, err
	}
	return &providers.Deployment{
		ID:        "fake_" + req.Name,
		AppName:   record.Name,
		URL:       record.URL,
		Status:    record.Status,
		Provider:  "fake",
		Region:    record.Region,
		Framework: record.Framework,
		Message:   "local fake provider; nothing was deployed",
	}, nil
}

func (p *Provider) Status(_ context.Context, name string) (*providers.AppStatus, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	record, ok := p.state.Apps[name]
	if !ok {
		return nil, fmt.Errorf("fake: %w", providers.ErrNotFound)
	}
	return statusOf(record), nil
}

func (p *Provider) Logs(_ context.Context, name string, opts providers.LogOptions) ([]providers.LogLine, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	record, ok := p.state.Apps[name]
	if !ok {
		return nil, fmt.Errorf("fake: %w", providers.ErrNotFound)
	}
	return providers.LimitLines(append([]providers.LogLine{}, record.Logs...), opts), nil
}

// SetSecret stores the value locally and writes a log line that names the
// key and not the value.
func (p *Provider) SetSecret(_ context.Context, name, key, value string) error {
	if err := p.ready(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	record, ok := p.state.Apps[name]
	if !ok {
		return fmt.Errorf("fake: %w", providers.ErrNotFound)
	}
	if record.Secrets == nil {
		record.Secrets = map[string]string{}
	}
	record.Secrets[key] = value
	now := time.Now().UTC()
	record.UpdatedAt = now
	record.Logs = append(record.Logs, providers.LogLine{
		Time:    now,
		Level:   "info",
		Message: "updated secret " + key,
	})
	return p.saveLocked()
}

func (p *Provider) ListApps(context.Context) ([]providers.App, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.state.Apps))
	for name := range p.state.Apps {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]providers.App, 0, len(names))
	for _, name := range names {
		record := p.state.Apps[name]
		out = append(out, providers.App{
			Name:     record.Name,
			ID:       "fake_" + record.Name,
			URL:      record.URL,
			Provider: "fake",
			Region:   record.Region,
			Status:   record.Status,
		})
	}
	return out, nil
}

// ProvisionAccount is a stub.
//
// TODO: do not implement. Automatic account creation would break the
// providers' terms and be blocked by captchas and KYC checks.
func (p *Provider) ProvisionAccount(context.Context, providers.ProvisionRequest) (*providers.Account, error) {
	return providers.RejectProvisionAccount()
}

func (p *Provider) ready() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.connected {
		return fmt.Errorf("fake: %w", providers.ErrNotConnected)
	}
	return nil
}

func (p *Provider) saveLocked() error {
	if p.path == "" {
		return nil
	}
	dir := filepath.Dir(p.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("fake: state dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("fake: chmod state dir: %w", err)
	}
	data, err := json.MarshalIndent(p.state, "", "  ")
	if err != nil {
		return fmt.Errorf("fake: encode state: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".fake-*")
	if err != nil {
		return fmt.Errorf("fake: create state: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, p.path); err != nil {
		return err
	}
	cleanup = false
	return os.Chmod(p.path, 0o600)
}

func statusOf(record *app) *providers.AppStatus {
	return &providers.AppStatus{
		Name:      record.Name,
		ID:        "fake_" + record.Name,
		URL:       record.URL,
		Status:    record.Status,
		Region:    record.Region,
		Provider:  "fake",
		UpdatedAt: record.UpdatedAt,
		Message:   "local fake provider",
	}
}

func emptyFramework(framework string) string {
	if framework == "" {
		return "unknown"
	}
	return framework
}

var _ providers.Provider = (*Provider)(nil)
