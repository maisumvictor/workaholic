package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
	"github.com/maisumvictor/Workaholic/internal/testkit"
)

func TestInvestigatorK8sDefsIncludeReplicaSetRevisionsNotRemediator(t *testing.T) {
	names := map[string]bool{}
	for _, d := range InvestigatorK8sDefs() {
		if d.Function != nil {
			names[d.Function.Name] = true
		}
	}
	if !names[ToolListReplicaSets] {
		t.Fatalf("missing %s in %+v", ToolListReplicaSets, names)
	}
	if names[ToolPatchHPA] {
		t.Fatal("remediator patch_hpa must not be an investigator tool")
	}
}

func TestK8sExecutorListReplicaSetsIsReadOnly(t *testing.T) {
	k := testkit.NewFakeK8s()
	testkit.SeedReplicaSets(k, "prod", "api", []ports.ReplicaSetView{{
		Namespace: "prod", Name: "api-bbb", Deployment: "api", Revision: "12", Images: []string{"api:new"},
	}})
	e := &K8sExecutor{Inv: k}
	out, err := e.Call(context.Background(), ToolListReplicaSets, `{"namespace":"prod","deployment":"api"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "api:new") || !strings.Contains(out, `"revision":"12"`) {
		t.Fatalf("tool output %s", out)
	}
	if got := k.ToolsCalled(); len(got) != 0 {
		t.Fatalf("list replicasets must not record remediator writes: %v", got)
	}
}

func TestChangeExecutorGitHubAndArgo(t *testing.T) {
	gh := &testkit.FakeGitHub{View: &ports.GitHubCompareView{Owner: "acme", Repo: "api", Status: "ahead", AheadBy: 1, HTMLURL: "https://github.com/acme/api/compare/a...b"}}
	argo := &testkit.FakeArgo{View: &ports.ArgoApplicationView{Name: "api", SyncStatus: "Synced", HealthStatus: "Healthy"}}
	e := &ChangeExecutor{GitHub: gh, Argo: argo}
	out, err := e.Call(context.Background(), ToolGitHubCompare, `{"owner":"acme","repo":"api","base":"a","head":"b"}`)
	if err != nil || !strings.Contains(out, "ahead") {
		t.Fatalf("github %s %v", out, err)
	}
	out, err = e.Call(context.Background(), ToolGetArgoApplication, `{"namespace":"argocd","name":"api"}`)
	if err != nil || !strings.Contains(out, "Healthy") {
		t.Fatalf("argo %s %v", out, err)
	}
	if _, err := e.Call(context.Background(), domain.ToolRestartRollout, `{}`); err == nil {
		t.Fatal("change executor must not run remediator tools")
	}
}
