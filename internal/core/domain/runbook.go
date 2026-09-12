package domain

import "strings"

// RiskLevel classifies whether an action plan may execute unattended.
type RiskLevel string

const (
	RiskAutoRemediate    RiskLevel = "auto_remediate"
	RiskRequiresApproval RiskLevel = "requires_approval"
)

// ParseRiskLevel maps a free-form string (from runbooks or LLM JSON) to a RiskLevel.
func ParseRiskLevel(raw string) (RiskLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(RiskAutoRemediate), "auto", "low":
		return RiskAutoRemediate, true
	case string(RiskRequiresApproval), "approval", "high", "medium":
		return RiskRequiresApproval, true
	default:
		return "", false
	}
}

// Runbook is a Markdown operational procedure consulted during investigation.
type Runbook struct {
	ID          string
	Title       string
	RiskLevel   RiskLevel
	MatchLabels map[string]string
	Body        string
	Path        string
}

// Matches reports whether every declared match label is present on the alert.
func (r Runbook) Matches(labels map[string]string) bool {
	if len(r.MatchLabels) == 0 {
		return false
	}
	for k, v := range r.MatchLabels {
		got, ok := labels[k]
		if !ok {
			return false
		}
		if v != "" && !strings.EqualFold(got, v) {
			return false
		}
	}
	return true
}
