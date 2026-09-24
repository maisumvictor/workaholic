package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	asgtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
)

const maxASGDesired int32 = 30

// Client implements both AWS investigator and remediator ports using the
// default credential chain (local SSO/env files in MODE=vm, IRSA in-cluster).
type Client struct {
	eks *eks.Client
	cw  *cloudwatch.Client
	asg *autoscaling.Client
}

func New(ctx context.Context, region string) (*Client, error) {
	opts := []func(*config.LoadOptions) error{}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &Client{
		eks: eks.NewFromConfig(cfg),
		cw:  cloudwatch.NewFromConfig(cfg),
		asg: autoscaling.NewFromConfig(cfg),
	}, nil
}

func (c *Client) DescribeEKSCluster(ctx context.Context, name string) (*ports.EKSClusterView, error) {
	out, err := c.eks.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
	if err != nil {
		return nil, domain.Wrap(err, "describe eks cluster")
	}
	cl := out.Cluster
	view := &ports.EKSClusterView{Name: name}
	if cl == nil {
		return view, nil
	}
	view.Status = string(cl.Status)
	view.Version = aws.ToString(cl.Version)
	view.Endpoint = aws.ToString(cl.Endpoint)
	if cl.Health != nil {
		for _, i := range cl.Health.Issues {
			view.Health = append(view.Health, fmt.Sprintf("%s:%s", i.Code, aws.ToString(i.Message)))
		}
	}
	return view, nil
}

func (c *Client) GetCloudWatchMetric(ctx context.Context, namespace, metricName, dimensionName, dimensionValue string) ([]ports.CloudWatchMetricPoint, error) {
	end := time.Now().UTC()
	start := end.Add(-30 * time.Minute)
	out, err := c.cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
		Namespace:  aws.String(namespace),
		MetricName: aws.String(metricName),
		StartTime:  aws.Time(start),
		EndTime:    aws.Time(end),
		Period:     aws.Int32(60),
		Statistics: []cwtypes.Statistic{cwtypes.StatisticAverage, cwtypes.StatisticMaximum},
		Dimensions: []cwtypes.Dimension{{
			Name:  aws.String(dimensionName),
			Value: aws.String(dimensionValue),
		}},
	})
	if err != nil {
		return nil, domain.Wrap(err, "get cloudwatch metric")
	}
	points := make([]ports.CloudWatchMetricPoint, 0, len(out.Datapoints))
	for _, dp := range out.Datapoints {
		ts := ""
		if dp.Timestamp != nil {
			ts = dp.Timestamp.UTC().Format(time.RFC3339)
		}
		points = append(points, ports.CloudWatchMetricPoint{
			Timestamp: ts,
			Average:   aws.ToFloat64(dp.Average),
			Maximum:   aws.ToFloat64(dp.Maximum),
		})
	}
	return points, nil
}

func (c *Client) DescribeAutoScalingGroup(ctx context.Context, name string) (*ports.ASGView, error) {
	out, err := c.asg.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{name},
	})
	if err != nil {
		return nil, domain.Wrap(err, "describe asg")
	}
	if len(out.AutoScalingGroups) == 0 {
		return nil, domain.ErrNotFound
	}
	return asgView(out.AutoScalingGroups[0]), nil
}

func (c *Client) UpdateASGDesiredCapacity(ctx context.Context, name string, desired int32) (*ports.ASGView, error) {
	if desired < 0 {
		return nil, fmt.Errorf("%w: desired must be >= 0", domain.ErrInvalidToolArgs)
	}
	if desired > maxASGDesired {
		return nil, fmt.Errorf("%w: %d > %d", domain.ErrReplicaCapExceeded, desired, maxASGDesired)
	}
	_, err := c.asg.SetDesiredCapacity(ctx, &autoscaling.SetDesiredCapacityInput{
		AutoScalingGroupName: aws.String(name),
		DesiredCapacity:      aws.Int32(desired),
		HonorCooldown:        aws.Bool(true),
	})
	if err != nil {
		return nil, domain.Wrap(err, "set asg desired capacity")
	}
	return c.DescribeAutoScalingGroup(ctx, name)
}

func asgView(g asgtypes.AutoScalingGroup) *ports.ASGView {
	health := aws.ToString(g.HealthCheckType)
	return &ports.ASGView{
		Name:            aws.ToString(g.AutoScalingGroupName),
		MinSize:         aws.ToInt32(g.MinSize),
		MaxSize:         aws.ToInt32(g.MaxSize),
		DesiredCapacity: aws.ToInt32(g.DesiredCapacity),
		Instances:       int32(len(g.Instances)),
		Health:          health,
	}
}
