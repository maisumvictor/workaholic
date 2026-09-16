package k8s

import (
	"context"
	"fmt"
	"strings"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
)

// HealthVerifier re-checks HPA and workload readiness after a remediator write.
type HealthVerifier struct {
	Inv ports.K8sInvestigator
}

func NewHealthVerifier(inv ports.K8sInvestigator) *HealthVerifier {
	return &HealthVerifier{Inv: inv}
}

func (v *HealthVerifier) Verify(ctx context.Context, incident *domain.Incident) (*ports.VerificationResult, error) {
	if v == nil || v.Inv == nil {
		return &ports.VerificationResult{OK: false, Summary: "verifier not configured"}, nil
	}
	if incident == nil || incident.ActionPlan == nil || len(incident.ActionPlan.Steps) == 0 {
		return &ports.VerificationResult{OK: false, Summary: "no action plan to verify"}, nil
	}
	notes := make([]string, 0, len(incident.ActionPlan.Steps))
	for _, step := range incident.ActionPlan.Steps {
		ns := argString(step.Arguments, "namespace")
		name := argString(step.Arguments, "name")
		ok, note, err := v.checkStep(ctx, step.Tool, ns, name)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
		if !ok {
			return &ports.VerificationResult{OK: false, Summary: strings.Join(notes, "; ")}, nil
		}
	}
	return &ports.VerificationResult{OK: true, Summary: strings.Join(notes, "; ")}, nil
}

func (v *HealthVerifier) checkStep(ctx context.Context, tool, ns, name string) (bool, string, error) {
	switch tool {
	case domain.ToolPatchHPAMaxReplicas:
		h, err := v.Inv.GetHPA(ctx, ns, name)
		if err != nil {
			return false, fmt.Sprintf("hpa %s/%s: %v", ns, name, err), nil
		}
		if h.MaxReplicas < h.Current {
			return false, fmt.Sprintf("hpa %s/%s current=%d exceeds max=%d", ns, name, h.Current, h.MaxReplicas), nil
		}
		target := name
		if kind, tname, ok := splitTarget(h.TargetRef); ok && strings.EqualFold(kind, "Deployment") {
			target = tname
		}
		if !v.workloadReady(ctx, ns, target) {
			return false, fmt.Sprintf("hpa %s/%s current=%d desired=%d max=%d but target %s not Ready", ns, name, h.Current, h.Desired, h.MaxReplicas, target), nil
		}
		return true, fmt.Sprintf("hpa %s/%s current=%d desired=%d max=%d target Ready", ns, name, h.Current, h.Desired, h.MaxReplicas), nil
	case domain.ToolRestartRollout, domain.ToolRollbackDeployment, domain.ToolScaleDeployment:
		if !v.workloadReady(ctx, ns, name) {
			return false, fmt.Sprintf("deployment %s/%s not Ready", ns, name), nil
		}
		return true, fmt.Sprintf("deployment %s/%s Ready", ns, name), nil
	case domain.ToolDeleteCrashLoopPod:
		p, err := v.Inv.GetPod(ctx, ns, name)
		if err != nil {
			return true, fmt.Sprintf("pod %s/%s gone after delete", ns, name), nil
		}
		if p.Reason == "CrashLoopBackOff" {
			return false, fmt.Sprintf("pod %s/%s still CrashLoopBackOff", ns, name), nil
		}
		return true, fmt.Sprintf("pod %s/%s phase=%s ready=%s", ns, name, p.Phase, p.Ready), nil
	case domain.ToolUpdateASGDesired:
		return true, "asg update recorded; cluster health not applicable", nil
	default:
		return false, fmt.Sprintf("unknown tool %s", tool), nil
	}
}

func (v *HealthVerifier) workloadReady(ctx context.Context, ns, name string) bool {
	d, err := v.Inv.GetDeployment(ctx, ns, name)
	if err != nil {
		return false
	}
	if d.Unavailable > 0 {
		return false
	}
	if d.Replicas > 0 && d.ReadyReplicas < d.Replicas {
		return false
	}
	return true
}

func splitTarget(ref string) (kind, name string, ok bool) {
	kind, name, ok = strings.Cut(ref, "/")
	return kind, name, ok && kind != "" && name != ""
}

func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}
