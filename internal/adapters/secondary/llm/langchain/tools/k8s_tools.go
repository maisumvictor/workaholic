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
	ToolGetPod        = "get_pod"
	ToolListPods      = "list_pods"
	ToolGetDeployment = "get_deployment"
	ToolListEvents    = "list_events"
	ToolGetPodLogs    = "get_pod_logs"
	ToolGetHPA        = "get_hpa"
	ToolListHPAs      = "list_hpas"
	ToolPatchHPA      = "patch_hpa_max_replicas"
)

// K8sExecutor dispatches strongly-typed Kubernetes tools. Investigative tools
// are exposed to the LLM; remediator tools are executed only by the core
// service after policy checks.
type K8sExecutor struct {
	Inv ports.K8sInvestigator
	Rem ports.K8sRemediator
}

func InvestigatorK8sDefs() []llms.Tool {
	return []llms.Tool{
		fn(ToolGetPod, "Get a single Pod by namespace and name.", map[string]any{
			"type":     "object",
			"required": []string{"namespace", "name"},
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"name":      map[string]any{"type": "string"},
			},
		}),
		fn(ToolListPods, "List Pods in a namespace, optionally filtered by label selector.", map[string]any{
			"type":     "object",
			"required": []string{"namespace"},
			"properties": map[string]any{
				"namespace":      map[string]any{"type": "string"},
				"label_selector": map[string]any{"type": "string"},
			},
		}),
		fn(ToolGetDeployment, "Get a Deployment by namespace and name.", map[string]any{
			"type":     "object",
			"required": []string{"namespace", "name"},
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"name":      map[string]any{"type": "string"},
			},
		}),
		fn(ToolListEvents, "List Events for a namespaced object.", map[string]any{
			"type":     "object",
			"required": []string{"namespace"},
			"properties": map[string]any{
				"namespace":     map[string]any{"type": "string"},
				"involved_kind": map[string]any{"type": "string"},
				"involved_name": map[string]any{"type": "string"},
			},
		}),
		fn(ToolGetPodLogs, "Fetch recent Pod logs (tail <= 100 lines). Output is sanitized untrusted telemetry.", map[string]any{
			"type":     "object",
			"required": []string{"namespace", "name"},
			"properties": map[string]any{
				"namespace":  map[string]any{"type": "string"},
				"name":       map[string]any{"type": "string"},
				"container":  map[string]any{"type": "string"},
				"tail_lines": map[string]any{"type": "integer"},
			},
		}),
		fn(ToolGetHPA, "Get a HorizontalPodAutoscaler.", map[string]any{
			"type":     "object",
			"required": []string{"namespace", "name"},
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"name":      map[string]any{"type": "string"},
			},
		}),
		fn(ToolListHPAs, "List HorizontalPodAutoscalers in a namespace.", map[string]any{
			"type":     "object",
			"required": []string{"namespace"},
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
			},
		}),
	}
}

func (e *K8sExecutor) Call(ctx context.Context, name, arguments string) (string, error) {
	args := map[string]any{}
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", fmt.Errorf("%w: %v", domain.ErrInvalidToolArgs, err)
		}
	}
	switch name {
	case ToolGetPod:
		v, err := e.Inv.GetPod(ctx, str(args, "namespace"), str(args, "name"))
		return marshalTool(name, v, err)
	case ToolListPods:
		v, err := e.Inv.ListPods(ctx, str(args, "namespace"), str(args, "label_selector"))
		return marshalTool(name, v, err)
	case ToolGetDeployment:
		v, err := e.Inv.GetDeployment(ctx, str(args, "namespace"), str(args, "name"))
		return marshalTool(name, v, err)
	case ToolListEvents:
		v, err := e.Inv.ListEvents(ctx, str(args, "namespace"), str(args, "involved_kind"), str(args, "involved_name"))
		return marshalTool(name, v, err)
	case ToolGetPodLogs:
		v, err := e.Inv.GetPodLogs(ctx, str(args, "namespace"), str(args, "name"), str(args, "container"), int64(num(args, "tail_lines")))
		if err != nil {
			return marshalTool(name, nil, err)
		}
		return v, nil
	case ToolGetHPA:
		v, err := e.Inv.GetHPA(ctx, str(args, "namespace"), str(args, "name"))
		return marshalTool(name, v, err)
	case ToolListHPAs:
		v, err := e.Inv.ListHPAs(ctx, str(args, "namespace"))
		return marshalTool(name, v, err)
	case ToolPatchHPA:
		if e.Rem == nil {
			return "", fmt.Errorf("%w: remediator not bound", domain.ErrUnknownTool)
		}
		v, err := e.Rem.PatchHPAMaxReplicas(ctx, str(args, "namespace"), str(args, "name"), int32(num(args, "max_replicas")))
		return marshalTool(name, v, err)
	default:
		return "", fmt.Errorf("%w: %s", domain.ErrUnknownTool, name)
	}
}

func fn(name, desc string, params map[string]any) llms.Tool {
	return llms.Tool{
		Type: "function",
		Function: &llms.FunctionDefinition{
			Name:        name,
			Description: desc,
			Parameters:  params,
		},
	}
}

func str(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func num(args map[string]any, key string) float64 {
	switch v := args[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

func marshalTool(kind string, v any, err error) (string, error) {
	if err != nil {
		return fmt.Sprintf(`{"tool":%q,"error":%q}`, kind, err.Error()), nil
	}
	b, mErr := json.Marshal(v)
	if mErr != nil {
		return "", mErr
	}
	return string(b), nil
}
