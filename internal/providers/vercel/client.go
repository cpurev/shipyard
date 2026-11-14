package vercel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cpurev/shipyard/internal/version"
)

const (
	defaultBaseURL = "https://api.vercel.com"
	// maxInlineFile is the largest file this skeleton will inline as base64.
	// TODO: use the Vercel files API for larger blobs. This path is the
	// straightforward deployments REST call.
	maxInlineFile = 1 << 20
	maxFiles      = 1000
	maxTotalBytes = 10 << 20
)

// Client is a small Vercel REST client. Tests point BaseURL at httptest.
type Client struct {
	BaseURL    string
	Token      string
	TeamID     string
	HTTPClient *http.Client
}

// File is one inlined project file.
type File struct {
	Path string
	Data []byte
}

type createDeploymentRequest struct {
	Name            string           `json:"name"`
	Files           []filePayload    `json:"files"`
	ProjectSettings *projectSettings `json:"projectSettings,omitempty"`
	Target          string           `json:"target"`
	Regions         []string         `json:"regions,omitempty"`
}

type filePayload struct {
	File     string `json:"file"`
	Data     string `json:"data"`
	Encoding string `json:"encoding"`
}

type projectSettings struct {
	Framework string `json:"framework,omitempty"`
}

// Deployment is the subset of the Vercel deployment resource we use.
type Deployment struct {
	ID         string
	Name       string
	URL        string
	ReadyState string
	Created    time.Time
}

// Project is the subset of a Vercel project we use.
type Project struct {
	ID     string
	Name   string
	URL    string
	Status string
}

// Event is one build log event.
type Event struct {
	Type    string
	Created time.Time
	Text    string
}

type deploymentJSON struct {
	ID         string `json:"id"`
	UID        string `json:"uid"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	ReadyState string `json:"readyState"`
	State      string `json:"state"`
	Created    int64  `json:"created"`
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultBaseURL
}

// CreateDeployment calls POST /v13/deployments.
func (c *Client) CreateDeployment(ctx context.Context, name, framework, region string, files []File) (*Deployment, error) {
	body := createDeploymentRequest{
		Name:    name,
		Target:  "production",
		Regions: []string{region},
		Files:   make([]filePayload, 0, len(files)),
	}
	if framework != "" {
		body.ProjectSettings = &projectSettings{Framework: framework}
	}
	for _, file := range files {
		body.Files = append(body.Files, filePayload{
			File:     file.Path,
			Data:     base64.StdEncoding.EncodeToString(file.Data),
			Encoding: "base64",
		})
	}
	var out deploymentJSON
	if err := c.do(ctx, http.MethodPost, "/v13/deployments", nil, body, &out); err != nil {
		return nil, err
	}
	return out.deployment(), nil
}

// GetDeployment calls GET /v13/deployments/{id}.
func (c *Client) GetDeployment(ctx context.Context, id string) (*Deployment, error) {
	var out deploymentJSON
	if err := c.do(ctx, http.MethodGet, "/v13/deployments/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return out.deployment(), nil
}

// ListDeployments calls GET /v6/deployments.
func (c *Client) ListDeployments(ctx context.Context, app string, limit int) ([]Deployment, error) {
	if limit <= 0 {
		limit = 1
	}
	q := url.Values{}
	q.Set("app", app)
	q.Set("limit", fmt.Sprintf("%d", limit))
	var out struct {
		Deployments []deploymentJSON `json:"deployments"`
	}
	if err := c.do(ctx, http.MethodGet, "/v6/deployments", q, nil, &out); err != nil {
		return nil, err
	}
	deps := make([]Deployment, 0, len(out.Deployments))
	for _, item := range out.Deployments {
		deps = append(deps, *item.deployment())
	}
	return deps, nil
}

// DeploymentEvents calls GET /v3/deployments/{id}/events.
func (c *Client) DeploymentEvents(ctx context.Context, id string) ([]Event, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/v3/deployments/"+url.PathEscape(id)+"/events", nil, nil, &raw); err != nil {
		return nil, err
	}
	return parseEvents(raw)
}

// ListProjects calls GET /v9/projects.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out struct {
		Projects []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Targets struct {
				Production struct {
					URL        string `json:"url"`
					ReadyState string `json:"readyState"`
				} `json:"production"`
			} `json:"targets"`
		} `json:"projects"`
	}
	if err := c.do(ctx, http.MethodGet, "/v9/projects", nil, nil, &out); err != nil {
		return nil, err
	}
	projects := make([]Project, 0, len(out.Projects))
	for _, item := range out.Projects {
		projects = append(projects, Project{
			ID:     item.ID,
			Name:   item.Name,
			URL:    ensureHTTPS(item.Targets.Production.URL),
			Status: strings.ToUpper(item.Targets.Production.ReadyState),
		})
	}
	return projects, nil
}

// CreateEnv calls POST /v10/projects/{idOrName}/env.
func (c *Client) CreateEnv(ctx context.Context, project, key, value string) error {
	body := map[string]any{
		"key":    key,
		"value":  value,
		"type":   "encrypted",
		"target": []string{"production", "preview"},
	}
	return c.do(ctx, http.MethodPost, "/v10/projects/"+url.PathEscape(project)+"/env", nil, body, nil)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	if c.Token == "" {
		return fmt.Errorf("vercel: missing token")
	}
	u, err := url.Parse(c.base() + path)
	if err != nil {
		return fmt.Errorf("vercel: url: %w", err)
	}
	q := u.Query()
	for key, values := range query {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	if c.TeamID != "" {
		q.Set("teamId", c.TeamID)
	}
	u.RawQuery = q.Encode()
	var buf io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("vercel: encode body: %w", err)
		}
		buf = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), buf)
	if err != nil {
		return fmt.Errorf("vercel: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("User-Agent", "shipyard/"+version.Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("vercel: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("vercel: read response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("vercel: %s %s: %s: %s", method, path, resp.Status, truncate(payload))
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("vercel: decode %s: %w", path, err)
	}
	return nil
}

func (d deploymentJSON) deployment() *Deployment {
	id := d.ID
	if id == "" {
		id = d.UID
	}
	state := d.ReadyState
	if state == "" {
		state = d.State
	}
	var created time.Time
	if d.Created > 0 {
		created = time.UnixMilli(d.Created).UTC()
	}
	return &Deployment{
		ID:         id,
		Name:       d.Name,
		URL:        ensureHTTPS(d.URL),
		ReadyState: strings.ToUpper(state),
		Created:    created,
	}
}

func parseEvents(raw json.RawMessage) ([]Event, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return []Event{}, nil
	}
	var items []eventJSON
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("vercel: decode events: %w", err)
		}
	} else {
		for _, line := range bytes.Split(raw, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var item eventJSON
			if err := json.Unmarshal(line, &item); err != nil {
				return nil, fmt.Errorf("vercel: decode event: %w", err)
			}
			items = append(items, item)
		}
	}
	events := make([]Event, 0, len(items))
	for _, item := range items {
		text := item.Text
		if text == "" {
			text = item.Payload.Text
		}
		var created time.Time
		if item.Created > 0 {
			created = time.UnixMilli(item.Created).UTC()
		}
		events = append(events, Event{Type: item.Type, Created: created, Text: text})
	}
	return events, nil
}

type eventJSON struct {
	Type    string `json:"type"`
	Created int64  `json:"created"`
	Text    string `json:"text"`
	Payload struct {
		Text string `json:"text"`
	} `json:"payload"`
}

func ensureHTTPS(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	return "https://" + raw
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

// CollectFiles reads a project directory for an inline Vercel deployment.
// It skips dependency and VCS directories, and it never uploads dotenv files
// or private keys. Those belong in set_secret.
func CollectFiles(dir string) ([]File, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("vercel: project dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("vercel: %s is not a directory", dir)
	}
	var files []File
	var total int
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != dir && skipUploadDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if sensitiveFile(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("vercel: read %s: %w", rel, err)
		}
		if len(data) > maxInlineFile {
			return fmt.Errorf("vercel: %s is larger than %d bytes; large-file upload is not in this skeleton", rel, maxInlineFile)
		}
		total += len(data)
		if total > maxTotalBytes {
			return fmt.Errorf("vercel: project is larger than %d bytes for an inline deployment", maxTotalBytes)
		}
		files = append(files, File{Path: filepath.ToSlash(rel), Data: data})
		if len(files) > maxFiles {
			return fmt.Errorf("vercel: more than %d files", maxFiles)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("vercel: %s has no files to deploy", dir)
	}
	return files, nil
}

func skipUploadDir(name string) bool {
	switch name {
	case ".git", "node_modules", ".next", "dist", "build", "__pycache__", ".venv", "venv", "vendor", "coverage":
		return true
	default:
		return false
	}
}

func sensitiveFile(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case ".env", ".env.local", ".env.production", ".env.development", ".env.test":
		return true
	}
	switch filepath.Ext(lower) {
	case ".pem", ".key", ".p12", ".pfx":
		return true
	default:
		return false
	}
}
