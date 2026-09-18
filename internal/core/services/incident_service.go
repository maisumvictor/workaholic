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
	ns := argString(step.Arguments, "namespace")
	name := argString(step.Arguments, "name")
	switch step.Tool {
	case domain.ToolPatchHPAMaxReplicas:
		k8s, err := e.k8sOrErr()
		if err != nil {
			return "", err
		}
		view, err := k8s.PatchHPAMaxReplicas(ctx, ns, name, argInt32(step.Arguments, "max_replicas"))
		return marshalView(view, err)
	case domain.ToolRestartRollout:
		k8s, err := e.k8sOrErr()
		if err != nil {
			return "", err
		}
		view, err := k8s.RestartRollout(ctx, ns, name)
		return marshalView(view, err)
	case domain.ToolRollbackDeployment:
		k8s, err := e.k8sOrErr()
		if err != nil {
			return "", err
		}
		view, err := k8s.RollbackDeployment(ctx, ns, name)
		return marshalView(view, err)
	case domain.ToolScaleDeployment:
		k8s, err := e.k8sOrErr()
		if err != nil {
			return "", err
		}
		view, err := k8s.ScaleDeployment(ctx, ns, name, argInt32(step.Arguments, "replicas"))
		return marshalView(view, err)
	case domain.ToolDeleteCrashLoopPod:
		k8s, err := e.k8sOrErr()
		if err != nil {
			return "", err
		}
		view, err := k8s.DeleteCrashLoopPod(ctx, ns, name)
		return marshalView(view, err)
	case domain.ToolUpdateASGDesired:
		if e.AWS == nil {
			return "", fmt.Errorf("%w: aws remediator not configured", domain.ErrUnknownTool)
		}
		view, err := e.AWS.UpdateASGDesiredCapacity(ctx, name, argInt32(step.Arguments, "desired_capacity"))
		return marshalView(view, err)
	default:
		return "", fmt.Errorf("%w: %s", domain.ErrUnknownTool, step.Tool)
	}
}

func (e PlanExecutor) k8sOrErr() (ports.K8sRemediator, error) {
	if e.K8s == nil {
		return nil, fmt.Errorf("%w: k8s remediator not configured", domain.ErrUnknownTool)
	}
	return e.K8s, nil
}

func marshalView(view any, err error) (string, error) {
	if err != nil {
		return "", err
	}
	b, mErr := json.Marshal(view)
	return string(b), mErr
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
	verifier ports.Verifier
	changes  ports.ChangeCorrelator
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
	verifier ports.Verifier,
	changes ports.ChangeCorrelator,
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
		verifier: verifier,
		changes:  changes,
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
		// Incident is persisted; Grafana must not retry the webhook.
		s.log.ErrorContext(ctx, "alert handling finished failed", "incident", inc.ID, "err", err)
		return inc, nil
	}
	return inc, nil
}

func (s *IncidentService) investigateAndDispatch(ctx context.Context, inc *domain.Incident) error {
	matched, err := s.runbooks.Match(ctx, inc.Labels)
	if err != nil {
		return domain.Wrap(err, "match runbooks")
	}
	contextBooks, err := s.contextRunbooks(ctx, inc.Labels)
	if err != nil {
		return err
	}

	plan, err := s.llm.Investigate(ctx, ports.InvestigationRequest{
		Incident:  inc,
		Runbooks:  contextBooks,
		Telemetry: inc.RawPayload,
		Changes:   s.correlate(ctx, inc.Labels),
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
	return s.settleExecution(ctx, inc, "system", "auto_remediated", true)
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

	if err := s.settleExecution(ctx, inc, actor, "resolved", false); err != nil {
		return inc, domain.Wrap(err, "execute approved plan")
	}
	return inc, nil
}

func (s *IncidentService) settleExecution(ctx context.Context, inc *domain.Incident, actor, successAction string, auto bool) error {
	outcome, err := s.exec.Execute(ctx, inc.ActionPlan)
	if err != nil {
		return s.failExecution(ctx, inc, actor, "execution_failed", err)
	}
	if s.verifier != nil {
		res, vErr := s.verifier.Verify(ctx, inc)
		if vErr != nil {
			return s.failExecution(ctx, inc, actor, "verification_failed", fmt.Errorf("%w: %v", domain.ErrVerificationFailed, vErr))
		}
		if res == nil || !res.OK {
			summary := "verification failed"
			if res != nil && res.Summary != "" {
				summary = res.Summary
			}
			return s.failExecution(ctx, inc, actor, "verification_failed", fmt.Errorf("%w: %s", domain.ErrVerificationFailed, summary))
		}
		s.audit(ctx, inc.ID, actor, "verified", res.Summary)
	}
	inc.Status = domain.StatusResolved
	inc.UpdatedAt = s.now()
	if err := s.repo.Update(ctx, inc); err != nil {
		return err
	}
	s.audit(ctx, inc.ID, actor, successAction, outcome)
	if s.msg != nil {
		if auto {
			_ = s.msg.NotifyAutoRemediated(ctx, inc, outcome)
		} else {
			_ = s.msg.NotifyResolved(ctx, inc, outcome)
		}
	}
	return nil
}

func (s *IncidentService) failExecution(ctx context.Context, inc *domain.Incident, actor, action string, err error) error {
	inc.Status = domain.StatusFailed
	inc.UpdatedAt = s.now()
	_ = s.repo.Update(ctx, inc)
	s.audit(ctx, inc.ID, actor, action, err.Error())
	if s.msg != nil {
		_ = s.msg.NotifyFailed(ctx, inc, err.Error())
	}
	return err
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

// FollowUp re-runs the investigator for an on-call Slack question. It never
// invokes the remediator, even if the model proposes write tools.
func (s *IncidentService) FollowUp(ctx context.Context, incidentID, actor, question string) (*domain.Incident, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, domain.ErrEmptyFollowUp
	}
	inc, err := s.repo.Get(ctx, incidentID)
	if err != nil {
		return nil, err
	}
	books, err := s.contextRunbooks(ctx, inc.Labels)
	if err != nil {
		return inc, err
	}
	plan, err := s.llm.Investigate(ctx, ports.InvestigationRequest{
		Incident:  inc,
		Runbooks:  books,
		Telemetry: inc.RawPayload,
		FollowUp:  question,
		Changes:   s.correlate(ctx, inc.Labels),
	})
	if err != nil {
		return inc, domain.Wrap(err, "follow-up investigate")
	}
	answer := firstNonEmpty(plan.Summary, plan.Rationale, "(no investigator summary)")
	s.audit(ctx, inc.ID, actor, "followup", question+": "+answer)
	if s.msg != nil {
		if msgErr := s.msg.ReplyFollowUp(ctx, inc, question, answer); msgErr != nil {
			s.log.ErrorContext(ctx, "follow-up slack reply failed", "err", msgErr, "incident", inc.ID)
		}
	}
	return inc, nil
}

func (s *IncidentService) correlate(ctx context.Context, labels map[string]string) *ports.ChangeContext {
	if s.changes == nil {
		return nil
	}
	ch, err := s.changes.Correlate(ctx, labels)
	if err != nil {
		s.log.WarnContext(ctx, "change correlation failed", "err", err)
		return nil
	}
	return ch
}

func (s *IncidentService) contextRunbooks(ctx context.Context, labels map[string]string) ([]domain.Runbook, error) {
	matched, err := s.runbooks.Match(ctx, labels)
	if err != nil {
		return nil, domain.Wrap(err, "match runbooks")
	}
	if len(matched) > 0 {
		return matched, nil
	}
	all, listErr := s.runbooks.List(ctx)
	if listErr != nil {
		return nil, domain.Wrap(listErr, "list runbooks")
	}
	return all, nil
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
