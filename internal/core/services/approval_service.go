package services

import (
	"context"
	"log/slog"
	"strings"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
)

// ApproverDirectory decides whether a Slack/CLI actor may approve remediation.
type ApproverDirectory interface {
	IsAllowed(userID string) bool
}

// StaticApprovers is a whitelist of Slack user IDs (or CLI identities).
type StaticApprovers struct {
	IDs map[string]struct{}
}

func NewStaticApprovers(ids []string) StaticApprovers {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			m[id] = struct{}{}
		}
	}
	return StaticApprovers{IDs: m}
}

func (s StaticApprovers) IsAllowed(userID string) bool {
	if len(s.IDs) == 0 {
		return false
	}
	_, ok := s.IDs[userID]
	return ok
}

// ApprovalService is the human-in-the-loop gate in front of remediator writes.
type ApprovalService struct {
	incidents *IncidentService
	approvers ApproverDirectory
	log       *slog.Logger
}

func NewApprovalService(incidents *IncidentService, approvers ApproverDirectory, log *slog.Logger) *ApprovalService {
	if log == nil {
		log = slog.Default()
	}
	return &ApprovalService{incidents: incidents, approvers: approvers, log: log}
}

func (s *ApprovalService) Approve(ctx context.Context, incidentID, actor string) (*domain.Incident, error) {
	if !s.approvers.IsAllowed(actor) {
		return nil, domain.ErrApproverDenied
	}
	return s.incidents.ExecuteApprovedPlan(ctx, incidentID, actor)
}

func (s *ApprovalService) Reject(ctx context.Context, incidentID, actor, reason string) (*domain.Incident, error) {
	if !s.approvers.IsAllowed(actor) {
		return nil, domain.ErrApproverDenied
	}
	if strings.TrimSpace(reason) == "" {
		reason = "rejected"
	}
	return s.incidents.Reject(ctx, incidentID, actor, reason)
}

func (s *ApprovalService) FollowUp(ctx context.Context, incidentID, actor, question string) (*domain.Incident, error) {
	if !s.approvers.IsAllowed(actor) {
		return nil, domain.ErrApproverDenied
	}
	return s.incidents.FollowUp(ctx, incidentID, actor, question)
}
