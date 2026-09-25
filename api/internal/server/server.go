// Package server wires HTTP routes, handlers and SPA static serving.
package server

import (
	"context"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/compose-deploy/api/internal/auth"
	"github.com/compose-deploy/api/internal/containers"
	"github.com/compose-deploy/api/internal/crypto"
	"github.com/compose-deploy/api/internal/db"
	"github.com/compose-deploy/api/internal/github"
	"github.com/compose-deploy/api/internal/httpx"
	"github.com/compose-deploy/api/internal/runner"
)

// Deployer is the subset of deployer.Deployer used by handlers.
type Deployer interface {
	Enqueue(id int64, force bool)
	EnqueueSHA(id int64, sha string)
	Stop(id int64)
	Down(ctx context.Context, st *db.Stack) error
	ValidateCompose(ctx context.Context, content string, env map[string]string) (string, error)
}

// WorkflowChecker reports the GitHub Actions state of a commit.
type WorkflowChecker interface {
	CommitState(ctx context.Context, repoURL, token, branch, sha, workflow string) (github.Result, error)
}

type Options struct {
	DB       *db.DB
	Box      *crypto.Box
	Auth     *auth.Auth
	Deployer Deployer
	// Workflows checks GitHub Actions runs (defaults to the public GitHub API).
	Workflows WorkflowChecker
	// Runner executes docker CLI commands for container inspection/logs
	// (defaults to runner.Exec).
	Runner  runner.Runner
	DataDir string
	Static  fs.FS // root of the built SPA (contains index.html); may be nil
	Logger  *slog.Logger
	// TrustProxy honours X-Forwarded-For / X-Real-IP for the client IP (used
	// by the login rate limiter and logs). Only enable behind a reverse proxy
	// that overwrites these headers, otherwise clients can spoof them.
	TrustProxy bool
}

type Server struct {
	db        *db.DB
	box       *crypto.Box
	auth      *auth.Auth
	dep       Deployer
	dataDir   string
	static    fs.FS
	log       *slog.Logger
	proxy     bool
	ctr       *containers.Client
	workflows WorkflowChecker
	shell     func(ctx context.Context, id string, cols, rows uint16) (shellSession, error)
}

func New(o Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Workflows == nil {
		o.Workflows = github.New()
	}
	if o.Runner == nil {
		o.Runner = runner.Exec{}
	}
	return &Server{
		db: o.DB, box: o.Box, auth: o.Auth, dep: o.Deployer, dataDir: o.DataDir, static: o.Static, log: o.Logger, proxy: o.TrustProxy,
		ctr: &containers.Client{Runner: o.Runner}, shell: startDockerShell, workflows: o.Workflows,
	}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	if s.proxy {
		r.Use(middleware.RealIP)
	}
	r.Use(securityHeaders)
	r.Use(s.requestLogger)
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", s.health)
		r.Post("/hooks/github/{id}", s.githubWebhook)

		r.Group(func(r chi.Router) {
			r.Use(requireJSON)
			r.Post("/auth/login", s.auth.Login)
		})

		r.Group(func(r chi.Router) {
			r.Use(s.auth.Middleware)
			r.Use(requireJSON)
			r.Post("/auth/logout", s.auth.Logout)
			r.Get("/auth/me", s.auth.Me)

			r.Get("/stacks", s.listStacks)
			r.Post("/stacks", s.createStack)
			r.Get("/stacks/{id}", s.getStack)
			r.Put("/stacks/{id}", s.updateStack)
			r.Delete("/stacks/{id}", s.deleteStack)
			r.Post("/stacks/{id}/deploy", s.deployStack)
			r.Post("/stacks/{id}/stop", s.stopStack)
			r.Get("/stacks/{id}/status", s.stackStatus)
			r.Post("/stacks/{id}/webhook-secret", s.rotateWebhookSecret)
			r.Post("/compose/validate", s.validateCompose)
			r.Get("/stacks/{id}/containers", s.listContainers)
			r.Get("/stacks/{id}/containers/{cid}/logs", s.containerLogs)
			r.Get("/stacks/{id}/containers/{cid}/exec", s.containerExec)

			r.Get("/registries", s.listRegistries)
			r.Post("/registries", s.createRegistry)
			r.Put("/registries/{id}", s.updateRegistry)
			r.Delete("/registries/{id}", s.deleteRegistry)
		})

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteError(w, http.StatusNotFound, "not found")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		})
	})

	r.Get("/*", s.spa)
	r.Head("/*", s.spa)
	return r
}

// securityHeaders sets conservative response headers for API and UI.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// requireJSON is a CSRF guard for state-changing requests: they must carry
// Content-Type: application/json (even when body-less). Browsers cannot send
// that cross-origin without a CORS preflight, which this server never grants,
// so a malicious page -- including one served from another port on the same
// host, which SameSite=Lax treats as same-site -- cannot trigger actions.
func requireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				httpx.WriteError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		// Don't log the chatty status polling at info level.
		lvl := slog.LevelInfo
		if strings.HasSuffix(r.URL.Path, "/status") || strings.HasSuffix(r.URL.Path, "/containers") || !strings.HasPrefix(r.URL.Path, "/api") {
			lvl = slog.LevelDebug
		}
		s.log.Log(r.Context(), lvl, "http",
			"method", r.Method, "path", r.URL.Path, "status", ww.Status(),
			"bytes", ww.BytesWritten(), "dur", time.Since(start).String(),
			"ip", r.RemoteAddr, "req_id", middleware.GetReqID(r.Context()))
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// spa serves static files from the embedded UI build, falling back to
// index.html for client-side routes.
func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	if s.static == nil {
		http.NotFound(w, r)
		return
	}
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p != "" && p != "index.html" {
		if fi, err := fs.Stat(s.static, p); err == nil && !fi.IsDir() {
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.static, p)
			return
		}
	}
	body, err := fs.ReadFile(s.static, "index.html")
	if err != nil {
		http.Error(w, "UI not built", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}
