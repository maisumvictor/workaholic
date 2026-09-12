package http

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/maisumvictor/Workaholic/internal/core/ports"
)

type grafanaWebhook struct {
	Receiver string         `json:"receiver"`
	Status   string         `json:"status"`
	Alerts   []grafanaAlert `json:"alerts"`
	Title    string         `json:"title"`
	Message  string         `json:"message"`
}

type grafanaAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	Fingerprint  string            `json:"fingerprint"`
	GeneratorURL string            `json:"generatorURL"`
	ValueString  string            `json:"valueString"`
}

func (s *Server) handleGrafana(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unable to read body"})
		return
	}
	var payload grafanaWebhook
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid grafana payload"})
		return
	}
	if strings.EqualFold(payload.Status, "resolved") {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored_resolved"})
		return
	}

	accepted := make([]string, 0, len(payload.Alerts))
	alerts := payload.Alerts
	if len(alerts) == 0 {
		alerts = []grafanaAlert{{
			Status: payload.Status,
			Annotations: map[string]string{
				"summary":     payload.Title,
				"description": payload.Message,
			},
		}}
	}
	for _, a := range alerts {
		if strings.EqualFold(a.Status, "resolved") {
			continue
		}
		alert := grafanaToAlert(payload, a, body)
		inc, err := s.incidents.HandleAlert(r.Context(), alert)
		if err != nil {
			s.log.ErrorContext(r.Context(), "handle grafana alert", "err", err, "title", alert.Title)
			writeDomainError(w, err)
			return
		}
		accepted = append(accepted, inc.ID)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"incident_ids": accepted})
}

func grafanaToAlert(payload grafanaWebhook, a grafanaAlert, raw []byte) ports.Alert {
	labels := a.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	title := first(a.Labels["alertname"], a.Annotations["summary"], payload.Title, "grafana-alert")
	msg := first(a.Annotations["description"], a.Annotations["summary"], payload.Message, a.ValueString)
	sev := first(a.Labels["severity"], a.Labels["priority"], "warning")
	return ports.Alert{
		Source:      "grafana",
		Title:       title,
		Severity:    sev,
		Message:     msg,
		Labels:      labels,
		StartsAt:    a.StartsAt,
		RawJSON:     raw,
		Fingerprint: a.Fingerprint,
	}
}

func first(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
