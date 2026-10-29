package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/compose-deploy/api/internal/db"
)

func TestDeploymentsAPI(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	ctx := context.Background()
	st := &db.Stack{Name: "app", RepoURL: "https://github.com/o/r", Branch: "main", ComposePath: "docker-compose.yml"}
	other := &db.Stack{Name: "other", RepoURL: "https://github.com/o/r", Branch: "main", ComposePath: "docker-compose.yml"}
	for _, s := range []*db.Stack{st, other} {
		if err := e.db.CreateStack(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	g1 := &db.Deployment{StackID: st.ID, Action: db.ActionDeploy, TriggeredBy: db.ByWebhook, TriggerDetail: "push to main"}
	e.db.StartDeployment(ctx, g1)
	sha := "abc123"
	e.db.FinishDeployment(ctx, g1.ID, db.FinishParams{Status: db.StatusSuccess, SHA: &sha, Log: "deployed ok"})
	g2 := &db.Deployment{StackID: st.ID, Action: db.ActionStop, TriggeredBy: db.ByManual}
	e.db.StartDeployment(ctx, g2)

	base := fmt.Sprintf("/api/stacks/%d", st.ID)
	resp, body := e.do(t, "GET", base+"/deployments", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list %d %s", resp.StatusCode, body)
	}
	var list []map[string]any
	json.Unmarshal(body, &list)
	if len(list) != 2 || list[0]["action"] != "stop" || list[0]["status"] != "deploying" || list[0]["finished_at"] != nil {
		t.Fatalf("list %s", body)
	}
	if list[1]["triggered_by"] != "webhook" || list[1]["trigger_detail"] != "push to main" || list[1]["sha"] != "abc123" ||
		list[1]["status"] != "success" || list[1]["finished_at"] == nil {
		t.Fatalf("list %s", body)
	}
	if strings.Contains(string(body), `"log"`) {
		t.Fatalf("list must not include logs: %s", body)
	}

	resp, body = e.do(t, "GET", fmt.Sprintf("%s/deployments/%d", base, g1.ID), nil)
	var one map[string]any
	json.Unmarshal(body, &one)
	if resp.StatusCode != 200 || one["log"] != "deployed ok" {
		t.Fatalf("get %d %s", resp.StatusCode, body)
	}
	// deployments are scoped to their stack
	for _, path := range []string{
		fmt.Sprintf("/api/stacks/%d/deployments/%d", other.ID, g1.ID), base + "/deployments/99", base + "/deployments/x", "/api/stacks/99/deployments",
	} {
		if resp, _ := e.do(t, "GET", path, nil); resp.StatusCode != 404 {
			t.Fatalf("%s: expected 404, got %d", path, resp.StatusCode)
		}
	}

	// manual deploys are attributed to the user
	before := len(e.dep.causes)
	if resp, _ := e.do(t, "POST", base+"/deploy", nil); resp.StatusCode != 202 {
		t.Fatalf("deploy %d", resp.StatusCode)
	}
	if c := e.dep.causes; len(c) != before+1 || c[before].By != db.ByManual {
		t.Fatalf("causes %+v", c)
	}
}
