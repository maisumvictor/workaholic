package ports

import "context"

// EKSClusterView is a read-only EKS cluster snapshot.
type EKSClusterView struct {
	Name     string
	Status   string
	Version  string
	Endpoint string
	Health   []string
}

// CloudWatchMetricPoint is a single datapoint.
type CloudWatchMetricPoint struct {
	Timestamp string
	Average   float64
	Maximum   float64
}

// ASGView is a read-only Auto Scaling Group snapshot.
type ASGView struct {
	Name            string
	MinSize         int32
	MaxSize         int32
	DesiredCapacity int32
	Instances       int32
	Health          string
}

// AWSInvestigator is the read-only AWS port used during investigation.
type AWSInvestigator interface {
	DescribeEKSCluster(ctx context.Context, name string) (*EKSClusterView, error)
	GetCloudWatchMetric(ctx context.Context, namespace, metricName, dimensionName, dimensionValue string) ([]CloudWatchMetricPoint, error)
	DescribeAutoScalingGroup(ctx context.Context, name string) (*ASGView, error)
}

// AWSRemediator is the strictly scoped AWS write port.
type AWSRemediator interface {
	UpdateASGDesiredCapacity(ctx context.Context, name string, desired int32) (*ASGView, error)
}
