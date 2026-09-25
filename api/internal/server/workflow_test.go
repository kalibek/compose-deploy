package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/compose-deploy/api/internal/github"
)

const headSHA = "0123456789abcdef0123456789abcdef01234567"

func TestDeployTriggerValidation(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", "/api/auth/login", `{"password":"pw"}`)
	if resp, body := e.do(t, "POST", "/api/stacks", map[string]any{"name": "gl", "repo_url": "https://gitlab.com/o/r", "deploy_trigger": "workflow"}); resp.StatusCode != 400 {
		t.Fatalf("non-github workflow stack: %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(t, "POST", "/api/stacks", map[string]any{"name": "x", "repo_url": "https://github.com/o/r", "deploy_trigger": "cron"}); resp.StatusCode != 400 {
		t.Fatalf("bad trigger: %d", resp.StatusCode)
	}
	resp, body := e.do(t, "POST", "/api/stacks", map[string]any{"name": "wf", "repo_url": "https://github.com/o/r", "deploy_trigger": "workflow", "workflow_name": " build.yml "})
	if resp.StatusCode != 201 {
		t.Fatalf("create %d %s", resp.StatusCode, body)
	}
	var created struct{ Stack map[string]any }
	json.Unmarshal(body, &created)
	if created.Stack["deploy_trigger"] != "workflow" || created.Stack["workflow_name"] != "build.yml" || created.Stack["waiting_sha"] != "" {
		t.Fatalf("stack = %v", created.Stack)
	}
	// switching back to push drops the workflow name; default for new stacks is push
	resp, body = e.do(t, "PUT", "/api/stacks/1", map[string]any{"deploy_trigger": "push"})
	var st map[string]any
	json.Unmarshal(body, &st)
	if resp.StatusCode != 200 || st["deploy_trigger"] != "push" || st["workflow_name"] != "" {
		t.Fatalf("update %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(t, "PUT", "/api/stacks/1", map[string]any{"deploy_trigger": "workflow", "repo_url": "https://example.com/o/r"}); resp.StatusCode != 400 {
		t.Fatalf("workflow + non-github repo on update: %d", resp.StatusCode)
	}
}

func TestWorkflowRunWebhook(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", "/api/auth/login", `{"password":"pw"}`)
	_, body := e.do(t, "POST", "/api/stacks", map[string]any{"name": "app", "repo_url": "https://github.com/o/r", "deploy_trigger": "workflow", "workflow_name": "build"})
	var created struct {
		WebhookSecret string `json:"webhook_secret"`
	}
	json.Unmarshal(body, &created)

	hook := func(event, payload string) (int, string) {
		req, _ := http.NewRequest("POST", e.ts.URL+"/api/hooks/github/1", strings.NewReader(payload))
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("X-Hub-Signature-256", sign(created.WebhookSecret, []byte(payload)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b))
	}
	run := func(name, branch, event, conclusion string) string {
		return fmt.Sprintf(`{"action":"completed","workflow_run":{"name":%q,"path":".github/workflows/%s.yml","event":%q,"head_branch":%q,"head_sha":%q,"status":"completed","conclusion":%q}}`,
			name, name, event, branch, headSHA, conclusion)
	}

	// plain pushes don't deploy a workflow-triggered stack
	if code, b := hook("push", `{"ref":"refs/heads/main"}`); code != 200 || !strings.Contains(b, "ignored") {
		t.Fatalf("push: %d %s", code, b)
	}
	for _, c := range []struct{ name, payload string }{
		{"other workflow", run("lint", "main", "push", "success")},
		{"other branch", run("build", "dev", "push", "success")},
		{"pull request run", run("build", "main", "pull_request", "success")},
		{"in progress", strings.Replace(run("build", "main", "push", "success"), `"completed"`, `"requested"`, 1)},
	} {
		if code, b := hook("workflow_run", c.payload); code != 200 || !strings.Contains(b, "ignored") {
			t.Fatalf("%s: %d %s", c.name, code, b)
		}
	}
	// failed run: not deployed, stack shows why
	if code, b := hook("workflow_run", run("build", "main", "push", "failure")); code != 200 || !strings.Contains(b, "workflow failed") {
		t.Fatalf("failure: %d %s", code, b)
	}
	_, body = e.do(t, "GET", "/api/stacks/1", nil)
	var st map[string]any
	json.Unmarshal(body, &st)
	if st["waiting_sha"] != headSHA || !strings.Contains(st["waiting_reason"].(string), "failure") {
		t.Fatalf("waiting state: %v", st)
	}
	if len(e.dep.shas) != 0 || len(e.dep.enqueued) != 0 {
		t.Fatalf("nothing should be deployed yet: %v %v", e.dep.shas, e.dep.enqueued)
	}
	// named workflow succeeded: deploy that exact commit, no API call needed
	if code, b := hook("workflow_run", run("build", "main", "push", "success")); code != 202 || b != `{"status":"queued"}` {
		t.Fatalf("success: %d %s", code, b)
	}
	e.dep.mu.Lock()
	shas := append([]string{}, e.dep.shas...)
	e.dep.mu.Unlock()
	if len(shas) != 1 || shas[0] != headSHA || len(e.wf.queried) != 0 {
		t.Fatalf("deploys %v, api queries %v", shas, e.wf.queried)
	}

	// without a workflow name, the API decides whether all runs are done
	e.do(t, "PUT", "/api/stacks/1", map[string]any{"workflow_name": ""})
	e.wf.mu.Lock()
	e.wf.result = github.Result{State: github.StatePending, Detail: "waiting for lint (in progress)"}
	e.wf.mu.Unlock()
	if code, b := hook("workflow_run", run("build", "main", "push", "success")); code != 202 || b != `{"status":"checking"}` {
		t.Fatalf("checking: %d %s", code, b)
	}
	waitFor(t, func() bool {
		_, body := e.do(t, "GET", "/api/stacks/1", nil)
		return strings.Contains(string(body), "waiting for lint")
	})
	e.wf.mu.Lock()
	e.wf.result = github.Result{State: github.StateSuccess}
	e.wf.mu.Unlock()
	hook("workflow_run", run("lint", "main", "push", "success"))
	waitFor(t, func() bool {
		e.dep.mu.Lock()
		defer e.dep.mu.Unlock()
		return len(e.dep.shas) == 2
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
