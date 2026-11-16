// Package httpserver is the long-running HTTP mode of the shipyard CLI.
// It serves a read-only status page, health probes, and the MCP server on
// the Go SDK streamable HTTP transport.
package httpserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cpurev/shipyard/internal/core"
	"github.com/cpurev/shipyard/internal/mcpserver"
	"github.com/cpurev/shipyard/internal/providers"
)

const defaultPort = "8080"

// Config is the process configuration for one server.
type Config struct {
	Addr    string
	Token   string
	Service *core.Service
	Logger  *slog.Logger
}

// Server is the HTTP handler.
type Server struct {
	svc      *core.Service
	token    string
	logger   *slog.Logger
	full     http.Handler
	readOnly http.Handler
}

// ListenAddr returns --addr, or :PORT, or :8080. A PORT value that already
// contains a colon is used as the address.
func ListenAddr(flagAddr, port string) string {
	if flagAddr != "" {
		return flagAddr
	}
	if port == "" {
		port = defaultPort
	}
	if strings.Contains(port, ":") {
		return port
	}
	return ":" + port
}

// ConnectDefaultFake marks the fake provider connected when it is the
// default. The token stays in memory. serve uses this so a container can
// list apps with no credentials and no cloud API.
func ConnectDefaultFake(ctx context.Context, svc *core.Service) error {
	if svc == nil {
		return errors.New("httpserver: service is required")
	}
	name := svc.Config.DefaultProvider
	if name == "" {
		name = "fake"
	}
	if name != "fake" {
		return nil
	}
	p := svc.Providers["fake"]
	if p == nil {
		return errors.New("httpserver: fake provider is not registered")
	}
	return p.Connect(ctx, providers.Credentials{Token: "local"})
}

// New builds the handler. A non-empty token is required for deploy and
// set_secret. Without it, /mcp registers only the read-only tools.
func New(cfg Config) (*Server, error) {
	if cfg.Service == nil {
		return nil, errors.New("httpserver: service is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	opts := &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		Logger:       logger,
	}
	fullServer := mcpserver.New(cfg.Service, logger)
	readServer := mcpserver.NewReadOnly(cfg.Service, logger)
	return &Server{
		svc:    cfg.Service,
		token:  cfg.Token,
		logger: logger,
		full: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return fullServer
		}, opts),
		readOnly: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return readServer
		}, opts),
	}, nil
}

// Handler routes /, /healthz, /readyz, and /mcp.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /{$}", s.index)
	mux.Handle("/mcp", http.HandlerFunc(s.mcp))
	return secureHeaders(mux)
}

// Listen serves until ctx is cancelled, then shuts down.
func Listen(ctx context.Context, cfg Config) error {
	if cfg.Addr == "" {
		return errors.New("httpserver: address is required")
	}
	srv, err := New(cfg)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		err := httpSrv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.ready(ctx); err != nil {
		s.logger.Info("not ready", "err", err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) ready(ctx context.Context) error {
	_, err := s.svc.ListApps(ctx, "")
	return err
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	if s.token == "" {
		// Mutating tools are not registered on this handler.
		s.readOnly.ServeHTTP(w, r)
		return
	}
	if !authorized(r, s.token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="shipyard"`)
		http.Error(w, "unauthorized\n", http.StatusUnauthorized)
		return
	}
	s.full.ServeHTTP(w, r)
}

func authorized(r *http.Request, token string) bool {
	got := bearer(r)
	sumGot := sha256.Sum256([]byte(got))
	sumWant := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(sumGot[:], sumWant[:]) == 1
}

func bearer(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
