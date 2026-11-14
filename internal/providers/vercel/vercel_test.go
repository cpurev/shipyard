package vercel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shipyardhq/shipyard/internal/providers"
)

func TestRegion(t *testing.T) {
	tests := map[string]string{
		"":               "arn1",
		"europe-west1":   "arn1",
		"europe-north1":  "arn1",
		"arn1":           "arn1",
		"cdg1":           "cdg1",
		"us-central1":    "iad1",
		"somewhere-else": "arn1",
	}
	for in, want := range tests {
		if got := Region(in); got != want {
			t.Fatalf("Region(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestMissingTokenDoesNotDial(t *testing.T) {
	client := &Client{HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("client dialed without a token")
		return nil, errors.New("dialed")
	})}}
	if _, err := client.ListProjects(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDeployAndAPIs(t *testing.T) {
	var sawAuth, sawTeam, sawRegion, sawFile bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		sawAuth = true
		if r.URL.Query().Get("teamId") != "team_123" {
			t.Errorf("teamId = %q", r.URL.Query().Get("teamId"))
		}
		sawTeam = true
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v13/deployments":
			var body struct {
				Name    string   `json:"name"`
				Regions []string `json:"regions"`
				Files   []struct {
					File     string `json:"file"`
					Data     string `json:"data"`
					Encoding string `json:"encoding"`
				} `json:"files"`
				ProjectSettings struct {
					Framework string `json:"framework"`
				} `json:"projectSettings"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
			}
			if body.Name != "hello" || body.ProjectSettings.Framework != "nextjs" {
				t.Errorf("body name=%s framework=%s", body.Name, body.ProjectSettings.Framework)
			}
			if len(body.Regions) != 1 || body.Regions[0] != "arn1" {
				t.Errorf("regions = %#v", body.Regions)
			}
			sawRegion = true
			for _, file := range body.Files {
				if file.File == "node_modules/left.js" || file.File == ".env" || strings.HasSuffix(file.File, ".pem") {
					t.Errorf("uploaded sensitive or skipped file %s", file.File)
				}
				if file.File == "pages/index.js" {
					raw, err := base64.StdEncoding.DecodeString(file.Data)
					if err != nil || string(raw) != "export default function Page(){return null}\n" {
						t.Errorf("index payload %q err=%v", raw, err)
					}
					if file.Encoding != "base64" {
						t.Errorf("encoding %s", file.Encoding)
					}
					sawFile = true
				}
			}
			_, _ = io.WriteString(w, `{"id":"dpl_1","url":"hello-abc.vercel.app","readyState":"QUEUED","name":"hello"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v6/deployments":
			if r.URL.Query().Get("app") != "hello" {
				t.Errorf("app = %q", r.URL.Query().Get("app"))
			}
			_, _ = io.WriteString(w, `{"deployments":[{"uid":"dpl_1","name":"hello","url":"hello-abc.vercel.app","state":"READY","created":1763000000000}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v3/deployments/dpl_1/events":
			_, _ = io.WriteString(w, `[{"type":"stdout","created":1763000001000,"payload":{"text":"built"}}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/v9/projects":
			_, _ = io.WriteString(w, `{"projects":[{"id":"prj_1","name":"hello","targets":{"production":{"url":"hello.vercel.app","readyState":"READY"}}}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v10/projects/hello/env":
			var body struct {
				Key   string `json:"key"`
				Value string `json:"value"`
				Type  string `json:"type"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("env decode: %v", err)
			}
			if body.Key != "API_KEY" || body.Value != "test-secret-value" || body.Type != "encrypted" {
				t.Errorf("env body = %+v", body)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"key":"API_KEY"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "pages", "index.js"), "export default function Page(){return null}\n")
	mustWrite(t, filepath.Join(dir, "node_modules", "left.js"), "module.exports = 1\n")
	mustWrite(t, filepath.Join(dir, ".env"), "TOKEN=should-not-upload\n")
	mustWrite(t, filepath.Join(dir, "key.pem"), "-----BEGIN CERT-----\n")

	p := New(Options{
		Token:      "test-token",
		TeamID:     "team_123",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})
	ctx := context.Background()
	dep, err := p.Deploy(ctx, providers.DeployRequest{
		Name:      "hello",
		Dir:       dir,
		Region:    "europe-west1",
		Framework: "nextjs",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dep.ID != "dpl_1" || dep.URL != "https://hello-abc.vercel.app" || dep.Region != "arn1" {
		t.Fatalf("deployment = %+v", dep)
	}
	st, err := p.Status(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "READY" || st.ID != "dpl_1" {
		t.Fatalf("status = %+v", st)
	}
	lines, err := p.Logs(ctx, "hello", providers.LogOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Message != "built" {
		t.Fatalf("logs = %+v", lines)
	}
	if err := p.SetSecret(ctx, "hello", "API_KEY", "test-secret-value"); err != nil {
		t.Fatal(err)
	}
	apps, err := p.ListApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Name != "hello" || apps[0].URL != "https://hello.vercel.app" {
		t.Fatalf("apps = %+v", apps)
	}
	if _, err := p.ProvisionAccount(ctx, providers.ProvisionRequest{}); !errors.Is(err, providers.ErrNotSupported) {
		t.Fatalf("provision = %v", err)
	}
	if !sawAuth || !sawTeam || !sawRegion || !sawFile {
		t.Fatalf("auth=%v team=%v region=%v file=%v", sawAuth, sawTeam, sawRegion, sawFile)
	}
}

func TestNotConnected(t *testing.T) {
	p := New(Options{BaseURL: "http://127.0.0.1:1", HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("dialed while disconnected")
		return nil, errors.New("dialed")
	})}})
	if _, err := p.Deploy(context.Background(), providers.DeployRequest{Name: "hello", Dir: t.TempDir()}); !errors.Is(err, providers.ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
}

func TestOAuth(t *testing.T) {
	authURL, err := AuthorizeURL("client", "http://127.0.0.1:8787/callback", "state-1")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "vercel.com" || u.Path != "/oauth/authorize" || u.Query().Get("client_id") != "client" {
		t.Fatalf("url = %s", authURL)
	}
	if _, err := AuthorizeURL("", "http://127.0.0.1/cb", "s"); err == nil {
		t.Fatal("expected missing client id to fail")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("code") != "code-1" || r.Form.Get("client_id") != "client" {
			t.Errorf("form = %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"token-from-oauth","refresh_token":"refresh-1","team_id":"team_9"}`)
	}))
	defer srv.Close()
	creds, err := Exchange(context.Background(), srv.Client(), srv.URL, "client", "secret", "code-1", "http://127.0.0.1:8787/callback")
	if err != nil {
		t.Fatal(err)
	}
	if creds.Token != "token-from-oauth" || creds.Extra["teamId"] != "team_9" {
		t.Fatalf("creds = %+v", creds)
	}
}

func TestGetDeployment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v13/deployments/dpl_9" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"dpl_9","url":"https://app.vercel.app","readyState":"READY"}`)
	}))
	defer srv.Close()
	client := &Client{BaseURL: srv.URL, Token: "t", HTTPClient: srv.Client()}
	got, err := client.GetDeployment(context.Background(), "dpl_9")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "dpl_9" || got.ReadyState != "READY" {
		t.Fatalf("got %+v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
