// Command shipyard-mcp is the stdio MCP server for coding agents.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/cpurev/shipyard/internal/core"
	"github.com/cpurev/shipyard/internal/mcpserver"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()
	svc, err := core.Open(ctx)
	if err != nil {
		logger.Error("open shipyard", "err", err)
		os.Exit(1)
	}
	if err := mcpserver.Serve(ctx, svc, logger); err != nil {
		logger.Error("mcp server stopped", "err", err)
		os.Exit(1)
	}
}
