package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/slack-go/slack"
)

const (
	ActionApprove          = "approve_incident"
	ActionReject           = "reject_incident"
	ActionAskInvestigator  = "ask_investigator"
	ActionFollowUpQuestion = "followup_question"
)

// Client implements ports.Messaging via Slack Block Kit.
type Client struct {
	api     *slack.Client
	channel string
}

func New(botToken, channel string) *Client {
	return &Client{
		api:     slack.New(botToken),
		channel: channel,
	}
}

// Noop is a Messaging implementation used when Slack is not configured.
type Noop struct{}

func (Noop) RequestApproval(context.Context, *domain.Incident, *domain.ActionPlan) error {
	return nil
}
func (Noop) NotifyResolved(context.Context, *domain.Incident, string) error { return nil }
func (Noop) NotifyFailed(context.Context, *domain.Incident, string) error   { return nil }
func (Noop) NotifyAutoRemediated(context.Context, *domain.Incident, string) error {
	return nil
}
func (Noop) ReplyFollowUp(context.Context, *domain.Incident, string, string) error { return nil }

func (c *Client) RequestApproval(ctx context.Context, incident *domain.Incident, plan *domain.ActionPlan) error {
	blocks := approvalBlocks(incident, plan)
	_, _, err := c.api.PostMessageContext(ctx, c.channel,
		slack.MsgOptionText(fmt.Sprintf("Approval required: %s", incident.Title), false),
		slack.MsgOptionBlocks(blocks...),
	)
	if err != nil {
		return domain.Wrap(err, "slack post approval")
	}
	return nil
}

func (c *Client) NotifyResolved(ctx context.Context, incident *domain.Incident, outcome string) error {
	return c.post(ctx, fmt.Sprintf(":white_check_mark: *Resolved* `%s` — %s\n%s", incident.ID, incident.Title, outcome))
}

func (c *Client) NotifyFailed(ctx context.Context, incident *domain.Incident, errMsg string) error {
	return c.post(ctx, fmt.Sprintf(":x: *Failed* `%s` — %s\n```%s```", incident.ID, incident.Title, errMsg))
}

func (c *Client) NotifyAutoRemediated(ctx context.Context, incident *domain.Incident, outcome string) error {
	return c.post(ctx, fmt.Sprintf(":robot_face: *Auto-remediated* `%s` — %s\n%s", incident.ID, incident.Title, outcome))
}

func (c *Client) ReplyFollowUp(ctx context.Context, incident *domain.Incident, question, answer string) error {
	return c.post(ctx, fmt.Sprintf(":mag: *Follow-up* `%s`\n*Q:* %s\n*A:* %s", incident.ID, question, answer))
}

func (c *Client) post(ctx context.Context, text string) error {
	_, _, err := c.api.PostMessageContext(ctx, c.channel, slack.MsgOptionText(text, false))
	if err != nil {
		return domain.Wrap(err, "slack post")
	}
	return nil
}

func approvalBlocks(incident *domain.Incident, plan *domain.ActionPlan) []slack.Block {
	summary := incident.Summary
	if plan != nil && plan.Summary != "" {
		summary = plan.Summary
	}
	rationale := ""
	steps := ""
	if plan != nil {
		rationale = plan.Rationale
		var b strings.Builder
		for i, s := range plan.Steps {
			args, _ := json.Marshal(s.Arguments)
			fmt.Fprintf(&b, "%d. `%s` %s\n    %s\n", i+1, s.Tool, string(args), s.Reason)
		}
		steps = b.String()
	}
	if len(steps) > 1800 {
		steps = steps[:1800] + "…"
	}

	header := slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, "Incident requires approval", false, false))
	body := slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf(
		"*ID:* `%s`\n*Title:* %s\n*Severity:* %s\n*Risk:* `%s`\n\n*Plan:* %s\n\n*Rationale:* %s\n\n*Steps:*\n%s",
		incident.ID, incident.Title, incident.Severity, incident.RiskLevel, summary, rationale, steps,
	), false, false), nil, nil)

	approve := slack.NewButtonBlockElement(ActionApprove, incident.ID, slack.NewTextBlockObject(slack.PlainTextType, "Approve", false, false))
	approve.Style = slack.StylePrimary
	reject := slack.NewButtonBlockElement(ActionReject, incident.ID, slack.NewTextBlockObject(slack.PlainTextType, "Reject", false, false))
	reject.Style = slack.StyleDanger
	ask := slack.NewButtonBlockElement(ActionAskInvestigator, incident.ID, slack.NewTextBlockObject(slack.PlainTextType, "Ask investigator", false, false))
	actions := slack.NewActionBlock("incident_actions", approve, reject, ask)

	placeholder := slack.NewTextBlockObject(slack.PlainTextType, "Ask a follow-up (read-only)", false, false)
	input := slack.NewPlainTextInputBlockElement(placeholder, ActionFollowUpQuestion)
	label := slack.NewTextBlockObject(slack.PlainTextType, "Follow-up question", false, false)
	inputBlock := slack.NewInputBlock("followup_input", label, nil, input)

	return []slack.Block{header, body, inputBlock, actions}
}
