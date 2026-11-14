package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
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

func TestTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"express":"4.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := fake.New("")
	if err := provider.Connect(ctx, providers.Credentials{Token: "local"}); err != nil {
		t.Fatal(err)
	}
	svc := &core.Service{
		Config:    config.Default(),
		Providers: map[string]providers.Provider{"fake": provider},
		Audit:     audit.NewMemory(),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := New(svc, logger)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"deploy", "status", "logs", "set_secret", "list_apps"} {
		if !names[name] {
			t.Fatalf("missing tool %s in %#v", name, names)
		}
	}

	deployed := call(t, ctx, session, "deploy", map[string]any{"name": "hello", "dir": dir})
	if !strings.Contains(deployed, "fake.shipyard.local") || !strings.Contains(deployed, "europe-west1") {
		t.Fatalf("deploy = %s", deployed)
	}
	status := call(t, ctx, session, "status", map[string]any{"name": "hello"})
	if !strings.Contains(status, "READY") {
		t.Fatalf("status = %s", status)
	}
	secret := "test-secret-value"
	set := call(t, ctx, session, "set_secret", map[string]any{"name": "hello", "key": "API_KEY", "value": secret})
	if !strings.Contains(set, `"updated":true`) && !strings.Contains(set, `"updated": true`) {
		t.Fatalf("set_secret = %s", set)
	}
	logs := call(t, ctx, session, "logs", map[string]any{"name": "hello", "limit": 10})
	if strings.Contains(logs, secret) || !strings.Contains(logs, "API_KEY") {
		t.Fatalf("logs = %s", logs)
	}
	apps := call(t, ctx, session, "list_apps", map[string]any{})
	if !strings.Contains(apps, "hello") {
		t.Fatalf("apps = %s", apps)
	}
	var parsed struct {
		Apps []struct {
			Name string `json:"name"`
		} `json:"apps"`
	}
	if err := json.Unmarshal([]byte(apps), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Apps) != 1 || parsed.Apps[0].Name != "hello" {
		t.Fatalf("parsed %#v", parsed)
	}
}

func call(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := textContent(res)
	if res.IsError {
		t.Fatalf("%s error: %s", name, text)
	}
	return text
}

func textContent(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, content := range res.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}
