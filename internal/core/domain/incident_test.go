package domain

import "testing"

func TestActionPlanCanAutoRemediate(t *testing.T) {
	ok := &ActionPlan{
		RiskLevel:  RiskAutoRemediate,
		Confidence: 0.9,
		Steps: []ActionStep{{
			Tool:      ToolPatchHPAMaxReplicas,
			Arguments: map[string]any{"namespace": "prod", "name": "api", "max_replicas": 12.0},
		}},
	}
	if !ok.CanAutoRemediate() {
		t.Fatal("expected auto-remediate")
	}

	asg := &ActionPlan{
		RiskLevel:  RiskAutoRemediate,
		Confidence: 0.99,
		Steps:      []ActionStep{{Tool: ToolUpdateASGDesired}},
	}
	if asg.CanAutoRemediate() {
		t.Fatal("ASG updates must never auto-remediate")
	}

	lowConf := &ActionPlan{RiskLevel: RiskAutoRemediate, Confidence: 0.4, Steps: ok.Steps}
	if lowConf.CanAutoRemediate() {
		t.Fatal("low confidence must require approval")
	}

	for _, tool := range []string{ToolRestartRollout, ToolRollbackDeployment, ToolScaleDeployment, ToolDeleteCrashLoopPod} {
		plan := &ActionPlan{
			RiskLevel:  RiskAutoRemediate,
			Confidence: 0.99,
			Steps:      []ActionStep{{Tool: tool}},
		}
		if plan.CanAutoRemediate() {
			t.Fatalf("%s must require approval even when the model claims auto_remediate", tool)
		}
	}
}
