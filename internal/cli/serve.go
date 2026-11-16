package cli

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/cpurev/shipyard/internal/core"
	"github.com/cpurev/shipyard/internal/httpserver"
)

func newServeCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the status page and the MCP server over HTTP",
		Long: `Listen for HTTP until the process is signaled to stop.

The listen address is --addr, or :$PORT, or :8080. GET / is a read-only
status page. GET /healthz and GET /readyz are the probes. /mcp is the MCP
server on the streamable HTTP transport.

When SHIPYARD_HTTP_TOKEN is unset, /mcp exposes only status, logs, and
list_apps. deploy and set_secret are not registered. When the token is set,
/mcp requires Authorization: Bearer and then exposes every tool.

The default provider is fake. serve connects it in memory, so the page
works with no credentials and no cloud API.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := core.Open(cmd.Context())
			if err != nil {
				return err
			}
			if err := httpserver.ConnectDefaultFake(cmd.Context(), svc); err != nil {
				return err
			}
			if addr == "" {
				addr = httpserver.ListenAddr("", os.Getenv("PORT"))
			}
			token := os.Getenv("SHIPYARD_HTTP_TOKEN")
			logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
			if token == "" {
				logger.Warn("SHIPYARD_HTTP_TOKEN is unset; deploy and set_secret are not exposed over HTTP")
			}
			logger.Info("listening", "addr", addr)
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return httpserver.Listen(ctx, httpserver.Config{
				Addr:    addr,
				Token:   token,
				Service: svc,
				Logger:  logger,
			})
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "listen address (default :$PORT or :8080)")
	return cmd
}
