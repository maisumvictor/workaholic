package ports

import (
	"context"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
)

// TicketRef is an identifier in an external ticketing system.
type TicketRef struct {
	System string
	ID     string
	URL    string
}

// Ticketing opens or updates tracking tickets for incidents. Optional:
// implementations may be nil and services must tolerate that.
type Ticketing interface {
	Open(ctx context.Context, incident *domain.Incident) (*TicketRef, error)
	Comment(ctx context.Context, ref TicketRef, body string) error
	Resolve(ctx context.Context, ref TicketRef, resolution string) error
}
