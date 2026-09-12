package ports

import (
	"context"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
)

// IncidentFilter selects incidents for listing.
type IncidentFilter struct {
	Status domain.IncidentStatus
	Limit  int
}

// IncidentRepository persists incidents and their audit trail.
type IncidentRepository interface {
	Create(ctx context.Context, inc *domain.Incident) error
	Update(ctx context.Context, inc *domain.Incident) error
	Get(ctx context.Context, id string) (*domain.Incident, error)
	List(ctx context.Context, filter IncidentFilter) ([]domain.Incident, error)
	AppendAudit(ctx context.Context, rec *domain.AuditRecord) error
	ListAudit(ctx context.Context, incidentID string) ([]domain.AuditRecord, error)
}
