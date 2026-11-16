// Package upcloud documents how an operator gets a kubeconfig for an
// UpCloud Managed Kubernetes cluster they already have.
//
// Nothing in this package calls the UpCloud API, upctl, or the network.
// Creating the cluster and downloading the kubeconfig stay with the operator.
// Shipyard then uses the kubernetes provider against that file.
package upcloud

import "strings"

const (
	// DefaultZone is UpCloud's Helsinki zone. It is the documented default
	// for a European cluster. se-sto1 (Stockholm) is the other nearby zone.
	// The zone is chosen when the cluster is created. Shipyard does not
	// create the cluster and does not send the zone to an API.
	DefaultZone = "fi-hel2"

	// DocsURL is the UpCloud guide for downloading a kubeconfig.
	DocsURL = "https://upcloud.com/docs/guides/get-started-managed-kubernetes/"

	// CLIDocsURL documents `upctl kubernetes config`.
	CLIDocsURL = "https://upcloudltd.github.io/upcloud-cli/latest/commands_reference/upctl_kubernetes/config/"
)

// EUZones are the UpCloud zones in Europe. The list is documentation.
var EUZones = []string{
	"fi-hel1",
	"fi-hel2",
	"se-sto1",
	"de-fra1",
	"nl-ams1",
	"uk-lon1",
	"es-mad1",
	"pl-waw1",
}

// Options fills the printed instructions. Empty fields use placeholders.
type Options struct {
	Zone       string
	Cluster    string
	OutputPath string
}

// KubeconfigCommand is the upctl invocation that writes a kubeconfig.
// The string is for the operator to run. This function does not run it.
func KubeconfigCommand(cluster, outputPath string) string {
	if strings.TrimSpace(cluster) == "" {
		cluster = "<cluster-uuid-or-name>"
	}
	if strings.TrimSpace(outputPath) == "" {
		outputPath = "kubeconfig.yaml"
	}
	return "upctl kubernetes config " + cluster + " --output yaml --write " + outputPath
}

// Steps is the operator checklist for pointing Shipyard at an existing
// UpCloud Managed Kubernetes cluster.
func Steps(opts Options) []string {
	zone := opts.Zone
	if zone == "" {
		zone = DefaultZone
	}
	out := opts.OutputPath
	if out == "" {
		out = "kubeconfig.yaml"
	}
	return []string{
		"Shipyard does not call UpCloud and does not create a cluster.",
		"1. Open a Managed Kubernetes cluster you already have, in an EU zone.",
		"   The documented default zone is " + zone + " (Helsinki). se-sto1 is Stockholm.",
		"   Other EU zones: " + strings.Join(EUZones, ", ") + ".",
		"   Docs: " + DocsURL,
		"2. Download the kubeconfig from the cluster Overview page in the UpCloud Hub,",
		"   or run this yourself. Shipyard does not run it:",
		"   " + KubeconfigCommand(opts.Cluster, out),
		"   CLI: " + CLIDocsURL,
		"3. Connect the file. This stores the path and does not call the cluster:",
		"   shipyard connect upcloud --kubeconfig " + out + " --zone " + zone,
		"4. Deploy a prebuilt image:",
		"   shipyard deploy hello --provider kubernetes --image example.com/hello:1",
	}
}
