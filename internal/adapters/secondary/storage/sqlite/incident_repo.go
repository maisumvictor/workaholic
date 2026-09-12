package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
)

// IncidentRepo implements ports.IncidentRepository against SQLite.
type IncidentRepo struct {
	db *sql.DB
}

func NewIncidentRepo(db *sql.DB) *IncidentRepo {
	return &IncidentRepo{db: db}
}

func (r *IncidentRepo) Create(ctx context.Context, inc *domain.Incident) error {
	plan, err := domain.MarshalActionPlan(inc.ActionPlan)
	if err != nil {
		return err
	}
	labels, err := marshalLabels(inc.Labels)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
INSERT INTO incidents (
    id, source, title, summary, severity, status, risk_level,
    action_plan, raw_payload, labels, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inc.ID, inc.Source, inc.Title, inc.Summary, inc.Severity, string(inc.Status),
		string(inc.RiskLevel), plan, inc.RawPayload, labels,
		inc.CreatedAt.UTC().Format(time.RFC3339Nano),
		inc.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert incident: %w", err)
	}
	return nil
}

func (r *IncidentRepo) Update(ctx context.Context, inc *domain.Incident) error {
	plan, err := domain.MarshalActionPlan(inc.ActionPlan)
	if err != nil {
		return err
	}
	labels, err := marshalLabels(inc.Labels)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `
UPDATE incidents SET
    source = ?, title = ?, summary = ?, severity = ?, status = ?,
    risk_level = ?, action_plan = ?, raw_payload = ?, labels = ?, updated_at = ?
WHERE id = ?`,
		inc.Source, inc.Title, inc.Summary, inc.Severity, string(inc.Status),
		string(inc.RiskLevel), plan, inc.RawPayload, labels,
		inc.UpdatedAt.UTC().Format(time.RFC3339Nano), inc.ID,
	)
	if err != nil {
		return fmt.Errorf("update incident: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *IncidentRepo) Get(ctx context.Context, id string) (*domain.Incident, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, source, title, summary, severity, status, risk_level,
       action_plan, raw_payload, labels, created_at, updated_at
FROM incidents WHERE id = ?`, id)
	inc, err := scanIncident(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get incident: %w", err)
	}
	return inc, nil
}

func (r *IncidentRepo) List(ctx context.Context, filter ports.IncidentFilter) ([]domain.Incident, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	var (
		rows *sql.Rows
		err  error
	)
	if filter.Status != "" {
		rows, err = r.db.QueryContext(ctx, `
SELECT id, source, title, summary, severity, status, risk_level,
       action_plan, raw_payload, labels, created_at, updated_at
FROM incidents WHERE status = ? ORDER BY created_at DESC LIMIT ?`, string(filter.Status), limit)
	} else {
		rows, err = r.db.QueryContext(ctx, `
SELECT id, source, title, summary, severity, status, risk_level,
       action_plan, raw_payload, labels, created_at, updated_at
FROM incidents ORDER BY created_at DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Incident, 0)
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *inc)
	}
	return out, rows.Err()
}

func (r *IncidentRepo) AppendAudit(ctx context.Context, rec *domain.AuditRecord) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO audit_records (id, incident_id, actor, action, details, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.IncidentID, rec.Actor, rec.Action, rec.Details,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert audit record: %w", err)
	}
	return nil
}

func (r *IncidentRepo) ListAudit(ctx context.Context, incidentID string) ([]domain.AuditRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, incident_id, actor, action, details, created_at
FROM audit_records WHERE incident_id = ? ORDER BY created_at ASC`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	defer rows.Close()

	out := make([]domain.AuditRecord, 0)
	for rows.Next() {
		var (
			rec       domain.AuditRecord
			createdAt string
		)
		if err := rows.Scan(&rec.ID, &rec.IncidentID, &rec.Actor, &rec.Action, &rec.Details, &createdAt); err != nil {
			return nil, fmt.Errorf("scan audit: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			t, _ = time.Parse(time.RFC3339, createdAt)
		}
		rec.CreatedAt = t
		out = append(out, rec)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanIncident(s scanner) (*domain.Incident, error) {
	var (
		inc                  domain.Incident
		status, risk, plan   string
		labels, created, upd string
	)
	if err := s.Scan(
		&inc.ID, &inc.Source, &inc.Title, &inc.Summary, &inc.Severity,
		&status, &risk, &plan, &inc.RawPayload, &labels, &created, &upd,
	); err != nil {
		return nil, err
	}
	inc.Status = domain.IncidentStatus(status)
	inc.RiskLevel = domain.RiskLevel(risk)
	p, err := domain.UnmarshalActionPlan(plan)
	if err != nil {
		return nil, err
	}
	inc.ActionPlan = p
	inc.Labels, err = unmarshalLabels(labels)
	if err != nil {
		return nil, err
	}
	inc.CreatedAt, _ = parseTime(created)
	inc.UpdatedAt, _ = parseTime(upd)
	return &inc, nil
}

func marshalLabels(m map[string]string) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal labels: %w", err)
	}
	return string(b), nil
}

func unmarshalLabels(raw string) (map[string]string, error) {
	if raw == "" {
		return map[string]string{}, nil
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("unmarshal labels: %w", err)
	}
	return out, nil
}

func parseTime(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, raw)
}
