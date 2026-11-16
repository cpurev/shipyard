package httpserver

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cpurev/shipyard/internal/audit"
	"github.com/cpurev/shipyard/internal/config"
	"github.com/cpurev/shipyard/internal/core"
	"github.com/cpurev/shipyard/internal/providers"
	"github.com/cpurev/shipyard/internal/providers/fake"
)

func TestProbesAndPage(t *testing.T) {
	svc := fakeService(t)
	if err := ConnectDefaultFake(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	dir := writeNodeApp(t)
	if _, err := svc.Deploy(context.Background(), core.DeployOptions{Name: "hello", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	handler := mustHandler(t, svc, "")
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	for _, path := range []string{"/healthz", "/readyz"} {
		res := get(t, srv.URL+path)
		if res.status != http.StatusOK || !strings.Contains(res.body, "ok") {
			t.Fatalf("%s = %d %s", path, res.status, res.body)
		}
	}
	page := get(t, srv.URL+"/")
	if page.status != http.StatusOK || !strings.Contains(page.header.Get("Content-Type"), "text/html") {
		t.Fatalf("page = %d %s", page.status, page.body)
	}
	if !strings.Contains(page.body, "Shipyard") || !strings.Contains(page.body, "hello") || !strings.Contains(page.body, "fake.shipyard.local") {
		t.Fatalf("page body:\n%s", page.body)
	}
	if !strings.Contains(page.body, "not exposed") {
		t.Fatal("page did not say mutating tools are hidden")
	}
}

func TestReadyFailsUntilFakeIsConnected(t *testing.T) {
	svc := fakeService(t)
	handler := mustHandler(t, svc, "")
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	if err := ConnectDefaultFake(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("after connect status = %d body %s", rec.Code, rec.Body.String())
	}
}

func TestMCPAuth(t *testing.T) {
	dir := writeNodeApp(t)
	open := httptest.NewServer(mustHandler(t, connectedFake(t), ""))
	t.Cleanup(open.Close)
	session := mcpSession(t, open.URL+"/mcp", "")
	names := toolNames(t, session)
	for _, hidden := range []string{"deploy", "set_secret"} {
		if names[hidden] {
			t.Fatalf("unauthenticated server exposed %s: %#v", hidden, names)
		}
	}
	for _, want := range []string{"status", "logs", "list_apps"} {
		if !names[want] {
			t.Fatalf("missing %s in %#v", want, names)
		}
	}
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "deploy", Arguments: map[string]any{"name": "hello", "dir": dir}}); err == nil {
		t.Fatal("deploy was callable without a token")
	}

	const token = "test-bearer-token"
	locked := httptest.NewServer(mustHandler(t, connectedFake(t), token))
	t.Cleanup(locked.Close)
	res, err := http.Post(locked.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", res.StatusCode)
	}
	if res.Header.Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate")
	}
	if _, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             locked.URL + "/mcp",
		HTTPClient:           clientWithToken(locked, "nope"),
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil); err == nil {
		t.Fatal("wrong token connected")
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	authedSession, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             locked.URL + "/mcp",
		HTTPClient:           clientWithToken(locked, token),
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = authedSession.Close() })
	if !toolNames(t, authedSession)["deploy"] {
		t.Fatal("authenticated server hid deploy")
	}
	result, err := authedSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "deploy",
		Arguments: map[string]any{"name": "hello", "dir": dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("deploy error: %+v", result)
	}
	health := get(t, locked.URL+"/healthz")
	if health.status != http.StatusOK {
		t.Fatalf("health with token configured = %d", health.status)
	}
}

func TestPageEscapes(t *testing.T) {
	var buf bytes.Buffer
	data := pageData{
		Version:  "0",
		Provider: "fake",
		Region:   "europe-west1",
		Apps: []appRow{{
			Name:   "<script>",
			Status: "READY",
			URL:    "javascript:alert(1)",
			Href:   safeHref("javascript:alert(1)"),
		}},
	}
	if err := pageTmpl.Execute(&buf, data); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if strings.Contains(html, "<script>") || strings.Contains(html, `href="javascript:`) {
		t.Fatalf("unescaped page:\n%s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("page:\n%s", html)
	}
}

func TestListenAddr(t *testing.T) {
	if ListenAddr("", "") != ":8080" || ListenAddr("", "9090") != ":9090" || ListenAddr("127.0.0.1:1", "9090") != "127.0.0.1:1" {
		t.Fatal(ListenAddr("", ""), ListenAddr("", "9090"), ListenAddr("127.0.0.1:1", "9090"))
	}
	if ListenAddr("", "127.0.0.1:9090") != "127.0.0.1:9090" {
		t.Fatal(ListenAddr("", "127.0.0.1:9090"))
	}
}

type roundTripResult struct {
	status int
	body   string
	header http.Header
}

func get(t *testing.T, url string) roundTripResult {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return roundTripResult{status: res.StatusCode, body: string(body), header: res.Header}
}

func mustHandler(t *testing.T, svc *core.Service, token string) http.Handler {
	t.Helper()
	srv, err := New(Config{
		Token:   token,
		Service: svc,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv.Handler()
}

func fakeService(t *testing.T) *core.Service {
	t.Helper()
	return &core.Service{
		Config:    config.Default(),
		Providers: map[string]providers.Provider{"fake": fake.New("")},
		Audit:     audit.NewMemory(),
	}
}

func connectedFake(t *testing.T) *core.Service {
	t.Helper()
	svc := fakeService(t)
	if err := ConnectDefaultFake(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	return svc
}

func writeNodeApp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"express":"4.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mcpSession(t *testing.T, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	client := &http.Client{}
	if token != "" {
		client.Transport = bearerTrip{base: http.DefaultTransport, token: token}
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           client,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func toolNames(t *testing.T, session *mcp.ClientSession) map[string]bool {
	t.Helper()
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	return names
}

func clientWithToken(srv *httptest.Server, token string) *http.Client {
	base := srv.Client()
	return &http.Client{
		Transport:     bearerTrip{base: base.Transport, token: token},
		CheckRedirect: base.CheckRedirect,
		Jar:           base.Jar,
		Timeout:       base.Timeout,
	}
}

type bearerTrip struct {
	base  http.RoundTripper
	token string
}

func (b bearerTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	base := b.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}
