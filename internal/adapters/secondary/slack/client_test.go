package slack

import (
	"testing"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	slackgo "github.com/slack-go/slack"
)

func TestApprovalBlocksIncludeFollowUpControls(t *testing.T) {
	inc := &domain.Incident{ID: "inc_1", Title: "crashloop", Summary: "api crashloop", Severity: "warning", RiskLevel: domain.RiskRequiresApproval}
	plan := &domain.ActionPlan{
		Summary: "restart api",
		Steps:   []domain.ActionStep{{Tool: domain.ToolRestartRollout, Arguments: map[string]any{"namespace": "prod", "name": "api"}}},
	}
	blocks := approvalBlocks(inc, plan)

	var sawAsk, sawInput bool
	for _, b := range blocks {
		switch blk := b.(type) {
		case *slackgo.ActionBlock:
			if blk.Elements == nil {
				continue
			}
			for _, el := range blk.Elements.ElementSet {
				btn, ok := el.(*slackgo.ButtonBlockElement)
				if !ok {
					continue
				}
				if btn.ActionID == ActionAskInvestigator && btn.Value == inc.ID {
					sawAsk = true
				}
			}
		case *slackgo.InputBlock:
			if blk.BlockID != "followup_input" {
				continue
			}
			el, ok := blk.Element.(*slackgo.PlainTextInputBlockElement)
			if ok && el.ActionID == ActionFollowUpQuestion {
				sawInput = true
			}
		}
	}
	if !sawAsk || !sawInput {
		t.Fatalf("approval card missing follow-up controls: ask=%v input=%v blocks=%d", sawAsk, sawInput, len(blocks))
	}
}
