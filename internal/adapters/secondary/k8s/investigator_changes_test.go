package k8s

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestInvestigatorClientListsReplicaSetRevisionsNewestFirst(t *testing.T) {
	ctx := context.Background()
	replicas := int32(2)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	ctrl := true
	oldRS := replicaSetFixture("prod", "api-aaa", "api", "11", "api:old", 0, ctrl)
	newRS := replicaSetFixture("prod", "api-bbb", "api", "12", "api:new", 2, ctrl)
	other := replicaSetFixture("prod", "web-ccc", "web", "3", "web:1", 1, ctrl)

	c := NewInvestigatorClient(fake.NewSimpleClientset(dep, oldRS, newRS, other), NewNamespaceGuard(nil))
	list, err := c.ListReplicaSets(ctx, "prod", "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d replica sets: %+v", len(list), list)
	}
	if list[0].Revision != "12" || list[0].Images[0] != "api:new" || list[0].Replicas != 2 {
		t.Fatalf("newest %+v", list[0])
	}
	if list[1].Revision != "11" || list[1].Images[0] != "api:old" {
		t.Fatalf("previous %+v", list[1])
	}
}

func replicaSetFixture(ns, name, deploy, rev, image string, replicas int32, controller bool) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": rev},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       deploy,
				Controller: &controller,
			}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: image}}},
			},
		},
		Status: appsv1.ReplicaSetStatus{Replicas: replicas, ReadyReplicas: replicas},
	}
}
