package k8s

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const (
	defaultLogTail int64 = 100
	maxLogBytes          = 32 * 1024
	maxListItems         = 50
)

// NamespaceGuard rejects mutations against protected namespaces.
type NamespaceGuard struct {
	protected map[string]struct{}
}

func NewNamespaceGuard(namespaces []string) *NamespaceGuard {
	m := make(map[string]struct{}, len(namespaces))
	for _, n := range namespaces {
		n = strings.TrimSpace(n)
		if n != "" {
			m[n] = struct{}{}
		}
	}
	return &NamespaceGuard{protected: m}
}

func (g *NamespaceGuard) AssertWritable(namespace string) error {
	if _, ok := g.protected[namespace]; ok {
		return fmt.Errorf("%w: %s", domain.ErrProtectedNamespace, namespace)
	}
	return nil
}

func (g *NamespaceGuard) Protected() []string {
	out := make([]string, 0, len(g.protected))
	for n := range g.protected {
		out = append(out, n)
	}
	return out
}

// InvestigatorClient is a read-only Kubernetes adapter.
type InvestigatorClient struct {
	cs    kubernetes.Interface
	guard *NamespaceGuard
}

func NewInvestigatorClient(cs kubernetes.Interface, guard *NamespaceGuard) *InvestigatorClient {
	return &InvestigatorClient{cs: cs, guard: guard}
}

func (c *InvestigatorClient) GetPod(ctx context.Context, namespace, name string) (*ports.PodView, error) {
	pod, err := c.cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, domain.Wrap(err, "get pod")
	}
	v := podView(*pod)
	return &v, nil
}

func (c *InvestigatorClient) ListPods(ctx context.Context, namespace, labelSelector string) ([]ports.PodView, error) {
	list, err := c.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
		Limit:         maxListItems,
	})
	if err != nil {
		return nil, domain.Wrap(err, "list pods")
	}
	out := make([]ports.PodView, 0, len(list.Items))
	for _, p := range list.Items {
		out = append(out, podView(p))
	}
	return out, nil
}

func (c *InvestigatorClient) GetDeployment(ctx context.Context, namespace, name string) (*ports.DeploymentView, error) {
	d, err := c.cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, domain.Wrap(err, "get deployment")
	}
	v := deploymentView(*d)
	return &v, nil
}

func (c *InvestigatorClient) ListEvents(ctx context.Context, namespace, involvedKind, involvedName string) ([]ports.EventView, error) {
	field := ""
	if involvedName != "" {
		field = fmt.Sprintf("involvedObject.name=%s", involvedName)
		if involvedKind != "" {
			field += fmt.Sprintf(",involvedObject.kind=%s", involvedKind)
		}
	}
	list, err := c.cs.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: field,
		Limit:         maxListItems,
	})
	if err != nil {
		return nil, domain.Wrap(err, "list events")
	}
	out := make([]ports.EventView, 0, len(list.Items))
	for _, e := range list.Items {
		last := e.LastTimestamp.Time
		if last.IsZero() {
			last = e.EventTime.Time
		}
		out = append(out, ports.EventView{
			Namespace: e.Namespace,
			Kind:      e.InvolvedObject.Kind,
			Name:      e.InvolvedObject.Name,
			Type:      e.Type,
			Reason:    e.Reason,
			Message:   e.Message,
			Count:     e.Count,
			LastSeen:  last.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

func (c *InvestigatorClient) GetPodLogs(ctx context.Context, namespace, name, container string, tailLines int64) (string, error) {
	if tailLines <= 0 || tailLines > defaultLogTail {
		tailLines = defaultLogTail
	}
	opts := &corev1.PodLogOptions{
		TailLines: &tailLines,
	}
	if container != "" {
		opts.Container = container
	}
	stream, err := c.cs.CoreV1().Pods(namespace).GetLogs(name, opts).Stream(ctx)
	if err != nil {
		return "", domain.Wrap(err, "stream pod logs")
	}
	defer stream.Close()
	limited := io.LimitReader(stream, maxLogBytes)
	b, err := io.ReadAll(limited)
	if err != nil {
		return "", domain.Wrap(err, "read pod logs")
	}
	return string(b), nil
}

func (c *InvestigatorClient) GetHPA(ctx context.Context, namespace, name string) (*ports.HPAView, error) {
	h, err := c.cs.AutoscalingV2().HorizontalPodAutoscalers(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, domain.Wrap(err, "get hpa")
	}
	v := hpaView(*h)
	return &v, nil
}

func (c *InvestigatorClient) ListHPAs(ctx context.Context, namespace string) ([]ports.HPAView, error) {
	list, err := c.cs.AutoscalingV2().HorizontalPodAutoscalers(namespace).List(ctx, metav1.ListOptions{Limit: maxListItems})
	if err != nil {
		return nil, domain.Wrap(err, "list hpas")
	}
	out := make([]ports.HPAView, 0, len(list.Items))
	for _, h := range list.Items {
		out = append(out, hpaView(h))
	}
	return out, nil
}

// RemediatorClient is a write-scoped Kubernetes adapter.
type RemediatorClient struct {
	cs          kubernetes.Interface
	guard       *NamespaceGuard
	maxReplicas int32
}

func NewRemediatorClient(cs kubernetes.Interface, guard *NamespaceGuard, maxReplicas int32) *RemediatorClient {
	if maxReplicas <= 0 {
		maxReplicas = 30
	}
	return &RemediatorClient{cs: cs, guard: guard, maxReplicas: maxReplicas}
}

func (c *RemediatorClient) PatchHPAMaxReplicas(ctx context.Context, namespace, name string, maxReplicas int32) (*ports.HPAView, error) {
	if err := c.guard.AssertWritable(namespace); err != nil {
		return nil, err
	}
	if maxReplicas < 1 {
		return nil, fmt.Errorf("%w: maxReplicas must be >= 1", domain.ErrInvalidToolArgs)
	}
	if maxReplicas > c.maxReplicas {
		return nil, fmt.Errorf("%w: %d > %d", domain.ErrReplicaCapExceeded, maxReplicas, c.maxReplicas)
	}
	patch := []byte(fmt.Sprintf(`{"spec":{"maxReplicas":%d}}`, maxReplicas))
	h, err := c.cs.AutoscalingV2().HorizontalPodAutoscalers(namespace).Patch(
		ctx, name, types.MergePatchType, patch, metav1.PatchOptions{},
	)
	if err != nil {
		return nil, domain.Wrap(err, "patch hpa maxReplicas")
	}
	v := hpaView(*h)
	return &v, nil
}

func podView(p corev1.Pod) ports.PodView {
	var restarts int32
	ready := 0
	total := len(p.Status.ContainerStatuses)
	for _, cs := range p.Status.ContainerStatuses {
		restarts += cs.RestartCount
		if cs.Ready {
			ready++
		}
	}
	reason, msg := "", ""
	if !p.DeletionTimestamp.IsZero() {
		reason = "Terminating"
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status != corev1.ConditionTrue {
			reason = c.Reason
			msg = c.Message
		}
	}
	return ports.PodView{
		Namespace: p.Namespace,
		Name:      p.Name,
		Phase:     string(p.Status.Phase),
		Ready:     fmt.Sprintf("%d/%d", ready, total),
		Restarts:  restarts,
		Node:      p.Spec.NodeName,
		Reason:    reason,
		Message:   msg,
		CreatedAt: p.CreationTimestamp.UTC().Format(time.RFC3339),
	}
}

func deploymentView(d appsv1.Deployment) ports.DeploymentView {
	conds := make([]string, 0, len(d.Status.Conditions))
	for _, c := range d.Status.Conditions {
		conds = append(conds, fmt.Sprintf("%s=%s:%s", c.Type, c.Status, c.Reason))
	}
	var replicas int32
	if d.Spec.Replicas != nil {
		replicas = *d.Spec.Replicas
	}
	return ports.DeploymentView{
		Namespace:       d.Namespace,
		Name:            d.Name,
		Replicas:        replicas,
		ReadyReplicas:   d.Status.ReadyReplicas,
		UpdatedReplicas: d.Status.UpdatedReplicas,
		Unavailable:     d.Status.UnavailableReplicas,
		Conditions:      conds,
	}
}

func hpaView(h autoscalingv2.HorizontalPodAutoscaler) ports.HPAView {
	var min int32
	if h.Spec.MinReplicas != nil {
		min = *h.Spec.MinReplicas
	}
	metrics := make([]string, 0, len(h.Status.CurrentMetrics))
	for _, m := range h.Status.CurrentMetrics {
		switch m.Type {
		case autoscalingv2.ResourceMetricSourceType:
			if m.Resource != nil {
				metrics = append(metrics, fmt.Sprintf("resource:%s avgUtil=%v", m.Resource.Name, m.Resource.Current.AverageUtilization))
			}
		default:
			metrics = append(metrics, string(m.Type))
		}
	}
	last := ""
	if h.Status.LastScaleTime != nil {
		last = h.Status.LastScaleTime.UTC().Format(time.RFC3339)
	}
	return ports.HPAView{
		Namespace:     h.Namespace,
		Name:          h.Name,
		MinReplicas:   min,
		MaxReplicas:   h.Spec.MaxReplicas,
		Desired:       h.Status.DesiredReplicas,
		Current:       h.Status.CurrentReplicas,
		TargetRef:     fmt.Sprintf("%s/%s", h.Spec.ScaleTargetRef.Kind, h.Spec.ScaleTargetRef.Name),
		Metrics:       metrics,
		LastScaleTime: last,
	}
}
