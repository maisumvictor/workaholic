package ports

import (
	"context"
	"time"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
)

// Alert is the normalized inbound payload from a monitoring system.
type Alert struct {
	Source      string
	Title       string
	Severity    string
	Message     string
	Labels      map[string]string
	StartsAt    time.Time
	RawJSON     []byte
	Fingerprint string
}

// AlertHandler is the primary port implemented by the incident service.
type AlertHandler interface {
	HandleAlert(ctx context.Context, alert Alert) (*domain.Incident, error)
}
