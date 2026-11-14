package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shipyardhq/shipyard/internal/config"
	"github.com/shipyardhq/shipyard/internal/providers"
)

func TestProvisionAndGuardsDoNotCall(t *testing.T) {
	calls := 0
	p := &Provider{}
	ctx := context.Background()
	if _, err := p.ProvisionAccount(ctx, providers.ProvisionRequest{Email: "a@example.com"}); !errors.Is(err, providers.ErrNotSupported) {
		t.Fatalf("provision = %v", err)
	}
	if _, err := p.Deploy(ctx, providers.DeployRequest{Name: "hello", Image: "example.com/app:1"}); !errors.Is(err, providers.ErrNotConnected) {
		t.Fatalf("deploy = %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer srv.Close()
	connected, err := New(ctx, Options{
		Project:    "demo-project",
		Region:     config.DefaultRegion,
		Token:      "test-token",
		Endpoint:   srv.URL,
		HTTPClient: guardClient(t, srv),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connected.Deploy(ctx, providers.DeployRequest{Name: "hello", Region: "arn1", Image: "example.com/app:1"}); err == nil || !strings.Contains(err.Error(), "Vercel") {
		t.Fatalf("region guard = %v", err)
	}
	if _, err := connected.Deploy(ctx, providers.DeployRequest{Name: "hello", Region: config.DefaultRegion}); err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("image guard = %v", err)
	}
	if calls != 0 {
		t.Fatalf("guards made %d calls", calls)
	}
}

func TestCloudRunAndLogging(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path)
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth = %q on %s %s", got, r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case r.Method == http.MethodPost && strings.Contains(path, "/services") && !strings.Contains(path, "entries"):
			if r.URL.Query().Get("serviceId") != "hello" {
				t.Errorf("serviceId = %q", r.URL.Query().Get("serviceId"))
			}
			if !strings.Contains(path, "/projects/demo-project/locations/europe-west1/services") {
				t.Errorf("create path %s", path)
			}
			if !strings.Contains(string(body), "example.com/hello:1") || !strings.Contains(string(body), "INGRESS_TRAFFIC_INTERNAL_ONLY") {
				t.Errorf("create body %s", body)
			}
			_, _ = io.WriteString(w, `{"name":"operations/op-1","done":true}`)
		case r.Method == http.MethodGet && strings.Contains(path, "/services/hello"):
			_, _ = io.WriteString(w, serviceJSON)
		case r.Method == http.MethodGet && strings.Contains(path, "/services"):
			_, _ = io.WriteString(w, `{"services":[`+serviceJSON+`]}`)
		case r.Method == http.MethodPatch && strings.Contains(path, "/services/hello"):
			if !strings.Contains(r.URL.RawQuery, "updateMask=template") && r.URL.Query().Get("updateMask") != "template" {
				t.Errorf("updateMask query %s", r.URL.RawQuery)
			}
			if !strings.Contains(string(body), "API_KEY") || !strings.Contains(string(body), "test-secret-value") {
				t.Errorf("patch body %s", body)
			}
			if !strings.Contains(string(body), "example.com/hello:1") {
				t.Errorf("patch dropped the image: %s", body)
			}
			_, _ = io.WriteString(w, `{"name":"operations/op-2","done":true}`)
		case r.Method == http.MethodPost && strings.Contains(path, "entries:list"):
			var logged struct {
				Filter        string   `json:"filter"`
				ResourceNames []string `json:"resourceNames"`
			}
			if err := json.Unmarshal(body, &logged); err != nil {
				t.Errorf("log body: %v", err)
			}
			if !strings.Contains(logged.Filter, "cloud_run_revision") || !strings.Contains(logged.Filter, `service_name="hello"`) {
				t.Errorf("log filter %s", logged.Filter)
			}
			if len(logged.ResourceNames) != 1 || logged.ResourceNames[0] != "projects/demo-project" {
				t.Errorf("log resource %#v", logged.ResourceNames)
			}
			_, _ = io.WriteString(w, `{"entries":[{"timestamp":"2026-11-14T12:00:01Z","severity":"INFO","textPayload":"listening"}]}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.RequestURI())
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p, err := New(context.Background(), Options{
		Project:    "demo-project",
		Region:     config.DefaultRegion,
		Token:      "test-token",
		Endpoint:   srv.URL,
		HTTPClient: guardClient(t, srv),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	dep, err := p.Deploy(ctx, providers.DeployRequest{
		Name:      "hello",
		Region:    config.DefaultRegion,
		Framework: "node",
		Image:     "example.com/hello:1",
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if dep.Provider != "gcp" || dep.Region != "europe-west1" || dep.URL != "https://hello-xyz.a.run.app" || dep.Status != "READY" {
		t.Fatalf("deployment = %+v", dep)
	}
	st, err := p.Status(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "READY" || st.Name != "hello" {
		t.Fatalf("status = %+v", st)
	}
	lines, err := p.Logs(ctx, "hello", providers.LogOptions{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Message != "listening" || lines[0].Level != "info" {
		t.Fatalf("logs = %+v", lines)
	}
	if err := p.SetSecret(ctx, "hello", "API_KEY", "test-secret-value"); err != nil {
		t.Fatal(err)
	}
	apps, err := p.ListApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Name != "hello" {
		t.Fatalf("apps = %+v", apps)
	}
	if _, err := p.ProvisionAccount(ctx, providers.ProvisionRequest{}); !errors.Is(err, providers.ErrNotSupported) {
		t.Fatal(err)
	}
	if len(calls) == 0 {
		t.Fatal("expected API calls")
	}
}

func TestCreateConflictPatches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"error":{"code":409,"message":"already exists","status":"ALREADY_EXISTS"}}`)
		case http.MethodGet:
			_, _ = io.WriteString(w, serviceJSON)
		case http.MethodPatch:
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "example.com/hello:2") {
				t.Errorf("updated image missing: %s", body)
			}
			_, _ = io.WriteString(w, `{"name":"operations/op-3","done":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p, err := New(context.Background(), Options{
		Project:    "demo-project",
		Token:      "test-token",
		Endpoint:   srv.URL,
		HTTPClient: guardClient(t, srv),
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := p.Deploy(context.Background(), providers.DeployRequest{
		Name:   "hello",
		Region: "europe-west1",
		Image:  "example.com/hello:2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dep.Message == "" || dep.ID != "operations/op-3" {
		t.Fatalf("deployment = %+v", dep)
	}
}

func TestServiceAccountDoesNotNeedALiveToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()
	p, err := New(context.Background(), Options{
		Project:         "demo-project",
		CredentialsJSON: []byte(`{"type":"service_account","project_id":"demo-project"}`),
		Endpoint:        srv.URL,
		HTTPClient:      guardClient(t, srv),
	})
	if err != nil {
		return
	}
	if _, err := p.ListApps(context.Background()); err == nil {
		t.Fatal("expected the incomplete service account to fail before a successful list")
	}
}

func TestOAuth(t *testing.T) {
	authURL, err := AuthorizeURL("client", "http://127.0.0.1:8787/callback", "state-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(authURL, "client_id=client") || !strings.Contains(authURL, "cloud-platform") {
		t.Fatalf("url = %s", authURL)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("code") != "code-1" {
			t.Errorf("code = %q", r.Form.Get("code"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"ya29.test","refresh_token":"refresh-1","token_type":"Bearer","expires_in":3600}`)
	}))
	defer srv.Close()
	creds, err := Exchange(context.Background(), srv.Client(), srv.URL, "client", "secret", "code-1", "http://127.0.0.1:8787/callback")
	if err != nil {
		t.Fatal(err)
	}
	if creds.Token != "ya29.test" || creds.RefreshToken != "refresh-1" {
		t.Fatalf("creds = %+v", creds)
	}
}

const serviceJSON = `{
  "name": "projects/demo-project/locations/europe-west1/services/hello",
  "uri": "https://hello-xyz.a.run.app",
  "updateTime": "2026-11-14T12:00:00Z",
  "reconciling": false,
  "ingress": "INGRESS_TRAFFIC_INTERNAL_ONLY",
  "terminalCondition": {"state": "CONDITION_SUCCEEDED", "message": "ready"},
  "template": {"containers": [{"image": "example.com/hello:1", "env": []}]}
}`

func guardClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	base := srv.Client()
	base.Timeout = 5 * time.Second
	inner := base.Transport
	base.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		host := r.URL.Hostname()
		if host != "127.0.0.1" && host != "localhost" && host != srv.Listener.Addr().String() {
			t.Errorf("blocked request to %s", r.URL.String())
			return nil, errors.New("blocked request to " + r.URL.Host)
		}
		if inner == nil {
			inner = http.DefaultTransport
		}
		return inner.RoundTrip(r)
	})
	return base
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
