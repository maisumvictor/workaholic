package ports

import (
	"context"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
)

// InvestigationRequest is the fully assembled, already-sanitized context
// handed to the LLM. Raw telemetry must be wrapped in isolation tags by the
// caller or the adapter.
type InvestigationRequest struct {
	Incident  *domain.Incident
	Runbooks  []domain.Runbook
	Telemetry string
}

// Investigator produces a structured action plan from alert context.
type Investigator interface {
	Investigate(ctx context.Context, req InvestigationRequest) (*domain.ActionPlan, error)
}
