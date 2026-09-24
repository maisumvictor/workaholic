package ports

import "context"

// ReplicaSetView is a read-only ReplicaSet revision owned by a Deployment.
type ReplicaSetView struct {
	Namespace     string   `json:"namespace"`
	Name          string   `json:"name"`
	Deployment    string   `json:"deployment"`
	Revision      string   `json:"revision"`
	Replicas      int32    `json:"replicas"`
	ReadyReplicas int32    `json:"ready_replicas"`
	Images        []string `json:"images"`
	CreatedAt     string   `json:"created_at"`
}

// GitHubCompareView is a read-only compare of two git refs.
type GitHubCompareView struct {
	Owner    string   `json:"owner"`
	Repo     string   `json:"repo"`
	Base     string   `json:"base"`
	Head     string   `json:"head"`
	Status   string   `json:"status"`
	AheadBy  int      `json:"ahead_by"`
	BehindBy int      `json:"behind_by"`
	Commits  []string `json:"commits"`
	HTMLURL  string   `json:"html_url"`
}

// ArgoApplicationView is a read-only Argo CD Application status.
type ArgoApplicationView struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	SyncStatus   string `json:"sync_status"`
	HealthStatus string `json:"health_status"`
	Revision     string `json:"revision"`
	RepoURL      string `json:"repo_url"`
}

// ChangeContext is recent deploy / PR / Argo evidence attached to an investigation.
type ChangeContext struct {
	ReplicaSets []ReplicaSetView     `json:"replica_sets,omitempty"`
	GitHub      *GitHubCompareView   `json:"github,omitempty"`
	Argo        *ArgoApplicationView `json:"argo,omitempty"`
}

// GitHubInvestigator is an optional read-only GitHub compare port.
type GitHubInvestigator interface {
	Compare(ctx context.Context, owner, repo, base, head string) (*GitHubCompareView, error)
}

// ArgoInvestigator is an optional read-only Argo CD application port.
type ArgoInvestigator interface {
	GetApplication(ctx context.Context, namespace, name string) (*ArgoApplicationView, error)
}

// ChangeCorrelator gathers recent cluster and git changes for an alert.
type ChangeCorrelator interface {
	Correlate(ctx context.Context, labels map[string]string) (*ChangeContext, error)
}
