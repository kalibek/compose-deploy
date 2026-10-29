// Package poller periodically checks each auto-deploy stack's branch head
// with `git ls-remote` and enqueues a deploy when it differs from last_sha.
// Stacks with the "workflow" trigger are only deployed once the new commit's
// GitHub Actions workflow run(s) succeeded. A commit whose workflow failed is
// re-checked with exponential backoff (a failed run may be re-run, but usually
// isn't), so it doesn't spend a GitHub API request on every tick.
package poller

import (
	"context"
	"log/slog"
	"time"

	"github.com/compose-deploy/api/internal/crypto"
	"github.com/compose-deploy/api/internal/db"
	"github.com/compose-deploy/api/internal/github"
)

type LsRemoter interface {
	LsRemote(ctx context.Context, repoURL, branch, token string) (string, error)
}

type Enqueuer interface {
	Enqueue(id int64, force bool, c db.Cause)
	EnqueueSHA(id int64, sha string, c db.Cause)
}

// WorkflowChecker reports the GitHub Actions state of a commit.
type WorkflowChecker interface {
	CommitState(ctx context.Context, repoURL, token, branch, sha, workflow string) (github.Result, error)
}

// maxBackoff caps the delay between workflow checks of a failed commit.
const maxBackoff = 30 * time.Minute

type Poller struct {
	DB           *db.DB
	Box          *crypto.Box
	Git          LsRemoter
	Deployer     Enqueuer
	Workflows    WorkflowChecker
	Interval     time.Duration
	InitialDelay time.Duration
	Logger       *slog.Logger
	Now          func() time.Time // defaults to time.Now

	// backoff holds, per stack, the failed commit being backed off. Only
	// touched by Tick, which runs on a single goroutine.
	backoff map[int64]backoff
}

type backoff struct {
	sha   string
	delay time.Duration
	next  time.Time
}

// Run blocks until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	log := p.Logger
	if log == nil {
		log = slog.Default()
	}
	delay := p.InitialDelay
	if delay == 0 {
		delay = 5 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	p.Tick(ctx, log)
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Tick(ctx, log)
		}
	}
}

// Tick checks every eligible stack once.
func (p *Poller) Tick(ctx context.Context, log *slog.Logger) {
	stacks, err := p.DB.ListStacks(ctx)
	if err != nil {
		log.Error("poller: list stacks", "err", err)
		return
	}
	for _, st := range stacks {
		if ctx.Err() != nil {
			return
		}
		if !st.AutoDeploy || st.Source == db.SourceInline {
			continue // inline stacks deploy when saved, there's nothing to poll
		}
		switch st.Status {
		case db.StatusStopped, db.StatusQueued, db.StatusDeploying:
			continue
		}
		token, err := p.Box.DecryptString(st.TokenEnc)
		if err != nil {
			log.Error("poller: decrypt token", "stack", st.Name)
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		sha, err := p.Git.LsRemote(cctx, st.RepoURL, st.Branch, token)
		cancel()
		if err != nil {
			log.Warn("poller: ls-remote failed", "stack", st.Name, "err", err)
			continue
		}
		if sha == st.LastSHA {
			delete(p.backoff, st.ID)
			if st.WaitingSHA != "" { // e.g. branch reset to the deployed commit
				p.setWaiting(ctx, log, st, "", "")
			}
			continue
		}
		if st.DeployTrigger != db.TriggerWorkflow {
			log.Info("poller: new commit", "stack", st.Name, "sha", sha, "last_sha", st.LastSHA)
			p.Deployer.Enqueue(st.ID, false, db.Cause{By: db.ByPoll, Detail: "new commit on " + st.Branch})
			continue
		}
		if b, ok := p.backoff[st.ID]; ok && b.sha == sha && p.now().Before(b.next) {
			continue // workflow failed for this commit; not due for a re-check yet
		}
		cctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		res, err := p.Workflows.CommitState(cctx, st.RepoURL, token, st.Branch, sha, st.WorkflowName)
		cancel()
		if err != nil {
			log.Warn("poller: workflow check failed", "stack", st.Name, "sha", sha, "err", err)
			p.setWaiting(ctx, log, st, sha, err.Error())
			continue
		}
		if res.State == github.StateFailed {
			p.backOff(log, st, sha)
		} else {
			delete(p.backoff, st.ID) // pending again, e.g. the failed run was re-run
		}
		if res.State == github.StateSuccess {
			log.Info("poller: workflow succeeded, deploying", "stack", st.Name, "sha", sha)
			p.Deployer.EnqueueSHA(st.ID, sha, db.Cause{By: db.ByPoll, Detail: workflowDetail(st.WorkflowName)})
			continue
		}
		p.setWaiting(ctx, log, st, sha, res.Detail)
	}
}

// backOff schedules the next workflow check of a failed commit, doubling the
// delay on each consecutive failure up to maxBackoff.
func (p *Poller) backOff(log *slog.Logger, st *db.Stack, sha string) {
	if p.backoff == nil {
		p.backoff = map[int64]backoff{}
	}
	b, ok := p.backoff[st.ID]
	if !ok || b.sha != sha {
		b = backoff{sha: sha, delay: p.Interval}
		if b.delay <= 0 {
			b.delay = time.Minute
		}
	}
	b.delay = min(2*b.delay, maxBackoff)
	b.next = p.now().Add(b.delay)
	p.backoff[st.ID] = b
	log.Info("poller: workflow failed, backing off", "stack", st.Name, "sha", sha, "next_check_in", b.delay.String())
}

func (p *Poller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// workflowDetail describes a deploy caused by succeeded workflow run(s).
func workflowDetail(name string) string {
	if name == "" {
		return "all workflows succeeded"
	}
	return "workflow " + name + " succeeded"
}

// setWaiting stores the waiting state when it changed.
func (p *Poller) setWaiting(ctx context.Context, log *slog.Logger, st *db.Stack, sha, reason string) {
	if st.WaitingSHA == sha && st.WaitingReason == reason {
		return
	}
	if sha != "" && st.WaitingSHA != sha {
		log.Info("poller: waiting for workflow", "stack", st.Name, "sha", sha, "reason", reason)
	}
	if err := p.DB.SetWaiting(ctx, st.ID, sha, reason); err != nil {
		log.Error("poller: set waiting", "stack", st.Name, "err", err)
	}
}
