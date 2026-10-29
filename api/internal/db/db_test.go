package db

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestMigrateIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.db")
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(p)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	var n int
	d.sql.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&n)
	files, _ := fs.ReadDir(migrationsFS, "migrations")
	if n != len(files) {
		t.Fatalf("expected %d schema_version rows (one per migration), got %d", len(files), n)
	}
	d.Close()
}

func TestStackCRUD(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)

	s := &Stack{Name: "b-app", RepoURL: "https://github.com/o/r", Branch: "main", ComposePath: "docker-compose.yml",
		AutoDeploy: true, TokenEnc: "tok", WebhookSecretEnc: "wh", EnvEnc: "env"}
	if err := d.CreateStack(ctx, s); err != nil {
		t.Fatal(err)
	}
	if s.ID == 0 || s.Status != StatusIdle {
		t.Fatalf("bad create result %+v", s)
	}
	if err := d.CreateStack(ctx, &Stack{Name: "b-app", RepoURL: "x", Branch: "m", ComposePath: "c"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if err := d.CreateStack(ctx, &Stack{Name: "a-app", RepoURL: "x", Branch: "m", ComposePath: "c"}); err != nil {
		t.Fatal(err)
	}

	list, err := d.ListStacks(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "a-app" {
		t.Fatalf("list: %v %+v", err, list)
	}

	got, err := d.GetStack(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenEnc != "tok" || !got.AutoDeploy || got.LastDeployAt != nil {
		t.Fatalf("unexpected %+v", got)
	}

	got.Branch = "dev"
	got.AutoDeploy = false
	if err := d.UpdateStackConfig(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, _ = d.GetStack(ctx, s.ID)
	if got.Branch != "dev" || got.AutoDeploy {
		t.Fatalf("update not persisted %+v", got)
	}

	if err := d.StartRun(ctx, s.ID, StatusDeploying); err != nil {
		t.Fatal(err)
	}
	if err := d.SetStackLog(ctx, s.ID, "partial"); err != nil {
		t.Fatal(err)
	}
	sha := "abc"
	now := time.Now()
	if err := d.FinishRun(ctx, s.ID, FinishParams{Status: StatusSuccess, SHA: &sha, DeployAt: &now, Log: "done"}); err != nil {
		t.Fatal(err)
	}
	got, _ = d.GetStack(ctx, s.ID)
	if got.Status != StatusSuccess || got.LastSHA != "abc" || got.LastDeployAt == nil || got.LastLog != "done" {
		t.Fatalf("finish not persisted %+v", got)
	}

	d.SetStackStatus(ctx, s.ID, StatusDeploying)
	n, err := d.MarkInterrupted(ctx)
	if err != nil || n != 1 {
		t.Fatalf("interrupted: %d %v", n, err)
	}
	got, _ = d.GetStack(ctx, s.ID)
	if got.Status != StatusFailed || got.LastError != "interrupted by restart" {
		t.Fatalf("got %+v", got)
	}

	if err := d.DeleteStack(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetStack(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := d.DeleteStack(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected not found on second delete")
	}
}

func TestRegistryCRUD(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	list, _ := d.ListRegistries(ctx)
	if list == nil || len(list) != 0 {
		t.Fatal("expected empty non-nil list")
	}
	g := &Registry{URL: "ghcr.io", Username: "bob", PasswordEnc: "p"}
	if err := d.CreateRegistry(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateRegistry(ctx, &Registry{URL: "ghcr.io"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	g2 := &Registry{URL: "registry.example.com"}
	d.CreateRegistry(ctx, g2)
	g2.URL = "ghcr.io"
	if err := d.UpdateRegistry(ctx, g2); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict on update, got %v", err)
	}
	g.Username = "alice"
	if err := d.UpdateRegistry(ctx, g); err != nil {
		t.Fatal(err)
	}
	got, _ := d.GetRegistry(ctx, g.ID)
	if got.Username != "alice" || got.PasswordEnc != "p" {
		t.Fatalf("got %+v", got)
	}
	if err := d.DeleteRegistry(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetRegistry(ctx, g.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected not found")
	}
}

func TestDeployments(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	s := &Stack{Name: "a", RepoURL: "https://github.com/o/r", Branch: "main", ComposePath: "c.yml"}
	if err := d.CreateStack(ctx, s); err != nil {
		t.Fatal(err)
	}
	g := &Deployment{StackID: s.ID, Action: ActionDeploy, TriggeredBy: ByPoll, TriggerDetail: "new commit on main", SHA: "pinned"}
	if err := d.StartDeployment(ctx, g); err != nil || g.ID == 0 || g.Status != StatusDeploying {
		t.Fatalf("start %+v %v", g, err)
	}
	if err := d.SetDeploymentLog(ctx, g.ID, "partial"); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetDeployment(ctx, s.ID, g.ID)
	if err != nil || got.Log != "partial" || got.FinishedAt != nil || got.SHA != "pinned" {
		t.Fatalf("get %+v %v", got, err)
	}
	sha := "abc"
	if err := d.FinishDeployment(ctx, g.ID, FinishParams{Status: StatusFailed, SHA: &sha, Error: "boom", Log: "full"}); err != nil {
		t.Fatal(err)
	}
	got, _ = d.GetDeployment(ctx, s.ID, g.ID)
	if got.Status != StatusFailed || got.SHA != "abc" || got.Error != "boom" || got.Log != "full" || got.FinishedAt == nil ||
		got.TriggeredBy != ByPoll || got.TriggerDetail != "new commit on main" {
		t.Fatalf("finish not persisted %+v", got)
	}
	if _, err := d.GetDeployment(ctx, s.ID+1, g.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found for another stack, got %v", err)
	}

	// old records are pruned; the list is newest first
	for i := 0; i < MaxDeployments+5; i++ {
		if err := d.StartDeployment(ctx, &Deployment{StackID: s.ID, Action: ActionStop, TriggeredBy: ByManual}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := d.ListDeployments(ctx, s.ID, 1000)
	if err != nil || len(list) != MaxDeployments || list[0].ID < list[1].ID || list[0].Log != "" {
		t.Fatalf("list %d %v", len(list), err)
	}

	// unfinished runs are closed after a restart
	if _, err := d.MarkInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = d.GetDeployment(ctx, s.ID, list[0].ID)
	if got.Status != StatusFailed || got.Error != "interrupted by restart" || got.FinishedAt == nil {
		t.Fatalf("not interrupted %+v", got)
	}

	// deleted with the stack
	if err := d.DeleteStack(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.ListDeployments(ctx, s.ID, 1000); len(list) != 0 {
		t.Fatalf("deployments outlived their stack: %d", len(list))
	}
}
