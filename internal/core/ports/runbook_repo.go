package ports

import (
	"context"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
)

// RunbookRepository loads Markdown runbooks from a backing store.
type RunbookRepository interface {
	List(ctx context.Context) ([]domain.Runbook, error)
	Get(ctx context.Context, id string) (*domain.Runbook, error)
	Match(ctx context.Context, labels map[string]string) ([]domain.Runbook, error)
}
