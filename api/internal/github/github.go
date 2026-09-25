// Package github asks the GitHub REST API whether a commit's GitHub Actions
// workflow runs have finished successfully.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Workflow run outcome for a commit.
const (
	StateSuccess = "success" // every relevant workflow run succeeded
	StatePending = "pending" // no run yet, or a run is queued / in progress
	StateFailed  = "failed"  // a relevant run finished unsuccessfully
)

// Result is the aggregated workflow state of a commit.
type Result struct {
	State  string
	Detail string // human readable, shown in the UI
}

// Client talks to the GitHub API (api.github.com unless BaseURL is set).
type Client struct {
	HTTP    *http.Client
	BaseURL string
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, BaseURL: "https://api.github.com"}
}

// OwnerRepo extracts "owner", "repo" from https://github.com/owner/repo(.git).
func OwnerRepo(repoURL string) (string, string, error) {
	u, err := url.Parse(repoURL)
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", "", errors.New("workflow trigger requires a https://github.com/<owner>/<repo> repository")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("repository URL must look like https://github.com/<owner>/<repo>")
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}

// MatchWorkflow reports whether a run of workflow `name` / file `wfPath`
// matches the configured filter ("" matches everything). The filter may be the
// workflow's name ("build") or its file ("build.yml").
func MatchWorkflow(filter, name, wfPath string) bool {
	if filter == "" {
		return true
	}
	return strings.EqualFold(filter, name) || strings.EqualFold(filter, path.Base(wfPath))
}

// IgnoredEvent reports runs that don't build the branch itself (pull request
// runs report the PR head branch, which may share the branch's name).
func IgnoredEvent(event string) bool {
	return strings.HasPrefix(event, "pull_request")
}

type run struct {
	ID         int64  `json:"id"`
	WorkflowID int64  `json:"workflow_id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	RunNumber  int64  `json:"run_number"`
	HeadBranch string `json:"head_branch"`
}

// CommitState aggregates the latest run of each (matching) workflow for sha.
func (c *Client) CommitState(ctx context.Context, repoURL, token, branch, sha, workflow string) (Result, error) {
	owner, repo, err := OwnerRepo(repoURL)
	if err != nil {
		return Result{}, err
	}
	q := url.Values{"head_sha": {sha}, "per_page": {"100"}}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/actions/runs?%s", c.BaseURL, url.PathEscape(owner), url.PathEscape(repo), q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Result{}, errors.New("github api: repository not found — the token needs the \"Actions: Read-only\" permission")
	case resp.StatusCode == http.StatusUnauthorized:
		return Result{}, errors.New("github api: token rejected (expired or revoked?)")
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return Result{}, errors.New("github api: rate limit exceeded (set a token on the stack for a higher limit)")
		}
		return Result{}, errors.New("github api: access denied — the token needs the \"Actions: Read-only\" permission")
	case resp.StatusCode != http.StatusOK:
		return Result{}, fmt.Errorf("github api: unexpected status %s", resp.Status)
	}
	var body struct {
		WorkflowRuns []run `json:"workflow_runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Result{}, fmt.Errorf("github api: decode: %w", err)
	}
	return aggregate(body.WorkflowRuns, branch, sha, workflow), nil
}

func aggregate(runs []run, branch, sha, workflow string) Result {
	// Latest run per workflow (a commit can be built more than once, e.g. push + manual re-run).
	latest := map[int64]run{}
	var order []int64
	for _, r := range runs {
		if IgnoredEvent(r.Event) || (r.HeadBranch != "" && r.HeadBranch != branch) || !MatchWorkflow(workflow, r.Name, r.Path) {
			continue
		}
		prev, seen := latest[r.WorkflowID]
		if !seen {
			order = append(order, r.WorkflowID)
		}
		if !seen || r.RunNumber > prev.RunNumber || (r.RunNumber == prev.RunNumber && r.ID > prev.ID) {
			latest[r.WorkflowID] = r
		}
	}
	short := sha
	if len(short) > 7 {
		short = short[:7]
	}
	if len(order) == 0 {
		what := "workflow run"
		if workflow != "" {
			what = fmt.Sprintf("run of workflow %q", workflow)
		}
		return Result{State: StatePending, Detail: fmt.Sprintf("no %s for %s yet", what, short)}
	}
	var pending, failed []string
	for _, id := range order {
		r := latest[id]
		switch {
		case r.Status != "completed":
			pending = append(pending, fmt.Sprintf("%s (%s)", r.Name, strings.ReplaceAll(r.Status, "_", " ")))
		case r.Conclusion == "success" || r.Conclusion == "skipped" || r.Conclusion == "neutral":
		default:
			failed = append(failed, fmt.Sprintf("%s (%s)", r.Name, r.Conclusion))
		}
	}
	if len(failed) > 0 {
		return Result{State: StateFailed, Detail: "workflow failed: " + strings.Join(failed, ", ")}
	}
	if len(pending) > 0 {
		return Result{State: StatePending, Detail: "waiting for " + strings.Join(pending, ", ")}
	}
	return Result{State: StateSuccess, Detail: "workflow succeeded"}
}
