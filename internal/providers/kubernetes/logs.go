package kubernetes

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/cpurev/shipyard/internal/providers"
)

// Logs reads the newest lines from pods selected by this app's labels.
// A deployment with no pods yet returns an empty slice. The secret value
// is never written here.
func (p *Provider) Logs(ctx context.Context, app string, opts providers.LogOptions) ([]providers.LogLine, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	if err := providers.ValidateAppName(app); err != nil {
		return nil, err
	}
	if _, err := p.managedDeployment(ctx, app); err != nil {
		return nil, err
	}
	selector := labels.SelectorFromSet(appLabels(app)).String()
	list, err := p.client.CoreV1().Pods(p.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("kubernetes: list pods: %w", err)
	}
	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].Name)
	}
	sort.Strings(names)

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	tail := int64(limit)
	logOpts := &corev1.PodLogOptions{Timestamps: true, TailLines: &tail}
	if !opts.Since.IsZero() {
		sec := int64(time.Since(opts.Since).Seconds())
		if sec < 1 {
			sec = 1
		}
		logOpts.SinceSeconds = &sec
	}

	var lines []providers.LogLine
	for _, name := range names {
		body, err := p.streamPodLog(ctx, name, logOpts)
		if err != nil {
			lines = append(lines, providers.LogLine{
				Level:   "error",
				Message: "pod " + name + ": " + err.Error(),
			})
			continue
		}
		lines = append(lines, parsePodLog(body)...)
	}
	return providers.LimitLines(lines, opts), nil
}

func (p *Provider) streamPodLog(ctx context.Context, pod string, opts *corev1.PodLogOptions) (string, error) {
	rc, err := p.client.CoreV1().Pods(p.namespace).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	return string(data), nil
}

func parsePodLog(body string) []providers.LogLine {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return nil
	}
	raw := strings.Split(body, "\n")
	out := make([]providers.LogLine, 0, len(raw))
	for _, line := range raw {
		if line == "" {
			continue
		}
		out = append(out, parsePodLogLine(line))
	}
	return out
}

func parsePodLogLine(line string) providers.LogLine {
	if i := strings.IndexByte(line, ' '); i > 0 {
		if when, err := time.Parse(time.RFC3339Nano, line[:i]); err == nil {
			return providers.LogLine{Time: when.UTC(), Level: "info", Message: line[i+1:]}
		}
	}
	return providers.LogLine{Level: "info", Message: line}
}
