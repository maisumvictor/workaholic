package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens a CGO-free SQLite database and applies schema migrations.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS incidents (
    id          TEXT PRIMARY KEY,
    source      TEXT NOT NULL,
    title       TEXT NOT NULL,
    summary     TEXT NOT NULL DEFAULT '',
    severity    TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL,
    risk_level  TEXT NOT NULL DEFAULT '',
    action_plan TEXT NOT NULL DEFAULT '',
    raw_payload TEXT NOT NULL DEFAULT '',
    labels      TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_records (
    id          TEXT PRIMARY KEY,
    incident_id TEXT NOT NULL,
    actor       TEXT NOT NULL,
    action      TEXT NOT NULL,
    details     TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    FOREIGN KEY (incident_id) REFERENCES incidents(id)
);

CREATE INDEX IF NOT EXISTS idx_incidents_status ON incidents(status);
CREATE INDEX IF NOT EXISTS idx_audit_incident ON audit_records(incident_id);
`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate sqlite schema: %w", err)
	}
	return nil
}
