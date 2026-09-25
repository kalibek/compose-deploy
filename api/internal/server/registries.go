package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/compose-deploy/api/internal/db"
	"github.com/compose-deploy/api/internal/httpx"
	"github.com/compose-deploy/api/internal/validate"
)

type registryResponse struct {
	ID          int64     `json:"id"`
	URL         string    `json:"url"`
	Username    string    `json:"username"`
	HasPassword bool      `json:"has_password"`
	CreatedAt   time.Time `json:"created_at"`
}

func toRegistryResponse(g *db.Registry) registryResponse {
	return registryResponse{ID: g.ID, URL: g.URL, Username: g.Username, HasPassword: g.PasswordEnc != "", CreatedAt: g.CreatedAt}
}

type registryRequest struct {
	URL      *string `json:"url"`
	Username *string `json:"username"`
	Password *string `json:"password"`
}

func (s *Server) listRegistries(w http.ResponseWriter, r *http.Request) {
	regs, err := s.db.ListRegistries(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]registryResponse, 0, len(regs))
	for _, g := range regs {
		out = append(out, toRegistryResponse(g))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// apply validates req and copies it onto g. create requires url.
func (s *Server) applyRegistry(g *db.Registry, req registryRequest, create bool) (int, error) {
	if req.URL != nil || create {
		u := ""
		if req.URL != nil {
			u = strings.TrimSpace(*req.URL)
		}
		if u != "" || create {
			if err := validate.RegistryURL(u); err != nil {
				return http.StatusBadRequest, err
			}
			g.URL = u
		}
	}
	if req.Username != nil {
		u := strings.TrimSpace(*req.Username)
		if err := validate.RegistryUsername(u); err != nil {
			return http.StatusBadRequest, err
		}
		g.Username = u
	}
	if req.Password != nil && *req.Password != "" {
		enc, err := s.box.EncryptString(*req.Password)
		if err != nil {
			return http.StatusInternalServerError, err
		}
		g.PasswordEnc = enc
	}
	return 0, nil
}

func (s *Server) createRegistry(w http.ResponseWriter, r *http.Request) {
	var req registryRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	g := &db.Registry{}
	if code, err := s.applyRegistry(g, req, true); err != nil {
		if code == http.StatusInternalServerError {
			s.internalError(w, err)
			return
		}
		httpx.WriteError(w, code, err.Error())
		return
	}
	if err := s.db.CreateRegistry(r.Context(), g); err != nil {
		if errors.Is(err, db.ErrConflict) {
			httpx.WriteError(w, http.StatusConflict, "a registry with this url already exists")
			return
		}
		s.internalError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toRegistryResponse(g))
}

func (s *Server) updateRegistry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "registry not found")
		return
	}
	g, err := s.db.GetRegistry(r.Context(), id)
	if errors.Is(err, db.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "registry not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	var req registryRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if code, err := s.applyRegistry(g, req, false); err != nil {
		if code == http.StatusInternalServerError {
			s.internalError(w, err)
			return
		}
		httpx.WriteError(w, code, err.Error())
		return
	}
	if err := s.db.UpdateRegistry(r.Context(), g); err != nil {
		switch {
		case errors.Is(err, db.ErrConflict):
			httpx.WriteError(w, http.StatusConflict, "a registry with this url already exists")
		case errors.Is(err, db.ErrNotFound):
			httpx.WriteError(w, http.StatusNotFound, "registry not found")
		default:
			s.internalError(w, err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toRegistryResponse(g))
}

func (s *Server) deleteRegistry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "registry not found")
		return
	}
	if err := s.db.DeleteRegistry(r.Context(), id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "registry not found")
			return
		}
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
