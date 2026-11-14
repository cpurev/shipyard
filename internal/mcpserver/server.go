// Package mcpserver exposes Shipyard as an MCP server over stdio.
package mcpserver

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shipyardhq/shipyard/internal/core"
	"github.com/shipyardhq/shipyard/internal/providers"
	"github.com/shipyardhq/shipyard/internal/version"
)

// New builds an MCP server with the deploy, status, logs, set_secret, and
// list_apps tools. The server does not listen until Run or Serve.
func New(svc *core.Service, logger *slog.Logger) *mcp.Server {
	if logger == nil {
		logger = slog.Default()
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "shipyard",
		Version: version.Version,
	}, &mcp.ServerOptions{
		Instructions: "Shipyard deploys an internal app onto a cloud account the user already connected. " +
			"The default provider is fake, which only records the app locally. " +
			"vercel and gcp are used only after `shipyard connect`. " +
			"Tools: deploy, status, logs, set_secret, list_apps.",
		Logger: logger,
	})
	h := &handlers{svc: svc}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "deploy",
		Description: "Deploy a local project. Refuses the deploy when the secret scan finds a credential, unless allowSecrets is true. The fake provider does not call a cloud API.",
	}, h.deploy)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "status",
		Description: "Show the status of one app on a connected provider.",
	}, h.status)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "logs",
		Description: "Read recent log lines for one app.",
	}, h.logs)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "set_secret",
		Description: "Store one secret on an app. The value is sent to the provider and is not written to the audit message.",
	}, h.setSecret)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_apps",
		Description: "List apps on one connected provider.",
	}, h.listApps)
	return server
}

// Serve runs the server on stdin and stdout until the client disconnects.
func Serve(ctx context.Context, svc *core.Service, logger *slog.Logger) error {
	return New(svc, logger).Run(ctx, &mcp.StdioTransport{})
}

type handlers struct {
	svc *core.Service
}

type deployIn struct {
	Name         string `json:"name" jsonschema:"application name; lowercase letters, digits, and hyphens"`
	Dir          string `json:"dir,omitempty" jsonschema:"project directory; defaults to the current directory"`
	Provider     string `json:"provider,omitempty" jsonschema:"fake, vercel, or gcp; defaults to the configured provider"`
	Image        string `json:"image,omitempty" jsonschema:"container image; required for gcp"`
	AllowSecrets bool   `json:"allowSecrets,omitempty" jsonschema:"deploy even if the secret scan finds matches"`
}

type deployOut struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Status    string `json:"status"`
	Provider  string `json:"provider"`
	Region    string `json:"region"`
	Framework string `json:"framework"`
	Message   string `json:"message,omitempty"`
}

func (h *handlers) deploy(ctx context.Context, _ *mcp.CallToolRequest, in deployIn) (*mcp.CallToolResult, deployOut, error) {
	dep, err := h.svc.Deploy(ctx, core.DeployOptions{
		Name:         in.Name,
		Dir:          in.Dir,
		Provider:     in.Provider,
		Image:        in.Image,
		AllowSecrets: in.AllowSecrets,
	})
	if err != nil {
		return nil, deployOut{}, err
	}
	return nil, deployOut{
		ID:        dep.ID,
		Name:      dep.AppName,
		URL:       dep.URL,
		Status:    dep.Status,
		Provider:  dep.Provider,
		Region:    dep.Region,
		Framework: dep.Framework,
		Message:   dep.Message,
	}, nil
}

type statusIn struct {
	Name     string `json:"name" jsonschema:"application name"`
	Provider string `json:"provider,omitempty" jsonschema:"fake, vercel, or gcp"`
}

type statusOut struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	URL      string `json:"url"`
	Status   string `json:"status"`
	Provider string `json:"provider"`
	Region   string `json:"region"`
	Message  string `json:"message,omitempty"`
}

func (h *handlers) status(ctx context.Context, _ *mcp.CallToolRequest, in statusIn) (*mcp.CallToolResult, statusOut, error) {
	st, err := h.svc.Status(ctx, in.Provider, in.Name)
	if err != nil {
		return nil, statusOut{}, err
	}
	return nil, statusOut{
		Name:     st.Name,
		ID:       st.ID,
		URL:      st.URL,
		Status:   st.Status,
		Provider: st.Provider,
		Region:   st.Region,
		Message:  st.Message,
	}, nil
}

type logsIn struct {
	Name     string `json:"name" jsonschema:"application name"`
	Provider string `json:"provider,omitempty" jsonschema:"fake, vercel, or gcp"`
	Limit    int    `json:"limit,omitempty" jsonschema:"maximum number of lines; default 100"`
}

type logLine struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type logsOut struct {
	Lines []logLine `json:"lines"`
}

func (h *handlers) logs(ctx context.Context, _ *mcp.CallToolRequest, in logsIn) (*mcp.CallToolResult, logsOut, error) {
	lines, err := h.svc.Logs(ctx, in.Provider, in.Name, providers.LogOptions{Limit: in.Limit})
	if err != nil {
		return nil, logsOut{}, err
	}
	out := logsOut{Lines: make([]logLine, 0, len(lines))}
	for _, line := range lines {
		when := ""
		if !line.Time.IsZero() {
			when = line.Time.UTC().Format(time.RFC3339)
		}
		out.Lines = append(out.Lines, logLine{Time: when, Level: line.Level, Message: line.Message})
	}
	return nil, out, nil
}

type secretIn struct {
	Name     string `json:"name" jsonschema:"application name"`
	Key      string `json:"key" jsonschema:"environment variable name"`
	Value    string `json:"value" jsonschema:"secret value"`
	Provider string `json:"provider,omitempty" jsonschema:"fake, vercel, or gcp"`
}

type secretOut struct {
	App     string `json:"app"`
	Key     string `json:"key"`
	Updated bool   `json:"updated"`
}

func (h *handlers) setSecret(ctx context.Context, _ *mcp.CallToolRequest, in secretIn) (*mcp.CallToolResult, secretOut, error) {
	if err := h.svc.SetSecret(ctx, in.Provider, in.Name, in.Key, in.Value); err != nil {
		return nil, secretOut{}, err
	}
	return nil, secretOut{App: in.Name, Key: in.Key, Updated: true}, nil
}

type listIn struct {
	Provider string `json:"provider,omitempty" jsonschema:"fake, vercel, or gcp"`
}

type appItem struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	URL      string `json:"url"`
	Provider string `json:"provider"`
	Region   string `json:"region"`
	Status   string `json:"status"`
}

type listOut struct {
	Apps []appItem `json:"apps"`
}

func (h *handlers) listApps(ctx context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, listOut, error) {
	apps, err := h.svc.ListApps(ctx, in.Provider)
	if err != nil {
		return nil, listOut{}, err
	}
	out := listOut{Apps: make([]appItem, 0, len(apps))}
	for _, app := range apps {
		out.Apps = append(out.Apps, appItem{
			Name:     app.Name,
			ID:       app.ID,
			URL:      app.URL,
			Provider: app.Provider,
			Region:   app.Region,
			Status:   app.Status,
		})
	}
	return nil, out, nil
}
