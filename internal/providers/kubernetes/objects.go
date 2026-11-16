package kubernetes

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	nameLabel           = "app.kubernetes.io/name"
	managedByLabel      = "app.kubernetes.io/managed-by"
	managedByValue      = "shipyard"
	partOfLabel         = "app.kubernetes.io/part-of"
	regionAnnotation    = "shipyard.dev/region"
	frameworkAnnotation = "shipyard.dev/framework"
	restartAnnotation   = "shipyard.dev/restartedAt"

	// containerPort is the port every deployed app is expected to listen on.
	// The Service targets it, and PORT is set to the same value.
	containerPort int32 = 8080
	servicePort   int32 = 80
)

func appLabels(name string) map[string]string {
	return map[string]string{
		nameLabel:      name,
		managedByLabel: managedByValue,
		partOfLabel:    managedByValue,
	}
}

func managed(labels map[string]string) bool {
	return labels[managedByLabel] == managedByValue
}

func namespaceObject(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func (p *Provider) deployment(name, image, region, framework string) *appsv1.Deployment {
	labels := appLabels(name)
	replicas := int32(1)
	optional := true
	annotations := map[string]string{}
	if region != "" {
		annotations[regionAnnotation] = region
	}
	if framework != "" {
		annotations[frameworkAnnotation] = framework
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   p.namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
				nameLabel:      name,
				managedByLabel: managedByValue,
			}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{p.container(name, image, &optional)},
				},
			},
		},
	}
}

func (p *Provider) container(name, image string, optional *bool) corev1.Container {
	return corev1.Container{
		Name:            name,
		Image:           image,
		ImagePullPolicy: pullPolicy(image),
		Ports: []corev1.ContainerPort{{
			Name:          "http",
			ContainerPort: containerPort,
			Protocol:      corev1.ProtocolTCP,
		}},
		Env: []corev1.EnvVar{{
			Name:  "PORT",
			Value: "8080",
		}},
		EnvFrom: []corev1.EnvFromSource{{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName(name)},
				Optional:             optional,
			},
		}},
	}
}

func pullPolicy(image string) corev1.PullPolicy {
	// A moving tag would otherwise stay cached on the node. Immutable tags
	// keep IfNotPresent so a redeploy does not pull the same bytes again.
	ref := image
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	if !strings.Contains(ref, ":") || strings.HasSuffix(ref, ":latest") {
		return corev1.PullAlways
	}
	return corev1.PullIfNotPresent
}

func (p *Provider) service(name string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: p.namespace,
			Labels:    appLabels(name),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{nameLabel: name, managedByLabel: managedByValue},
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       servicePort,
				TargetPort: intstr.FromInt32(containerPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

func (p *Provider) ingress(name string) *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	annotations := map[string]string{}
	if p.tlsIssuer != "" {
		annotations["cert-manager.io/cluster-issuer"] = p.tlsIssuer
	}
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   p.namespace,
			Labels:      appLabels(name),
			Annotations: annotations,
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: name,
									Port: networkingv1.ServiceBackendPort{Number: servicePort},
								},
							},
						}},
					},
				},
			}},
		},
	}
	if p.ingressClass != "" {
		class := p.ingressClass
		ing.Spec.IngressClassName = &class
	}
	if p.domain != "" {
		host := name + "." + p.domain
		ing.Spec.Rules[0].Host = host
		ing.Spec.TLS = []networkingv1.IngressTLS{{
			Hosts:      []string{host},
			SecretName: name + "-tls",
		}}
	}
	return ing
}

func secretName(app string) string {
	return app + "-env"
}

func (p *Provider) applyDeployment(ctx context.Context, name, image, region, framework string) error {
	want := p.deployment(name, image, region, framework)
	got, err := p.client.AppsV1().Deployments(p.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = p.client.AppsV1().Deployments(p.namespace).Create(ctx, want, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("kubernetes: create deployment: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("kubernetes: get deployment: %w", err)
	}
	if !managed(got.Labels) {
		return fmt.Errorf("kubernetes: deployment %s exists and is not managed by shipyard", name)
	}
	got.Labels = mergeStringMap(got.Labels, want.Labels)
	got.Annotations = mergeStringMap(got.Annotations, want.Annotations)
	got.Spec.Template.Labels = want.Spec.Template.Labels
	got.Spec.Template.Spec.Containers = want.Spec.Template.Spec.Containers
	if _, err := p.client.AppsV1().Deployments(p.namespace).Update(ctx, got, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("kubernetes: update deployment: %w", err)
	}
	return nil
}

func (p *Provider) applyService(ctx context.Context, name string) error {
	want := p.service(name)
	got, err := p.client.CoreV1().Services(p.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = p.client.CoreV1().Services(p.namespace).Create(ctx, want, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("kubernetes: create service: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("kubernetes: get service: %w", err)
	}
	if !managed(got.Labels) {
		return fmt.Errorf("kubernetes: service %s exists and is not managed by shipyard", name)
	}
	got.Labels = mergeStringMap(got.Labels, want.Labels)
	got.Spec.Ports = want.Spec.Ports
	got.Spec.Selector = want.Spec.Selector
	if _, err := p.client.CoreV1().Services(p.namespace).Update(ctx, got, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("kubernetes: update service: %w", err)
	}
	return nil
}

func (p *Provider) applyIngress(ctx context.Context, name string) error {
	want := p.ingress(name)
	got, err := p.client.NetworkingV1().Ingresses(p.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = p.client.NetworkingV1().Ingresses(p.namespace).Create(ctx, want, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("kubernetes: create ingress: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("kubernetes: get ingress: %w", err)
	}
	if !managed(got.Labels) {
		return fmt.Errorf("kubernetes: ingress %s exists and is not managed by shipyard", name)
	}
	got.Labels = mergeStringMap(got.Labels, want.Labels)
	got.Annotations = mergeStringMap(got.Annotations, want.Annotations)
	got.Spec.Rules = want.Spec.Rules
	got.Spec.TLS = want.Spec.TLS
	got.Spec.IngressClassName = want.Spec.IngressClassName
	if _, err := p.client.NetworkingV1().Ingresses(p.namespace).Update(ctx, got, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("kubernetes: update ingress: %w", err)
	}
	return nil
}

func (p *Provider) writeSecret(ctx context.Context, app, key, value string) error {
	name := secretName(app)
	got, err := p.client.CoreV1().Secrets(p.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = p.client.CoreV1().Secrets(p.namespace).Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: p.namespace,
				Labels:    appLabels(app),
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{key: []byte(value)},
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("kubernetes: create secret: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("kubernetes: get secret: %w", err)
	}
	if !managed(got.Labels) {
		return fmt.Errorf("kubernetes: secret %s exists and is not managed by shipyard", name)
	}
	if got.Data == nil {
		got.Data = map[string][]byte{}
	}
	got.Data[key] = []byte(value)
	if _, err := p.client.CoreV1().Secrets(p.namespace).Update(ctx, got, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("kubernetes: update secret: %w", err)
	}
	return nil
}

func deploymentState(dep *appsv1.Deployment) (string, string) {
	var available corev1.ConditionStatus
	availableMessage := ""
	for _, cond := range dep.Status.Conditions {
		switch cond.Type {
		case appsv1.DeploymentAvailable:
			available = cond.Status
			availableMessage = cond.Message
		case appsv1.DeploymentProgressing:
			if cond.Reason == "ProgressDeadlineExceeded" {
				msg := cond.Message
				if msg == "" {
					msg = "progress deadline exceeded"
				}
				return "ERROR", msg
			}
		}
	}
	ready := dep.Status.AvailableReplicas
	want := int32(1)
	if dep.Spec.Replicas != nil {
		want = *dep.Spec.Replicas
	}
	summary := fmt.Sprintf("%d/%d ready", ready, want)
	if ready > 0 && available != corev1.ConditionFalse {
		if availableMessage != "" {
			return "READY", availableMessage
		}
		return "READY", summary
	}
	if available == corev1.ConditionFalse {
		if availableMessage == "" {
			availableMessage = "deployment is not available"
		}
		return "ERROR", availableMessage
	}
	return "DEPLOYING", summary
}

func mergeStringMap(base, overlay map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}
