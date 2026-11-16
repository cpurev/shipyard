package kubernetes

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
)

func TestFallbackManifestsAndImage(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	docker, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(docker)
	for _, want := range []string{"CGO_ENABLED=0", "USER 65532:65532", "EXPOSE 8080", "CMD [\"serve\"]", "SHIPYARD_PROVIDER=fake", "PORT=8080"} {
		if !strings.Contains(text, want) {
			t.Fatalf("Dockerfile missing %s", want)
		}
	}

	ankra, err := os.ReadFile(filepath.Join(root, ".ankra", "ankra.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ankraText := string(ankra)
	for _, want := range []string{"apiVersion: ankra.io/v1", "container_port: 8080", "dockerfile: Dockerfile", "- /healthz", "- /readyz"} {
		if !strings.Contains(ankraText, want) {
			t.Fatalf("ankra.yaml missing %s", want)
		}
	}
	if strings.Contains(ankraText, "/healthz") && strings.Count(ankraText, "/healthz") != 1 {
		t.Fatal("liveness and readiness must not share /healthz")
	}

	kustomize, err := os.ReadFile(filepath.Join(root, "deploy", "kubernetes", "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"namespace.yaml", "deployment.yaml", "service.yaml", "ingress.yaml"} {
		if !strings.Contains(string(kustomize), name) {
			t.Fatalf("kustomization missing %s", name)
		}
	}

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, appsv1.AddToScheme, networkingv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	decode := serializer.NewCodecFactory(scheme).UniversalDeserializer()
	var (
		deploy  *appsv1.Deployment
		svc     *corev1.Service
		ingress *networkingv1.Ingress
		ns      *corev1.Namespace
	)
	for _, name := range []string{"namespace.yaml", "deployment.yaml", "service.yaml", "ingress.yaml"} {
		data, err := os.ReadFile(filepath.Join(root, "deploy", "kubernetes", name))
		if err != nil {
			t.Fatal(err)
		}
		dec := kyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
		for {
			var raw runtime.RawExtension
			if err := dec.Decode(&raw); err != nil {
				if err == io.EOF {
					break
				}
				t.Fatal(err)
			}
			if len(bytes.TrimSpace(raw.Raw)) == 0 {
				continue
			}
			obj, _, err := decode.Decode(raw.Raw, nil, nil)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			switch typed := obj.(type) {
			case *appsv1.Deployment:
				deploy = typed
			case *corev1.Service:
				svc = typed
			case *networkingv1.Ingress:
				ingress = typed
			case *corev1.Namespace:
				ns = typed
			default:
				t.Fatalf("%s: unexpected %T", name, obj)
			}
		}
	}
	if ns == nil || ns.Name != "shipyard" {
		t.Fatalf("namespace = %+v", ns)
	}
	if deploy == nil || deploy.Spec.Template.Spec.SecurityContext == nil || !pointerBool(deploy.Spec.Template.Spec.SecurityContext.RunAsNonRoot) {
		t.Fatal("deployment is missing a non-root security context")
	}
	if deploy.Spec.Template.Spec.SecurityContext.RunAsUser == nil || *deploy.Spec.Template.Spec.SecurityContext.RunAsUser != 65532 {
		t.Fatal("deployment user")
	}
	container := deploy.Spec.Template.Spec.Containers[0]
	if container.Image != "shipyard:local" || len(container.Command) != 2 || container.Command[1] != "serve" {
		t.Fatalf("container = %+v", container)
	}
	if container.Ports[0].ContainerPort != 8080 {
		t.Fatal("container port")
	}
	foundPort, foundProvider := false, false
	for _, env := range container.Env {
		if env.Name == "PORT" && env.Value == "8080" {
			foundPort = true
		}
		if env.Name == "SHIPYARD_PROVIDER" && env.Value == "fake" {
			foundProvider = true
		}
	}
	if !foundPort || !foundProvider {
		t.Fatalf("env = %+v", container.Env)
	}
	if container.SecurityContext == nil || container.SecurityContext.ReadOnlyRootFilesystem == nil || !*container.SecurityContext.ReadOnlyRootFilesystem {
		t.Fatal("root filesystem is writable")
	}
	paths := map[string]bool{}
	for _, probe := range []*corev1.Probe{container.LivenessProbe, container.ReadinessProbe, container.StartupProbe} {
		if probe == nil || probe.HTTPGet == nil {
			t.Fatal("missing probe")
		}
		paths[probe.HTTPGet.Path] = true
	}
	if !paths["/healthz"] || !paths["/readyz"] {
		t.Fatalf("probes = %#v", paths)
	}
	if svc == nil || svc.Spec.Ports[0].Port != 80 || svc.Spec.Ports[0].TargetPort.StrVal != "http" {
		t.Fatalf("service = %+v", svc)
	}
	if ingress == nil || ingress.Spec.Rules[0].Host != "shipyard.example.com" || ingress.Spec.Rules[0].HTTP.Paths[0].Path != "/" {
		t.Fatalf("ingress = %+v", ingress)
	}
	if len(ingress.Spec.TLS) != 1 || ingress.Spec.TLS[0].SecretName != "shipyard-tls" {
		t.Fatalf("tls = %+v", ingress.Spec.TLS)
	}
}

func pointerBool(v *bool) bool {
	return v != nil && *v
}
