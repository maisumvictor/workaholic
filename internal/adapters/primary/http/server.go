package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
	"github.com/maisumvictor/Workaholic/internal/core/services"
)

// Config is HTTP server configuration.
type Config struct {
	Addr               string
	APIToken           string
	SlackSigningSecret string
	ReadHeaderTimeout  time.Duration
}

// Server is the primary HTTP adapter (Grafana, Slack, CLI API).
type Server struct {
	cfg       Config
	incidents *services.IncidentService
	approvals *services.ApprovalService
	log       *slog.Logger
	http      *http.Server
}

func NewServer(cfg Config, incidents *services.IncidentService, approvals *services.ApprovalService, log *slog.Logger) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.ReadHeaderTimeout == 0 {
		cfg.ReadHeaderTimeout = 10 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	s := &Server{cfg: cfg, incidents: incidents, approvals: approvals, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleHealth)
	mux.HandleFunc("POST /webhooks/grafana", s.handleGrafana)
	mux.HandleFunc("POST /webhooks/slack/interactive", s.handleSlackInteractive)
	mux.HandleFunc("GET /api/v1/incidents", s.withAPIAuth(s.handleListIncidents))
	mux.HandleFunc("GET /api/v1/incidents/{id}", s.withAPIAuth(s.handleGetIncident))
	mux.HandleFunc("GET /api/v1/incidents/{id}/audit", s.withAPIAuth(s.handleAudit))
	mux.HandleFunc("POST /api/v1/incidents/{id}/approve", s.withAPIAuth(s.handleApprove))
	mux.HandleFunc("POST /api/v1/incidents/{id}/reject", s.withAPIAuth(s.handleReject))

	s.http = &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}
	return s
}

func (s *Server) ListenAndServe() error {
	s.log.Info("http listening", "addr", s.cfg.Addr)
	err := s.http.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) withAPIAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIToken == "" {
			next(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got != s.cfg.APIToken {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, domain.ErrUnauthorized), errors.Is(err, domain.ErrApproverDenied), errors.Is(err, domain.ErrSignatureInvalid):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidStatus), errors.Is(err, domain.ErrAlreadyTerminal), errors.Is(err, domain.ErrEmptyActionPlan):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func actorFromRequest(r *http.Request) string {
	if v := r.Header.Get("X-Actor"); v != "" {
		return v
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return "cli:" + host
	}
	return "cli:unknown"
}

func incidentJSON(inc *domain.Incident) map[string]any {
	return map[string]any{
		"id":          inc.ID,
		"source":      inc.Source,
		"title":       inc.Title,
		"summary":     inc.Summary,
		"severity":    inc.Severity,
		"status":      inc.Status,
		"risk_level":  inc.RiskLevel,
		"action_plan": inc.ActionPlan,
		"labels":      inc.Labels,
		"created_at":  inc.CreatedAt,
		"updated_at":  inc.UpdatedAt,
	}
}

func listFilter(r *http.Request) ports.IncidentFilter {
	q := r.URL.Query()
	f := ports.IncidentFilter{Limit: 50}
	if v := q.Get("status"); v != "" {
		f.Status = domain.IncidentStatus(v)
	}
	return f
}
