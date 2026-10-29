package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/compose-deploy/api/internal/db"
	"github.com/compose-deploy/api/internal/github"
	"github.com/compose-deploy/api/internal/httpx"
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

func ignored(w http.ResponseWriter, reason string) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": reason})
}

// VerifySignature checks a GitHub X-Hub-Signature-256 header ("sha256=<hex>")
// against the HMAC-SHA256 of body using secret, in constant time.
func VerifySignature(secret, body []byte, header string) bool {
	if len(secret) == 0 {
		return false
	}
	hexSig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hmac.Equal(got, m.Sum(nil))
}

func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "stack not found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, httpx.MaxBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpx.WriteError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		httpx.WriteError(w, http.StatusBadRequest, "cannot read body")
		return
	}
	st, err := s.db.GetStack(r.Context(), id)
	if errors.Is(err, db.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "stack not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	secret, err := s.box.DecryptString(st.WebhookSecretEnc)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if !VerifySignature([]byte(secret), body, r.Header.Get("X-Hub-Signature-256")) {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	if st.Source == db.SourceInline {
		ignored(w, "inline stack: deploys when its compose file is saved")
		return
	}
	switch r.Header.Get("X-GitHub-Event") {
	case "ping":
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "pong"})
		return
	case "push":
		var payload struct {
			Ref string `json:"ref"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid JSON payload")
			return
		}
		if st.DeployTrigger == db.TriggerWorkflow {
			ignored(w, "stack deploys after its workflow succeeds; subscribe the webhook to \"Workflow runs\"")
			return
		}
		if payload.Ref == "refs/heads/"+st.Branch && st.AutoDeploy && st.Status != db.StatusStopped {
			s.log.Info("webhook: push received, deploy queued", "stack", st.Name)
			s.dep.Enqueue(st.ID, false, db.Cause{By: db.ByWebhook, Detail: "push to " + st.Branch})
			httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": db.StatusQueued})
			return
		}
	case "workflow_run":
		s.workflowRunHook(w, st, body)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
}

// workflowRunHook deploys a "workflow" trigger stack when a GitHub Actions run
// for its branch completes successfully.
func (s *Server) workflowRunHook(w http.ResponseWriter, st *db.Stack, body []byte) {
	var payload struct {
		Action      string `json:"action"`
		WorkflowRun struct {
			Name       string `json:"name"`
			Path       string `json:"path"`
			Event      string `json:"event"`
			HeadBranch string `json:"head_branch"`
			HeadSHA    string `json:"head_sha"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"workflow_run"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	run := payload.WorkflowRun
	switch {
	case st.DeployTrigger != db.TriggerWorkflow:
		ignored(w, "stack deploys on push")
		return
	case !st.AutoDeploy || st.Status == db.StatusStopped:
		ignored(w, "auto-deploy is off or the stack is stopped")
		return
	case payload.Action != "completed":
		ignored(w, "run not completed")
		return
	case run.HeadBranch != st.Branch || github.IgnoredEvent(run.Event):
		ignored(w, "run is not for branch "+st.Branch)
		return
	case !github.MatchWorkflow(st.WorkflowName, run.Name, run.Path):
		ignored(w, "not the configured workflow")
		return
	case !shaRe.MatchString(run.HeadSHA):
		httpx.WriteError(w, http.StatusBadRequest, "invalid head_sha")
		return
	case run.HeadSHA == st.LastSHA && st.Status == db.StatusSuccess:
		ignored(w, "commit already deployed")
		return
	}
	ctx := context.Background()
	if run.Conclusion != "success" {
		reason := "workflow failed: " + run.Name + " (" + run.Conclusion + ")"
		if err := s.db.SetWaiting(ctx, st.ID, run.HeadSHA, reason); err != nil {
			s.log.Error("set waiting", "stack", st.Name, "err", err)
		}
		ignored(w, reason)
		return
	}
	if st.WorkflowName != "" {
		s.log.Info("webhook: workflow succeeded, deploy queued", "stack", st.Name, "workflow", run.Name, "sha", run.HeadSHA)
		s.dep.EnqueueSHA(st.ID, run.HeadSHA, db.Cause{By: db.ByWebhook, Detail: "workflow " + run.Name + " succeeded"})
		httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": db.StatusQueued})
		return
	}
	// No specific workflow configured: this run succeeded, but others for the
	// same commit may still be running. Ask the API about all of them.
	token, err := s.box.DecryptString(st.TokenEnc)
	if err != nil {
		s.internalError(w, err)
		return
	}
	go func(st db.Stack, sha string) {
		cctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		res, err := s.workflows.CommitState(cctx, st.RepoURL, token, st.Branch, sha, "")
		switch {
		case err != nil:
			s.log.Warn("webhook: workflow check failed", "stack", st.Name, "err", err)
			s.db.SetWaiting(cctx, st.ID, sha, err.Error())
		case res.State == github.StateSuccess:
			s.log.Info("webhook: all workflows succeeded, deploy queued", "stack", st.Name, "sha", sha)
			s.dep.EnqueueSHA(st.ID, sha, db.Cause{By: db.ByWebhook, Detail: "all workflows succeeded"})
		default:
			s.db.SetWaiting(cctx, st.ID, sha, res.Detail)
		}
	}(*st, run.HeadSHA)
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "checking"})
}
