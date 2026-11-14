// Package cli is the shipyard command line.
package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shipyardhq/shipyard/internal/core"
	"github.com/shipyardhq/shipyard/internal/providers"
	"github.com/shipyardhq/shipyard/internal/version"
)

// Execute runs one CLI invocation. args do not include the program name.
func Execute(args []string, stdout, stderr io.Writer) error {
	cmd := newRoot()
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())
	return cmd.Execute()
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "shipyard",
		Short: "Turn a local internal app into a deployment on an account you already own",
		Long: `Shipyard deploys a project with the Vercel or Cloud Run account you connect.

The default provider is fake. It records the app on this machine and does not
call a cloud API. Connect vercel or gcp before using those providers.

Nothing in this command creates a cloud account.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetVersionTemplate("shipyard {{ .Version }}\n")
	root.AddCommand(
		newDeployCmd(),
		newStatusCmd(),
		newLogsCmd(),
		newSecretsCmd(),
		newAppsCmd(),
		newConnectCmd(),
	)
	silence(root)
	return root
}

func silence(cmd *cobra.Command) {
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	for _, child := range cmd.Commands() {
		silence(child)
	}
}

func newDeployCmd() *cobra.Command {
	var opts core.DeployOptions
	cmd := &cobra.Command{
		Use:   "deploy [name]",
		Short: "Deploy a local project",
		Example: `  shipyard connect fake
  shipyard deploy hello --dir ./examples/hello
  shipyard deploy hello --provider vercel --dir .`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Name == "" && len(args) == 1 {
				opts.Name = args[0]
			}
			if opts.Name == "" {
				return fmt.Errorf("pass an app name, for example `shipyard deploy hello`")
			}
			svc, err := core.Open(cmd.Context())
			if err != nil {
				return err
			}
			dep, err := svc.Deploy(cmd.Context(), opts)
			if err != nil {
				return err
			}
			printf(cmd.OutOrStdout(), "%s deployed\n", dep.AppName)
			printf(cmd.OutOrStdout(), "  provider:   %s\n", dep.Provider)
			printf(cmd.OutOrStdout(), "  region:     %s\n", dep.Region)
			printf(cmd.OutOrStdout(), "  framework:  %s\n", empty(dep.Framework, "unknown"))
			printf(cmd.OutOrStdout(), "  status:     %s\n", empty(dep.Status, "UNKNOWN"))
			printf(cmd.OutOrStdout(), "  url:        %s\n", empty(dep.URL, "(none yet)"))
			if dep.Message != "" {
				printf(cmd.OutOrStdout(), "  message:    %s\n", dep.Message)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "app name")
	cmd.Flags().StringVar(&opts.Dir, "dir", ".", "project directory")
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "fake, vercel, or gcp")
	cmd.Flags().StringVar(&opts.Image, "image", "", "container image (required for gcp)")
	cmd.Flags().BoolVar(&opts.AllowSecrets, "allow-secrets", false, "deploy even if the secret scan finds matches")
	return cmd
}

func newStatusCmd() *cobra.Command {
	var providerName string
	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show the status of one app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := core.Open(cmd.Context())
			if err != nil {
				return err
			}
			st, err := svc.Status(cmd.Context(), providerName, args[0])
			if err != nil {
				return err
			}
			printf(cmd.OutOrStdout(), "%s\n", st.Name)
			printf(cmd.OutOrStdout(), "  id:        %s\n", st.ID)
			printf(cmd.OutOrStdout(), "  provider:  %s\n", st.Provider)
			printf(cmd.OutOrStdout(), "  region:    %s\n", empty(st.Region, "(unknown)"))
			printf(cmd.OutOrStdout(), "  status:    %s\n", empty(st.Status, "UNKNOWN"))
			printf(cmd.OutOrStdout(), "  url:       %s\n", empty(st.URL, "(none yet)"))
			if !st.UpdatedAt.IsZero() {
				printf(cmd.OutOrStdout(), "  updated:   %s\n", st.UpdatedAt.UTC().Format(time.RFC3339))
			}
			if st.Message != "" {
				printf(cmd.OutOrStdout(), "  message:   %s\n", st.Message)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&providerName, "provider", "", "fake, vercel, or gcp")
	return cmd
}

func newLogsCmd() *cobra.Command {
	var providerName string
	var limit int
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Read recent log lines for one app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := core.Open(cmd.Context())
			if err != nil {
				return err
			}
			lines, err := svc.Logs(cmd.Context(), providerName, args[0], providers.LogOptions{Limit: limit})
			if err != nil {
				return err
			}
			if len(lines) == 0 {
				println(cmd.OutOrStdout(), "no log lines")
				return nil
			}
			for _, line := range lines {
				when := line.Time.UTC().Format(time.RFC3339)
				if line.Time.IsZero() {
					when = "-"
				}
				printf(cmd.OutOrStdout(), "%s  %s  %s\n", when, empty(line.Level, "info"), line.Message)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&providerName, "provider", "", "fake, vercel, or gcp")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum number of lines")
	return cmd
}

func newSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Set a secret on an app",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	var providerName string
	var value string
	var fromStdin bool
	set := &cobra.Command{
		Use:   "set <app> <key>",
		Short: "Set one secret",
		Args:  cobra.ExactArgs(2),
		Example: `  shipyard secrets set hello API_KEY --value 's3cret'
  printf '%s' 's3cret' | shipyard secrets set hello API_KEY --from-stdin`,
		RunE: func(cmd *cobra.Command, args []string) error {
			secret := value
			if fromStdin {
				if value != "" {
					return fmt.Errorf("pass either --value or --from-stdin")
				}
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				secret = string(data)
				secret = strings.TrimRight(secret, "\n")
			}
			if secret == "" {
				return fmt.Errorf("pass --value or --from-stdin")
			}
			svc, err := core.Open(cmd.Context())
			if err != nil {
				return err
			}
			if err := svc.SetSecret(cmd.Context(), providerName, args[0], args[1], secret); err != nil {
				return err
			}
			printf(cmd.OutOrStdout(), "set %s on %s\n", args[1], args[0])
			return nil
		},
	}
	set.Flags().StringVar(&providerName, "provider", "", "fake, vercel, or gcp")
	set.Flags().StringVar(&value, "value", "", "secret value")
	set.Flags().BoolVar(&fromStdin, "from-stdin", false, "read the secret value from stdin")
	cmd.AddCommand(set)
	return cmd
}

func newAppsCmd() *cobra.Command {
	var providerName string
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "List apps on the connected provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := core.Open(cmd.Context())
			if err != nil {
				return err
			}
			apps, err := svc.ListApps(cmd.Context(), providerName)
			if err != nil {
				return err
			}
			if len(apps) == 0 {
				println(cmd.OutOrStdout(), "no apps")
				return nil
			}
			for _, app := range apps {
				printf(cmd.OutOrStdout(), "%s  %s  %s  %s  %s\n",
					app.Name,
					empty(app.Status, "UNKNOWN"),
					empty(app.Region, "-"),
					app.Provider,
					empty(app.URL, "-"),
				)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&providerName, "provider", "", "fake, vercel, or gcp")
	return cmd
}

func empty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func printf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func println(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}
