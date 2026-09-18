package http_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	primaryhttp "github.com/maisumvictor/Workaholic/internal/adapters/primary/http"
	k8sadapter "github.com/maisumvictor/Workaholic/internal/adapters/secondary/k8s"
	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
	"github.com/maisumvictor/Workaholic/internal/core/services"
	"github.com/maisumvictor/Workaholic/internal/testkit"
)

const (
	testSlackSecret = "test-signing-secret"
	testApprover    = "U0123ABCD"
)

type harness struct {
	t      *testing.T
	server *httptest.Server
	k8s    *testkit.FakeK8s
	msg    *testkit.FakeMessaging
	llm    *testkit.ScriptedInvestigator
}

func newHarness(t *testing.T, plan *domain.ActionPlan, books []domain.Runbook) *harness {
	t.Helper()
	k8s := testkit.NewFakeK8s()
	llm := &testkit.ScriptedInvestigator{Plan: plan}
	msg := &testkit.FakeMessaging{}
	incidents := services.NewIncidentService(
		testkit.NewMemoryRepo(),
		testkit.StaticRunbooks{Books: books},
		llm,
		msg,
		nil,
		services.PlanExecutor{K8s: k8s},
		k8sadapter.NewHealthVerifier(k8s),
		nil,
	)
	approvals := services.NewApprovalService(incidents, services.NewStaticApprovers([]string{testApprover}), nil)
	srv := primaryhttp.NewServer(primaryhttp.Config{
		Addr:               "127.0.0.1:0",
		SlackSigningSecret: testSlackSecret,
	}, incidents, approvals, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &harness{t: t, server: ts, k8s: k8s, msg: msg, llm: llm}
}

func (h *harness) postGrafana(body string) (int, map[string]any) {
	h.t.Helper()
	res, err := http.Post(h.server.URL+"/webhooks/grafana", "application/json", strings.NewReader(body))
	if err != nil {
		h.t.Fatalf("grafana post: %v", err)
	}
	defer res.Body.Close()
	return res.StatusCode, decodeJSON(h.t, res.Body)
}

func (h *harness) getIncident(id string) map[string]any {
	h.t.Helper()
	res, err := http.Get(h.server.URL + "/api/v1/incidents/" + id)
	if err != nil {
		h.t.Fatalf("get incident: %v", err)
	}
	defer res.Body.Close()
	return decodeJSON(h.t, res.Body)
}

func (h *harness) getAudit(id string) []map[string]any {
	h.t.Helper()
	res, err := http.Get(h.server.URL + "/api/v1/incidents/" + id + "/audit")
	if err != nil {
		h.t.Fatalf("get audit: %v", err)
	}
	defer res.Body.Close()
	raw := decodeJSON(h.t, res.Body)
	items, _ := raw["audit"].([]any)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		m, _ := it.(map[string]any)
		out = append(out, m)
	}
	return out
}

func (h *harness) approveAPI(id, actor string) (int, map[string]any) {
	h.t.Helper()
	body := fmt.Sprintf(`{"actor":%q}`, actor)
	res, err := http.Post(h.server.URL+"/api/v1/incidents/"+id+"/approve", "application/json", strings.NewReader(body))
	if err != nil {
		h.t.Fatalf("approve: %v", err)
	}
	defer res.Body.Close()
	return res.StatusCode, decodeJSON(h.t, res.Body)
}

func (h *harness) slackApprove(id, actor string) int {
	h.t.Helper()
	return h.slackInteractive(fmt.Sprintf(`{"type":"block_actions","user":{"id":%q},"actions":[{"action_id":"approve_incident","block_id":"actions","type":"button","value":%q}]}`, actor, id), true)
}

func (h *harness) slackAsk(id, actor, question string, sign bool) int {
	h.t.Helper()
	payload := fmt.Sprintf(`{"type":"block_actions","user":{"id":%q},"state":{"values":{"followup_input":{"followup_question":{"type":"plain_text_input","value":%s}}}},"actions":[{"action_id":"ask_investigator","block_id":"followup_actions","type":"button","value":%q}]}`, actor, mustJSON(question), id)
	return h.slackInteractive(payload, sign)
}

func (h *harness) slackInteractive(payload string, sign bool) int {
	h.t.Helper()
	form := url.Values{"payload": {payload}}.Encode()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/webhooks/slack/interactive", strings.NewReader(form))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	if sign {
		req.Header.Set("X-Slack-Signature", slackSig(testSlackSecret, ts, []byte(form)))
	} else {
		req.Header.Set("X-Slack-Signature", "v0=deadbeef")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("slack interactive: %v", err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

func mustJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func firstIncidentID(t *testing.T, body map[string]any) string {
	t.Helper()
	ids, _ := body["incident_ids"].([]any)
	if len(ids) != 1 {
		t.Fatalf("incident_ids=%v", body)
	}
	id, _ := ids[0].(string)
	if id == "" {
		t.Fatalf("empty incident id in %v", body)
	}
	return id
}

func decodeJSON(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	return out
}

func slackSig(secret, ts string, body []byte) string {
	base := fmt.Sprintf("v0:%s:%s", ts, body)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(base))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func grafanaFiring(alertname, ns, name string) string {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(map[string]any{
		"receiver": "workaholic",
		"status":   "firing",
		"alerts": []map[string]any{{
			"status": "firing",
			"labels": map[string]string{
				"alertname":               alertname,
				"namespace":               ns,
				"horizontalpodautoscaler": name,
				"severity":                "warning",
			},
			"annotations": map[string]string{
				"summary":     alertname,
				"description": "e2e fixture",
			},
			"fingerprint": "fp-" + name,
		}},
	})
	return buf.String()
}

func hpaRunbook() domain.Runbook {
	return domain.Runbook{
		ID:          "hpa-maxed",
		Title:       "HPA at max",
		RiskLevel:   domain.RiskAutoRemediate,
		MatchLabels: map[string]string{"alertname": "KubeHPAReplicasAtMax"},
	}
}

func crashRunbook() domain.Runbook {
	return domain.Runbook{
		ID:          "crashloop",
		Title:       "CrashLoop",
		RiskLevel:   domain.RiskRequiresApproval,
		MatchLabels: map[string]string{"alertname": "KubePodCrashLooping"},
	}
}

func readyDeploy(ns, name string, replicas int32) *ports.DeploymentView {
	return &ports.DeploymentView{
		Namespace: ns, Name: name,
		Replicas: replicas, ReadyReplicas: replicas, UpdatedReplicas: replicas,
	}
}

func auditHas(recs []map[string]any, action string) bool {
	for _, r := range recs {
		if r["Action"] == action || r["action"] == action {
			return true
		}
	}
	return false
}

func TestE2E_GrafanaHPAAutoRemediateVerifiesThenResolves(t *testing.T) {
	plan := &domain.ActionPlan{
		Summary:    "raise HPA max",
		RiskLevel:  domain.RiskAutoRemediate,
		Rationale:  "current==max, pods ready",
		Confidence: 0.92,
		RunbookID:  "hpa-maxed",
		Steps: []domain.ActionStep{{
			Tool:      domain.ToolPatchHPAMaxReplicas,
			Arguments: map[string]any{"namespace": "prod", "name": "api", "max_replicas": 12.0},
			Reason:    "healthy and capped",
		}},
	}
	h := newHarness(t, plan, []domain.Runbook{hpaRunbook()})
	testkit.SeedHPAAtMax(h.k8s, "prod", "api", 10)

	code, body := h.postGrafana(grafanaFiring("KubeHPAReplicasAtMax", "prod", "api"))
	if code != http.StatusAccepted {
		t.Fatalf("status %d body=%v", code, body)
	}
	id := firstIncidentID(t, body)

	inc := h.getIncident(id)
	if inc["status"] != string(domain.StatusResolved) {
		t.Fatalf("want resolved, got %v", inc["status"])
	}
	if got := h.k8s.ToolsCalled(); len(got) != 1 || got[0] != domain.ToolPatchHPAMaxReplicas {
		t.Fatalf("remediator calls %v", got)
	}
	if len(h.msg.Auto) != 1 {
		t.Fatalf("expected auto-remediated Slack notify, got %v", h.msg.Auto)
	}
	if !auditHas(h.getAudit(id), "verified") {
		t.Fatalf("audit missing verified: %v", h.getAudit(id))
	}
}

func TestE2E_RestartRolloutRequiresApprovalThenSlackApproveAndVerify(t *testing.T) {
	plan := &domain.ActionPlan{
		Summary:    "restart api rollout",
		RiskLevel:  domain.RiskAutoRemediate,
		Rationale:  "crashloop",
		Confidence: 0.99,
		RunbookID:  "crashloop",
		Steps: []domain.ActionStep{{
			Tool:      domain.ToolRestartRollout,
			Arguments: map[string]any{"namespace": "prod", "name": "api"},
			Reason:    "pods crashlooping",
		}},
	}
	h := newHarness(t, plan, []domain.Runbook{crashRunbook()})
	h.k8s.Deployments["prod/api"] = readyDeploy("prod", "api", 2)

	code, body := h.postGrafana(grafanaFiring("KubePodCrashLooping", "prod", "api"))
	if code != http.StatusAccepted {
		t.Fatalf("status %d body=%v", code, body)
	}
	id := firstIncidentID(t, body)

	inc := h.getIncident(id)
	if inc["status"] != string(domain.StatusAwaitingApproval) {
		t.Fatalf("want awaiting_approval, got %v", inc["status"])
	}
	if calls := h.k8s.ToolsCalled(); len(calls) != 0 {
		t.Fatalf("remediator must not run before approval: %v", calls)
	}
	if len(h.msg.Approvals) != 1 {
		t.Fatalf("expected Slack approval request")
	}

	if st := h.slackApprove(id, testApprover); st != http.StatusOK {
		t.Fatalf("slack approve status %d", st)
	}
	inc = h.getIncident(id)
	if inc["status"] != string(domain.StatusResolved) {
		t.Fatalf("want resolved after slack approve, got %v", inc["status"])
	}
	if got := h.k8s.ToolsCalled(); len(got) != 1 || got[0] != domain.ToolRestartRollout {
		t.Fatalf("remediator calls %v", got)
	}
	if !auditHas(h.getAudit(id), "verified") {
		t.Fatalf("audit missing verified")
	}
}

func TestE2E_VerificationFailureDoesNotResolve(t *testing.T) {
	plan := &domain.ActionPlan{
		Summary:    "raise HPA max",
		RiskLevel:  domain.RiskAutoRemediate,
		Confidence: 0.95,
		RunbookID:  "hpa-maxed",
		Steps: []domain.ActionStep{{
			Tool:      domain.ToolPatchHPAMaxReplicas,
			Arguments: map[string]any{"namespace": "prod", "name": "api", "max_replicas": 12.0},
			Reason:    "at max",
		}},
	}
	h := newHarness(t, plan, []domain.Runbook{hpaRunbook()})
	testkit.SeedHPAAtMax(h.k8s, "prod", "api", 10)
	h.k8s.Deployments["prod/api"].ReadyReplicas = 0
	h.k8s.Deployments["prod/api"].Unavailable = 3

	_, body := h.postGrafana(grafanaFiring("KubeHPAReplicasAtMax", "prod", "api"))
	id := firstIncidentID(t, body)
	inc := h.getIncident(id)
	if inc["status"] != string(domain.StatusFailed) {
		t.Fatalf("want failed after bad verify, got %v", inc["status"])
	}
	if len(h.k8s.ToolsCalled()) != 1 {
		t.Fatalf("patch should still have run, calls=%v", h.k8s.ToolsCalled())
	}
	if !auditHas(h.getAudit(id), "verification_failed") {
		t.Fatalf("audit missing verification_failed: %v", h.getAudit(id))
	}
}

func TestE2E_ProtectedNamespaceRestartIsDenied(t *testing.T) {
	plan := &domain.ActionPlan{
		Summary:    "restart coredns",
		RiskLevel:  domain.RiskRequiresApproval,
		Confidence: 0.9,
		RunbookID:  "crashloop",
		Steps: []domain.ActionStep{{
			Tool:      domain.ToolRestartRollout,
			Arguments: map[string]any{"namespace": "kube-system", "name": "coredns"},
			Reason:    "crashloop",
		}},
	}
	h := newHarness(t, plan, []domain.Runbook{crashRunbook()})
	h.k8s.Deployments["kube-system/coredns"] = readyDeploy("kube-system", "coredns", 2)

	_, body := h.postGrafana(grafanaFiring("KubePodCrashLooping", "kube-system", "coredns"))
	id := firstIncidentID(t, body)
	_, _ = h.approveAPI(id, testApprover)
	inc := h.getIncident(id)
	if inc["status"] != string(domain.StatusFailed) {
		t.Fatalf("protected namespace must fail, got %v", inc["status"])
	}
	if len(h.k8s.ToolsCalled()) != 0 {
		t.Fatalf("must not record a successful write: %v", h.k8s.ToolsCalled())
	}
}

func TestE2E_NewRemediatorToolsRequireApprovalThenExecute(t *testing.T) {
	tests := []struct {
		tool string
		args map[string]any
		seed func(*testkit.FakeK8s)
	}{
		{
			tool: domain.ToolScaleDeployment,
			args: map[string]any{"namespace": "prod", "name": "api", "replicas": 4.0},
			seed: func(k *testkit.FakeK8s) { k.Deployments["prod/api"] = readyDeploy("prod", "api", 2) },
		},
		{
			tool: domain.ToolRollbackDeployment,
			args: map[string]any{"namespace": "prod", "name": "api"},
			seed: func(k *testkit.FakeK8s) { k.Deployments["prod/api"] = readyDeploy("prod", "api", 2) },
		},
		{
			tool: domain.ToolDeleteCrashLoopPod,
			args: map[string]any{"namespace": "prod", "name": "api-0"},
			seed: func(k *testkit.FakeK8s) {
				k.Deployments["prod/api"] = readyDeploy("prod", "api", 1)
				k.Pods["prod/api-0"] = &ports.PodView{
					Namespace: "prod", Name: "api-0", Phase: "Running", Ready: "0/1", Reason: "CrashLoopBackOff",
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			plan := &domain.ActionPlan{
				Summary:    tc.tool,
				RiskLevel:  domain.RiskAutoRemediate,
				Confidence: 0.99,
				RunbookID:  "crashloop",
				Steps:      []domain.ActionStep{{Tool: tc.tool, Arguments: tc.args, Reason: "e2e"}},
			}
			h := newHarness(t, plan, []domain.Runbook{crashRunbook()})
			tc.seed(h.k8s)

			_, body := h.postGrafana(grafanaFiring("KubePodCrashLooping", "prod", "api"))
			id := firstIncidentID(t, body)
			inc := h.getIncident(id)
			if inc["status"] != string(domain.StatusAwaitingApproval) {
				t.Fatalf("want awaiting_approval for %s, got %v", tc.tool, inc["status"])
			}
			if len(h.k8s.ToolsCalled()) != 0 {
				t.Fatalf("no writes before approval: %v", h.k8s.ToolsCalled())
			}
			st, _ := h.approveAPI(id, testApprover)
			if st != http.StatusOK {
				t.Fatalf("approve status %d", st)
			}
			inc = h.getIncident(id)
			if inc["status"] != string(domain.StatusResolved) {
				t.Fatalf("want resolved for %s, got %v", tc.tool, inc["status"])
			}
			if got := h.k8s.ToolsCalled(); len(got) != 1 || got[0] != tc.tool {
				t.Fatalf("remediator calls %v want %s", got, tc.tool)
			}
		})
	}
}

func TestE2E_SlackFollowUpInvestigatesWithoutRemediator(t *testing.T) {
	plan := &domain.ActionPlan{
		Summary:    "restart api rollout",
		RiskLevel:  domain.RiskRequiresApproval,
		Confidence: 0.9,
		RunbookID:  "crashloop",
		Steps: []domain.ActionStep{{
			Tool:      domain.ToolRestartRollout,
			Arguments: map[string]any{"namespace": "prod", "name": "api"},
			Reason:    "crashloop",
		}},
	}
	h := newHarness(t, plan, []domain.Runbook{crashRunbook()})
	h.k8s.Deployments["prod/api"] = readyDeploy("prod", "api", 2)

	_, body := h.postGrafana(grafanaFiring("KubePodCrashLooping", "prod", "api"))
	id := firstIncidentID(t, body)
	if h.llm.Calls != 1 {
		t.Fatalf("initial investigate calls=%d", h.llm.Calls)
	}

	h.llm.Plan = &domain.ActionPlan{
		Summary:    "3/3 pods Ready; crashloop was a stale replica",
		RiskLevel:  domain.RiskAutoRemediate,
		Confidence: 0.99,
		RunbookID:  "crashloop",
		Steps: []domain.ActionStep{{
			Tool:      domain.ToolRestartRollout,
			Arguments: map[string]any{"namespace": "prod", "name": "api"},
			Reason:    "model still wants a write",
		}},
	}

	if st := h.slackAsk(id, testApprover, "are the pods still crashlooping?", true); st != http.StatusOK {
		t.Fatalf("follow-up status %d", st)
	}
	inc := h.getIncident(id)
	if inc["status"] != string(domain.StatusAwaitingApproval) {
		t.Fatalf("follow-up must not execute the plan, status=%v", inc["status"])
	}
	if h.llm.Calls != 2 {
		t.Fatalf("follow-up should call investigator again, calls=%d", h.llm.Calls)
	}
	if !strings.Contains(h.llm.LastQuestion(), "are the pods still crashlooping?") {
		t.Fatalf("investigator did not see the question: %q", h.llm.LastQuestion())
	}
	if got := h.k8s.ToolsCalled(); len(got) != 0 {
		t.Fatalf("remediator must not run from Slack follow-up: %v", got)
	}
	if !auditHas(h.getAudit(id), "followup") {
		t.Fatalf("audit missing followup: %v", h.getAudit(id))
	}
	if len(h.msg.FollowUps) != 1 || h.msg.FollowUps[0].Answer == "" {
		t.Fatalf("expected Slack thread reply, got %+v", h.msg.FollowUps)
	}
}

func TestE2E_SlackFollowUpRejectsBadSignatureAndEmptyQuestion(t *testing.T) {
	plan := &domain.ActionPlan{
		Summary:    "restart",
		RiskLevel:  domain.RiskRequiresApproval,
		Confidence: 0.9,
		RunbookID:  "crashloop",
		Steps:      []domain.ActionStep{{Tool: domain.ToolRestartRollout, Arguments: map[string]any{"namespace": "prod", "name": "api"}}},
	}
	h := newHarness(t, plan, []domain.Runbook{crashRunbook()})
	h.k8s.Deployments["prod/api"] = readyDeploy("prod", "api", 2)
	_, body := h.postGrafana(grafanaFiring("KubePodCrashLooping", "prod", "api"))
	id := firstIncidentID(t, body)

	if st := h.slackAsk(id, testApprover, "what broke?", false); st != http.StatusUnauthorized {
		t.Fatalf("unsigned follow-up status %d", st)
	}
	if st := h.slackAsk(id, testApprover, "   ", true); st != http.StatusBadRequest {
		t.Fatalf("empty question status %d", st)
	}
	if h.llm.Calls != 1 {
		t.Fatalf("bad follow-ups must not call investigator, calls=%d", h.llm.Calls)
	}
	if st := h.slackAsk(id, "U-not-allowed", "what broke?", true); st != http.StatusForbidden {
		t.Fatalf("non-approver follow-up status %d", st)
	}
}
