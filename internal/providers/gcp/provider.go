// Package gcp deploys to Cloud Run on a project the operator already owns.
// API calls go through the generated Go clients for the Cloud Run Admin API
// (google.golang.org/api/run/v2) and Cloud Logging
// (google.golang.org/api/logging/v2). Tests pass an httptest endpoint.
// This package never creates a Google account or a project.
package gcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
	loggingapi "google.golang.org/api/logging/v2"
	"google.golang.org/api/option"
	runapi "google.golang.org/api/run/v2"

	"github.com/cpurev/shipyard/internal/config"
	"github.com/cpurev/shipyard/internal/providers"
	"github.com/cpurev/shipyard/internal/version"
)

// vercelStyle is a Vercel region code, which is not a Cloud Run location.
var vercelStyle = regexp.MustCompile(`^[a-z]{3}[0-9]$`)

// Provider talks to Cloud Run and Cloud Logging.
type Provider struct {
	project   string
	region    string
	connected bool
	run       *runapi.Service
	logs      *loggingapi.Service
}

// Options configures the Cloud Run client. Endpoint and HTTPClient are for tests.
type Options struct {
	Project         string
	Region          string
	Token           string
	CredentialsJSON []byte
	Endpoint        string
	HTTPClient      *http.Client
}

// New builds Cloud Run and Logging clients. It does not call Google until a
// later method does.
func New(ctx context.Context, opts Options) (*Provider, error) {
	if opts.Project == "" {
		return nil, errors.New("gcp: project is required")
	}
	if opts.Token == "" && len(opts.CredentialsJSON) == 0 {
		return nil, errors.New("gcp: token or service account JSON is required")
	}
	runSvc, logSvc, err := newClients(ctx, opts)
	if err != nil {
		return nil, err
	}
	region := opts.Region
	if region == "" {
		region = config.DefaultRegion
	}
	return &Provider{
		project:   opts.Project,
		region:    region,
		connected: true,
		run:       runSvc,
		logs:      logSvc,
	}, nil
}

func newClients(ctx context.Context, opts Options) (*runapi.Service, *loggingapi.Service, error) {
	base := clientOptions(opts)
	runSvc, err := runapi.NewService(ctx, append([]option.ClientOption{}, base...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("gcp: cloud run client: %w", err)
	}
	logSvc, err := loggingapi.NewService(ctx, append([]option.ClientOption{}, base...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("gcp: logging client: %w", err)
	}
	if opts.Endpoint != "" {
		runSvc.BasePath = withSlash(opts.Endpoint)
		logSvc.BasePath = withSlash(opts.Endpoint)
	}
	runSvc.UserAgent = "shipyard/" + version.Version
	logSvc.UserAgent = "shipyard/" + version.Version
	return runSvc, logSvc, nil
}

func clientOptions(opts Options) []option.ClientOption {
	var out []option.ClientOption
	if opts.Endpoint != "" {
		out = append(out, option.WithEndpoint(withSlash(opts.Endpoint)))
	}
	// WithHTTPClient replaces the generated client's transport entirely, so the
	// Google auth wrapper never adds a bearer token. Attach the token here
	// when the caller passed one, including the httptest client used in tests.
	switch {
	case opts.Token != "":
		out = append(out, option.WithHTTPClient(authorizedClient(opts.HTTPClient, opts.Token)))
	case len(opts.CredentialsJSON) > 0:
		out = append(out, option.WithAuthCredentialsJSON(option.ServiceAccount, opts.CredentialsJSON))
		if opts.HTTPClient != nil {
			out = append(out, option.WithHTTPClient(opts.HTTPClient))
		}
	}
	return out
}

func authorizedClient(base *http.Client, token string) *http.Client {
	if base == nil {
		base = &http.Client{Timeout: 60 * time.Second}
	}
	timeout := base.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &oauth2.Transport{
			Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token}),
			Base:   base.Transport,
		},
	}
}

func withSlash(endpoint string) string {
	if strings.HasSuffix(endpoint, "/") {
		return endpoint
	}
	return endpoint + "/"
}

func (p *Provider) Name() string { return "gcp" }

// Connect rebuilds the clients from credentials for an existing project.
func (p *Provider) Connect(ctx context.Context, creds providers.Credentials) error {
	region := p.region
	next, err := New(ctx, Options{
		Project:         creds.Project,
		Region:          region,
		Token:           creds.Token,
		CredentialsJSON: creds.ServiceAccountJSON,
		Endpoint:        endpointOf(p),
		HTTPClient:      httpClientOf(p),
	})
	if err != nil {
		return err
	}
	*p = *next
	return nil
}

func (p *Provider) Deploy(ctx context.Context, req providers.DeployRequest) (*providers.Deployment, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	if req.Image == "" {
		return nil, errors.New("gcp: Cloud Run needs a container image (--image); this skeleton does not build or push images")
	}
	region, err := p.location(req.Region)
	if err != nil {
		return nil, err
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", p.project, region)
	service := cloudRunService(req.Name, req.Image)
	op, err := p.run.Projects.Locations.Services.Create(parent, service).ServiceId(req.Name).Context(ctx).Do()
	if err != nil && statusCode(err) == http.StatusConflict {
		return p.updateImage(ctx, req, region)
	}
	if err != nil {
		return nil, fmt.Errorf("gcp: create service: %w", err)
	}
	if op != nil && op.Error != nil {
		return nil, fmt.Errorf("gcp: operation %s: %s", op.Name, op.Error.Message)
	}
	dep := &providers.Deployment{
		ID:        operationID(op),
		AppName:   req.Name,
		Status:    operationStatus(op),
		Provider:  "gcp",
		Region:    region,
		Framework: req.Framework,
		Message:   "submitted to Cloud Run with internal ingress; the login gate is not in front of it yet",
	}
	if op != nil && op.Done && op.Error == nil {
		got, statusErr := p.run.Projects.Locations.Services.Get(p.serviceName(region, req.Name)).Context(ctx).Do()
		if statusErr == nil {
			st := statusFromService(got)
			dep.URL = st.URL
			dep.Status = st.Status
			if st.Message != "" {
				dep.Message = st.Message
			}
		}
	}
	return dep, nil
}

func (p *Provider) updateImage(ctx context.Context, req providers.DeployRequest, region string) (*providers.Deployment, error) {
	name := p.serviceName(region, req.Name)
	current, err := p.run.Projects.Locations.Services.Get(name).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gcp: get existing service: %w", err)
	}
	if current.Template == nil || len(current.Template.Containers) == 0 {
		return nil, errors.New("gcp: existing service has no containers")
	}
	current.Template.Containers[0].Image = req.Image
	patch := &runapi.GoogleCloudRunV2Service{Template: current.Template}
	op, err := p.run.Projects.Locations.Services.Patch(name, patch).UpdateMask("template").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gcp: update service: %w", err)
	}
	if op != nil && op.Error != nil {
		return nil, fmt.Errorf("gcp: operation %s: %s", op.Name, op.Error.Message)
	}
	return &providers.Deployment{
		ID:        operationID(op),
		AppName:   req.Name,
		Status:    operationStatus(op),
		Provider:  "gcp",
		Region:    region,
		Framework: req.Framework,
		Message:   "updated the existing Cloud Run service",
	}, nil
}

func (p *Provider) Status(ctx context.Context, app string) (*providers.AppStatus, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	region, err := p.location("")
	if err != nil {
		return nil, err
	}
	got, err := p.run.Projects.Locations.Services.Get(p.serviceName(region, app)).Context(ctx).Do()
	if err != nil {
		if statusCode(err) == http.StatusNotFound {
			return nil, fmt.Errorf("gcp: %w", providers.ErrNotFound)
		}
		return nil, fmt.Errorf("gcp: get service: %w", err)
	}
	return statusFromService(got), nil
}

func (p *Provider) Logs(ctx context.Context, app string, opts providers.LogOptions) ([]providers.LogLine, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	filter := fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s"`, app)
	if !opts.Since.IsZero() {
		filter += fmt.Sprintf(` AND timestamp>="%s"`, opts.Since.UTC().Format(time.RFC3339))
	}
	resp, err := p.logs.Entries.List(&loggingapi.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + p.project},
		Filter:        filter,
		OrderBy:       "timestamp desc",
		PageSize:      int64(limit),
	}).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gcp: list logs: %w", err)
	}
	lines := make([]providers.LogLine, 0, len(resp.Entries))
	for i := len(resp.Entries) - 1; i >= 0; i-- {
		entry := resp.Entries[i]
		msg := entry.TextPayload
		if msg == "" && len(entry.JsonPayload) > 0 {
			msg = string(entry.JsonPayload)
		}
		var when time.Time
		if entry.Timestamp != "" {
			when, _ = time.Parse(time.RFC3339, entry.Timestamp)
		}
		level := strings.ToLower(entry.Severity)
		if level == "" {
			level = "info"
		}
		lines = append(lines, providers.LogLine{Time: when, Level: level, Message: msg})
	}
	return providers.LimitLines(lines, opts), nil
}

// SetSecret adds an environment variable on the Cloud Run service.
//
// TODO: store the value in Secret Manager and reference it from the service
// instead of sending a literal env var. The literal update is the
// straightforward Admin API call.
func (p *Provider) SetSecret(ctx context.Context, app, key, value string) error {
	if err := p.ready(); err != nil {
		return err
	}
	region, err := p.location("")
	if err != nil {
		return err
	}
	name := p.serviceName(region, app)
	current, err := p.run.Projects.Locations.Services.Get(name).Context(ctx).Do()
	if err != nil {
		if statusCode(err) == http.StatusNotFound {
			return fmt.Errorf("gcp: %w", providers.ErrNotFound)
		}
		return fmt.Errorf("gcp: get service: %w", err)
	}
	if current.Template == nil || len(current.Template.Containers) == 0 {
		return errors.New("gcp: service has no containers to update")
	}
	env := current.Template.Containers[0].Env
	replaced := false
	for _, item := range env {
		if item.Name == key {
			item.Value = value
			replaced = true
			break
		}
	}
	if !replaced {
		env = append(env, &runapi.GoogleCloudRunV2EnvVar{Name: key, Value: value})
	}
	current.Template.Containers[0].Env = env
	patch := &runapi.GoogleCloudRunV2Service{Template: current.Template}
	if _, err := p.run.Projects.Locations.Services.Patch(name, patch).UpdateMask("template").Context(ctx).Do(); err != nil {
		return fmt.Errorf("gcp: set secret: %w", err)
	}
	return nil
}

func (p *Provider) ListApps(ctx context.Context) ([]providers.App, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	region, err := p.location("")
	if err != nil {
		return nil, err
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", p.project, region)
	var apps []providers.App
	pageToken := ""
	for page := 0; page < 10; page++ {
		call := p.run.Projects.Locations.Services.List(parent).PageSize(100).Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("gcp: list services: %w", err)
		}
		for _, svc := range resp.Services {
			if svc == nil {
				continue
			}
			st := statusFromService(svc)
			apps = append(apps, providers.App{
				Name:     st.Name,
				ID:       st.ID,
				URL:      st.URL,
				Provider: "gcp",
				Region:   st.Region,
				Status:   st.Status,
			})
		}
		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}
	if apps == nil {
		apps = []providers.App{}
	}
	return apps, nil
}

// ProvisionAccount is a stub.
//
// TODO: do not implement. Automatic account creation would break the
// providers' terms and be blocked by captchas and KYC checks. Creating a
// Google account or a project from here would also spend the operator's
// quota and fail KYC. Connect a project they already own.
func (p *Provider) ProvisionAccount(context.Context, providers.ProvisionRequest) (*providers.Account, error) {
	return providers.RejectProvisionAccount()
}

func (p *Provider) ready() error {
	if p == nil || !p.connected || p.run == nil || p.logs == nil {
		return fmt.Errorf("gcp: %w", providers.ErrNotConnected)
	}
	return nil
}

func (p *Provider) location(requested string) (string, error) {
	region := requested
	if region == "" {
		region = p.region
	}
	if region == "" {
		region = config.DefaultRegion
	}
	if vercelStyle.MatchString(region) {
		return "", fmt.Errorf("gcp: region %q looks like a Vercel region; Cloud Run needs a location such as %s", region, config.DefaultRegion)
	}
	return region, nil
}

func (p *Provider) serviceName(region, app string) string {
	return fmt.Sprintf("projects/%s/locations/%s/services/%s", p.project, region, app)
}

func cloudRunService(name, image string) *runapi.GoogleCloudRunV2Service {
	return &runapi.GoogleCloudRunV2Service{
		Description:   "Deployed by Shipyard",
		Client:        "shipyard",
		ClientVersion: version.Version,
		// Internal until the company login gate exists. The service is not
		// given a public invoker binding.
		Ingress: "INGRESS_TRAFFIC_INTERNAL_ONLY",
		Labels:  map[string]string{"managed-by": "shipyard"},
		Template: &runapi.GoogleCloudRunV2RevisionTemplate{
			Containers: []*runapi.GoogleCloudRunV2Container{{
				Name:  name,
				Image: image,
			}},
		},
	}
}

func statusFromService(svc *runapi.GoogleCloudRunV2Service) *providers.AppStatus {
	name := lastSegment(svc.Name)
	region := regionFromName(svc.Name)
	status := "UNKNOWN"
	message := ""
	if svc.Reconciling {
		status = "DEPLOYING"
	}
	if svc.TerminalCondition != nil {
		message = svc.TerminalCondition.Message
		switch svc.TerminalCondition.State {
		case "CONDITION_SUCCEEDED":
			status = "READY"
		case "CONDITION_FAILED":
			status = "ERROR"
		case "CONDITION_RECONCILING", "CONDITION_PENDING":
			status = "DEPLOYING"
		}
	}
	var updated time.Time
	if svc.UpdateTime != "" {
		updated, _ = time.Parse(time.RFC3339, svc.UpdateTime)
	}
	return &providers.AppStatus{
		Name:      name,
		ID:        svc.Name,
		URL:       svc.Uri,
		Status:    status,
		Region:    region,
		Provider:  "gcp",
		UpdatedAt: updated,
		Message:   message,
	}
}

func operationID(op *runapi.GoogleLongrunningOperation) string {
	if op == nil || op.Name == "" {
		return ""
	}
	return op.Name
}

func operationStatus(op *runapi.GoogleLongrunningOperation) string {
	if op == nil || !op.Done {
		return "DEPLOYING"
	}
	if op.Error != nil {
		return "ERROR"
	}
	return "READY"
}

func statusCode(err error) int {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code
	}
	return 0
}

func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func regionFromName(name string) string {
	parts := strings.Split(name, "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "locations" {
			return parts[i+1]
		}
	}
	return ""
}

func endpointOf(p *Provider) string {
	if p == nil || p.run == nil {
		return ""
	}
	return p.run.BasePath
}

func httpClientOf(*Provider) *http.Client {
	return nil
}

var _ providers.Provider = (*Provider)(nil)
