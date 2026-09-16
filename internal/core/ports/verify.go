package ports

import (
	"context"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
)

// VerificationResult is the health check after a remediator write.
type VerificationResult struct {
	OK      bool
	Summary string
}

// Verifier re-checks cluster health (HPA current/desired, pod Ready)
// before an incident may move to StatusResolved.
type Verifier interface {
	Verify(ctx context.Context, incident *domain.Incident) (*VerificationResult, error)
}
