// Command compose-deploy serves the API + UI and deploys docker compose
// stacks from GitHub repositories.
package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/compose-deploy/api/internal/auth"
	"github.com/compose-deploy/api/internal/config"
	"github.com/compose-deploy/api/internal/crypto"
	"github.com/compose-deploy/api/internal/db"
	"github.com/compose-deploy/api/internal/deployer"
	"github.com/compose-deploy/api/internal/github"
	"github.com/compose-deploy/api/internal/poller"
	"github.com/compose-deploy/api/internal/runner"
	"github.com/compose-deploy/api/internal/server"
	"github.com/compose-deploy/api/web"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Keep secrets out of child process environments.
	os.Unsetenv("ADMIN_PASSWORD")
	os.Unsetenv("APP_SECRET")

	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "stacks"), 0o700); err != nil {
		return err
	}
	_ = os.Chmod(cfg.DataDir, 0o700)

	database, err := db.Open(filepath.Join(cfg.DataDir, "compose-deploy.db"))
	if err != nil {
		return err
	}
	defer database.Close()

	if n, err := database.MarkInterrupted(context.Background()); err != nil {
		return err
	} else if n > 0 {
		logger.Warn("marked interrupted deploys as failed", "count", n)
	}

	box, err := crypto.New(cfg.AppSecret)
	if err != nil {
		return err
	}
	dep := deployer.New(deployer.Options{
		DB: database, Box: box, Runner: runner.Exec{}, DataDir: cfg.DataDir, Logger: logger,
	})

	static, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return err
	}
	gh := github.New()
	srv := server.New(server.Options{
		DB:         database,
		Box:        box,
		Auth:       auth.New(cfg.AdminPassword, cfg.AppSecret, cfg.CookieSecure),
		Deployer:   dep,
		Workflows:  gh,
		DataDir:    cfg.DataDir,
		Static:     static,
		Logger:     logger,
		TrustProxy: cfg.TrustProxy,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	p := &poller.Poller{DB: database, Box: box, Git: dep.Git(), Deployer: dep, Workflows: gh, Interval: cfg.PollInterval, Logger: logger}
	pollDone := make(chan struct{})
	go func() { defer close(pollDone); p.Run(ctx) }()

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr, "data_dir", cfg.DataDir, "poll_interval", cfg.PollInterval.String())
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case err := <-errCh:
		if err != nil {
			stop()
			<-pollDone
			dep.Shutdown()
			return err
		}
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		logger.Warn("http shutdown", "err", err)
	}
	<-pollDone
	dep.Shutdown()
	logger.Info("bye")
	return nil
}
