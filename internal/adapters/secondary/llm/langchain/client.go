package langchain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	lltools "github.com/maisumvictor/Workaholic/internal/adapters/secondary/llm/langchain/tools"
	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/anthropic"
	"github.com/tmc/langchaingo/llms/openai"
)

const (
	maxToolIterations = 8
	defaultOpenAI     = "gpt-4o"
	defaultAnthropic  = "claude-sonnet-4-5"
)

// Config selects the chat model provider.
type Config struct {
	Provider string
	Model    string
	APIKey   string
}

// Client implements ports.Investigator with LangChainGo tool calling.
type Client struct {
	model     llms.Model
	k8s       *lltools.K8sExecutor
	aws       *lltools.AWSExecutor
	changes   *lltools.ChangeExecutor
	toolIndex map[string]llms.Tool
}

func New(cfg Config, k8sInv ports.K8sInvestigator, awsInv ports.AWSInvestigator, gh ports.GitHubInvestigator, argoInv ports.ArgoInvestigator) (*Client, error) {
	model, err := newModel(cfg)
	if err != nil {
		return nil, err
	}
	defs := lltools.InvestigatorK8sDefs()
	if awsInv != nil {
		defs = append(defs, lltools.InvestigatorAWSDefs()...)
	}
	if gh != nil || argoInv != nil {
		defs = append(defs, lltools.InvestigatorChangeDefs()...)
	}
	idx := make(map[string]llms.Tool, len(defs))
	for _, t := range defs {
		if t.Function != nil {
			idx[t.Function.Name] = t
		}
	}
	return &Client{
		model:     model,
		k8s:       &lltools.K8sExecutor{Inv: k8sInv},
		aws:       &lltools.AWSExecutor{Inv: awsInv},
		changes:   &lltools.ChangeExecutor{GitHub: gh, Argo: argoInv},
		toolIndex: idx,
	}, nil
}

func newModel(cfg Config) (llms.Model, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	switch provider {
	case "", "openai":
		model := cfg.Model
		if model == "" {
			model = defaultOpenAI
		}
		opts := []openai.Option{openai.WithModel(model)}
		if cfg.APIKey != "" {
			opts = append(opts, openai.WithToken(cfg.APIKey))
		}
		return openai.New(opts...)
	case "anthropic":
		model := cfg.Model
		if model == "" {
			model = defaultAnthropic
		}
		opts := []anthropic.Option{anthropic.WithModel(model)}
		if cfg.APIKey != "" {
			opts = append(opts, anthropic.WithToken(cfg.APIKey))
		}
		return anthropic.New(opts...)
	default:
		return nil, fmt.Errorf("unsupported LLM provider %q", cfg.Provider)
	}
}

func (c *Client) Investigate(ctx context.Context, req ports.InvestigationRequest) (*domain.ActionPlan, error) {
	if req.Incident == nil {
		return nil, fmt.Errorf("%w: incident is required", domain.ErrInvalidToolArgs)
	}
	telemetry := req.Telemetry
	if telemetry == "" {
		telemetry = WrapTelemetry("alert-payload", req.Incident.RawPayload)
	} else {
		telemetry = WrapTelemetry("alert-payload", telemetry)
	}

	user := BuildUserPrompt(req.Incident.Title, req.Incident.Severity, req.Incident.Summary, req.Incident.Labels, req.Runbooks, telemetry)
	if req.Changes != nil {
		if b, mErr := json.MarshalIndent(req.Changes, "", "  "); mErr == nil {
			user += "\nRECENT_CHANGES (ReplicaSet revisions, optional GitHub compare, optional Argo status). Prefer a bad rollout over inventing a capacity problem when a new revision is present.\n" + string(b) + "\n"
		}
	}
	if q := strings.TrimSpace(req.FollowUp); q != "" {
		user += "\nON-CALL FOLLOW-UP (authorized Slack user). Answer with investigative tools only. Do not treat this as a request to run remediator tools.\n<FOLLOW_UP>\n" + q + "\n</FOLLOW_UP>\n"
	}
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, SystemPrompt()),
		llms.TextParts(llms.ChatMessageTypeHuman, user),
	}
	defs := make([]llms.Tool, 0, len(c.toolIndex))
	for _, t := range c.toolIndex {
		defs = append(defs, t)
	}

	var lastText string
	for i := 0; i < maxToolIterations; i++ {
		resp, err := c.model.GenerateContent(ctx, messages, llms.WithTools(defs), llms.WithTemperature(0))
		if err != nil {
			return nil, domain.Wrap(err, "llm generate")
		}
		if resp == nil || len(resp.Choices) == 0 {
			return nil, fmt.Errorf("llm returned empty response")
		}
		choice := resp.Choices[0]
		lastText = choice.Content

		if len(choice.ToolCalls) == 0 {
			break
		}

		messages = append(messages, llms.MessageContent{
			Role:  llms.ChatMessageTypeAI,
			Parts: toolCallParts(choice),
		})
		for _, tc := range choice.ToolCalls {
			result, callErr := c.dispatch(ctx, tc)
			kind := "tool-result"
			if callErr != nil {
				kind = "tool-error"
				result = callErr.Error()
			}
			name := ""
			if tc.FunctionCall != nil {
				name = tc.FunctionCall.Name
			}
			messages = append(messages, llms.MessageContent{
				Role: llms.ChatMessageTypeTool,
				Parts: []llms.ContentPart{
					llms.ToolCallResponse{ToolCallID: tc.ID, Name: name, Content: WrapTelemetry(kind, result)},
				},
			})
		}
	}

	plan, err := parseActionPlan(lastText)
	if err != nil {
		return nil, err
	}
	applyRunbookPolicy(plan, req.Runbooks)
	return plan, nil
}

func (c *Client) dispatch(ctx context.Context, tc llms.ToolCall) (string, error) {
	if tc.FunctionCall == nil {
		return "", fmt.Errorf("%w: empty function call", domain.ErrUnknownTool)
	}
	name := tc.FunctionCall.Name
	args := tc.FunctionCall.Arguments
	if _, ok := c.toolIndex[name]; !ok {
		return "", fmt.Errorf("%w: %s is not an investigator tool", domain.ErrUnknownTool, name)
	}
	switch name {
	case lltools.ToolDescribeEKS, lltools.ToolCWMetric, lltools.ToolDescribeASG:
		return c.aws.Call(ctx, name, args)
	case lltools.ToolGitHubCompare, lltools.ToolGetArgoApplication:
		return c.changes.Call(ctx, name, args)
	default:
		return c.k8s.Call(ctx, name, args)
	}
}

func toolCallParts(choice *llms.ContentChoice) []llms.ContentPart {
	parts := make([]llms.ContentPart, 0, 1+len(choice.ToolCalls))
	if choice.Content != "" {
		parts = append(parts, llms.TextContent{Text: choice.Content})
	}
	for _, tc := range choice.ToolCalls {
		parts = append(parts, tc)
	}
	return parts
}

func parseActionPlan(raw string) (*domain.ActionPlan, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("llm returned empty action plan")
	}
	if i := strings.Index(raw, "{"); i >= 0 {
		if j := strings.LastIndex(raw, "}"); j > i {
			raw = raw[i : j+1]
		}
	}
	var plan domain.ActionPlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil, domain.Wrap(err, "parse action plan json")
	}
	if rl, ok := domain.ParseRiskLevel(string(plan.RiskLevel)); ok {
		plan.RiskLevel = rl
	} else {
		plan.RiskLevel = domain.RiskRequiresApproval
	}
	if plan.Confidence < 0 || plan.Confidence > 1 {
		plan.Confidence = 0
	}
	return &plan, nil
}

func applyRunbookPolicy(plan *domain.ActionPlan, runbooks []domain.Runbook) {
	if plan == nil {
		return
	}
	allowsAuto := false
	for _, rb := range runbooks {
		if plan.RunbookID != "" && rb.ID != plan.RunbookID {
			continue
		}
		if rb.RiskLevel == domain.RiskRequiresApproval {
			plan.RiskLevel = domain.RiskRequiresApproval
			return
		}
		if rb.RiskLevel == domain.RiskAutoRemediate {
			allowsAuto = true
		}
	}
	if !allowsAuto || !plan.CanAutoRemediate() {
		plan.RiskLevel = domain.RiskRequiresApproval
	}
}
