package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func TestOwnerRepo(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/o/r":      "o/r",
		"https://github.com/o/r.git":  "o/r",
		"https://GitHub.com/o/r/":     "o/r",
		"https://gitlab.com/o/r":      "",
		"https://github.com/o":        "",
		"https://github.com/o/r/tree": "",
	} {
		o, r, err := OwnerRepo(in)
		got := ""
		if err == nil {
			got = o + "/" + r
		}
		if got != want {
			t.Errorf("OwnerRepo(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestAggregate(t *testing.T) {
	build := func(num int64, status, concl string) run {
		return run{ID: num, WorkflowID: 1, Name: "build", Path: ".github/workflows/build.yml", Event: "push", Status: status, Conclusion: concl, RunNumber: num, HeadBranch: "main"}
	}
	lint := run{ID: 50, WorkflowID: 2, Name: "lint", Path: ".github/workflows/lint.yml", Event: "push", Status: "completed", Conclusion: "failure", RunNumber: 3, HeadBranch: "main"}
	pr := run{ID: 60, WorkflowID: 1, Name: "build", Path: ".github/workflows/build.yml", Event: "pull_request", Status: "in_progress", RunNumber: 99, HeadBranch: "main"}

	cases := []struct {
		name     string
		runs     []run
		workflow string
		want     string
	}{
		{"none", nil, "", StatePending},
		{"in progress", []run{build(1, "in_progress", "")}, "", StatePending},
		{"success", []run{build(1, "completed", "success")}, "", StateSuccess},
		{"rerun after failure wins", []run{build(1, "completed", "failure"), build(2, "completed", "success")}, "", StateSuccess},
		{"other workflow failed, all", []run{build(1, "completed", "success"), lint}, "", StateFailed},
		{"other workflow failed, filtered by name", []run{build(1, "completed", "success"), lint}, "build", StateSuccess},
		{"filtered by file", []run{build(1, "completed", "success"), lint}, "build.yml", StateSuccess},
		{"pull request runs ignored", []run{build(1, "completed", "success"), pr}, "", StateSuccess},
		{"filter matches nothing", []run{lint}, "build", StatePending},
		{"cancelled is failure", []run{build(1, "completed", "cancelled")}, "", StateFailed},
	}
	for _, c := range cases {
		if got := aggregate(c.runs, "main", sha, c.workflow); got.State != c.want {
			t.Errorf("%s: got %+v, want %s", c.name, got, c.want)
		}
	}
}

func TestCommitStateHTTP(t *testing.T) {
	var gotAuth, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.String()
		if strings.Contains(r.URL.Path, "/missing/") {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"workflow_runs":[{"id":1,"workflow_id":7,"name":"build","path":".github/workflows/build.yml","event":"push","status":"completed","conclusion":"success","run_number":4,"head_branch":"main"}]}`))
	}))
	defer ts.Close()
	c := &Client{HTTP: ts.Client(), BaseURL: ts.URL}

	res, err := c.CommitState(context.Background(), "https://github.com/acme/app.git", "tok", "main", sha, "")
	if err != nil || res.State != StateSuccess {
		t.Fatalf("got %+v %v", res, err)
	}
	if gotAuth != "Bearer tok" || gotPath != "/repos/acme/app/actions/runs?head_sha="+sha+"&per_page=100" {
		t.Fatalf("auth %q path %q", gotAuth, gotPath)
	}
	if _, err := c.CommitState(context.Background(), "https://github.com/missing/x", "", "main", sha, ""); err == nil || !strings.Contains(err.Error(), "Actions: Read-only") {
		t.Fatalf("expected permission hint, got %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("no token must mean no Authorization header, got %q", gotAuth)
	}
}
