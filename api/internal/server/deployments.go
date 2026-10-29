package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/compose-deploy/api/internal/db"
	"github.com/compose-deploy/api/internal/httpx"
)

type deploymentResponse struct {
	ID     int64  `json:"id"`
	Action string `json:"action"`
	// TriggeredBy is "manual", "webhook", "poll" or "config"; TriggerDetail
	// adds context, e.g. "push to main".
	TriggeredBy   string     `json:"triggered_by"`
	TriggerDetail string     `json:"trigger_detail"`
	Status        string     `json:"status"`
	SHA           string     `json:"sha"`
	Error         string     `json:"error"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	Log           *string    `json:"log,omitempty"`
}

func toDeploymentResponse(g *db.Deployment) deploymentResponse {
	return deploymentResponse{
		ID: g.ID, Action: g.Action, TriggeredBy: g.TriggeredBy, TriggerDetail: g.TriggerDetail, Status: g.Status,
		SHA: g.SHA, Error: g.Error, StartedAt: g.StartedAt, FinishedAt: g.FinishedAt,
	}
}

// listDeployments returns the stack's deploy/stop history, newest first (no logs).
func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStack(w, r)
	if !ok {
		return
	}
	limit := db.MaxDeployments
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v < limit {
		limit = v
	}
	list, err := s.db.ListDeployments(r.Context(), st.ID, limit)
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]deploymentResponse, 0, len(list))
	for _, g := range list {
		out = append(out, toDeploymentResponse(g))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// getDeployment returns one deployment including its log.
func (s *Server) getDeployment(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStack(w, r)
	if !ok {
		return
	}
	did, err := strconv.ParseInt(chi.URLParam(r, "did"), 10, 64)
	if err != nil || did <= 0 {
		httpx.WriteError(w, http.StatusNotFound, "deployment not found")
		return
	}
	g, err := s.db.GetDeployment(r.Context(), st.ID, did)
	if errors.Is(err, db.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	res := toDeploymentResponse(g)
	res.Log = &g.Log
	httpx.WriteJSON(w, http.StatusOK, res)
}
