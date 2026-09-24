package testkit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
)

// MemoryRepo is an in-memory IncidentRepository for tests.
type MemoryRepo struct {
	mu        sync.Mutex
	incidents map[string]*domain.Incident
	audit     []domain.AuditRecord
}

func NewMemoryRepo() *MemoryRepo {
	return &MemoryRepo{incidents: map[string]*domain.Incident{}}
}

func (r *MemoryRepo) Create(_ context.Context, inc *domain.Incident) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.incidents[inc.ID]; ok {
		return fmt.Errorf("duplicate incident %s", inc.ID)
	}
	r.incidents[inc.ID] = cloneIncident(inc)
	return nil
}

func (r *MemoryRepo) Update(_ context.Context, inc *domain.Incident) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.incidents[inc.ID]; !ok {
		return domain.ErrNotFound
	}
	r.incidents[inc.ID] = cloneIncident(inc)
	return nil
}

func (r *MemoryRepo) Get(_ context.Context, id string) (*domain.Incident, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inc, ok := r.incidents[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return cloneIncident(inc), nil
}

func (r *MemoryRepo) List(_ context.Context, filter ports.IncidentFilter) ([]domain.Incident, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	out := make([]domain.Incident, 0, len(r.incidents))
	for _, inc := range r.incidents {
		if filter.Status != "" && inc.Status != filter.Status {
			continue
		}
		out = append(out, *cloneIncident(inc))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *MemoryRepo) AppendAudit(_ context.Context, rec *domain.AuditRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *rec
	r.audit = append(r.audit, cp)
	return nil
}

func (r *MemoryRepo) ListAudit(_ context.Context, incidentID string) ([]domain.AuditRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.AuditRecord, 0)
	for _, rec := range r.audit {
		if rec.IncidentID == incidentID {
			out = append(out, rec)
		}
	}
	return out, nil
}

func cloneIncident(inc *domain.Incident) *domain.Incident {
	cp := *inc
	if inc.Labels != nil {
		cp.Labels = make(map[string]string, len(inc.Labels))
		for k, v := range inc.Labels {
			cp.Labels[k] = v
		}
	}
	if inc.ActionPlan != nil {
		plan := *inc.ActionPlan
		plan.Steps = append([]domain.ActionStep(nil), inc.ActionPlan.Steps...)
		for i := range plan.Steps {
			if plan.Steps[i].Arguments != nil {
				args := make(map[string]any, len(plan.Steps[i].Arguments))
				for k, v := range plan.Steps[i].Arguments {
					args[k] = v
				}
				plan.Steps[i].Arguments = args
			}
		}
		cp.ActionPlan = &plan
	}
	return &cp
}

// StaticRunbooks is a fixed RunbookRepository.
type StaticRunbooks struct {
	Books []domain.Runbook
}

func (r StaticRunbooks) List(context.Context) ([]domain.Runbook, error) { return r.Books, nil }

func (r StaticRunbooks) Get(_ context.Context, id string) (*domain.Runbook, error) {
	for i := range r.Books {
		if r.Books[i].ID == id {
			cp := r.Books[i]
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r StaticRunbooks) Match(_ context.Context, labels map[string]string) ([]domain.Runbook, error) {
	out := make([]domain.Runbook, 0)
	for _, rb := range r.Books {
		if rb.Matches(labels) {
			out = append(out, rb)
		}
	}
	return out, nil
}

// ScriptedInvestigator returns a fixed action plan.
type ScriptedInvestigator struct {
	Plan  *domain.ActionPlan
	Err   error
	Calls int
	Last  ports.InvestigationRequest
}

func (s *ScriptedInvestigator) Investigate(_ context.Context, req ports.InvestigationRequest) (*domain.ActionPlan, error) {
	s.Calls++
	s.Last = req
	if s.Err != nil {
		return nil, s.Err
	}
	if s.Plan == nil {
		return nil, fmt.Errorf("no scripted plan")
	}
	cp := *s.Plan
	cp.Steps = append([]domain.ActionStep(nil), s.Plan.Steps...)
	return &cp, nil
}

func (s *ScriptedInvestigator) LastQuestion() string {
	if s.Last.FollowUp != "" {
		return s.Last.FollowUp
	}
	return s.Last.Telemetry
}

// FakeMessaging records Messaging port calls.
type FollowUpNote struct {
	IncidentID string
	Question   string
	Answer     string
}

type FakeMessaging struct {
	mu        sync.Mutex
	Approvals []string
	Resolved  []string
	Failed    []string
	Auto      []string
	FollowUps []FollowUpNote
}

func (m *FakeMessaging) RequestApproval(_ context.Context, incident *domain.Incident, _ *domain.ActionPlan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Approvals = append(m.Approvals, incident.ID)
	return nil
}
func (m *FakeMessaging) NotifyResolved(_ context.Context, incident *domain.Incident, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Resolved = append(m.Resolved, incident.ID)
	return nil
}
func (m *FakeMessaging) NotifyFailed(_ context.Context, incident *domain.Incident, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Failed = append(m.Failed, incident.ID)
	return nil
}
func (m *FakeMessaging) NotifyAutoRemediated(_ context.Context, incident *domain.Incident, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Auto = append(m.Auto, incident.ID)
	return nil
}
func (m *FakeMessaging) ReplyFollowUp(_ context.Context, incident *domain.Incident, question, answer string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FollowUps = append(m.FollowUps, FollowUpNote{IncidentID: incident.ID, Question: question, Answer: answer})
	return nil
}

// Call is a recorded remediator invocation.
type Call struct {
	Tool string
	Args map[string]any
}

// FakeK8s implements K8sInvestigator and K8sRemediator with in-memory objects.
type FakeK8s struct {
	mu          sync.Mutex
	Calls       []Call
	HPAs        map[string]*ports.HPAView
	Deployments map[string]*ports.DeploymentView
	Pods        map[string]*ports.PodView
	Events      []ports.EventView
	Protected   map[string]struct{}
	MaxReplicas int32
}

func NewFakeK8s() *FakeK8s {
	return &FakeK8s{
		HPAs:        map[string]*ports.HPAView{},
		Deployments: map[string]*ports.DeploymentView{},
		Pods:        map[string]*ports.PodView{},
		Protected:   map[string]struct{}{"kube-system": {}, "monitoring": {}, "cert-manager": {}},
		MaxReplicas: 30,
	}
}

func key(ns, name string) string { return ns + "/" + name }

func (f *FakeK8s) assertWritable(namespace string) error {
	if _, ok := f.Protected[namespace]; ok {
		return fmt.Errorf("%w: %s", domain.ErrProtectedNamespace, namespace)
	}
	return nil
}

func (f *FakeK8s) GetPod(_ context.Context, namespace, name string) (*ports.PodView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.Pods[key(namespace, name)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (f *FakeK8s) ListPods(_ context.Context, namespace, _ string) ([]ports.PodView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ports.PodView, 0)
	for _, p := range f.Pods {
		if p.Namespace == namespace {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (f *FakeK8s) GetDeployment(_ context.Context, namespace, name string) (*ports.DeploymentView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.Deployments[key(namespace, name)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *d
	return &cp, nil
}

func (f *FakeK8s) ListEvents(context.Context, string, string, string) ([]ports.EventView, error) {
	return append([]ports.EventView(nil), f.Events...), nil
}

func (f *FakeK8s) GetPodLogs(context.Context, string, string, string, int64) (string, error) {
	return "fake logs", nil
}

func (f *FakeK8s) GetHPA(_ context.Context, namespace, name string) (*ports.HPAView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.HPAs[key(namespace, name)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *h
	return &cp, nil
}

func (f *FakeK8s) ListHPAs(_ context.Context, namespace string) ([]ports.HPAView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ports.HPAView, 0)
	for _, h := range f.HPAs {
		if h.Namespace == namespace {
			out = append(out, *h)
		}
	}
	return out, nil
}

func (f *FakeK8s) record(tool string, args map[string]any) {
	f.Calls = append(f.Calls, Call{Tool: tool, Args: args})
}

func (f *FakeK8s) PatchHPAMaxReplicas(_ context.Context, namespace, name string, maxReplicas int32) (*ports.HPAView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.assertWritable(namespace); err != nil {
		return nil, err
	}
	if maxReplicas < 1 {
		return nil, domain.ErrInvalidToolArgs
	}
	if f.MaxReplicas > 0 && maxReplicas > f.MaxReplicas {
		return nil, fmt.Errorf("%w: %d > %d", domain.ErrReplicaCapExceeded, maxReplicas, f.MaxReplicas)
	}
	h, ok := f.HPAs[key(namespace, name)]
	if !ok {
		h = &ports.HPAView{Namespace: namespace, Name: name}
		f.HPAs[key(namespace, name)] = h
	}
	h.MaxReplicas = maxReplicas
	f.record(domain.ToolPatchHPAMaxReplicas, map[string]any{"namespace": namespace, "name": name, "max_replicas": maxReplicas})
	cp := *h
	return &cp, nil
}

func (f *FakeK8s) RestartRollout(_ context.Context, namespace, name string) (*ports.DeploymentView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.assertWritable(namespace); err != nil {
		return nil, err
	}
	d, ok := f.Deployments[key(namespace, name)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	d.UpdatedReplicas = d.Replicas
	f.record(domain.ToolRestartRollout, map[string]any{"namespace": namespace, "name": name})
	cp := *d
	return &cp, nil
}

func (f *FakeK8s) RollbackDeployment(_ context.Context, namespace, name string) (*ports.DeploymentView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.assertWritable(namespace); err != nil {
		return nil, err
	}
	d, ok := f.Deployments[key(namespace, name)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	f.record(domain.ToolRollbackDeployment, map[string]any{"namespace": namespace, "name": name})
	cp := *d
	return &cp, nil
}

func (f *FakeK8s) ScaleDeployment(_ context.Context, namespace, name string, replicas int32) (*ports.DeploymentView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.assertWritable(namespace); err != nil {
		return nil, err
	}
	if replicas < 1 {
		return nil, domain.ErrInvalidToolArgs
	}
	if f.MaxReplicas > 0 && replicas > f.MaxReplicas {
		return nil, fmt.Errorf("%w: %d > %d", domain.ErrReplicaCapExceeded, replicas, f.MaxReplicas)
	}
	d, ok := f.Deployments[key(namespace, name)]
	if !ok {
		d = &ports.DeploymentView{Namespace: namespace, Name: name}
		f.Deployments[key(namespace, name)] = d
	}
	d.Replicas = replicas
	d.ReadyReplicas = replicas
	f.record(domain.ToolScaleDeployment, map[string]any{"namespace": namespace, "name": name, "replicas": replicas})
	cp := *d
	return &cp, nil
}

func (f *FakeK8s) DeleteCrashLoopPod(_ context.Context, namespace, name string) (*ports.PodView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.assertWritable(namespace); err != nil {
		return nil, err
	}
	p, ok := f.Pods[key(namespace, name)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if p.Reason != "CrashLoopBackOff" {
		return nil, fmt.Errorf("%w: pod %s/%s is not CrashLoopBackOff", domain.ErrInvalidToolArgs, namespace, name)
	}
	delete(f.Pods, key(namespace, name))
	f.record(domain.ToolDeleteCrashLoopPod, map[string]any{"namespace": namespace, "name": name})
	cp := *p
	return &cp, nil
}

// ToolsCalled returns remediator tool names in order.
func (f *FakeK8s) ToolsCalled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.Calls))
	for _, c := range f.Calls {
		out = append(out, c.Tool)
	}
	return out
}

// FakeAWSRemediator records ASG updates.
type FakeAWSRemediator struct {
	Calls []Call
}

func (f *FakeAWSRemediator) UpdateASGDesiredCapacity(_ context.Context, name string, desired int32) (*ports.ASGView, error) {
	f.Calls = append(f.Calls, Call{Tool: domain.ToolUpdateASGDesired, Args: map[string]any{"name": name, "desired_capacity": desired}})
	return &ports.ASGView{Name: name, DesiredCapacity: desired}, nil
}

// SeedHPAAtMax is a typical KubeHPAReplicasAtMax cluster snapshot.
func SeedHPAAtMax(k *FakeK8s, ns, name string, replicas int32) {
	k.HPAs[key(ns, name)] = &ports.HPAView{
		Namespace: ns, Name: name,
		MinReplicas: 1, MaxReplicas: replicas, Desired: replicas, Current: replicas,
		TargetRef: "Deployment/" + name,
	}
	k.Deployments[key(ns, name)] = &ports.DeploymentView{
		Namespace: ns, Name: name,
		Replicas: replicas, ReadyReplicas: replicas, UpdatedReplicas: replicas,
	}
	k.Pods[key(ns, name+"-0")] = &ports.PodView{
		Namespace: ns, Name: name + "-0", Phase: "Running", Ready: "1/1",
	}
}

// Now is a stable clock for tests that need one.
func Now() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) }
