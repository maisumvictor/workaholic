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
	ToolDescribeEKS = "describe_eks_cluster"
	ToolCWMetric    = "get_cloudwatch_metric"
	ToolDescribeASG = "describe_asg"
	ToolUpdateASG   = "update_asg_desired_capacity"
)

// AWSExecutor dispatches strongly-typed AWS tools.
type AWSExecutor struct {
	Inv ports.AWSInvestigator
	Rem ports.AWSRemediator
}

func InvestigatorAWSDefs() []llms.Tool {
	return []llms.Tool{
		fn(ToolDescribeEKS, "Describe an EKS cluster by name.", map[string]any{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		}),
		fn(ToolCWMetric, "Fetch the last 30 minutes of a CloudWatch metric.", map[string]any{
			"type":     "object",
			"required": []string{"namespace", "metric_name", "dimension_name", "dimension_value"},
			"properties": map[string]any{
				"namespace":       map[string]any{"type": "string"},
				"metric_name":     map[string]any{"type": "string"},
				"dimension_name":  map[string]any{"type": "string"},
				"dimension_value": map[string]any{"type": "string"},
			},
		}),
		fn(ToolDescribeASG, "Describe an EC2 Auto Scaling Group.", map[string]any{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		}),
	}
}

func (e *AWSExecutor) Call(ctx context.Context, name, arguments string) (string, error) {
	if e.Inv == nil && name != ToolUpdateASG {
		return "", fmt.Errorf("%w: aws investigator not bound", domain.ErrUnknownTool)
	}
	args := map[string]any{}
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", fmt.Errorf("%w: %v", domain.ErrInvalidToolArgs, err)
		}
	}
	switch name {
	case ToolDescribeEKS:
		v, err := e.Inv.DescribeEKSCluster(ctx, str(args, "name"))
		return marshalTool(name, v, err)
	case ToolCWMetric:
		v, err := e.Inv.GetCloudWatchMetric(ctx, str(args, "namespace"), str(args, "metric_name"), str(args, "dimension_name"), str(args, "dimension_value"))
		return marshalTool(name, v, err)
	case ToolDescribeASG:
		v, err := e.Inv.DescribeAutoScalingGroup(ctx, str(args, "name"))
		return marshalTool(name, v, err)
	case ToolUpdateASG:
		if e.Rem == nil {
			return "", fmt.Errorf("%w: remediator not bound", domain.ErrUnknownTool)
		}
		v, err := e.Rem.UpdateASGDesiredCapacity(ctx, str(args, "name"), int32(num(args, "desired_capacity")))
		return marshalTool(name, v, err)
	default:
		return "", fmt.Errorf("%w: %s", domain.ErrUnknownTool, name)
	}
}
