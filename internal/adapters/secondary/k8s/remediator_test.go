package k8s

import (
	"context"
	"errors"
	"testing"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRemediatorClientRestartScaleAndGuards(t *testing.T) {
	ctx := context.Background()
	replicas := int32(2)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "api:1"}}},
			},
		},
	}
	sys := dep.DeepCopy()
	sys.Namespace = "kube-system"
	sys.Name = "coredns"
	c := NewRemediatorClient(fake.NewSimpleClientset(dep, sys), NewNamespaceGuard([]string{"kube-system"}), 30)

	if _, err := c.RestartRollout(ctx, "kube-system", "coredns"); !errors.Is(err, domain.ErrProtectedNamespace) {
		t.Fatalf("protected restart: %v", err)
	}
	view, err := c.RestartRollout(ctx, "prod", "api")
	if err != nil {
		t.Fatal(err)
	}
	if view.Name != "api" {
		t.Fatalf("restart view=%+v", view)
	}
	scaled, err := c.ScaleDeployment(ctx, "prod", "api", 5)
	if err != nil {
		t.Fatal(err)
	}
	if scaled.Replicas != 5 {
		t.Fatalf("scaled replicas=%d", scaled.Replicas)
	}
	if _, err := c.ScaleDeployment(ctx, "prod", "api", 99); !errors.Is(err, domain.ErrReplicaCapExceeded) {
		t.Fatalf("cap: %v", err)
	}
}

func TestRemediatorClientDeleteCrashLoopOnly(t *testing.T) {
	ctx := context.Background()
	loop := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "prod"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		}}},
	}
	healthy := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "prod"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	cs := fake.NewSimpleClientset(loop, healthy)
	c := NewRemediatorClient(cs, NewNamespaceGuard(nil), 30)

	if _, err := c.DeleteCrashLoopPod(ctx, "prod", "api-1"); !errors.Is(err, domain.ErrInvalidToolArgs) {
		t.Fatalf("healthy delete: %v", err)
	}
	if _, err := c.DeleteCrashLoopPod(ctx, "prod", "api-0"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().Pods("prod").Get(ctx, "api-0", metav1.GetOptions{}); err == nil {
		t.Fatal("crashloop pod should be gone")
	}
}
