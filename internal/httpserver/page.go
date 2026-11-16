package httpserver

import (
	"html/template"
	"net/http"
	"net/url"

	"github.com/cpurev/shipyard/internal/providers"
	"github.com/cpurev/shipyard/internal/version"
)

type pageData struct {
	Version  string
	Provider string
	Region   string
	Apps     []appRow
	Error    string
	Mutating bool
}

type appRow struct {
	Name   string
	Status string
	Region string
	URL    string
	Href   string
}

var pageTmpl = template.Must(template.New("status").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Shipyard</title>
  <style>
    :root { color-scheme: light dark; }
    body { font: 1rem/1.5 system-ui, sans-serif; max-width: 42rem; margin: 2rem auto; padding: 0 1rem; }
    h1 { font-size: 1.6rem; margin-bottom: 0.25rem; }
    p, li { margin: 0.4rem 0; }
    table { border-collapse: collapse; width: 100%; margin-top: 0.5rem; }
    th, td { text-align: left; padding: 0.35rem 0.5rem; border-bottom: 1px solid color-mix(in srgb, CanvasText 20%, transparent); }
    code { font-family: ui-monospace, monospace; }
    .muted { opacity: 0.8; }
  </style>
</head>
<body>
  <h1>Shipyard</h1>
  <p>Shipyard deploys a small internal app onto a cloud account you already own. It does not host the app itself. This page is a read-only status view of the provider configured for this process.</p>
  <p>Provider <code>{{.Provider}}</code> in <code>{{.Region}}</code>. Version <code>{{.Version}}</code>.</p>
  <p>The MCP endpoint is <code>/mcp</code> on the streamable HTTP transport. {{if .Mutating}}A bearer token is required. Send <code>Authorization: Bearer</code> with the value of <code>SHIPYARD_HTTP_TOKEN</code>.{{else}}<code>SHIPYARD_HTTP_TOKEN</code> is unset, so <code>deploy</code> and <code>set_secret</code> are not exposed. <code>status</code>, <code>logs</code>, and <code>list_apps</code> are available.{{end}}</p>
  <h2>Apps</h2>
  {{if .Error}}<p>{{.Error}}</p>{{else if .Apps}}
  <table>
    <thead><tr><th>Name</th><th>Status</th><th>Region</th><th>URL</th></tr></thead>
    <tbody>
    {{range .Apps}}
      <tr>
        <td>{{.Name}}</td>
        <td>{{.Status}}</td>
        <td>{{.Region}}</td>
        <td>{{if .Href}}<a href="{{.Href}}">{{.URL}}</a>{{else}}{{.URL}}{{end}}</td>
      </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}<p>No apps yet.</p>{{end}}
  <p class="muted">The default provider is fake. It records apps in this process and does not call a cloud API. A restart drops that local list. Apps deployed with the kubernetes provider live in the cluster, not in this process.</p>
</body>
</html>
`))

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	data := pageData{
		Version:  version.Version,
		Provider: s.svc.Config.DefaultProvider,
		Region:   s.svc.Config.Region,
		Mutating: s.token != "",
	}
	if data.Provider == "" {
		data.Provider = "fake"
	}
	if data.Region == "" {
		data.Region = "europe-west1"
	}
	apps, err := s.svc.ListApps(r.Context(), "")
	if err != nil {
		data.Error = err.Error()
	} else {
		data.Apps = rows(apps)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := pageTmpl.Execute(w, data); err != nil {
		s.logger.Error("render status page", "err", err)
	}
}

func rows(apps []providers.App) []appRow {
	out := make([]appRow, 0, len(apps))
	for _, app := range apps {
		out = append(out, appRow{
			Name:   app.Name,
			Status: app.Status,
			Region: app.Region,
			URL:    app.URL,
			Href:   safeHref(app.URL),
		})
	}
	return out
}

func safeHref(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return raw
}
