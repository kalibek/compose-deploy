package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/compose-deploy/api/internal/auth"
	"github.com/compose-deploy/api/internal/crypto"
	"github.com/compose-deploy/api/internal/db"
	"github.com/compose-deploy/api/internal/github"
)

type fakeDeployer struct {
	mu       sync.Mutex
	shas     []string
	enqueued []int64
	stopped  []int64
	downs    []string
}

func (f *fakeDeployer) Enqueue(id int64, force bool) {
	f.mu.Lock()
	f.enqueued = append(f.enqueued, id)
	f.mu.Unlock()
}
func (f *fakeDeployer) EnqueueSHA(id int64, sha string) {
	f.mu.Lock()
	f.shas = append(f.shas, sha)
	f.mu.Unlock()
}
func (f *fakeDeployer) Stop(id int64) { f.mu.Lock(); f.stopped = append(f.stopped, id); f.mu.Unlock() }
func (f *fakeDeployer) Down(ctx context.Context, st *db.Stack) error {
	f.mu.Lock()
	f.downs = append(f.downs, st.Name)
	f.mu.Unlock()
	return nil
}

type env struct {
	ts     *httptest.Server
	client *http.Client
	db     *db.DB
	box    *crypto.Box
	dep    *fakeDeployer
	docker *fakeDocker
	wf     *fakeWorkflows
	srv    *Server
}

// fakeWorkflows returns a fixed result and records queried shas.
type fakeWorkflows struct {
	mu      sync.Mutex
	result  github.Result
	queried []string
}

func (f *fakeWorkflows) CommitState(ctx context.Context, repoURL, token, branch, sha, workflow string) (github.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queried = append(f.queried, sha)
	return f.result, nil
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	box, _ := crypto.New("secret")
	dep := &fakeDeployer{}
	static := fstest.MapFS{
		"index.html":    {Data: []byte("<html>spa</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	docker := &fakeDocker{}
	wf := &fakeWorkflows{result: github.Result{State: github.StateSuccess}}
	srv := New(Options{DB: d, Box: box, Auth: auth.New("pw", "secret", false), Deployer: dep, Runner: docker, Workflows: wf, DataDir: dir, Static: static})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return &env{ts: ts, client: &http.Client{Jar: jar}, db: d, box: box, dep: dep, docker: docker, wf: wf, srv: srv}
}

func (e *env) do(t *testing.T, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			r = strings.NewReader(b)
		default:
			j, _ := json.Marshal(b)
			r = bytes.NewReader(j)
		}
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, r)
	if method != http.MethodGet && method != http.MethodHead {
		// like the UI: always JSON on mutating requests (CSRF guard)
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func TestAPIFlow(t *testing.T) {
	e := newEnv(t)

	// health is public, stacks are not
	if resp, _ := e.do(t, "GET", "/api/health", nil); resp.StatusCode != 200 {
		t.Fatalf("health %d", resp.StatusCode)
	}
	if resp, body := e.do(t, "GET", "/api/stacks", nil); resp.StatusCode != 401 || !strings.Contains(string(body), `"error"`) {
		t.Fatalf("expected 401, got %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(t, "GET", "/api/auth/me", nil); resp.StatusCode != 401 {
		t.Fatalf("me unauthenticated %d", resp.StatusCode)
	}
	if resp, body := e.do(t, "POST", "/api/auth/login", `{"password":"bad"}`); resp.StatusCode != 401 || !strings.Contains(string(body), "invalid password") {
		t.Fatalf("bad login %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(t, "POST", "/api/auth/login", `{"password":"pw"}`); resp.StatusCode != 204 {
		t.Fatalf("login %d", resp.StatusCode)
	}
	if resp, body := e.do(t, "GET", "/api/auth/me", nil); resp.StatusCode != 200 || strings.TrimSpace(string(body)) != `{"authenticated":true}` {
		t.Fatalf("me %d %s", resp.StatusCode, body)
	}

	// empty list is [] not null
	resp, body := e.do(t, "GET", "/api/stacks", nil)
	if resp.StatusCode != 200 || strings.TrimSpace(string(body)) != "[]" || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("empty list: %d %q", resp.StatusCode, body)
	}

	// validation
	if resp, _ := e.do(t, "POST", "/api/stacks", map[string]any{"name": "Bad Name", "repo_url": "https://github.com/o/r"}); resp.StatusCode != 400 {
		t.Fatalf("expected 400 for bad name, got %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "POST", "/api/stacks", map[string]any{"name": "x", "repo_url": "https://github.com/o/r", "compose_path": "../x.yml"}); resp.StatusCode != 400 {
		t.Fatalf("expected 400 for traversal, got %d", resp.StatusCode)
	}

	// create
	resp, body = e.do(t, "POST", "/api/stacks", map[string]any{
		"name": "my-app", "repo_url": "https://github.com/o/r", "token": "ghp_TOPSECRET",
		"env": map[string]string{"DB_PASSWORD": "dbsecret", "PORT": "80"}, "auto_deploy": true,
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create %d %s", resp.StatusCode, body)
	}
	var created struct {
		Stack         map[string]any `json:"stack"`
		WebhookSecret string         `json:"webhook_secret"`
	}
	json.Unmarshal(body, &created)
	if len(created.WebhookSecret) != 64 {
		t.Fatalf("webhook secret %q", created.WebhookSecret)
	}
	st := created.Stack
	if st["branch"] != "main" || st["compose_path"] != "docker-compose.yml" || st["has_token"] != true ||
		st["status"] != "idle" || st["last_deploy_at"] != nil || st["last_sha"] != "" || st["webhook_url"] != "/api/hooks/github/1" {
		t.Fatalf("bad stack %v", st)
	}
	if resp, _ := e.do(t, "POST", "/api/stacks", map[string]any{"name": "my-app", "repo_url": "https://github.com/o/r"}); resp.StatusCode != 409 {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}

	// get: secrets never in response
	resp, body = e.do(t, "GET", "/api/stacks/1", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get %d", resp.StatusCode)
	}
	for _, secret := range []string{"ghp_TOPSECRET", "dbsecret", created.WebhookSecret, "token_enc", "\"token\"", "last_log"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("response leaks %q: %s", secret, body)
		}
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	wantKeys := []string{"id", "name", "repo_url", "branch", "compose_path", "auto_deploy", "has_token", "env_keys",
		"webhook_url", "status", "last_sha", "last_deploy_at", "last_error", "deploy_trigger", "workflow_name",
		"waiting_sha", "waiting_reason", "created_at", "updated_at"}
	if len(got) != len(wantKeys) {
		t.Fatalf("unexpected field set: %v", got)
	}
	for _, k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Fatalf("missing field %s", k)
		}
	}
	if keys, _ := json.Marshal(got["env_keys"]); string(keys) != `["DB_PASSWORD","PORT"]` {
		t.Fatalf("env_keys %s", keys)
	}
	if resp, _ := e.do(t, "GET", "/api/stacks/99", nil); resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}

	// PUT env with null keep semantics: keep DB_PASSWORD, drop PORT, add NEW
	resp, body = e.do(t, "PUT", "/api/stacks/1", `{"env":{"DB_PASSWORD":null,"NEW":"v"},"branch":"dev","name":"ignored"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("put %d %s", resp.StatusCode, body)
	}
	json.Unmarshal(body, &got)
	if keys, _ := json.Marshal(got["env_keys"]); string(keys) != `["DB_PASSWORD","NEW"]` || got["branch"] != "dev" || got["name"] != "my-app" || got["has_token"] != true {
		t.Fatalf("after put: %s", body)
	}
	stored, _ := e.db.GetStack(context.Background(), 1)
	raw, _ := e.box.Decrypt(stored.EnvEnc)
	var envMap map[string]string
	json.Unmarshal(raw, &envMap)
	if envMap["DB_PASSWORD"] != "dbsecret" || envMap["NEW"] != "v" || len(envMap) != 2 {
		t.Fatalf("stored env %v", envMap)
	}
	tok, _ := e.box.DecryptString(stored.TokenEnc)
	if tok != "ghp_TOPSECRET" {
		t.Fatal("token should be kept when omitted")
	}

	// PUT without env keeps env; empty token keeps token; clear_token removes it
	resp, body = e.do(t, "PUT", "/api/stacks/1", `{"token":"","auto_deploy":false}`)
	json.Unmarshal(body, &got)
	if keys, _ := json.Marshal(got["env_keys"]); string(keys) != `["DB_PASSWORD","NEW"]` || got["has_token"] != true || got["auto_deploy"] != false {
		t.Fatalf("after put 2: %s", body)
	}
	resp, body = e.do(t, "PUT", "/api/stacks/1", `{"clear_token":true,"env":{}}`)
	json.Unmarshal(body, &got)
	if keys, _ := json.Marshal(got["env_keys"]); string(keys) != `[]` || got["has_token"] != false {
		t.Fatalf("after put 3: %s", body)
	}
	if resp, _ := e.do(t, "PUT", "/api/stacks/1", `{"env":{"BAD-KEY":"x"}}`); resp.StatusCode != 400 {
		t.Fatalf("expected 400 for bad env key, got %d", resp.StatusCode)
	}

	// deploy / stop / status
	if resp, body := e.do(t, "POST", "/api/stacks/1/deploy", nil); resp.StatusCode != 202 || strings.TrimSpace(string(body)) != `{"status":"queued"}` {
		t.Fatalf("deploy %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(t, "POST", "/api/stacks/1/stop", nil); resp.StatusCode != 202 {
		t.Fatalf("stop %d", resp.StatusCode)
	}
	resp, body = e.do(t, "GET", "/api/stacks/1/status", nil)
	var status map[string]any
	json.Unmarshal(body, &status)
	for _, k := range []string{"status", "last_sha", "last_deploy_at", "last_log", "last_error"} {
		if _, ok := status[k]; !ok {
			t.Fatalf("status missing %s: %s", k, body)
		}
	}

	// rotate webhook secret
	resp, body = e.do(t, "POST", "/api/stacks/1/webhook-secret", nil)
	var rot map[string]string
	json.Unmarshal(body, &rot)
	if resp.StatusCode != 200 || len(rot["webhook_secret"]) != 64 || rot["webhook_secret"] == created.WebhookSecret {
		t.Fatalf("rotate %d %s", resp.StatusCode, body)
	}

	// registries
	resp, body = e.do(t, "GET", "/api/registries", nil)
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("registries empty %s", body)
	}
	resp, body = e.do(t, "POST", "/api/registries", `{"url":"ghcr.io","username":"bob","password":"regpw"}`)
	if resp.StatusCode != 201 || strings.Contains(string(body), "regpw") || !strings.Contains(string(body), `"has_password":true`) {
		t.Fatalf("create registry %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(t, "POST", "/api/registries", `{"url":"ghcr.io"}`); resp.StatusCode != 409 {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "POST", "/api/registries", `{"url":""}`); resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	resp, body = e.do(t, "PUT", "/api/registries/1", `{"url":"ghcr.io","username":"alice","password":""}`)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"username":"alice"`) || !strings.Contains(string(body), `"has_password":true`) {
		t.Fatalf("update registry %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(t, "DELETE", "/api/registries/1", nil); resp.StatusCode != 204 {
		t.Fatalf("delete registry %d", resp.StatusCode)
	}

	// unknown api path → JSON 404; SPA fallback for others
	resp, body = e.do(t, "GET", "/api/nope", nil)
	if resp.StatusCode != 404 || !strings.Contains(string(body), `"error"`) {
		t.Fatalf("api 404: %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(t, "GET", "/stacks/1", nil)
	if resp.StatusCode != 200 || string(body) != "<html>spa</html>" {
		t.Fatalf("spa fallback %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(t, "GET", "/assets/app.js", nil)
	if resp.StatusCode != 200 || string(body) != "console.log(1)" {
		t.Fatalf("static %d %s", resp.StatusCode, body)
	}

	// delete with down
	if resp, _ := e.do(t, "DELETE", "/api/stacks/1?down=true", nil); resp.StatusCode != 204 {
		t.Fatalf("delete %d", resp.StatusCode)
	}
	if len(e.dep.downs) != 1 {
		t.Fatal("down not called")
	}
	if resp, _ := e.do(t, "GET", "/api/stacks/1", nil); resp.StatusCode != 404 {
		t.Fatal("stack not deleted")
	}

	// logout
	if resp, _ := e.do(t, "POST", "/api/auth/logout", nil); resp.StatusCode != 204 {
		t.Fatalf("logout %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "GET", "/api/stacks", nil); resp.StatusCode != 401 {
		t.Fatalf("after logout %d", resp.StatusCode)
	}
}

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	good := sign("s3cret", body)
	if !VerifySignature([]byte("s3cret"), body, good) {
		t.Fatal("valid signature rejected")
	}
	for _, bad := range []string{"", "sha256=", "sha1=" + good[7:], good[:len(good)-2] + "00", "sha256=zz", sign("other", body)} {
		if VerifySignature([]byte("s3cret"), body, bad) {
			t.Fatalf("bad signature %q accepted", bad)
		}
	}
	if VerifySignature([]byte("s3cret"), append(body, ' '), good) {
		t.Fatal("tampered body accepted")
	}
	if VerifySignature(nil, body, sign("", body)) {
		t.Fatal("empty secret accepted")
	}
}

func TestWebhook(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", "/api/auth/login", `{"password":"pw"}`)
	_, body := e.do(t, "POST", "/api/stacks", map[string]any{"name": "app", "repo_url": "https://github.com/o/r"})
	var created struct {
		WebhookSecret string `json:"webhook_secret"`
	}
	json.Unmarshal(body, &created)

	hook := func(event string, payload string, sig string) (int, string) {
		req, _ := http.NewRequest("POST", e.ts.URL+"/api/hooks/github/1", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("X-Hub-Signature-256", sig)
		resp, err := http.DefaultClient.Do(req) // no cookies: public endpoint
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b))
	}
	p := `{"ref":"refs/heads/main"}`
	if code, _ := hook("push", p, sign("wrong", []byte(p))); code != 401 {
		t.Fatalf("bad sig: %d", code)
	}
	if code, b := hook("ping", `{}`, sign(created.WebhookSecret, []byte(`{}`))); code != 200 || b != `{"status":"pong"}` {
		t.Fatalf("ping %d %s", code, b)
	}
	if code, b := hook("push", p, sign(created.WebhookSecret, []byte(p))); code != 202 || b != `{"status":"queued"}` {
		t.Fatalf("push %d %s", code, b)
	}
	other := `{"ref":"refs/heads/dev"}`
	if code, b := hook("push", other, sign(created.WebhookSecret, []byte(other))); code != 200 || b != `{"status":"ignored"}` {
		t.Fatalf("other branch %d %s", code, b)
	}
	if len(e.dep.enqueued) != 1 {
		t.Fatalf("enqueued %v", e.dep.enqueued)
	}
}

func TestCSRFGuardRequiresJSON(t *testing.T) {
	e := newEnv(t)
	post := func(path, ct, body string) int {
		req, _ := http.NewRequest("POST", e.ts.URL+path, strings.NewReader(body))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		resp, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// login with a form-ish content type is rejected
	if code := post("/api/auth/login", "text/plain", `{"password":"pw"}`); code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain login: %d", code)
	}
	if code := post("/api/auth/login", "application/json; charset=utf-8", `{"password":"pw"}`); code != http.StatusNoContent {
		t.Fatalf("json login: %d", code)
	}
	if code := post("/api/stacks", "application/json", `{"name":"app","repo_url":"https://github.com/o/r"}`); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	// body-less action without Content-Type is rejected even with a valid session
	if code := post("/api/stacks/1/deploy", "", ""); code != http.StatusUnsupportedMediaType {
		t.Fatalf("deploy without content-type: %d", code)
	}
	if code := post("/api/stacks/1/deploy", "application/x-www-form-urlencoded", "a=b"); code != http.StatusUnsupportedMediaType {
		t.Fatalf("deploy form: %d", code)
	}
	if len(e.dep.enqueued) != 0 {
		t.Fatalf("deploy should not have been enqueued: %v", e.dep.enqueued)
	}
	if code := post("/api/stacks/1/deploy", "application/json", ""); code != http.StatusAccepted {
		t.Fatalf("deploy json: %d", code)
	}
	// GETs are unaffected
	if resp, _ := e.do(t, "GET", "/api/stacks", nil); resp.StatusCode != 200 {
		t.Fatalf("list: %d", resp.StatusCode)
	}
}
