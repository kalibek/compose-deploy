package poller

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/compose-deploy/api/internal/crypto"
	"github.com/compose-deploy/api/internal/db"
	"github.com/compose-deploy/api/internal/github"
)

const (
	oldSHA = "1111111111111111111111111111111111111111"
	newSHA = "2222222222222222222222222222222222222222"
)

type fakeGit struct{ sha string }

func (f fakeGit) LsRemote(ctx context.Context, repoURL, branch, token string) (string, error) {
	return f.sha, nil
}

type fakeDeployer struct {
	heads []int64
	shas  []string
}

func (f *fakeDeployer) Enqueue(id int64, force bool, c db.Cause)    { f.heads = append(f.heads, id) }
func (f *fakeDeployer) EnqueueSHA(id int64, sha string, c db.Cause) { f.shas = append(f.shas, sha) }

type fakeChecker struct {
	res  github.Result
	args []string
}

func (f *fakeChecker) CommitState(ctx context.Context, repoURL, token, branch, sha, workflow string) (github.Result, error) {
	f.args = append(f.args, sha+" "+workflow)
	return f.res, nil
}

func setup(t *testing.T, trigger string) (*Poller, *db.DB, *fakeDeployer, *fakeChecker, int64) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	box, _ := crypto.New("secret")
	st := &db.Stack{Name: "app", RepoURL: "https://github.com/o/r", Branch: "main", ComposePath: "c.yml",
		AutoDeploy: true, DeployTrigger: trigger, WorkflowName: "build"}
	if err := d.CreateStack(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	d.FinishRun(context.Background(), st.ID, db.FinishParams{Status: db.StatusSuccess, SHA: ptr(oldSHA)})
	dep := &fakeDeployer{}
	chk := &fakeChecker{}
	p := &Poller{DB: d, Box: box, Git: fakeGit{newSHA}, Deployer: dep, Workflows: chk}
	return p, d, dep, chk, st.ID
}

func ptr(s string) *string { return &s }

func TestPushTriggerDeploysHead(t *testing.T) {
	p, _, dep, chk, id := setup(t, db.TriggerPush)
	p.Tick(context.Background(), slog.Default())
	if len(dep.heads) != 1 || dep.heads[0] != id || len(chk.args) != 0 {
		t.Fatalf("heads %v, checks %v", dep.heads, chk.args)
	}
}

func TestWorkflowTriggerWaitsThenDeploysCommit(t *testing.T) {
	p, d, dep, chk, id := setup(t, db.TriggerWorkflow)
	ctx := context.Background()

	chk.res = github.Result{State: github.StatePending, Detail: "waiting for build (in progress)"}
	p.Tick(ctx, slog.Default())
	st, _ := d.GetStack(ctx, id)
	if len(dep.heads)+len(dep.shas) != 0 || st.WaitingSHA != newSHA || st.WaitingReason != chk.res.Detail {
		t.Fatalf("pending: deploys %v %v, stack %+v", dep.heads, dep.shas, st)
	}
	if chk.args[0] != newSHA+" build" {
		t.Fatalf("checked %v", chk.args)
	}

	chk.res = github.Result{State: github.StateSuccess}
	p.Tick(ctx, slog.Default())
	if len(dep.heads) != 0 || len(dep.shas) != 1 || dep.shas[0] != newSHA {
		t.Fatalf("success: heads %v shas %v", dep.heads, dep.shas)
	}

	// branch reset back to the deployed commit clears the waiting state
	d.SetWaiting(ctx, id, newSHA, "x")
	p.Git = fakeGit{oldSHA}
	p.Tick(ctx, slog.Default())
	if st, _ := d.GetStack(ctx, id); st.WaitingSHA != "" {
		t.Fatalf("waiting not cleared: %+v", st)
	}
}

func TestFailedWorkflowBacksOff(t *testing.T) {
	p, d, dep, chk, id := setup(t, db.TriggerWorkflow)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p.Now = func() time.Time { return now }
	p.Interval = time.Minute
	tick := func(after time.Duration) {
		now = now.Add(after)
		p.Tick(ctx, slog.Default())
	}

	chk.res = github.Result{State: github.StateFailed, Detail: "workflow failed: build (failure)"}
	tick(0)
	if len(chk.args) != 1 {
		t.Fatalf("first check: %v", chk.args)
	}
	if st, _ := d.GetStack(ctx, id); st.WaitingSHA != newSHA || st.WaitingReason != chk.res.Detail {
		t.Fatalf("waiting state: %+v", st)
	}

	// backoff doubles from the interval: next checks after 2m, then 4m
	tick(time.Minute)
	if len(chk.args) != 1 {
		t.Fatalf("checked during backoff: %v", chk.args)
	}
	tick(time.Minute)
	if len(chk.args) != 2 {
		t.Fatalf("not re-checked after 2m: %v", chk.args)
	}
	tick(3 * time.Minute)
	if len(chk.args) != 2 {
		t.Fatalf("checked during 4m backoff: %v", chk.args)
	}
	tick(time.Minute)
	if len(chk.args) != 3 {
		t.Fatalf("not re-checked after 4m: %v", chk.args)
	}

	// a re-run that succeeds deploys once it's checked
	chk.res = github.Result{State: github.StateSuccess}
	tick(8 * time.Minute)
	if len(dep.shas) != 1 || dep.shas[0] != newSHA {
		t.Fatalf("success after backoff: shas %v", dep.shas)
	}

	// a new commit is checked right away, regardless of the old backoff
	chk.res = github.Result{State: github.StateFailed, Detail: "workflow failed"}
	d.FinishRun(ctx, id, db.FinishParams{Status: db.StatusSuccess, SHA: ptr(oldSHA)})
	tick(0)
	n := len(chk.args)
	p.Git = fakeGit{"3333333333333333333333333333333333333333"}
	tick(0)
	if len(chk.args) != n+1 {
		t.Fatalf("new commit not checked immediately: %v", chk.args)
	}
}
