package http

import (
	"encoding/json"
	"io"
	"net/http"
)

type actorBody struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	items, err := s.incidents.List(r.Context(), listFilter(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for i := range items {
		out = append(out, incidentJSON(&items[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": out})
}

func (s *Server) handleGetIncident(w http.ResponseWriter, r *http.Request) {
	inc, err := s.incidents.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, incidentJSON(inc))
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	recs, err := s.incidents.AuditTrail(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": recs})
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	actor, _ := readActorBody(r)
	if actor == "" {
		actor = actorFromRequest(r)
	}
	inc, err := s.approvals.Approve(r.Context(), r.PathValue("id"), actor)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, incidentJSON(inc))
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	actor, reason := readActorBody(r)
	if actor == "" {
		actor = actorFromRequest(r)
	}
	inc, err := s.approvals.Reject(r.Context(), r.PathValue("id"), actor, reason)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, incidentJSON(inc))
}

func readActorBody(r *http.Request) (string, string) {
	if r.Body == nil {
		return "", ""
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil || len(b) == 0 {
		return r.Header.Get("X-Actor"), ""
	}
	var body actorBody
	if err := json.Unmarshal(b, &body); err != nil {
		return r.Header.Get("X-Actor"), ""
	}
	if body.Actor == "" {
		body.Actor = r.Header.Get("X-Actor")
	}
	return body.Actor, body.Reason
}
