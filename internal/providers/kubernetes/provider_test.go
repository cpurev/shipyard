package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/cpurev/shipyard/internal/providers"
)

func TestDeployStatusListAndSecret(t *testing.T) {
	ctx := context.Background()
	p, client := newTestProvider(t, Options{
		Zone:         "fi-hel2",
		Domain:       "apps.example.com",
		IngressClass: "traefik",
		TLSIssuer:    "letsencrypt-prod",
	})

	_, err := p.Deploy(ctx, providers.DeployRequest{Name: "hello", Region: "europe-west1", Framework: "node"})
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("missing image: %v", err)
	}

	dep, err := p.Deploy(ctx, providers.DeployRequest{
		Name:      "hello",
		Image:     "example.com/hello:1",
		Region:    "europe-west1",
		Framework: "node",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dep.Provider != "kubernetes" || dep.Region != "fi-hel2" || dep.URL != "https://hello.apps.example.com" {
		t.Fatalf("deployment = %+v", dep)
	}
	if dep.Status != "DEPLOYING" {
		t.Fatalf("status = %s", dep.Status)
	}
	if !strings.Contains(dep.Message, "hello-tls") {
		t.Fatalf("message = %s", dep.Message)
	}

	ns, err := client.CoreV1().Namespaces().Get(ctx, "apps", metav1.GetOptions{})
	if err != nil || ns.Name != "apps" {
		t.Fatalf("namespace: %v %+v", err, ns)
	}
	got, err := client.AppsV1().Deployments("apps").Get(ctx, "hello", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Labels[managedByLabel] != managedByValue {
		t.Fatalf("labels = %#v", got.Labels)
	}
	if got.Annotations[regionAnnotation] != "fi-hel2" || got.Annotations[frameworkAnnotation] != "node" {
		t.Fatalf("annotations = %#v", got.Annotations)
	}
	container := got.Spec.Template.Spec.Containers[0]
	if container.Image != "example.com/hello:1" || container.Ports[0].ContainerPort != 8080 {
		t.Fatalf("container = %+v", container)
	}
	if len(container.Env) != 1 || container.Env[0].Name != "PORT" || container.Env[0].Value != "8080" {
		t.Fatalf("env = %+v", container.Env)
	}
	if container.EnvFrom[0].SecretRef == nil || container.EnvFrom[0].SecretRef.Name != "hello-env" || container.EnvFrom[0].SecretRef.Optional == nil || !*container.EnvFrom[0].SecretRef.Optional {
		t.Fatalf("envFrom = %+v", container.EnvFrom)
	}
	svc, err := client.CoreV1().Services("apps").Get(ctx, "hello", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Spec.Ports[0].Port != 80 || svc.Spec.Ports[0].TargetPort.IntVal != 8080 {
		t.Fatalf("service ports = %+v", svc.Spec.Ports)
	}
	ing, err := client.NetworkingV1().Ingresses("apps").Get(ctx, "hello", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ing.Spec.Rules[0].Host != "hello.apps.example.com" {
		t.Fatalf("host = %s", ing.Spec.Rules[0].Host)
	}
	if ing.Spec.IngressClassName == nil || *ing.Spec.IngressClassName != "traefik" {
		t.Fatalf("class = %v", ing.Spec.IngressClassName)
	}
	if len(ing.Spec.TLS) != 1 || ing.Spec.TLS[0].SecretName != "hello-tls" {
		t.Fatalf("tls = %+v", ing.Spec.TLS)
	}
	if ing.Annotations["cert-manager.io/cluster-issuer"] != "letsencrypt-prod" {
		t.Fatalf("issuer annotation = %#v", ing.Annotations)
	}

	secretValue := "s3cret-value"
	if err := p.SetSecret(ctx, "hello", "API_KEY", secretValue); err != nil {
		t.Fatal(err)
	}
	secret, err := client.CoreV1().Secrets("apps").Get(ctx, "hello-env", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(secret.Data["API_KEY"]) != secretValue {
		t.Fatalf("secret data = %#v", secret.Data)
	}
	got, err = client.AppsV1().Deployments("apps").Get(ctx, "hello", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secretValue) {
		t.Fatal("secret value was copied onto the deployment")
	}
	if got.Spec.Template.Annotations[restartAnnotation] == "" {
		t.Fatal("secret update did not roll the deployment")
	}

	updated, err := p.Deploy(ctx, providers.DeployRequest{Name: "hello", Image: "example.com/hello:2", Framework: "node"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Region != "fi-hel2" {
		t.Fatalf("region = %s", updated.Region)
	}
	got, err = client.AppsV1().Deployments("apps").Get(ctx, "hello", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.Template.Spec.Containers[0].Image != "example.com/hello:2" {
		t.Fatalf("image = %s", got.Spec.Template.Spec.Containers[0].Image)
	}
	secret, err = client.CoreV1().Secrets("apps").Get(ctx, "hello-env", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(secret.Data["API_KEY"]) != secretValue {
		t.Fatal("redeploy dropped the secret")
	}

	other := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "apps", Labels: map[string]string{"app": "other"}},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "other"}}},
	}
	if _, err := client.AppsV1().Deployments("apps").Create(ctx, other, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	apps, err := p.ListApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Name != "hello" || apps[0].URL != "https://hello.apps.example.com" {
		t.Fatalf("apps = %+v", apps)
	}

	if _, err := p.Status(ctx, "missing"); !errors.Is(err, providers.ErrNotFound) {
		t.Fatalf("status missing: %v", err)
	}
	if err := p.SetSecret(ctx, "missing", "API_KEY", "x"); !errors.Is(err, providers.ErrNotFound) {
		t.Fatalf("secret missing: %v", err)
	}
	account, err := p.ProvisionAccount(ctx, providers.ProvisionRequest{})
	if account != nil || !errors.Is(err, providers.ErrNotSupported) {
		t.Fatalf("provision = %v %v", account, err)
	}
}

func TestStatusReady(t *testing.T) {
	ctx := context.Background()
	p, client := newTestProvider(t, Options{Domain: "apps.example.com"})
	if _, err := p.Deploy(ctx, providers.DeployRequest{Name: "hello", Image: "example.com/hello:1"}); err != nil {
		t.Fatal(err)
	}
	dep, err := client.AppsV1().Deployments("apps").Get(ctx, "hello", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dep.Status.Replicas = 1
	dep.Status.AvailableReplicas = 1
	dep.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:           appsv1.DeploymentAvailable,
		Status:         corev1.ConditionTrue,
		Message:        "ready",
		LastUpdateTime: metav1.NewTime(time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)),
	}}
	if _, err := client.AppsV1().Deployments("apps").UpdateStatus(ctx, dep, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	st, err := p.Status(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "READY" || st.URL != "https://hello.apps.example.com" {
		t.Fatalf("status = %+v", st)
	}
}

func TestLogsUseFakeClientset(t *testing.T) {
	ctx := context.Background()
	p, client := newTestProvider(t, Options{Domain: "apps.example.com"})
	client.PrependReactor("get", "pods/log", func(clienttesting.Action) (bool, runtime.Object, error) {
		body := "2026-11-16T12:00:00.000000000Z listening on 8080\nplain line\n"
		return true, &runtime.Unknown{Raw: []byte(body)}, nil
	})
	if _, err := p.Deploy(ctx, providers.DeployRequest{Name: "hello", Image: "example.com/hello:1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Pods("apps").Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "hello-abc", Namespace: "apps", Labels: appLabels("hello")},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Pods("apps").Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "apps", Labels: map[string]string{"app": "other"}},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	secret := "s3cret-value"
	if err := p.SetSecret(ctx, "hello", "API_KEY", secret); err != nil {
		t.Fatal(err)
	}
	lines, err := p.Logs(ctx, "hello", providers.LogOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[0].Message != "listening on 8080" || lines[0].Time.IsZero() || lines[1].Message != "plain line" {
		t.Fatalf("lines = %+v", lines)
	}
	blob := lines[0].Message + lines[1].Message
	if strings.Contains(blob, secret) {
		t.Fatal("logs contained the secret")
	}
	if _, err := p.Logs(ctx, "missing", providers.LogOptions{}); !errors.Is(err, providers.ErrNotFound) {
		t.Fatalf("logs missing: %v", err)
	}
}

func TestRefusesUnmanagedObjects(t *testing.T) {
	ctx := context.Background()
	p, client := newTestProvider(t, Options{})
	if _, err := client.AppsV1().Deployments("apps").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "hello", Namespace: "apps"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "hello"}},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "hello", Image: "example.com/other:1"}}}},
		},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err := p.Deploy(ctx, providers.DeployRequest{Name: "hello", Image: "example.com/hello:1"})
	if err == nil || !strings.Contains(err.Error(), "not managed") {
		t.Fatalf("err = %v", err)
	}
}

func TestNoPublicURLWithoutDomain(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	p, err := New(Options{Client: client, Namespace: "apps"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := p.Deploy(ctx, providers.DeployRequest{Name: "hello", Image: "example.com/hello:1", Region: "europe-west1"})
	if err != nil {
		t.Fatal(err)
	}
	if dep.URL != "http://hello.apps.svc" || dep.Region != "europe-west1" {
		t.Fatalf("deployment = %+v", dep)
	}
	ing, err := client.NetworkingV1().Ingresses("apps").Get(ctx, "hello", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ing.Spec.Rules[0].Host != "" || len(ing.Spec.TLS) != 0 {
		t.Fatalf("ingress = %+v", ing.Spec)
	}
}

func TestLatestTagIsAlwaysPulled(t *testing.T) {
	if pullPolicy("example.com/hello:1") != corev1.PullIfNotPresent {
		t.Fatal("tagged image")
	}
	if pullPolicy("example.com/hello:latest") != corev1.PullAlways {
		t.Fatal("latest")
	}
	if pullPolicy("example.com/hello") != corev1.PullAlways {
		t.Fatal("implicit latest")
	}
}

func TestValidateDomain(t *testing.T) {
	if err := ValidateDomain(""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDomain("apps.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDomain("https://apps.example.com"); err == nil {
		t.Fatal("accepted a URL")
	}
}

func TestKubeconfigIsLocal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	body := []byte(`apiVersion: v1
kind: Config
clusters:
- name: demo
  cluster:
    server: https://127.0.0.1:1
    insecure-skip-tls-verify: true
contexts:
- name: demo
  context:
    cluster: demo
    user: demo
current-context: demo
users:
- name: demo
  user:
    token: test-token
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	abs, err := ValidateKubeconfig(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateKubeconfig(path, "missing"); err == nil {
		t.Fatal("missing context was accepted")
	}
	// Building the client must not dial the dead server.
	p, err := New(Options{Kubeconfig: abs, Namespace: "apps", Domain: "apps.example.com", Zone: "fi-hel2"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "kubernetes" || p.zone != "fi-hel2" {
		t.Fatalf("provider = %+v", p)
	}
	if err := p.Connect(context.Background(), providers.Credentials{Extra: map[string]string{
		ExtraKubeconfig: abs,
		ExtraNamespace:  "apps",
		ExtraZone:       "se-sto1",
	}}); err != nil {
		t.Fatal(err)
	}
	if p.zone != "se-sto1" {
		t.Fatalf("zone = %s", p.zone)
	}
}

func TestInClusterConfigDoesNotDial(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	_, err := New(Options{InCluster: true})
	if err == nil {
		t.Fatal("expected in-cluster config to fail outside a pod")
	}
	if strings.Contains(err.Error(), "dial") {
		t.Fatalf("in-cluster setup dialed: %v", err)
	}
}

func TestApplyEnv(t *testing.T) {
	t.Setenv("SHIPYARD_KUBECONFIG", "/tmp/kubeconfig")
	t.Setenv("SHIPYARD_K8S_NAMESPACE", "workloads")
	t.Setenv("SHIPYARD_K8S_DOMAIN", "example.com")
	t.Setenv("SHIPYARD_K8S_ZONE", "fi-hel2")
	t.Setenv("SHIPYARD_K8S_IN_CLUSTER", "true")
	t.Setenv("SHIPYARD_K8S_INGRESS_CLASS", "")
	t.Setenv("SHIPYARD_K8S_CONTEXT", "")
	t.Setenv("SHIPYARD_K8S_TLS_ISSUER", "")
	opts := ApplyEnv(Options{Namespace: "default", IngressClass: "nginx"})
	if opts.Kubeconfig != "/tmp/kubeconfig" || opts.Namespace != "workloads" || opts.Zone != "fi-hel2" || !opts.InCluster {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.IngressClass != "nginx" {
		t.Fatal("empty env cleared a stored ingress class")
	}
}

func newTestProvider(t *testing.T, opts Options) (*Provider, *fake.Clientset) {
	t.Helper()
	client := fake.NewSimpleClientset()
	opts.Client = client
	if opts.Namespace == "" {
		opts.Namespace = "apps"
	}
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p, client
}
