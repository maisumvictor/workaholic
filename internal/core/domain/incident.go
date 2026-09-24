package domain

import (
	"encoding/json"
	"time"
)

// IncidentStatus is the lifecycle state of an incident.
type IncidentStatus string

const (
	StatusInvestigating    IncidentStatus = "investigating"
	StatusAwaitingApproval IncidentStatus = "awaiting_approval"
	StatusExecuting        IncidentStatus = "executing"
	StatusResolved         IncidentStatus = "resolved"
	StatusFailed           IncidentStatus = "failed"
)

// Terminal reports whether no further automated work should run.
func (s IncidentStatus) Terminal() bool {
	return s == StatusResolved || s == StatusFailed
}

// Incident is the aggregate root for a 1st-level response case.
type Incident struct {
	ID         string
	Source     string
	Title      string
	Summary    string
	Severity   string
	Status     IncidentStatus
	RiskLevel  RiskLevel
	ActionPlan *ActionPlan
	RawPayload string
	Labels     map[string]string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ActionPlan is the structured remediation proposal produced by the LLM.
type ActionPlan struct {
	Summary    string       `json:"summary"`
	RiskLevel  RiskLevel    `json:"risk_level"`
	Rationale  string       `json:"rationale"`
	Confidence float64      `json:"confidence"`
	RunbookID  string       `json:"runbook_id"`
	Steps      []ActionStep `json:"steps"`
}

// ActionStep is a strongly-typed remediator invocation. Tool names must match
// registered remediator tools (never a generic shell).
type ActionStep struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	Reason    string         `json:"reason"`
}

// AuditRecord is an immutable trail entry for investigation and remediation.
type AuditRecord struct {
	ID         string
	IncidentID string
	Actor      string
	Action     string
	Details    string
	CreatedAt  time.Time
}

const (
	ToolPatchHPAMaxReplicas = "patch_hpa_max_replicas"
	ToolUpdateASGDesired    = "update_asg_desired_capacity"
)

// AllowedAutoRemediateTools is the closed set of tools that may run without a
// human when risk is AutoRemediate. Anything else forces approval.
var AllowedAutoRemediateTools = map[string]struct{}{
	ToolPatchHPAMaxReplicas: {},
}

// CanAutoRemediate applies policy on top of the LLM's suggested risk level.
func (p *ActionPlan) CanAutoRemediate() bool {
	if p == nil || len(p.Steps) == 0 {
		return false
	}
	if p.RiskLevel != RiskAutoRemediate {
		return false
	}
	if p.Confidence < 0.7 {
		return false
	}
	for _, step := range p.Steps {
		if _, ok := AllowedAutoRemediateTools[step.Tool]; !ok {
			return false
		}
	}
	return true
}

// MarshalActionPlan serializes an action plan for persistence.
func MarshalActionPlan(p *ActionPlan) (string, error) {
	if p == nil {
		return "", nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", Wrap(err, "marshal action plan")
	}
	return string(b), nil
}

// UnmarshalActionPlan deserializes an action plan from storage.
func UnmarshalActionPlan(raw string) (*ActionPlan, error) {
	if raw == "" {
		return nil, nil
	}
	var p ActionPlan
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, Wrap(err, "unmarshal action plan")
	}
	return &p, nil
}
