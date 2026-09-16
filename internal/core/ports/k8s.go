package ports

import "context"

// PodView is a read-only snapshot of a Pod.
type PodView struct {
	Namespace string
	Name      string
	Phase     string
	Ready     string
	Restarts  int32
	Node      string
	Reason    string
	Message   string
	CreatedAt string
}

// DeploymentView is a read-only snapshot of a Deployment.
type DeploymentView struct {
	Namespace       string
	Name            string
	Replicas        int32
	ReadyReplicas   int32
	UpdatedReplicas int32
	Unavailable     int32
	Conditions      []string
}

// EventView is a cluster event related to a workload.
type EventView struct {
	Namespace string
	Kind      string
	Name      string
	Type      string
	Reason    string
	Message   string
	Count     int32
	LastSeen  string
}

// HPAView is a read-only snapshot of a HorizontalPodAutoscaler.
type HPAView struct {
	Namespace     string
	Name          string
	MinReplicas   int32
	MaxReplicas   int32
	Desired       int32
	Current       int32
	TargetRef     string
	Metrics       []string
	LastScaleTime string
}

// K8sInvestigator is the read-only cluster port used during investigation.
type K8sInvestigator interface {
	GetPod(ctx context.Context, namespace, name string) (*PodView, error)
	ListPods(ctx context.Context, namespace, labelSelector string) ([]PodView, error)
	GetDeployment(ctx context.Context, namespace, name string) (*DeploymentView, error)
	ListEvents(ctx context.Context, namespace, involvedKind, involvedName string) ([]EventView, error)
	GetPodLogs(ctx context.Context, namespace, name, container string, tailLines int64) (string, error)
	GetHPA(ctx context.Context, namespace, name string) (*HPAView, error)
	ListHPAs(ctx context.Context, namespace string) ([]HPAView, error)
}

// K8sRemediator is the strictly scoped write port. Implementations MUST
// refuse mutations in protected namespaces and clamp replica targets.
// New tools default to human approval in the core service; this port is
// still the only path that may mutate the cluster.
type K8sRemediator interface {
	PatchHPAMaxReplicas(ctx context.Context, namespace, name string, maxReplicas int32) (*HPAView, error)
	RestartRollout(ctx context.Context, namespace, name string) (*DeploymentView, error)
	RollbackDeployment(ctx context.Context, namespace, name string) (*DeploymentView, error)
	ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) (*DeploymentView, error)
	DeleteCrashLoopPod(ctx context.Context, namespace, name string) (*PodView, error)
}
