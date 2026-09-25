package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/compose-deploy/api/internal/deployer"
)

const composeV1 = "services:\n  web:\n    image: nginx:1.27\n"

func TestInlineStack(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", "/api/auth/login", `{"password":"pw"}`)

	if resp, _ := e.do(t, "POST", "/api/stacks", map[string]any{"name": "x", "source": "inline", "compose_content": "  "}); resp.StatusCode != 400 {
		t.Fatalf("empty content: %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "POST", "/api/stacks", map[string]any{"name": "x", "source": "ftp"}); resp.StatusCode != 400 {
		t.Fatalf("bad source: %d", resp.StatusCode)
	}

	// create: repo fields are ignored, auto-deploy deploys right away
	resp, body := e.do(t, "POST", "/api/stacks", map[string]any{
		"name": "web", "source": "inline", "compose_content": composeV1, "repo_url": "https://github.com/o/r",
		"deploy_trigger": "workflow", "env": map[string]string{"A": "1"}, "auto_deploy": true,
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create %d %s", resp.StatusCode, body)
	}
	var created struct{ Stack map[string]any }
	json.Unmarshal(body, &created)
	st := created.Stack
	if st["source"] != "inline" || st["repo_url"] != "" || st["deploy_trigger"] != "push" ||
		st["compose_content"] != composeV1 || st["content_sha"] != deployer.ContentSHA(composeV1) {
		t.Fatalf("stack = %v", st)
	}
	if len(e.dep.enqueued) != 1 {
		t.Fatalf("expected immediate deploy, got %v", e.dep.enqueued)
	}

	// git-only settings are rejected; unchanged content doesn't redeploy; changed content does
	if resp, _ := e.do(t, "PUT", "/api/stacks/1", map[string]any{"repo_url": "https://github.com/o/r"}); resp.StatusCode != 400 {
		t.Fatalf("repo_url on inline: %d", resp.StatusCode)
	}
	e.do(t, "PUT", "/api/stacks/1", map[string]any{"compose_content": composeV1})
	if len(e.dep.enqueued) != 1 {
		t.Fatalf("unchanged content redeployed: %v", e.dep.enqueued)
	}
	v2 := strings.Replace(composeV1, "1.27", "1.28", 1)
	resp, body = e.do(t, "PUT", "/api/stacks/1", map[string]any{"compose_content": v2})
	if resp.StatusCode != 200 || !strings.Contains(string(body), "1.28") || len(e.dep.enqueued) != 2 {
		t.Fatalf("update %d %s, deploys %v", resp.StatusCode, body, e.dep.enqueued)
	}
	// auto-deploy off: saving doesn't deploy
	e.do(t, "PUT", "/api/stacks/1", map[string]any{"auto_deploy": false, "compose_content": composeV1})
	if len(e.dep.enqueued) != 2 {
		t.Fatalf("deployed with auto-deploy off: %v", e.dep.enqueued)
	}

	// compose_content is rejected for git stacks; webhook ignores inline stacks
	e.do(t, "POST", "/api/stacks", map[string]any{"name": "g", "repo_url": "https://github.com/o/r"})
	if resp, _ := e.do(t, "PUT", "/api/stacks/2", map[string]any{"compose_content": composeV1}); resp.StatusCode != 400 {
		t.Fatalf("compose_content on git stack: %d", resp.StatusCode)
	}
}

func TestValidateCompose(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", "/api/auth/login", `{"password":"pw"}`)
	e.do(t, "POST", "/api/stacks", map[string]any{"name": "web", "source": "inline", "compose_content": composeV1,
		"env": map[string]string{"A": "stored"}, "auto_deploy": false})

	check := func(body map[string]any, want string) {
		t.Helper()
		resp, b := e.do(t, "POST", "/api/compose/validate", body)
		if resp.StatusCode != 200 || strings.TrimSpace(string(b)) != want {
			t.Fatalf("validate %v: %d %s", body, resp.StatusCode, b)
		}
	}
	check(map[string]any{"content": composeV1}, `{"output":"env=","valid":true}`)
	check(map[string]any{"content": composeV1, "stack_id": 1}, `{"output":"env=stored","valid":true}`)
	check(map[string]any{"content": composeV1, "stack_id": 1, "env": map[string]any{"A": nil}}, `{"output":"env=stored","valid":true}`)
	check(map[string]any{"content": composeV1, "stack_id": 1, "env": map[string]any{"A": "draft"}}, `{"output":"env=draft","valid":true}`)
	check(map[string]any{"content": "bad"}, `{"output":"yaml: line 1: bad","valid":false}`)
	if resp, _ := e.do(t, "POST", "/api/compose/validate", map[string]any{"content": ""}); resp.StatusCode != 400 {
		t.Fatalf("empty: %d", resp.StatusCode)
	}
}
