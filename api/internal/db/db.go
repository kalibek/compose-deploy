// Package db owns the SQLite database: opening, migrations and typed queries.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// Stack statuses.
const (
	StatusIdle      = "idle"
	StatusQueued    = "queued"
	StatusDeploying = "deploying"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusStopped   = "stopped"
)

// Stack sources.
const (
	SourceGit    = "git"    // compose file from a GitHub/HTTPS repository
	SourceInline = "inline" // compose file edited in the UI, stored in ComposeContent
)

// Deploy triggers.
const (
	TriggerPush     = "push"     // deploy every new commit on the branch
	TriggerWorkflow = "workflow" // deploy a commit once its GitHub Actions workflow succeeded
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type DB struct {
	sql *sql.DB
	now func() time.Time
}

// Open opens (creating if needed) the database at path and applies migrations.
func Open(path string) (*DB, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	dsn := "file:" + path + "?" + q.Encode()
	s, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A single connection serialises writers and avoids SQLITE_BUSY; the
	// workload is tiny.
	s.SetMaxOpenConns(1)
	if err := s.Ping(); err != nil {
		s.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	d := &DB{sql: s, now: func() time.Time { return time.Now().UTC() }}
	if err := d.migrate(context.Background()); err != nil {
		s.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error { return d.sql.Close() }

func (d *DB) Ping(ctx context.Context) error { return d.sql.PingContext(ctx) }

func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.sql.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var cur int
	if err := d.sql.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&cur); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	type mig struct {
		v    int
		name string
	}
	var migs []mig
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			continue
		}
		v, err := strconv.Atoi(num)
		if err != nil {
			return fmt.Errorf("bad migration name %s", e.Name())
		}
		migs = append(migs, mig{v, e.Name()})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].v < migs[j].v })
	for _, m := range migs {
		if m.v <= cur {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + m.name)
		if err != nil {
			return err
		}
		tx, err := d.sql.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, m.v); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t.UTC()
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// ---------------------------------------------------------------- stacks

type Stack struct {
	ID               int64
	Name             string
	RepoURL          string
	Branch           string
	ComposePath      string
	AutoDeploy       bool
	TokenEnc         string
	WebhookSecretEnc string
	EnvEnc           string
	Status           string
	LastSHA          string
	LastDeployAt     *time.Time
	LastError        string
	LastLog          string
	Source           string // SourceGit | SourceInline
	ComposeContent   string // SourceInline only
	DeployTrigger    string // TriggerPush | TriggerWorkflow
	WorkflowName     string // optional, TriggerWorkflow only
	WaitingSHA       string // commit waiting for its workflow ("" = none)
	WaitingReason    string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const stackCols = `id, name, repo_url, branch, compose_path, auto_deploy, token_enc, webhook_secret_enc,
	env_enc, status, last_sha, last_deploy_at, last_error, last_log, deploy_trigger, workflow_name,
	waiting_sha, waiting_reason, source, compose_content, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanStack(r scanner) (*Stack, error) {
	var s Stack
	var auto int
	var lastDeploy sql.NullString
	var created, updated string
	err := r.Scan(&s.ID, &s.Name, &s.RepoURL, &s.Branch, &s.ComposePath, &auto, &s.TokenEnc,
		&s.WebhookSecretEnc, &s.EnvEnc, &s.Status, &s.LastSHA, &lastDeploy, &s.LastError,
		&s.LastLog, &s.DeployTrigger, &s.WorkflowName, &s.WaitingSHA, &s.WaitingReason, &s.Source, &s.ComposeContent, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.AutoDeploy = auto != 0
	if lastDeploy.Valid && lastDeploy.String != "" {
		t := parseTS(lastDeploy.String)
		s.LastDeployAt = &t
	}
	s.CreatedAt = parseTS(created)
	s.UpdatedAt = parseTS(updated)
	return &s, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// CreateStack inserts s and fills ID/Status/timestamps. Returns ErrConflict if the name exists.
func (d *DB) CreateStack(ctx context.Context, s *Stack) error {
	now := d.now()
	if s.Status == "" {
		s.Status = StatusIdle
	}
	if s.DeployTrigger == "" {
		s.DeployTrigger = TriggerPush
	}
	if s.Source == "" {
		s.Source = SourceGit
	}
	res, err := d.sql.ExecContext(ctx, `INSERT INTO stacks
		(name, repo_url, branch, compose_path, auto_deploy, token_enc, webhook_secret_enc, env_enc, status,
		 deploy_trigger, workflow_name, source, compose_content, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Name, s.RepoURL, s.Branch, s.ComposePath, b2i(s.AutoDeploy), s.TokenEnc, s.WebhookSecretEnc,
		s.EnvEnc, s.Status, s.DeployTrigger, s.WorkflowName, s.Source, s.ComposeContent, ts(now), ts(now))
	if err != nil {
		if isUnique(err) {
			return ErrConflict
		}
		return err
	}
	s.ID, _ = res.LastInsertId()
	s.CreatedAt = parseTS(ts(now))
	s.UpdatedAt = s.CreatedAt
	return nil
}

func (d *DB) GetStack(ctx context.Context, id int64) (*Stack, error) {
	return scanStack(d.sql.QueryRowContext(ctx, `SELECT `+stackCols+` FROM stacks WHERE id = ?`, id))
}

func (d *DB) ListStacks(ctx context.Context) ([]*Stack, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT `+stackCols+` FROM stacks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Stack{}
	for rows.Next() {
		s, err := scanStack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateStackConfig persists the user-editable configuration columns of s.
// It also clears the "waiting for workflow" state, which the config may have invalidated.
func (d *DB) UpdateStackConfig(ctx context.Context, s *Stack) error {
	now := d.now()
	res, err := d.sql.ExecContext(ctx, `UPDATE stacks SET repo_url = ?, branch = ?, compose_path = ?,
		auto_deploy = ?, token_enc = ?, webhook_secret_enc = ?, env_enc = ?, deploy_trigger = ?, workflow_name = ?,
		compose_content = ?, waiting_sha = '', waiting_reason = '', updated_at = ? WHERE id = ?`,
		s.RepoURL, s.Branch, s.ComposePath, b2i(s.AutoDeploy), s.TokenEnc, s.WebhookSecretEnc, s.EnvEnc,
		s.DeployTrigger, s.WorkflowName, s.ComposeContent, ts(now), s.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.UpdatedAt = parseTS(ts(now))
	s.WaitingSHA, s.WaitingReason = "", ""
	return nil
}

// SetWaiting records that commit sha waits for its workflow (sha "" clears it).
func (d *DB) SetWaiting(ctx context.Context, id int64, sha, reason string) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE stacks SET waiting_sha = ?, waiting_reason = ? WHERE id = ?`, sha, reason, id)
	return err
}

func (d *DB) DeleteStack(ctx context.Context, id int64) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM stacks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) SetStackStatus(ctx context.Context, id int64, status string) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE stacks SET status = ? WHERE id = ?`, status, id)
	return err
}

// StartRun marks a stack as running an operation: sets status, clears last_error and last_log.
func (d *DB) StartRun(ctx context.Context, id int64, status string) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE stacks SET status = ?, last_error = '', last_log = '',
		waiting_sha = '', waiting_reason = '' WHERE id = ?`, status, id)
	return err
}

func (d *DB) SetStackLog(ctx context.Context, id int64, log string) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE stacks SET last_log = ? WHERE id = ?`, log, id)
	return err
}

// FinishParams describes the outcome of a deploy/stop run.
type FinishParams struct {
	Status   string
	SHA      *string    // nil = leave last_sha unchanged
	DeployAt *time.Time // nil = leave last_deploy_at unchanged
	Error    string
	Log      string
}

func (d *DB) FinishRun(ctx context.Context, id int64, p FinishParams) error {
	q := `UPDATE stacks SET status = ?, last_error = ?, last_log = ?`
	args := []any{p.Status, p.Error, p.Log}
	if p.SHA != nil {
		q += `, last_sha = ?`
		args = append(args, *p.SHA)
	}
	if p.DeployAt != nil {
		q += `, last_deploy_at = ?`
		args = append(args, ts(*p.DeployAt))
	}
	q += ` WHERE id = ?`
	args = append(args, id)
	_, err := d.sql.ExecContext(ctx, q, args...)
	return err
}

// MarkInterrupted flips stacks left in queued/deploying (e.g. after a crash) to failed.
func (d *DB) MarkInterrupted(ctx context.Context) (int64, error) {
	res, err := d.sql.ExecContext(ctx, `UPDATE stacks SET status = ?, last_error = 'interrupted by restart'
		WHERE status IN (?, ?)`, StatusFailed, StatusQueued, StatusDeploying)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ------------------------------------------------------------- registries

type Registry struct {
	ID          int64
	URL         string
	Username    string
	PasswordEnc string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const registryCols = `id, url, username, password_enc, created_at, updated_at`

func scanRegistry(r scanner) (*Registry, error) {
	var g Registry
	var created, updated string
	if err := r.Scan(&g.ID, &g.URL, &g.Username, &g.PasswordEnc, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	g.CreatedAt = parseTS(created)
	g.UpdatedAt = parseTS(updated)
	return &g, nil
}

func (d *DB) CreateRegistry(ctx context.Context, g *Registry) error {
	now := d.now()
	res, err := d.sql.ExecContext(ctx, `INSERT INTO registries (url, username, password_enc, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, g.URL, g.Username, g.PasswordEnc, ts(now), ts(now))
	if err != nil {
		if isUnique(err) {
			return ErrConflict
		}
		return err
	}
	g.ID, _ = res.LastInsertId()
	g.CreatedAt = parseTS(ts(now))
	g.UpdatedAt = g.CreatedAt
	return nil
}

func (d *DB) GetRegistry(ctx context.Context, id int64) (*Registry, error) {
	return scanRegistry(d.sql.QueryRowContext(ctx, `SELECT `+registryCols+` FROM registries WHERE id = ?`, id))
}

func (d *DB) ListRegistries(ctx context.Context) ([]*Registry, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT `+registryCols+` FROM registries ORDER BY url`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Registry{}
	for rows.Next() {
		g, err := scanRegistry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (d *DB) UpdateRegistry(ctx context.Context, g *Registry) error {
	now := d.now()
	res, err := d.sql.ExecContext(ctx, `UPDATE registries SET url = ?, username = ?, password_enc = ?, updated_at = ? WHERE id = ?`,
		g.URL, g.Username, g.PasswordEnc, ts(now), g.ID)
	if err != nil {
		if isUnique(err) {
			return ErrConflict
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	g.UpdatedAt = parseTS(ts(now))
	return nil
}

func (d *DB) DeleteRegistry(ctx context.Context, id int64) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM registries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
