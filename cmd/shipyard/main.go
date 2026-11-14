// Command shipyard is the operator CLI. It shares its core with shipyard-mcp.
package main

import (
	"fmt"
	"os"

	"github.com/shipyardhq/shipyard/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}
