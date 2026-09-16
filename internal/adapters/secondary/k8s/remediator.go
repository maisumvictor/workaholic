package k8s

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (c *RemediatorClient) RestartRollout(ctx context.Context, namespace, name string) (*ports.DeploymentView, error) {
	if err := c.guard.AssertWritable(namespace); err != nil {
		return nil, err
	}
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`, time.Now().UTC().Format(time.RFC3339))
	d, err := c.cs.AppsV1().Deployments(namespace).Patch(ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	if err != nil {
		return nil, domain.Wrap(err, "restart rollout")
	}
	v := deploymentView(*d)
	return &v, nil
}

func (c *RemediatorClient) RollbackDeployment(ctx context.Context, namespace, name string) (*ports.DeploymentView, error) {
	if err := c.guard.AssertWritable(namespace); err != nil {
		return nil, err
	}
	dep, err := c.cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, domain.Wrap(err, "get deployment for rollback")
	}
	list, err := c.cs.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, domain.Wrap(err, "list replicasets")
	}
	type revRS struct {
		rev int
		rs  appsv1.ReplicaSet
	}
	owned := make([]revRS, 0)
	for _, rs := range list.Items {
		if !ownedByDeployment(&rs, dep) {
			continue
		}
		rev := 0
		if raw := rs.Annotations["deployment.kubernetes.io/revision"]; raw != "" {
			rev, _ = strconv.Atoi(raw)
		}
		owned = append(owned, revRS{rev: rev, rs: rs})
	}
	if len(owned) < 2 {
		return nil, fmt.Errorf("%w: no previous replica set for %s/%s", domain.ErrInvalidToolArgs, namespace, name)
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].rev > owned[j].rev })
	prev := owned[1].rs
	dep.Spec.Template = prev.Spec.Template
	updated, err := c.cs.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	if err != nil {
		return nil, domain.Wrap(err, "rollback deployment")
	}
	v := deploymentView(*updated)
	return &v, nil
}

func (c *RemediatorClient) ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) (*ports.DeploymentView, error) {
	if err := c.guard.AssertWritable(namespace); err != nil {
		return nil, err
	}
	if replicas < 1 {
		return nil, fmt.Errorf("%w: replicas must be >= 1", domain.ErrInvalidToolArgs)
	}
	if replicas > c.maxReplicas {
		return nil, fmt.Errorf("%w: %d > %d", domain.ErrReplicaCapExceeded, replicas, c.maxReplicas)
	}
	patch := []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas))
	d, err := c.cs.AppsV1().Deployments(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return nil, domain.Wrap(err, "scale deployment")
	}
	v := deploymentView(*d)
	return &v, nil
}

func (c *RemediatorClient) DeleteCrashLoopPod(ctx context.Context, namespace, name string) (*ports.PodView, error) {
	if err := c.guard.AssertWritable(namespace); err != nil {
		return nil, err
	}
	pod, err := c.cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, domain.Wrap(err, "get pod")
	}
	if !isCrashLoop(*pod) {
		return nil, fmt.Errorf("%w: pod %s/%s is not CrashLoopBackOff", domain.ErrInvalidToolArgs, namespace, name)
	}
	if err := c.cs.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return nil, domain.Wrap(err, "delete crashloop pod")
	}
	v := podView(*pod)
	return &v, nil
}

func ownedByDeployment(rs *appsv1.ReplicaSet, dep *appsv1.Deployment) bool {
	for _, o := range rs.OwnerReferences {
		if o.Kind == "Deployment" && o.Name == dep.Name && o.UID == dep.UID {
			return true
		}
	}
	return false
}

func isCrashLoop(p corev1.Pod) bool {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
		if cs.LastTerminationState.Terminated != nil && cs.RestartCount > 0 && !cs.Ready {
			return true
		}
	}
	return false
}
