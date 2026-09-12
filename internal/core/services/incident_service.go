package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
)

// PlanExecutor runs validated remediator tools. It is the only path that
// may invoke write ports.
type PlanExecutor struct {
	K8s ports.K8sRemediator
	AWS ports.AWSRemediator
}

func (e PlanExecutor) Execute(ctx context.Context, plan *domain.ActionPlan) (string, error) {
	if plan == nil || len(plan.Steps) == 0 {
		return "", domain.ErrEmptyActionPlan
	}
	var out strings.Builder
	for i, step := range plan.Steps {
		result, err := e.runStep(ctx, step)
		if err != nil {
			return out.String(), fmt.Errorf("step %d (%s): %w", i, step.Tool, err)
		}
		fmt.Fprintf(&out, "step %d %s: %s\n", i+1, step.Tool, result)
	}
	return out.String(), nil
}

func (e PlanExecutor) runStep(ctx context.Context, step domain.ActionStep) (string, error) {
	switch step.Tool {
	case domain.ToolPatchHPAMaxReplicas:
		if e.K8s == nil {
			return "", fmt.Errorf("%w: k8s remediator not configured", domain.ErrUnknownTool)
		}
		view, err := e.K8s.PatchHPAMaxReplicas(ctx, argString(step.Arguments, "namespace"), argString(step.Arguments, "name"), argInt32(step.Arguments, "max_replicas"))
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(view)
		return string(b), err
	case domain.ToolUpdateASGDesired:
		if e.AWS == nil {
			return "", fmt.Errorf("%w: aws remediator not configured", domain.ErrUnknownTool)
		}
		view, err := e.AWS.UpdateASGDesiredCapacity(ctx, argString(step.Arguments, "name"), argInt32(step.Arguments, "desired_capacity"))
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(view)
		return string(b), err
	default:
		return "", fmt.Errorf("%w: %s", domain.ErrUnknownTool, step.Tool)
	}
}

func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func argInt32(args map[string]any, key string) int32 {
	switch v := args[key].(type) {
	case float64:
		return int32(v)
	case int:
		return int32(v)
	case int32:
		return v
	case int64:
		return int32(v)
	case json.Number:
		n, _ := v.Int64()
		return int32(n)
	default:
		return 0
	}
}

// IncidentService orchestrates alert ingestion, investigation, and dispatch.
type IncidentService struct {
	repo     ports.IncidentRepository
	runbooks ports.RunbookRepository
	llm      ports.Investigator
	msg      ports.Messaging
	tickets  ports.Ticketing
	exec     PlanExecutor
	log      *slog.Logger
	now      func() time.Time
	newID    func() string
}

func NewIncidentService(
	repo ports.IncidentRepository,
	runbooks ports.RunbookRepository,
	llm ports.Investigator,
	msg ports.Messaging,
	tickets ports.Ticketing,
	exec PlanExecutor,
	log *slog.Logger,
) *IncidentService {
	if log == nil {
		log = slog.Default()
	}
	return &IncidentService{
		repo:     repo,
		runbooks: runbooks,
		llm:      llm,
		msg:      msg,
		tickets:  tickets,
		exec:     exec,
		log:      log,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    newULIDLike,
	}
}

func (s *IncidentService) HandleAlert(ctx context.Context, alert ports.Alert) (*domain.Incident, error) {
	now := s.now()
	inc := &domain.Incident{
		ID:         s.newID(),
		Source:     alert.Source,
		Title:      firstNonEmpty(alert.Title, "untitled alert"),
		Summary:    alert.Message,
		Severity:   firstNonEmpty(alert.Severity, "unknown"),
		Status:     domain.StatusInvestigating,
		Labels:     alert.Labels,
		RawPayload: string(alert.RawJSON),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if inc.Labels == nil {
		inc.Labels = map[string]string{}
	}
	if alert.Fingerprint != "" {
		inc.Labels["fingerprint"] = alert.Fingerprint
	}

	if err := s.repo.Create(ctx, inc); err != nil {
		return nil, err
	}
	s.audit(ctx, inc.ID, "system", "ingested", fmt.Sprintf("source=%s title=%s", inc.Source, inc.Title))

	if err := s.investigateAndDispatch(ctx, inc); err != nil {
		inc.Status = domain.StatusFailed
		inc.UpdatedAt = s.now()
		_ = s.repo.Update(ctx, inc)
		s.audit(ctx, inc.ID, "system", "failed", err.Error())
		if s.msg != nil {
			_ = s.msg.NotifyFailed(ctx, inc, err.Error())
		}
		return inc, err
	}
	return inc, nil
}

func (s *IncidentService) investigateAndDispatch(ctx context.Context, inc *domain.Incident) error {
	matched, err := s.runbooks.Match(ctx, inc.Labels)
	if err != nil {
		return domain.Wrap(err, "match runbooks")
	}
	contextBooks := matched
	if len(matched) == 0 {
		all, listErr := s.runbooks.List(ctx)
		if listErr != nil {
			return domain.Wrap(listErr, "list runbooks")
		}
		contextBooks = all
	}

	plan, err := s.llm.Investigate(ctx, ports.InvestigationRequest{
		Incident:  inc,
		Runbooks:  contextBooks,
		Telemetry: inc.RawPayload,
	})
	if err != nil {
		return domain.Wrap(err, "investigate")
	}
	if len(matched) == 0 {
		plan.RiskLevel = domain.RiskRequiresApproval
	}
	inc.ActionPlan = plan
	inc.RiskLevel = plan.RiskLevel
	inc.UpdatedAt = s.now()
	s.audit(ctx, inc.ID, "llm", "action_plan", fmt.Sprintf("risk=%s confidence=%.2f steps=%d", plan.RiskLevel, plan.Confidence, len(plan.Steps)))

	if plan.CanAutoRemediate() {
		return s.autoRemediate(ctx, inc)
	}
	inc.RiskLevel = domain.RiskRequiresApproval
	inc.Status = domain.StatusAwaitingApproval
	if err := s.repo.Update(ctx, inc); err != nil {
		return err
	}
	s.audit(ctx, inc.ID, "system", "awaiting_approval", "human approval required")
	if s.msg != nil {
		if err := s.msg.RequestApproval(ctx, inc, plan); err != nil {
			return domain.Wrap(err, "request approval")
		}
	}
	return nil
}

func (s *IncidentService) autoRemediate(ctx context.Context, inc *domain.Incident) error {
	inc.Status = domain.StatusExecuting
	inc.UpdatedAt = s.now()
	if err := s.repo.Update(ctx, inc); err != nil {
		return err
	}
	outcome, err := s.exec.Execute(ctx, inc.ActionPlan)
	if err != nil {
		inc.Status = domain.StatusFailed
		inc.UpdatedAt = s.now()
		_ = s.repo.Update(ctx, inc)
		return domain.Wrap(err, "auto-remediate")
	}
	inc.Status = domain.StatusResolved
	inc.UpdatedAt = s.now()
	if err := s.repo.Update(ctx, inc); err != nil {
		return err
	}
	s.audit(ctx, inc.ID, "system", "auto_remediated", outcome)
	if s.msg != nil {
		_ = s.msg.NotifyAutoRemediated(ctx, inc, outcome)
	}
	return nil
}

// ExecuteApprovedPlan is used by ApprovalService after authorization.
func (s *IncidentService) ExecuteApprovedPlan(ctx context.Context, incidentID, actor string) (*domain.Incident, error) {
	inc, err := s.repo.Get(ctx, incidentID)
	if err != nil {
		return nil, err
	}
	if inc.Status.Terminal() {
		return inc, domain.ErrAlreadyTerminal
	}
	if inc.Status != domain.StatusAwaitingApproval {
		return inc, fmt.Errorf("%w: current=%s", domain.ErrInvalidStatus, inc.Status)
	}
	inc.Status = domain.StatusExecuting
	inc.UpdatedAt = s.now()
	if err := s.repo.Update(ctx, inc); err != nil {
		return inc, err
	}
	s.audit(ctx, inc.ID, actor, "approved", "executing action plan")

	outcome, err := s.exec.Execute(ctx, inc.ActionPlan)
	if err != nil {
		inc.Status = domain.StatusFailed
		inc.UpdatedAt = s.now()
		_ = s.repo.Update(ctx, inc)
		s.audit(ctx, inc.ID, actor, "execution_failed", err.Error())
		if s.msg != nil {
			_ = s.msg.NotifyFailed(ctx, inc, err.Error())
		}
		return inc, domain.Wrap(err, "execute approved plan")
	}
	inc.Status = domain.StatusResolved
	inc.UpdatedAt = s.now()
	if err := s.repo.Update(ctx, inc); err != nil {
		return inc, err
	}
	s.audit(ctx, inc.ID, actor, "resolved", outcome)
	if s.msg != nil {
		_ = s.msg.NotifyResolved(ctx, inc, outcome)
	}
	return inc, nil
}

func (s *IncidentService) Reject(ctx context.Context, incidentID, actor, reason string) (*domain.Incident, error) {
	inc, err := s.repo.Get(ctx, incidentID)
	if err != nil {
		return nil, err
	}
	if inc.Status.Terminal() {
		return inc, domain.ErrAlreadyTerminal
	}
	if inc.Status != domain.StatusAwaitingApproval {
		return inc, fmt.Errorf("%w: current=%s", domain.ErrInvalidStatus, inc.Status)
	}
	inc.Status = domain.StatusFailed
	inc.UpdatedAt = s.now()
	if err := s.repo.Update(ctx, inc); err != nil {
		return inc, err
	}
	s.audit(ctx, inc.ID, actor, "rejected", reason)
	if s.msg != nil {
		_ = s.msg.NotifyFailed(ctx, inc, "rejected by "+actor+": "+reason)
	}
	return inc, nil
}

func (s *IncidentService) Get(ctx context.Context, id string) (*domain.Incident, error) {
	return s.repo.Get(ctx, id)
}

func (s *IncidentService) List(ctx context.Context, filter ports.IncidentFilter) ([]domain.Incident, error) {
	return s.repo.List(ctx, filter)
}

func (s *IncidentService) AuditTrail(ctx context.Context, incidentID string) ([]domain.AuditRecord, error) {
	return s.repo.ListAudit(ctx, incidentID)
}

func (s *IncidentService) audit(ctx context.Context, incidentID, actor, action, details string) {
	rec := &domain.AuditRecord{
		ID:         s.newID(),
		IncidentID: incidentID,
		Actor:      actor,
		Action:     action,
		Details:    details,
		CreatedAt:  s.now(),
	}
	if err := s.repo.AppendAudit(ctx, rec); err != nil {
		s.log.ErrorContext(ctx, "append audit failed", "err", err, "incident", incidentID)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func newULIDLike() string {
	return fmt.Sprintf("inc_%d", time.Now().UnixNano())
}
