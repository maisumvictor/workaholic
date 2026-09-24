package ports

import (
	"context"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
)

// Messaging delivers human-in-the-loop prompts and status updates.
type Messaging interface {
	RequestApproval(ctx context.Context, incident *domain.Incident, plan *domain.ActionPlan) error
	NotifyResolved(ctx context.Context, incident *domain.Incident, outcome string) error
	NotifyFailed(ctx context.Context, incident *domain.Incident, errMsg string) error
	NotifyAutoRemediated(ctx context.Context, incident *domain.Incident, outcome string) error
}
