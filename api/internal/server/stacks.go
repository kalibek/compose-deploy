package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/compose-deploy/api/internal/db"
	"github.com/compose-deploy/api/internal/deployer"
	"github.com/compose-deploy/api/internal/github"
	"github.com/compose-deploy/api/internal/httpx"
	"github.com/compose-deploy/api/internal/validate"
)

type stackResponse struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	RepoURL      string     `json:"repo_url"`
	Branch       string     `json:"branch"`
	ComposePath  string     `json:"compose_path"`
	AutoDeploy   bool       `json:"auto_deploy"`
	HasToken     bool       `json:"has_token"`
	EnvKeys      []string   `json:"env_keys"`
	WebhookURL   string     `json:"webhook_url"`
	Status       string     `json:"status"`
	LastSHA      string     `json:"last_sha"`
	LastDeployAt *time.Time `json:"last_deploy_at"`
	LastError    string     `json:"last_error"`
	// Source is "git" or "inline" (compose file edited in the UI).
	Source         string `json:"source"`
	ComposeContent string `json:"compose_content"`
	// ContentSHA identifies the current inline compose file; it equals LastSHA
	// when that revision is deployed ("" for git stacks).
	ContentSHA string `json:"content_sha"`
	// DeployTrigger is "push" or "workflow" (deploy after GitHub Actions succeeded).
	DeployTrigger string `json:"deploy_trigger"`
	WorkflowName  string `json:"workflow_name"`
	// WaitingSHA is a newer commit waiting for its workflow; WaitingReason says why.
	WaitingSHA    string    `json:"waiting_sha"`
	WaitingReason string    `json:"waiting_reason"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (s *Server) decryptEnv(enc string) (map[string]string, error) {
	env := map[string]string{}
	if enc == "" {
		return env, nil
	}
	raw, err := s.box.Decrypt(enc)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	return env, nil
}

func (s *Server) encryptEnv(env map[string]string) (string, error) {
	if len(env) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return s.box.Encrypt(raw)
}

func (s *Server) toStackResponse(st *db.Stack) stackResponse {
	keys := []string{}
	if env, err := s.decryptEnv(st.EnvEnc); err == nil {
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	} else {
		s.log.Error("decrypt env", "stack", st.Name, "err", err)
	}
	return stackResponse{
		ID:             st.ID,
		Name:           st.Name,
		RepoURL:        st.RepoURL,
		Branch:         st.Branch,
		ComposePath:    st.ComposePath,
		AutoDeploy:     st.AutoDeploy,
		HasToken:       st.TokenEnc != "",
		EnvKeys:        keys,
		WebhookURL:     "/api/hooks/github/" + strconv.FormatInt(st.ID, 10),
		Status:         st.Status,
		LastSHA:        st.LastSHA,
		LastDeployAt:   st.LastDeployAt,
		LastError:      st.LastError,
		Source:         st.Source,
		ComposeContent: st.ComposeContent,
		ContentSHA:     contentSHA(st),
		DeployTrigger:  st.DeployTrigger,
		WorkflowName:   st.WorkflowName,
		WaitingSHA:     st.WaitingSHA,
		WaitingReason:  st.WaitingReason,
		CreatedAt:      st.CreatedAt,
		UpdatedAt:      st.UpdatedAt,
	}
}

func contentSHA(st *db.Stack) string {
	if st.Source != db.SourceInline {
		return ""
	}
	return deployer.ContentSHA(st.ComposeContent)
}

// maxComposeSize bounds inline compose files (the JSON body limit is 1 MiB).
const maxComposeSize = 256 << 10

func validateComposeContent(c string) error {
	if strings.TrimSpace(c) == "" {
		return errors.New("compose_content is required")
	}
	if len(c) > maxComposeSize {
		return errors.New("compose_content is too large (max 256 KiB)")
	}
	if strings.ContainsRune(c, 0) {
		return errors.New("compose_content must be text")
	}
	return nil
}

func newWebhookSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

// loadStack fetches the stack named by the {id} URL param, writing 404/500 on failure.
func (s *Server) loadStack(w http.ResponseWriter, r *http.Request) (*db.Stack, bool) {
	id, ok := pathID(r)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "stack not found")
		return nil, false
	}
	st, err := s.db.GetStack(r.Context(), id)
	if errors.Is(err, db.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "stack not found")
		return nil, false
	}
	if err != nil {
		s.internalError(w, err)
		return nil, false
	}
	return st, true
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.log.Error("internal error", "err", err)
	httpx.WriteError(w, http.StatusInternalServerError, "internal error")
}

func (s *Server) listStacks(w http.ResponseWriter, r *http.Request) {
	stacks, err := s.db.ListStacks(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]stackResponse, 0, len(stacks))
	for _, st := range stacks {
		out = append(out, s.toStackResponse(st))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createStackRequest struct {
	Name        string            `json:"name"`
	RepoURL     string            `json:"repo_url"`
	Branch      string            `json:"branch"`
	ComposePath string            `json:"compose_path"`
	Token       string            `json:"token"`
	Env         map[string]string `json:"env"`
	AutoDeploy  *bool             `json:"auto_deploy"`
	// DeployTrigger: "push" (default) or "workflow".
	DeployTrigger string `json:"deploy_trigger"`
	WorkflowName  string `json:"workflow_name"`
	// Source: "git" (default) or "inline"; inline stacks need ComposeContent
	// and ignore the repository fields.
	Source         string `json:"source"`
	ComposeContent string `json:"compose_content"`
}

// validateTrigger checks the deploy trigger settings of st (after defaults/trimming).
func validateTrigger(st *db.Stack) error {
	if st.Source == db.SourceInline {
		if st.DeployTrigger != db.TriggerPush {
			return errors.New("inline stacks deploy when saved; deploy_trigger must be \"push\"")
		}
		st.WorkflowName = ""
		return nil
	}
	switch st.DeployTrigger {
	case db.TriggerPush:
		st.WorkflowName = ""
		return nil
	case db.TriggerWorkflow:
		if _, _, err := github.OwnerRepo(st.RepoURL); err != nil {
			return err
		}
		if len(st.WorkflowName) > 255 || strings.ContainsAny(st.WorkflowName, "\r\n\x00") {
			return errors.New("workflow_name is invalid")
		}
		return nil
	}
	return errors.New(`deploy_trigger must be "push" or "workflow"`)
}

func (s *Server) createStack(w http.ResponseWriter, r *http.Request) {
	var req createStackRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.RepoURL = strings.TrimSpace(req.RepoURL)
	req.Branch = strings.TrimSpace(req.Branch)
	req.ComposePath = strings.TrimSpace(req.ComposePath)
	req.Token = strings.TrimSpace(req.Token)
	req.Source = strings.TrimSpace(req.Source)
	switch req.Source {
	case "":
		req.Source = db.SourceGit
	case db.SourceGit, db.SourceInline:
	default:
		httpx.WriteError(w, http.StatusBadRequest, `source must be "git" or "inline"`)
		return
	}
	if req.Source == db.SourceInline {
		if err := validateComposeContent(req.ComposeContent); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Repository settings don't apply; keep the row consistent.
		req.RepoURL, req.Branch, req.Token, req.DeployTrigger = "", "", "", db.TriggerPush
		req.ComposePath = deployer.InlineFile
	} else {
		req.ComposeContent = ""
	}
	if req.Branch == "" && req.Source == db.SourceGit {
		req.Branch = "main"
	}
	if req.ComposePath == "" {
		req.ComposePath = "docker-compose.yml"
	}
	if err := validate.StackName(req.Name); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	cp := req.ComposePath
	if req.Source == db.SourceGit {
		if err := validate.RepoURL(req.RepoURL); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := validate.Branch(req.Branch); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		var err error
		if cp, err = validate.ComposePath(req.ComposePath); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := validate.Env(req.Env); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	auto := true
	if req.AutoDeploy != nil {
		auto = *req.AutoDeploy
	}

	secret, err := newWebhookSecret()
	if err != nil {
		s.internalError(w, err)
		return
	}
	st := &db.Stack{Name: req.Name, RepoURL: req.RepoURL, Branch: req.Branch, ComposePath: cp, AutoDeploy: auto,
		DeployTrigger: strings.TrimSpace(req.DeployTrigger), WorkflowName: strings.TrimSpace(req.WorkflowName),
		Source: req.Source, ComposeContent: req.ComposeContent}
	if st.DeployTrigger == "" {
		st.DeployTrigger = db.TriggerPush
	}
	if err := validateTrigger(st); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if st.TokenEnc, err = s.box.EncryptString(req.Token); err != nil {
		s.internalError(w, err)
		return
	}
	if st.WebhookSecretEnc, err = s.box.EncryptString(secret); err != nil {
		s.internalError(w, err)
		return
	}
	if st.EnvEnc, err = s.encryptEnv(req.Env); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.db.CreateStack(r.Context(), st); err != nil {
		if errors.Is(err, db.ErrConflict) {
			httpx.WriteError(w, http.StatusConflict, "a stack with this name already exists")
			return
		}
		s.internalError(w, err)
		return
	}
	s.log.Info("stack created", "stack", st.Name, "id", st.ID, "source", st.Source)
	if st.Source == db.SourceInline && st.AutoDeploy {
		s.dep.Enqueue(st.ID, false, db.Cause{By: db.ByConfig, Detail: "stack created"}) // nothing to poll: deploy right away
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"stack":          s.toStackResponse(st),
		"webhook_secret": secret,
	})
}

func (s *Server) getStack(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStack(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s.toStackResponse(st))
}

type updateStackRequest struct {
	RepoURL       *string            `json:"repo_url"`
	Branch        *string            `json:"branch"`
	ComposePath   *string            `json:"compose_path"`
	AutoDeploy    *bool              `json:"auto_deploy"`
	Token         *string            `json:"token"`
	ClearToken    bool               `json:"clear_token"`
	Env           map[string]*string `json:"env"`
	DeployTrigger *string            `json:"deploy_trigger"`
	WorkflowName  *string            `json:"workflow_name"`
	// ComposeContent replaces an inline stack's compose file.
	ComposeContent *string `json:"compose_content"`
}

func (s *Server) updateStack(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStack(w, r)
	if !ok {
		return
	}
	var req updateStackRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	inline := st.Source == db.SourceInline
	if inline && (req.RepoURL != nil || req.Branch != nil || req.ComposePath != nil || req.Token != nil || req.ClearToken) {
		httpx.WriteError(w, http.StatusBadRequest, "inline stacks have no repository settings")
		return
	}
	if !inline && req.ComposeContent != nil {
		httpx.WriteError(w, http.StatusBadRequest, "compose_content only applies to inline stacks")
		return
	}
	contentChanged := false
	if req.ComposeContent != nil {
		if err := validateComposeContent(*req.ComposeContent); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		contentChanged = *req.ComposeContent != st.ComposeContent
		st.ComposeContent = *req.ComposeContent
	}
	if req.RepoURL != nil {
		v := strings.TrimSpace(*req.RepoURL)
		if err := validate.RepoURL(v); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		st.RepoURL = v
	}
	if req.Branch != nil {
		v := strings.TrimSpace(*req.Branch)
		if err := validate.Branch(v); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		st.Branch = v
	}
	if req.ComposePath != nil {
		cp, err := validate.ComposePath(strings.TrimSpace(*req.ComposePath))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		st.ComposePath = cp
	}
	if req.AutoDeploy != nil {
		st.AutoDeploy = *req.AutoDeploy
	}
	if req.DeployTrigger != nil {
		st.DeployTrigger = strings.TrimSpace(*req.DeployTrigger)
	}
	if req.WorkflowName != nil {
		st.WorkflowName = strings.TrimSpace(*req.WorkflowName)
	}
	if err := validateTrigger(st); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	var err error
	switch {
	case req.ClearToken:
		st.TokenEnc = ""
	case req.Token != nil && strings.TrimSpace(*req.Token) != "":
		if st.TokenEnc, err = s.box.EncryptString(strings.TrimSpace(*req.Token)); err != nil {
			s.internalError(w, err)
			return
		}
	}
	if req.Env != nil {
		old, err := s.decryptEnv(st.EnvEnc)
		if err != nil {
			s.internalError(w, err)
			return
		}
		next := map[string]string{}
		for k, v := range req.Env {
			if err := validate.EnvKey(k); err != nil {
				httpx.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			if v == nil {
				// keep the stored value; a null for an unknown key is dropped
				if ov, ok := old[k]; ok {
					next[k] = ov
				}
				continue
			}
			if err := validate.EnvValue(k, *v); err != nil {
				httpx.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			next[k] = *v
		}
		if st.EnvEnc, err = s.encryptEnv(next); err != nil {
			s.internalError(w, err)
			return
		}
	}
	if err := s.db.UpdateStackConfig(r.Context(), st); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "stack not found")
			return
		}
		s.internalError(w, err)
		return
	}
	// Inline stacks have nothing to poll: saving a changed file is the trigger.
	if contentChanged && st.AutoDeploy && st.Status != db.StatusStopped {
		s.log.Info("inline compose file changed, deploy queued", "stack", st.Name)
		s.dep.Enqueue(st.ID, false, db.Cause{By: db.ByConfig, Detail: "compose file saved"})
	}
	httpx.WriteJSON(w, http.StatusOK, s.toStackResponse(st))
}

type validateComposeRequest struct {
	Content string `json:"content"`
	// StackID (optional) supplies the stack's stored env vars for interpolation.
	StackID int64 `json:"stack_id"`
	// Env (optional) overrides env vars like PUT /stacks/{id}: it replaces the
	// set of keys, a null value keeps the stored one.
	Env map[string]*string `json:"env"`
}

// validateCompose checks a draft compose file with `docker compose config`.
// The output is empty when the file is valid; otherwise it holds the error.
func (s *Server) validateCompose(w http.ResponseWriter, r *http.Request) {
	var req validateComposeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateComposeContent(req.Content); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	stored := map[string]string{}
	if req.StackID > 0 {
		st, err := s.db.GetStack(r.Context(), req.StackID)
		if errors.Is(err, db.ErrNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "stack not found")
			return
		}
		if err != nil {
			s.internalError(w, err)
			return
		}
		if stored, err = s.decryptEnv(st.EnvEnc); err != nil {
			s.internalError(w, err)
			return
		}
	}
	env := stored
	if req.Env != nil {
		env = map[string]string{}
		for k, v := range req.Env {
			if validate.EnvKey(k) != nil {
				continue
			}
			if v == nil {
				if ov, ok := stored[k]; ok {
					env[k] = ov
				}
			} else if validate.EnvValue(k, *v) == nil {
				env[k] = *v
			}
		}
	}
	out, err := s.dep.ValidateCompose(r.Context(), req.Content, env)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"valid": err == nil, "output": out})
}

func (s *Server) deleteStack(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStack(w, r)
	if !ok {
		return
	}
	if down, _ := strconv.ParseBool(r.URL.Query().Get("down")); down {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		if err := s.dep.Down(ctx, st); err != nil {
			s.log.Warn("compose down before delete failed (ignored)", "stack", st.Name, "err", err)
		}
		cancel()
	}
	if err := s.db.DeleteStack(r.Context(), st.ID); err != nil && !errors.Is(err, db.ErrNotFound) {
		s.internalError(w, err)
		return
	}
	if err := os.RemoveAll(deployer.StackDir(s.dataDir, st.Name)); err != nil {
		s.log.Warn("remove stack dir", "stack", st.Name, "err", err)
	}
	s.log.Info("stack deleted", "stack", st.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deployStack(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStack(w, r)
	if !ok {
		return
	}
	s.dep.Enqueue(st.ID, true, db.Cause{By: db.ByManual})
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": db.StatusQueued})
}

func (s *Server) stopStack(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStack(w, r)
	if !ok {
		return
	}
	s.dep.Stop(st.ID)
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": db.StatusQueued})
}

func (s *Server) stackStatus(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStack(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":         st.Status,
		"last_sha":       st.LastSHA,
		"last_deploy_at": st.LastDeployAt,
		"last_log":       st.LastLog,
		"last_error":     st.LastError,
	})
}

func (s *Server) rotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStack(w, r)
	if !ok {
		return
	}
	secret, err := newWebhookSecret()
	if err != nil {
		s.internalError(w, err)
		return
	}
	if st.WebhookSecretEnc, err = s.box.EncryptString(secret); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.db.UpdateStackConfig(r.Context(), st); err != nil {
		s.internalError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"webhook_secret": secret})
}
