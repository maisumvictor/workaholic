package changes

import (
	"context"
	"strings"

	"github.com/maisumvictor/Workaholic/internal/core/ports"
)

// Correlator gathers ReplicaSet revisions and optional GitHub / Argo evidence.
type Correlator struct {
	K8s    ports.K8sInvestigator
	GitHub ports.GitHubInvestigator
	Argo   ports.ArgoInvestigator
}

func (c *Correlator) Correlate(ctx context.Context, labels map[string]string) (*ports.ChangeContext, error) {
	if c == nil {
		return nil, nil
	}
	out := &ports.ChangeContext{}
	ns := strings.TrimSpace(label(labels, "namespace"))
	deploy := workloadName(labels)
	if deploy == "" && c.K8s != nil && ns != "" {
		if hpaName := label(labels, "horizontalpodautoscaler"); hpaName != "" {
			if hpa, err := c.K8s.GetHPA(ctx, ns, hpaName); err == nil && hpa != nil {
				kind, name, ok := strings.Cut(hpa.TargetRef, "/")
				if ok && strings.EqualFold(kind, "Deployment") {
					deploy = name
				}
			}
		}
	}
	if c.K8s != nil && ns != "" && deploy != "" {
		rss, err := c.K8s.ListReplicaSets(ctx, ns, deploy)
		if err != nil {
			return nil, err
		}
		out.ReplicaSets = rss
	}
	if c.GitHub != nil {
		repo := label(labels, "github_repo")
		base := label(labels, "github_base")
		head := label(labels, "github_head")
		owner, name, ok := strings.Cut(repo, "/")
		if ok && owner != "" && name != "" && base != "" && head != "" {
			if gh, err := c.GitHub.Compare(ctx, owner, name, base, head); err == nil {
				out.GitHub = gh
			}
		}
	}
	if c.Argo != nil {
		app := label(labels, "argocd_application")
		if app == "" {
			app = label(labels, "argocd_app")
		}
		if app != "" {
			if view, err := c.Argo.GetApplication(ctx, label(labels, "argocd_app_namespace"), app); err == nil {
				out.Argo = view
			}
		}
	}
	if len(out.ReplicaSets) == 0 && out.GitHub == nil && out.Argo == nil {
		return nil, nil
	}
	return out, nil
}

func workloadName(labels map[string]string) string {
	for _, k := range []string{"deployment", "deployment_name", "workload"} {
		if v := label(labels, k); v != "" {
			return v
		}
	}
	return ""
}

func label(labels map[string]string, key string) string {
	if labels == nil {
		return ""
	}
	return strings.TrimSpace(labels[key])
}
