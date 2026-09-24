package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
	"github.com/tmc/langchaingo/llms"
)

const (
	ToolGitHubCompare      = "github_compare"
	ToolGetArgoApplication = "get_argo_application"
)

// ChangeExecutor dispatches optional GitHub compare and Argo status tools.
type ChangeExecutor struct {
	GitHub ports.GitHubInvestigator
	Argo   ports.ArgoInvestigator
}

func InvestigatorChangeDefs() []llms.Tool {
	return []llms.Tool{
		fn(ToolGitHubCompare, "Compare two git refs on GitHub (read-only). Use after a rollout to see what changed.", map[string]any{
			"type":     "object",
			"required": []string{"owner", "repo", "base", "head"},
			"properties": map[string]any{
				"owner": map[string]any{"type": "string"},
				"repo":  map[string]any{"type": "string"},
				"base":  map[string]any{"type": "string"},
				"head":  map[string]any{"type": "string"},
			},
		}),
		fn(ToolGetArgoApplication, "Get Argo CD Application sync and health status (read-only).", map[string]any{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"name":      map[string]any{"type": "string"},
			},
		}),
	}
}

func (e *ChangeExecutor) Call(ctx context.Context, name, arguments string) (string, error) {
	args := map[string]any{}
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", fmt.Errorf("%w: %v", domain.ErrInvalidToolArgs, err)
		}
	}
	switch name {
	case ToolGitHubCompare:
		if e == nil || e.GitHub == nil {
			return "", fmt.Errorf("%w: github investigator not bound", domain.ErrUnknownTool)
		}
		v, err := e.GitHub.Compare(ctx, str(args, "owner"), str(args, "repo"), str(args, "base"), str(args, "head"))
		return marshalTool(name, v, err)
	case ToolGetArgoApplication:
		if e == nil || e.Argo == nil {
			return "", fmt.Errorf("%w: argo investigator not bound", domain.ErrUnknownTool)
		}
		v, err := e.Argo.GetApplication(ctx, str(args, "namespace"), str(args, "name"))
		return marshalTool(name, v, err)
	default:
		return "", fmt.Errorf("%w: %s", domain.ErrUnknownTool, name)
	}
}
