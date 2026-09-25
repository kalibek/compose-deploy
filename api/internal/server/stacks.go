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
		ID:            st.ID,
		Name:          st.Name,
		RepoURL:       st.RepoURL,
		Branch:        st.Branch,
		ComposePath:   st.ComposePath,
		AutoDeploy:    st.AutoDeploy,
		HasToken:      st.TokenEnc != "",
		EnvKeys:       keys,
		WebhookURL:    "/api/hooks/github/" + strconv.FormatInt(st.ID, 10),
		Status:        st.Status,
		LastSHA:       st.LastSHA,
		LastDeployAt:  st.LastDeployAt,
		LastError:     st.LastError,
		DeployTrigger: st.DeployTrigger,
		WorkflowName:  st.WorkflowName,
		WaitingSHA:    st.WaitingSHA,
		WaitingReason: st.WaitingReason,
		CreatedAt:     st.CreatedAt,
		UpdatedAt:     st.UpdatedAt,
	}
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
}

// validateTrigger checks the deploy trigger settings of st (after defaults/trimming).
func validateTrigger(st *db.Stack) error {
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
	if req.Branch == "" {
		req.Branch = "main"
	}
	if req.ComposePath == "" {
		req.ComposePath = "docker-compose.yml"
	}
	if err := validate.StackName(req.Name); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validate.RepoURL(req.RepoURL); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validate.Branch(req.Branch); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	cp, err := validate.ComposePath(req.ComposePath)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
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
		DeployTrigger: strings.TrimSpace(req.DeployTrigger), WorkflowName: strings.TrimSpace(req.WorkflowName)}
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
	s.log.Info("stack created", "stack", st.Name, "id", st.ID)
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
	httpx.WriteJSON(w, http.StatusOK, s.toStackResponse(st))
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
	s.dep.Enqueue(st.ID, true)
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
