package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cpurev/shipyard/internal/config"
	"github.com/cpurev/shipyard/internal/credentials"
	"github.com/cpurev/shipyard/internal/providers"
	"github.com/cpurev/shipyard/internal/providers/fake"
	"github.com/cpurev/shipyard/internal/providers/gcp"
	k8s "github.com/cpurev/shipyard/internal/providers/kubernetes"
	"github.com/cpurev/shipyard/internal/providers/vercel"
)

func openStore(home string, cfg config.Config) (credentials.Store, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, fmt.Errorf("create shipyard home: %w", err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return nil, fmt.Errorf("chmod shipyard home: %w", err)
	}
	switch cfg.CredentialBackend {
	case "", config.CredentialFileBackend:
		return credentials.NewFileStore(filepath.Join(home, "credentials.json")), nil
	case config.CredentialKeychainBackend:
		return credentials.OSKeyring{}, nil
	default:
		return nil, fmt.Errorf("unknown credential backend %q", cfg.CredentialBackend)
	}
}

func registerProviders(ctx context.Context, svc *Service, home string, all map[string]providers.Credentials) error {
	fakeProvider := fake.New(filepath.Join(home, "fake.json"))
	if creds, ok := all["fake"]; ok && providers.CredentialsReady("fake", creds) {
		if err := fakeProvider.Connect(ctx, creds); err != nil {
			return err
		}
	}
	svc.Providers["fake"] = fakeProvider

	if creds, ok := all["vercel"]; ok && providers.CredentialsReady("vercel", creds) {
		team := ""
		if creds.Extra != nil {
			team = creds.Extra["teamId"]
		}
		svc.Providers["vercel"] = vercel.New(vercel.Options{Token: creds.Token, TeamID: team})
	}
	if creds, ok := all["gcp"]; ok && providers.CredentialsReady("gcp", creds) {
		gcpProvider, err := gcp.New(ctx, gcp.Options{
			Project:         creds.Project,
			Region:          svc.Config.Region,
			Token:           creds.Token,
			CredentialsJSON: creds.ServiceAccountJSON,
		})
		if err != nil {
			return err
		}
		svc.Providers["gcp"] = gcpProvider
	}
	if creds, ok := all["kubernetes"]; kubernetesWanted(creds, ok) {
		opts := k8s.ApplyEnv(k8s.OptionsFromCredentials(creds))
		k8sProvider, err := k8s.New(opts)
		if err != nil {
			return err
		}
		svc.Providers["kubernetes"] = k8sProvider
	}
	return nil
}

// kubernetesWanted reports whether the operator asked for the kubernetes
// provider. SHIPYARD_KUBECONFIG is explicit. The ambient KUBECONFIG variable
// is ignored so a laptop kubectl setup is not picked up by tests or by serve.
func kubernetesWanted(creds providers.Credentials, ok bool) bool {
	if ok && providers.CredentialsReady("kubernetes", creds) {
		return true
	}
	if os.Getenv("SHIPYARD_KUBECONFIG") != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SHIPYARD_K8S_IN_CLUSTER"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
