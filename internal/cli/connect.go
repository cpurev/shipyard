package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/cpurev/shipyard/internal/audit"
	"github.com/cpurev/shipyard/internal/config"
	"github.com/cpurev/shipyard/internal/core"
	"github.com/cpurev/shipyard/internal/credentials"
	"github.com/cpurev/shipyard/internal/providers"
	"github.com/cpurev/shipyard/internal/providers/gcp"
	"github.com/cpurev/shipyard/internal/providers/vercel"
)

const defaultOAuthRedirect = "http://127.0.0.1:8787/callback"

func newConnectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect a cloud account you already own",
		Long: `Store an API token, a kubeconfig path, or complete an OAuth code exchange
for an account you already have. Shipyard does not create Vercel, Google,
or UpCloud accounts, and it does not create clusters.

Tokens are written to a file mode 0600 under the shipyard config directory
(or SHIPYARD_HOME). The OS keychain backend is a stub. A kubeconfig is not
copied; only its path is stored.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newConnectStatusCmd(),
		newConnectFakeCmd(),
		newConnectVercelCmd(),
		newConnectGCPCmd(),
		newConnectKubernetesCmd(),
		newConnectUpCloudCmd(),
	)
	return cmd
}

func newConnectStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which providers are connected, without printing tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := config.Dir()
			if err != nil {
				return err
			}
			cfg, err := config.Load(home)
			if err != nil {
				return err
			}
			store, err := openStore(home, cfg)
			if err != nil {
				return err
			}
			all, err := store.LoadAll(cmd.Context())
			if err != nil {
				return err
			}
			printf(cmd.OutOrStdout(), "region:    %s\n", cfg.Region)
			printf(cmd.OutOrStdout(), "provider:  %s\n", cfg.DefaultProvider)
			printf(cmd.OutOrStdout(), "backend:   %s\n", cfg.CredentialBackend)
			for _, name := range []string{"fake", "vercel", "gcp", "kubernetes"} {
				creds, ok := all[name]
				state := "not connected"
				if ok && providers.CredentialsReady(name, creds) {
					state = "connected"
				}
				printf(cmd.OutOrStdout(), "%-12s  %s\n", name, state)
			}
			return nil
		},
	}
}

func newConnectFakeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fake",
		Short: "Enable the local fake provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := save(cmd.Context(), "fake", providers.Credentials{Token: "local"}); err != nil {
				return err
			}
			println(cmd.OutOrStdout(), "connected fake")
			println(cmd.OutOrStdout(), "deploys stay on this machine and do not call a cloud API")
			return nil
		},
	}
}

func newConnectVercelCmd() *cobra.Command {
	var token, team, code string
	var oauth bool
	cmd := &cobra.Command{
		Use:   "vercel",
		Short: "Connect an existing Vercel account with a token or OAuth code",
		Example: `  shipyard connect vercel --token "$VERCEL_TOKEN"
  shipyard connect vercel --oauth
  shipyard connect vercel --code "$CODE"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if oauth && (token != "" || code != "") {
				return fmt.Errorf("pass only one of --oauth, --token, or --code")
			}
			if token != "" && code != "" {
				return fmt.Errorf("pass only one of --token or --code")
			}
			if oauth {
				return printVercelOAuth(cmd)
			}
			if code != "" {
				return exchangeVercel(cmd, code, team)
			}
			if token == "" {
				token = os.Getenv("VERCEL_TOKEN")
			}
			if token == "" {
				return fmt.Errorf("pass --token, set VERCEL_TOKEN, or run --oauth")
			}
			creds := providers.Credentials{Token: token}
			if team != "" {
				creds.Extra = map[string]string{"teamId": team}
			}
			if err := save(cmd.Context(), "vercel", creds); err != nil {
				return err
			}
			println(cmd.OutOrStdout(), "connected vercel")
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "Vercel API token for an account you already own")
	cmd.Flags().StringVar(&team, "team", "", "optional Vercel team id")
	cmd.Flags().BoolVar(&oauth, "oauth", false, "print the OAuth URL; does not contact Vercel")
	cmd.Flags().StringVar(&code, "code", "", "exchange an OAuth code for a token")
	return cmd
}

func newConnectGCPCmd() *cobra.Command {
	var project, token, serviceAccount, code string
	var oauth bool
	cmd := &cobra.Command{
		Use:   "gcp",
		Short: "Connect an existing Google Cloud project",
		Example: `  shipyard connect gcp --project "$GCP_PROJECT" --token "$GCP_TOKEN"
  shipyard connect gcp --project "$GCP_PROJECT" --service-account ./sa.json
  shipyard connect gcp --project "$GCP_PROJECT" --oauth`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			modes := 0
			if token != "" {
				modes++
			}
			if serviceAccount != "" {
				modes++
			}
			if oauth {
				modes++
			}
			if code != "" {
				modes++
			}
			if modes > 1 {
				return fmt.Errorf("pass only one of --token, --service-account, --oauth, or --code")
			}
			if project == "" {
				project = os.Getenv("GCP_PROJECT")
			}
			if oauth {
				if project == "" {
					return fmt.Errorf("pass --project or set GCP_PROJECT")
				}
				return printGCPOAuth(cmd, project)
			}
			if code != "" {
				if project == "" {
					return fmt.Errorf("pass --project or set GCP_PROJECT")
				}
				return exchangeGCP(cmd, project, code)
			}
			if serviceAccount != "" {
				return connectGCPServiceAccount(cmd, project, serviceAccount)
			}
			if token == "" {
				token = os.Getenv("GCP_TOKEN")
			}
			if token == "" || project == "" {
				return fmt.Errorf("pass --project and --token, or --service-account, or --oauth")
			}
			if err := save(cmd.Context(), "gcp", providers.Credentials{Token: token, Project: project}); err != nil {
				return err
			}
			printf(cmd.OutOrStdout(), "connected gcp project %s\n", project)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "existing GCP project id")
	cmd.Flags().StringVar(&token, "token", "", "access token for that project")
	cmd.Flags().StringVar(&serviceAccount, "service-account", "", "path to a service-account JSON key you already created")
	cmd.Flags().BoolVar(&oauth, "oauth", false, "print the Google OAuth URL; does not contact Google")
	cmd.Flags().StringVar(&code, "code", "", "exchange an OAuth code for a token")
	return cmd
}

func printVercelOAuth(cmd *cobra.Command) error {
	clientID := os.Getenv("VERCEL_OAUTH_CLIENT_ID")
	redirect := envOr("VERCEL_OAUTH_REDIRECT_URI", defaultOAuthRedirect)
	state, err := randomState()
	if err != nil {
		return err
	}
	authURL, err := vercel.AuthorizeURL(clientID, redirect, state)
	if err != nil {
		return err
	}
	println(cmd.OutOrStdout(), "Open this URL and approve access to the Vercel account you already own.")
	println(cmd.OutOrStdout(), "This command does not listen for the redirect. TODO: verify the state and run a local callback.")
	println(cmd.OutOrStdout(), authURL)
	printf(cmd.OutOrStdout(), "\nThen run: shipyard connect vercel --code CODE\n")
	return nil
}

func exchangeVercel(cmd *cobra.Command, code, team string) error {
	clientID := os.Getenv("VERCEL_OAUTH_CLIENT_ID")
	clientSecret := os.Getenv("VERCEL_OAUTH_CLIENT_SECRET")
	redirect := envOr("VERCEL_OAUTH_REDIRECT_URI", defaultOAuthRedirect)
	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("set VERCEL_OAUTH_CLIENT_ID and VERCEL_OAUTH_CLIENT_SECRET")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	creds, err := vercel.Exchange(ctx, &http.Client{Timeout: 30 * time.Second}, "", clientID, clientSecret, code, redirect)
	if err != nil {
		return err
	}
	if team != "" {
		if creds.Extra == nil {
			creds.Extra = map[string]string{}
		}
		creds.Extra["teamId"] = team
	}
	if err := save(cmd.Context(), "vercel", creds); err != nil {
		return err
	}
	println(cmd.OutOrStdout(), "connected vercel")
	return nil
}

func printGCPOAuth(cmd *cobra.Command, project string) error {
	clientID := os.Getenv("GCP_OAUTH_CLIENT_ID")
	redirect := envOr("GCP_OAUTH_REDIRECT_URI", defaultOAuthRedirect)
	state, err := randomState()
	if err != nil {
		return err
	}
	authURL, err := gcp.AuthorizeURL(clientID, redirect, state)
	if err != nil {
		return err
	}
	printf(cmd.OutOrStdout(), "Open this URL and approve access to Google Cloud project %s.\n", project)
	println(cmd.OutOrStdout(), "This command does not listen for the redirect. TODO: verify the state and run a local callback.")
	println(cmd.OutOrStdout(), authURL)
	printf(cmd.OutOrStdout(), "\nThen run: shipyard connect gcp --project %s --code CODE\n", project)
	return nil
}

func exchangeGCP(cmd *cobra.Command, project, code string) error {
	clientID := os.Getenv("GCP_OAUTH_CLIENT_ID")
	clientSecret := os.Getenv("GCP_OAUTH_CLIENT_SECRET")
	redirect := envOr("GCP_OAUTH_REDIRECT_URI", defaultOAuthRedirect)
	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("set GCP_OAUTH_CLIENT_ID and GCP_OAUTH_CLIENT_SECRET")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	creds, err := gcp.Exchange(ctx, &http.Client{Timeout: 30 * time.Second}, "", clientID, clientSecret, code, redirect)
	if err != nil {
		return err
	}
	creds.Project = project
	if err := save(cmd.Context(), "gcp", creds); err != nil {
		return err
	}
	printf(cmd.OutOrStdout(), "connected gcp project %s\n", project)
	return nil
}

func connectGCPServiceAccount(cmd *cobra.Command, project, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("service account file: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		printf(cmd.ErrOrStderr(), "warning: %s is readable by group or others; chmod 600 is safer\n", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read service account: %w", err)
	}
	var meta struct {
		Type      string `json:"type"`
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("parse service account: %w", err)
	}
	if meta.Type != "service_account" {
		return fmt.Errorf("service account JSON type is %q, want service_account", meta.Type)
	}
	if project == "" {
		project = meta.ProjectID
	}
	if project == "" {
		return fmt.Errorf("pass --project or set GCP_PROJECT; the key has no project_id")
	}
	if err := save(cmd.Context(), "gcp", providers.Credentials{Project: project, ServiceAccountJSON: data}); err != nil {
		return err
	}
	printf(cmd.OutOrStdout(), "connected gcp project %s using a service account\n", project)
	return nil
}

func save(ctx context.Context, provider string, creds providers.Credentials) error {
	home, err := config.Dir()
	if err != nil {
		return err
	}
	cfg, err := config.Load(home)
	if err != nil {
		return err
	}
	store, err := openStore(home, cfg)
	if err != nil {
		return err
	}
	svc := &core.Service{
		Config: cfg,
		Store:  store,
		Audit:  audit.NewMemory(),
	}
	return svc.Connect(ctx, provider, creds)
}

func openStore(home string, cfg config.Config) (credentials.Store, error) {
	switch cfg.CredentialBackend {
	case "", config.CredentialFileBackend:
		if err := os.MkdirAll(home, 0o700); err != nil {
			return nil, err
		}
		if err := os.Chmod(home, 0o700); err != nil {
			return nil, err
		}
		return credentials.NewFileStore(filepath.Join(home, "credentials.json")), nil
	case config.CredentialKeychainBackend:
		return credentials.OSKeyring{}, nil
	default:
		return nil, fmt.Errorf("unknown credential backend %q", cfg.CredentialBackend)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func randomState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("oauth state: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
