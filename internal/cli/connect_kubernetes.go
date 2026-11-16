package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cpurev/shipyard/internal/providers"
	"github.com/cpurev/shipyard/internal/providers/kubernetes"
	"github.com/cpurev/shipyard/internal/providers/upcloud"
)

func newConnectKubernetesCmd() *cobra.Command {
	opts := kubeConnectOpts{}
	cmd := &cobra.Command{
		Use:   "kubernetes",
		Short: "Connect a Kubernetes cluster you already have, using a kubeconfig",
		Long: `Store the path to a kubeconfig for a cluster you already run.
Shipyard does not create the cluster and does not call it during connect.
The documented target is an UpCloud Managed Kubernetes cluster; any
kubeconfig works.

The file is not copied. Later commands read it from the stored path.
Tokens inside the kubeconfig are not printed.

shipyard connect upcloud prints the upctl command that writes the file.
That command is not run from here.`,
		Example: `  shipyard connect kubernetes --kubeconfig "$HOME/.kube/config" --namespace apps --domain apps.example.com
  shipyard connect kubernetes --in-cluster --namespace shipyard`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runKubeConnect(cmd, opts)
		},
	}
	bindKubeFlags(cmd, &opts)
	return cmd
}

func newConnectUpCloudCmd() *cobra.Command {
	var cluster string
	opts := kubeConnectOpts{}
	cmd := &cobra.Command{
		Use:   "upcloud",
		Short: "Point Shipyard at an UpCloud Managed Kubernetes cluster you already have",
		Long: `Print how to download a kubeconfig for an UpCloud Managed Kubernetes
cluster, or store one you already downloaded.

This command does not call the UpCloud API, does not run upctl, and does
not create a cluster. With --kubeconfig it stores the same kubernetes
credentials as shipyard connect kubernetes, and records the zone (default
fi-hel2, Helsinki) as the region shown for later deploys.`,
		Example: `  shipyard connect upcloud
  shipyard connect upcloud --kubeconfig ./kubeconfig.yaml --zone fi-hel2 --domain apps.example.com`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			zone := opts.zone
			if zone == "" {
				zone = upcloud.DefaultZone
			}
			if opts.kubeconfig == "" && !opts.inCluster {
				for _, line := range upcloud.Steps(upcloud.Options{Zone: zone, Cluster: cluster}) {
					println(cmd.OutOrStdout(), line)
				}
				return nil
			}
			opts.zone = zone
			return runKubeConnect(cmd, opts)
		},
	}
	cmd.Flags().StringVar(&cluster, "cluster", "", "cluster UUID or name, used only in the printed upctl command")
	bindKubeFlags(cmd, &opts)
	return cmd
}

type kubeConnectOpts struct {
	kubeconfig   string
	contextName  string
	namespace    string
	domain       string
	ingressClass string
	zone         string
	tlsIssuer    string
	inCluster    bool
}

func bindKubeFlags(cmd *cobra.Command, opts *kubeConnectOpts) {
	cmd.Flags().StringVar(&opts.kubeconfig, "kubeconfig", "", "path to a kubeconfig for a cluster you already run")
	cmd.Flags().StringVar(&opts.contextName, "context", "", "kubeconfig context (default: the file's current context)")
	cmd.Flags().StringVar(&opts.namespace, "namespace", "", "namespace for apps (default: default, or SHIPYARD_K8S_NAMESPACE)")
	cmd.Flags().StringVar(&opts.domain, "domain", "", "base domain for Ingress hosts, for example apps.example.com")
	cmd.Flags().StringVar(&opts.ingressClass, "ingress-class", "", "IngressClass name (default: the cluster default)")
	cmd.Flags().StringVar(&opts.zone, "zone", "", "region recorded on each app, for example fi-hel2")
	cmd.Flags().StringVar(&opts.tlsIssuer, "tls-issuer", "", "optional cert-manager ClusterIssuer annotation; Shipyard does not create the issuer")
	cmd.Flags().BoolVar(&opts.inCluster, "in-cluster", false, "use the pod service account instead of a kubeconfig file")
}

func runKubeConnect(cmd *cobra.Command, opts kubeConnectOpts) error {
	if opts.inCluster && opts.kubeconfig != "" {
		return fmt.Errorf("pass either --kubeconfig or --in-cluster")
	}
	if !opts.inCluster {
		if opts.kubeconfig == "" {
			opts.kubeconfig = os.Getenv("KUBECONFIG")
		}
		if opts.kubeconfig == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("pass --kubeconfig or set KUBECONFIG")
			}
			opts.kubeconfig = filepath.Join(home, ".kube", "config")
		}
		abs, err := kubernetes.ValidateKubeconfig(opts.kubeconfig, opts.contextName)
		if err != nil {
			return err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			printf(cmd.ErrOrStderr(), "warning: %s is readable by group or others; chmod 600 is safer\n", abs)
		}
		opts.kubeconfig = abs
	}
	if opts.namespace == "" {
		opts.namespace = envOr("SHIPYARD_K8S_NAMESPACE", "default")
	}
	if opts.domain == "" {
		opts.domain = os.Getenv("SHIPYARD_K8S_DOMAIN")
	}
	if err := kubernetes.ValidateDomain(opts.domain); err != nil {
		return err
	}
	if opts.zone == "" {
		opts.zone = os.Getenv("SHIPYARD_K8S_ZONE")
	}
	if opts.ingressClass == "" {
		opts.ingressClass = os.Getenv("SHIPYARD_K8S_INGRESS_CLASS")
	}
	if opts.tlsIssuer == "" {
		opts.tlsIssuer = os.Getenv("SHIPYARD_K8S_TLS_ISSUER")
	}
	extra := map[string]string{kubernetes.ExtraNamespace: opts.namespace}
	if opts.inCluster {
		extra[kubernetes.ExtraInCluster] = "true"
	} else {
		extra[kubernetes.ExtraKubeconfig] = opts.kubeconfig
	}
	putExtra(extra, kubernetes.ExtraContext, opts.contextName)
	putExtra(extra, kubernetes.ExtraDomain, opts.domain)
	putExtra(extra, kubernetes.ExtraIngressClass, opts.ingressClass)
	putExtra(extra, kubernetes.ExtraZone, opts.zone)
	putExtra(extra, kubernetes.ExtraTLSIssuer, opts.tlsIssuer)
	if err := save(cmd.Context(), "kubernetes", providers.Credentials{Extra: extra}); err != nil {
		return err
	}
	if opts.inCluster {
		println(cmd.OutOrStdout(), "connected kubernetes (in-cluster)")
	} else {
		printf(cmd.OutOrStdout(), "connected kubernetes using %s\n", opts.kubeconfig)
	}
	println(cmd.OutOrStdout(), "deploys use --provider kubernetes and a prebuilt image; Shipyard does not build images or create the cluster")
	return nil
}

func putExtra(extra map[string]string, key, value string) {
	if value != "" {
		extra[key] = value
	}
}
