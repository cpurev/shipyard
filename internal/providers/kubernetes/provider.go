// Package kubernetes deploys a prebuilt container image to a cluster the
// operator already runs. The client is client-go. Tests pass a fake clientset.
// This package does not create clusters, build images, or call a cloud API
// while it is being constructed.
package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cpurev/shipyard/internal/providers"
	"github.com/cpurev/shipyard/internal/version"
)

const providerName = "kubernetes"

// Credential extra keys stored by `shipyard connect kubernetes`.
const (
	ExtraKubeconfig   = "kubeconfig"
	ExtraContext      = "context"
	ExtraNamespace    = "namespace"
	ExtraDomain       = "domain"
	ExtraIngressClass = "ingressClass"
	ExtraZone         = "zone"
	ExtraTLSIssuer    = "tlsIssuer"
	ExtraInCluster    = "inCluster"
)

// Options configures a client. Client is for tests; when it is set, New does
// not read a kubeconfig and does not talk to an API server.
type Options struct {
	Kubeconfig   string
	Context      string
	Namespace    string
	Domain       string
	IngressClass string
	// Zone is the region recorded on each app. For UpCloud this is a zone
	// such as fi-hel2. It is not sent to a cloud API.
	Zone      string
	TLSIssuer string
	InCluster bool
	Client    kubernetes.Interface
}

// Provider talks to one Kubernetes cluster.
type Provider struct {
	client       kubernetes.Interface
	namespace    string
	domain       string
	ingressClass string
	zone         string
	tlsIssuer    string
	connected    bool
}

// New builds a provider. A kubeconfig is loaded locally. New does not call
// the API server. InCluster reads the pod service-account files and also
// does not call the API server.
func New(opts Options) (*Provider, error) {
	ns := opts.Namespace
	if ns == "" {
		ns = "default"
	}
	if err := validateNamespace(ns); err != nil {
		return nil, err
	}
	if err := ValidateDomain(opts.Domain); err != nil {
		return nil, err
	}
	p := &Provider{
		namespace:    ns,
		domain:       opts.Domain,
		ingressClass: opts.IngressClass,
		zone:         opts.Zone,
		tlsIssuer:    opts.TLSIssuer,
		connected:    true,
	}
	if opts.Client != nil {
		p.client = opts.Client
		return p, nil
	}
	if !opts.InCluster && opts.Kubeconfig == "" {
		return nil, fmt.Errorf("kubernetes: %w", providers.ErrNotConnected)
	}
	cfg, err := restConfig(opts)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: client: %w", err)
	}
	p.client = client
	return p, nil
}

func restConfig(opts Options) (*rest.Config, error) {
	var (
		cfg *rest.Config
		err error
	)
	// A kubeconfig path wins so an explicit file is never ignored because
	// the process also happens to be running inside a cluster.
	if opts.Kubeconfig != "" {
		rules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: opts.Kubeconfig}
		overrides := &clientcmd.ConfigOverrides{CurrentContext: opts.Context}
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("kubernetes: kubeconfig: %w", err)
		}
	} else {
		cfg, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("kubernetes: in-cluster config: %w", err)
		}
	}
	cfg.UserAgent = "shipyard/" + version.Version
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	return cfg, nil
}

// OptionsFromCredentials reads the extra fields stored by connect.
func OptionsFromCredentials(creds providers.Credentials) Options {
	extra := creds.Extra
	if extra == nil {
		extra = map[string]string{}
	}
	return Options{
		Kubeconfig:   extra[ExtraKubeconfig],
		Context:      extra[ExtraContext],
		Namespace:    extra[ExtraNamespace],
		Domain:       extra[ExtraDomain],
		IngressClass: extra[ExtraIngressClass],
		Zone:         extra[ExtraZone],
		TLSIssuer:    extra[ExtraTLSIssuer],
		InCluster:    extra[ExtraInCluster] == "true",
	}
}

// ApplyEnv overlays SHIPYARD_KUBECONFIG and SHIPYARD_K8S_* on opts.
// An empty variable leaves the stored value alone. KUBECONFIG is not read
// here; a developer machine often has that set for kubectl, and tests must
// not pick it up.
func ApplyEnv(opts Options) Options {
	if v := os.Getenv("SHIPYARD_KUBECONFIG"); v != "" {
		opts.Kubeconfig = v
	}
	if v := os.Getenv("SHIPYARD_K8S_CONTEXT"); v != "" {
		opts.Context = v
	}
	if v := os.Getenv("SHIPYARD_K8S_NAMESPACE"); v != "" {
		opts.Namespace = v
	}
	if v := os.Getenv("SHIPYARD_K8S_DOMAIN"); v != "" {
		opts.Domain = v
	}
	if v := os.Getenv("SHIPYARD_K8S_INGRESS_CLASS"); v != "" {
		opts.IngressClass = v
	}
	if v := os.Getenv("SHIPYARD_K8S_ZONE"); v != "" {
		opts.Zone = v
	}
	if v := os.Getenv("SHIPYARD_K8S_TLS_ISSUER"); v != "" {
		opts.TLSIssuer = v
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SHIPYARD_K8S_IN_CLUSTER"))) {
	case "1", "true", "yes":
		opts.InCluster = true
	}
	return opts
}

// ValidateKubeconfig parses path and returns its absolute path.
// It does not contact the cluster. contextName may be empty to keep the
// file's current context.
func ValidateKubeconfig(path, contextName string) (string, error) {
	if path == "" {
		return "", errors.New("kubernetes: kubeconfig path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("kubernetes: kubeconfig path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("kubernetes: kubeconfig: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("kubernetes: kubeconfig %s is a directory", abs)
	}
	cfg, err := clientcmd.LoadFromFile(abs)
	if err != nil {
		return "", fmt.Errorf("kubernetes: read kubeconfig: %w", err)
	}
	if len(cfg.Clusters) == 0 {
		return "", errors.New("kubernetes: kubeconfig has no cluster")
	}
	if contextName != "" {
		if _, ok := cfg.Contexts[contextName]; !ok {
			return "", fmt.Errorf("kubernetes: context %q is not in the kubeconfig", contextName)
		}
	} else if cfg.CurrentContext == "" {
		return "", errors.New("kubernetes: kubeconfig has no current context; pass --context")
	}
	return abs, nil
}

// ValidateDomain rejects a URL. An empty domain is allowed; the Service is
// then only reachable inside the cluster.
func ValidateDomain(domain string) error {
	if domain == "" {
		return nil
	}
	if strings.Contains(domain, "://") || strings.Contains(domain, "/") || strings.ContainsAny(domain, " \t") {
		return fmt.Errorf("kubernetes: domain %q must be a hostname, not a URL", domain)
	}
	if strings.Contains(domain, "..") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return fmt.Errorf("kubernetes: domain %q is not a hostname", domain)
	}
	return nil
}

func (p *Provider) Name() string { return providerName }

// Connect rebuilds the client from credentials for a cluster the operator
// already has. It does not call the API server.
func (p *Provider) Connect(_ context.Context, creds providers.Credentials) error {
	next, err := New(OptionsFromCredentials(creds))
	if err != nil {
		return err
	}
	*p = *next
	return nil
}

// Deploy creates or updates a Deployment, Service, and Ingress for a
// prebuilt image. The container listens on port 8080 (PORT=8080).
func (p *Provider) Deploy(ctx context.Context, req providers.DeployRequest) (*providers.Deployment, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	if err := providers.ValidateAppName(req.Name); err != nil {
		return nil, err
	}
	image := strings.TrimSpace(req.Image)
	if image == "" {
		return nil, errors.New("kubernetes: a container image is required (--image); this provider does not build or push images")
	}
	if strings.ContainsAny(image, " \t\r\n") {
		return nil, fmt.Errorf("kubernetes: image %q contains whitespace", req.Image)
	}
	req.Image = image
	if err := p.ensureNamespace(ctx); err != nil {
		return nil, err
	}
	region := p.region(req.Region)
	if err := p.applyDeployment(ctx, req.Name, req.Image, region, req.Framework); err != nil {
		return nil, err
	}
	if err := p.applyService(ctx, req.Name); err != nil {
		return nil, err
	}
	if err := p.applyIngress(ctx, req.Name); err != nil {
		return nil, err
	}
	status, message := "DEPLOYING", p.deployMessage(req.Name)
	st, err := p.Status(ctx, req.Name)
	if err == nil && st.Status == "READY" {
		status = st.Status
		if st.Message != "" {
			message = st.Message
		}
	}
	return &providers.Deployment{
		ID:        objectID(p.namespace, req.Name),
		AppName:   req.Name,
		URL:       p.url(req.Name),
		Status:    status,
		Provider:  providerName,
		Region:    region,
		Framework: req.Framework,
		Message:   message,
	}, nil
}

func (p *Provider) Status(ctx context.Context, app string) (*providers.AppStatus, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	if err := providers.ValidateAppName(app); err != nil {
		return nil, err
	}
	dep, err := p.managedDeployment(ctx, app)
	if err != nil {
		return nil, err
	}
	state, message := deploymentState(dep)
	updated := dep.CreationTimestamp.Time
	for _, cond := range dep.Status.Conditions {
		if cond.LastUpdateTime.After(updated) {
			updated = cond.LastUpdateTime.Time
		}
	}
	return &providers.AppStatus{
		Name:      app,
		ID:        objectID(p.namespace, app),
		URL:       p.statusURL(ctx, app),
		Status:    state,
		Region:    recordedRegion(dep, p.zone),
		Provider:  providerName,
		UpdatedAt: updated,
		Message:   message,
	}, nil
}

// SetSecret writes one key on a Secret named <app>-env and rolls the
// Deployment so the new value is loaded into the pod. The value is not
// written into annotations or log lines.
func (p *Provider) SetSecret(ctx context.Context, app, key, value string) error {
	if err := p.ready(); err != nil {
		return err
	}
	if err := providers.ValidateAppName(app); err != nil {
		return err
	}
	dep, err := p.managedDeployment(ctx, app)
	if err != nil {
		return err
	}
	if err := p.writeSecret(ctx, app, key, value); err != nil {
		return err
	}
	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = map[string]string{}
	}
	dep.Spec.Template.Annotations[restartAnnotation] = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := p.client.AppsV1().Deployments(p.namespace).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("kubernetes: restart deployment: %w", err)
	}
	return nil
}

func (p *Provider) ListApps(ctx context.Context) ([]providers.App, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	list, err := p.client.AppsV1().Deployments(p.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: managedByLabel + "=" + managedByValue,
	})
	if err != nil {
		return nil, fmt.Errorf("kubernetes: list deployments: %w", err)
	}
	out := make([]providers.App, 0, len(list.Items))
	for i := range list.Items {
		dep := &list.Items[i]
		if !managed(dep.Labels) {
			continue
		}
		state, _ := deploymentState(dep)
		out = append(out, providers.App{
			Name:     dep.Name,
			ID:       objectID(p.namespace, dep.Name),
			URL:      p.statusURL(ctx, dep.Name),
			Provider: providerName,
			Region:   recordedRegion(dep, p.zone),
			Status:   state,
		})
	}
	return out, nil
}

// ProvisionAccount is a stub.
//
// TODO: do not implement. Automatic account creation would break the
// providers' terms and be blocked by captchas and KYC checks. Connect a
// cluster the operator already runs.
func (p *Provider) ProvisionAccount(context.Context, providers.ProvisionRequest) (*providers.Account, error) {
	return providers.RejectProvisionAccount()
}

func (p *Provider) ready() error {
	if p == nil || !p.connected || p.client == nil {
		return fmt.Errorf("kubernetes: %w", providers.ErrNotConnected)
	}
	return nil
}

func (p *Provider) region(requested string) string {
	if p.zone != "" {
		return p.zone
	}
	return requested
}

func recordedRegion(dep *appsv1.Deployment, zone string) string {
	if dep != nil && dep.Annotations != nil && dep.Annotations[regionAnnotation] != "" {
		return dep.Annotations[regionAnnotation]
	}
	return zone
}

func (p *Provider) url(name string) string {
	if p.domain != "" {
		return "https://" + name + "." + p.domain
	}
	return "http://" + name + "." + p.namespace + ".svc"
}

func (p *Provider) statusURL(ctx context.Context, name string) string {
	ing, err := p.client.NetworkingV1().Ingresses(p.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil || len(ing.Spec.Rules) == 0 || ing.Spec.Rules[0].Host == "" {
		return p.url(name)
	}
	return "https://" + ing.Spec.Rules[0].Host
}

func (p *Provider) deployMessage(name string) string {
	if p.domain != "" {
		return "submitted Deployment, Service, and Ingress for " + name + "; the TLS secret " + name + "-tls is not created by Shipyard"
	}
	return "submitted Deployment, Service, and Ingress for " + name + "; set a domain for a public HTTPS host"
}

func (p *Provider) ensureNamespace(ctx context.Context) error {
	_, err := p.client.CoreV1().Namespaces().Get(ctx, p.namespace, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("kubernetes: namespace %s: %w", p.namespace, err)
	}
	_, err = p.client.CoreV1().Namespaces().Create(ctx, namespaceObject(p.namespace), metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("kubernetes: create namespace %s: %w", p.namespace, err)
	}
	return nil
}

func (p *Provider) managedDeployment(ctx context.Context, app string) (*appsv1.Deployment, error) {
	dep, err := p.client.AppsV1().Deployments(p.namespace).Get(ctx, app, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("kubernetes: %w", providers.ErrNotFound)
		}
		return nil, fmt.Errorf("kubernetes: get deployment: %w", err)
	}
	if !managed(dep.Labels) {
		return nil, fmt.Errorf("kubernetes: deployment %s exists and is not managed by shipyard", app)
	}
	return dep, nil
}

func objectID(namespace, name string) string {
	return namespace + "/" + name
}

func validateNamespace(ns string) error {
	if len(ns) == 0 || len(ns) > 63 {
		return fmt.Errorf("kubernetes: namespace %q must be a DNS label", ns)
	}
	for i, r := range ns {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0 && i < len(ns)-1:
		default:
			return fmt.Errorf("kubernetes: namespace %q must be a DNS label", ns)
		}
	}
	return nil
}

var _ providers.Provider = (*Provider)(nil)
