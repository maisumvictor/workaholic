package k8s

import (
	"fmt"
	"os"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	ModeVM           = "vm"
	ModeK8sContainer = "k8s-container"
)

// FactoryConfig controls how the dual Kubernetes clients are constructed.
type FactoryConfig struct {
	Mode string

	// Optional split kubeconfigs (MODE=vm). When empty, both clients share
	// the default kubeconfig / in-cluster config. RBAC still splits duties:
	// InvestigatorClient never writes; RemediatorClient only patches HPAs.
	InvestigatorKubeconfig string
	RemediatorKubeconfig   string

	ProtectedNamespaces []string
	MaxReplicas         int32
}

// Clients is the dual-client pair required by the security model.
type Clients struct {
	Investigator *InvestigatorClient
	Remediator   *RemediatorClient
}

// NewFactoryConfigFromEnv reads MODE and optional kubeconfig split from the environment.
func NewFactoryConfigFromEnv() FactoryConfig {
	mode := strings.TrimSpace(os.Getenv("MODE"))
	if mode == "" {
		mode = ModeVM
	}
	max := int32(30)
	if v := strings.TrimSpace(os.Getenv("MAX_REPLICAS")); v != "" {
		var parsed int32
		if _, err := fmt.Sscanf(v, "%d", &parsed); err == nil && parsed > 0 {
			max = parsed
		}
	}
	ns := []string{"kube-system", "monitoring", "cert-manager"}
	if v := strings.TrimSpace(os.Getenv("PROTECTED_NAMESPACES")); v != "" {
		ns = splitCSV(v)
	}
	return FactoryConfig{
		Mode:                   mode,
		InvestigatorKubeconfig: os.Getenv("KUBECONFIG_INVESTIGATOR"),
		RemediatorKubeconfig:   os.Getenv("KUBECONFIG_REMEDIATOR"),
		ProtectedNamespaces:    ns,
		MaxReplicas:            max,
	}
}

// Build constructs Investigator and Remediator clients.
func Build(cfg FactoryConfig) (*Clients, error) {
	invREST, err := restConfig(cfg.Mode, cfg.InvestigatorKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("investigator kubeconfig: %w", err)
	}
	remPath := cfg.RemediatorKubeconfig
	if remPath == "" {
		remPath = cfg.InvestigatorKubeconfig
	}
	remREST, err := restConfig(cfg.Mode, remPath)
	if err != nil {
		return nil, fmt.Errorf("remediator kubeconfig: %w", err)
	}

	invCS, err := kubernetes.NewForConfig(invREST)
	if err != nil {
		return nil, fmt.Errorf("investigator clientset: %w", err)
	}
	remCS, err := kubernetes.NewForConfig(remREST)
	if err != nil {
		return nil, fmt.Errorf("remediator clientset: %w", err)
	}

	guard := NewNamespaceGuard(cfg.ProtectedNamespaces)
	return &Clients{
		Investigator: NewInvestigatorClient(invCS, guard),
		Remediator:   NewRemediatorClient(remCS, guard, cfg.MaxReplicas),
	}, nil
}

func restConfig(mode, kubeconfigPath string) (*rest.Config, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ModeK8sContainer:
		return rest.InClusterConfig()
	case ModeVM, "":
		if kubeconfigPath != "" {
			return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		}
		loading := clientcmd.NewDefaultClientConfigLoadingRules()
		overrides := &clientcmd.ConfigOverrides{}
		return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loading, overrides).ClientConfig()
	default:
		return nil, fmt.Errorf("unknown MODE %q (want %s or %s)", mode, ModeVM, ModeK8sContainer)
	}
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
