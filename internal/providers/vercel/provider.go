package vercel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/shipyardhq/shipyard/internal/providers"
)

// Provider deploys a directory with the Vercel deployments REST API.
type Provider struct {
	client    *Client
	connected bool
}

// Options configures a Vercel provider. BaseURL is for tests.
type Options struct {
	Token      string
	TeamID     string
	BaseURL    string
	HTTPClient *http.Client
}

// New builds a provider. A non-empty token marks it connected. New does not
// call Vercel.
func New(opts Options) *Provider {
	p := &Provider{
		client: &Client{
			BaseURL:    opts.BaseURL,
			Token:      opts.Token,
			TeamID:     opts.TeamID,
			HTTPClient: opts.HTTPClient,
		},
	}
	if opts.Token != "" {
		p.connected = true
	}
	return p
}

func (p *Provider) Name() string { return "vercel" }

// Connect stores an API token for an existing Vercel account.
// It does not call Vercel. OAuth code exchange lives in Exchange.
func (p *Provider) Connect(_ context.Context, creds providers.Credentials) error {
	if creds.Token == "" {
		return errors.New("vercel: token is required")
	}
	p.client.Token = creds.Token
	if creds.Extra != nil {
		if team := creds.Extra["teamId"]; team != "" {
			p.client.TeamID = team
		}
	}
	p.connected = true
	return nil
}

// Deploy reads the project directory and calls POST /v13/deployments.
func (p *Provider) Deploy(ctx context.Context, req providers.DeployRequest) (*providers.Deployment, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	if req.Dir == "" {
		return nil, errors.New("vercel: deploy directory is required")
	}
	files, err := CollectFiles(req.Dir)
	if err != nil {
		return nil, err
	}
	region := Region(req.Region)
	created, err := p.client.CreateDeployment(ctx, req.Name, FrameworkPreset(req.Framework), region, files)
	if err != nil {
		return nil, err
	}
	status := created.ReadyState
	if status == "" {
		status = "QUEUED"
	}
	return &providers.Deployment{
		ID:        created.ID,
		AppName:   req.Name,
		URL:       created.URL,
		Status:    status,
		Provider:  "vercel",
		Region:    region,
		Framework: req.Framework,
		Message:   "submitted to the connected Vercel account",
	}, nil
}

// Status reads the newest deployment for the app name.
func (p *Provider) Status(ctx context.Context, app string) (*providers.AppStatus, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	deps, err := p.client.ListDeployments(ctx, app, 1)
	if err != nil {
		return nil, err
	}
	if len(deps) == 0 {
		return nil, fmt.Errorf("vercel: %w", providers.ErrNotFound)
	}
	dep := deps[0]
	return &providers.AppStatus{
		Name:      app,
		ID:        dep.ID,
		URL:       dep.URL,
		Status:    dep.ReadyState,
		Provider:  "vercel",
		UpdatedAt: dep.Created,
	}, nil
}

// Logs reads build events for the newest deployment.
func (p *Provider) Logs(ctx context.Context, app string, opts providers.LogOptions) ([]providers.LogLine, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	deps, err := p.client.ListDeployments(ctx, app, 1)
	if err != nil {
		return nil, err
	}
	if len(deps) == 0 {
		return nil, fmt.Errorf("vercel: %w", providers.ErrNotFound)
	}
	events, err := p.client.DeploymentEvents(ctx, deps[0].ID)
	if err != nil {
		return nil, err
	}
	lines := make([]providers.LogLine, 0, len(events))
	for _, event := range events {
		level := "info"
		if strings.Contains(strings.ToLower(event.Type), "error") || strings.Contains(strings.ToLower(event.Type), "stderr") {
			level = "error"
		}
		lines = append(lines, providers.LogLine{
			Time:    event.Created,
			Level:   level,
			Message: event.Text,
		})
	}
	return providers.LimitLines(lines, opts), nil
}

// SetSecret calls POST /v10/projects/{app}/env on the connected account.
func (p *Provider) SetSecret(ctx context.Context, app, key, value string) error {
	if err := p.ready(); err != nil {
		return err
	}
	return p.client.CreateEnv(ctx, app, key, value)
}

// ListApps calls GET /v9/projects.
func (p *Provider) ListApps(ctx context.Context) ([]providers.App, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	projects, err := p.client.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	apps := make([]providers.App, 0, len(projects))
	for _, project := range projects {
		status := project.Status
		if status == "" {
			status = "UNKNOWN"
		}
		apps = append(apps, providers.App{
			Name:     project.Name,
			ID:       project.ID,
			URL:      project.URL,
			Provider: "vercel",
			Status:   status,
		})
	}
	return apps, nil
}

// ProvisionAccount is a stub.
//
// TODO: do not implement. Automatic account creation would break the
// providers' terms and be blocked by captchas and KYC checks.
func (p *Provider) ProvisionAccount(context.Context, providers.ProvisionRequest) (*providers.Account, error) {
	return providers.RejectProvisionAccount()
}

func (p *Provider) ready() error {
	if p == nil || p.client == nil || !p.connected {
		return fmt.Errorf("vercel: %w", providers.ErrNotConnected)
	}
	return nil
}

var _ providers.Provider = (*Provider)(nil)
