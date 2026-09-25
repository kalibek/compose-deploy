// Package deployer runs deploy / stop operations for stacks. Operations for
// the same stack are serialised; requests arriving while one is running are
// coalesced so exactly one more run happens afterwards.
package deployer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/compose-deploy/api/internal/crypto"
	"github.com/compose-deploy/api/internal/db"
	"github.com/compose-deploy/api/internal/gitops"
	"github.com/compose-deploy/api/internal/runner"
)

type kind int

const (
	kindDeploy kind = iota
	kindStop
)

type request struct {
	kind  kind
	force bool
	sha   string // deploy this commit instead of the branch head ("" = head)
}

type queue struct {
	running bool
	next    *request
}

type Options struct {
	DB         *db.DB
	Box        *crypto.Box
	Runner     runner.Runner
	DataDir    string
	CmdTimeout time.Duration // per command, default 15m
	FlushEvery time.Duration // partial log flush interval, default 1s
	Logger     *slog.Logger
}

type Deployer struct {
	db         *db.DB
	box        *crypto.Box
	run        runner.Runner
	git        *gitops.Git
	dataDir    string
	cmdTimeout time.Duration
	flushEvery time.Duration
	log        *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	queues map[int64]*queue
	locks  map[int64]*sync.Mutex
}

func New(o Options) *Deployer {
	if o.CmdTimeout == 0 {
		o.CmdTimeout = 15 * time.Minute
	}
	if o.FlushEvery == 0 {
		o.FlushEvery = time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Deployer{
		db:         o.DB,
		box:        o.Box,
		run:        o.Runner,
		git:        &gitops.Git{Runner: o.Runner, Timeout: o.CmdTimeout},
		dataDir:    o.DataDir,
		cmdTimeout: o.CmdTimeout,
		flushEvery: o.FlushEvery,
		log:        o.Logger,
		ctx:        ctx,
		cancel:     cancel,
		queues:     map[int64]*queue{},
		locks:      map[int64]*sync.Mutex{},
	}
}

// Git exposes the git helper (used by the poller).
func (d *Deployer) Git() *gitops.Git { return d.git }

// StackDir is DATA_DIR/stacks/<name>.
func StackDir(dataDir, name string) string { return filepath.Join(dataDir, "stacks", name) }

// RepoDir is DATA_DIR/stacks/<name>/repo.
func RepoDir(dataDir, name string) string { return filepath.Join(StackDir(dataDir, name), "repo") }

// Enqueue schedules a deploy. force=true is a manual deploy (also clears
// "stopped"); force=false is an automatic trigger and is dropped for stopped stacks.
func (d *Deployer) Enqueue(id int64, force bool) {
	d.submit(id, request{kind: kindDeploy, force: force})
}

// EnqueueSHA schedules an automatic deploy of a specific commit of the branch
// (used when a commit's workflow succeeded; the head may have moved on since).
func (d *Deployer) EnqueueSHA(id int64, sha string) {
	d.submit(id, request{kind: kindDeploy, sha: sha})
}

// Stop schedules `docker compose down`; the stack ends in status "stopped".
func (d *Deployer) Stop(id int64) {
	d.submit(id, request{kind: kindStop})
}

// Shutdown cancels running operations and waits for workers to exit.
func (d *Deployer) Shutdown() {
	d.cancel()
	d.wg.Wait()
}

func (d *Deployer) submit(id int64, req request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx.Err() != nil {
		return
	}
	q := d.queues[id]
	if q == nil {
		q = &queue{}
		d.queues[id] = q
	}
	if q.next != nil && req.kind == kindDeploy && q.next.kind == kindDeploy {
		req.force = req.force || q.next.force
	}
	q.next = &req // latest intent wins
	if (req.kind == kindStop || req.force) && !q.running {
		if err := d.db.SetStackStatus(context.Background(), id, db.StatusQueued); err != nil {
			d.log.Error("set queued", "stack_id", id, "err", err)
		}
	}
	if !q.running {
		q.running = true
		d.wg.Add(1)
		go d.worker(id, q)
	}
}

func (d *Deployer) worker(id int64, q *queue) {
	defer d.wg.Done()
	for {
		d.mu.Lock()
		req := q.next
		q.next = nil
		if req == nil || d.ctx.Err() != nil {
			q.running = false
			delete(d.queues, id)
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()
		switch req.kind {
		case kindDeploy:
			d.execDeploy(id, req.force, req.sha)
		case kindStop:
			d.execStop(id)
		}
	}
}

func (d *Deployer) stackLock(id int64) *sync.Mutex {
	d.mu.Lock()
	defer d.mu.Unlock()
	l := d.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		d.locks[id] = l
	}
	return l
}

// ------------------------------------------------------------------ runs

// flusher periodically writes partial logs to the DB until stop is called.
func (d *Deployer) flusher(id int64, lb *logBuf) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(d.flushEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if lb.takeDirty() {
					if err := d.db.SetStackLog(context.Background(), id, lb.String()); err != nil {
						d.log.Warn("flush log", "stack_id", id, "err", err)
					}
				}
			}
		}
	}()
	return func() { close(done); wg.Wait() }
}

func (d *Deployer) decryptEnv(enc string) (map[string]string, error) {
	env := map[string]string{}
	if enc == "" {
		return env, nil
	}
	raw, err := d.box.Decrypt(enc)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	return env, nil
}

func (d *Deployer) execDeploy(id int64, force bool, target string) {
	lock := d.stackLock(id)
	lock.Lock()
	defer lock.Unlock()

	bg := context.Background()
	st, err := d.db.GetStack(bg, id)
	if err != nil {
		if !errors.Is(err, db.ErrNotFound) {
			d.log.Error("load stack", "stack_id", id, "err", err)
		}
		return
	}
	if !force && st.Status == db.StatusStopped {
		return
	}
	log := d.log.With("stack", st.Name)
	log.Info("deploy started", "force", force, "target", target)

	if err := d.db.StartRun(bg, id, db.StatusDeploying); err != nil {
		log.Error("start run", "err", err)
		return
	}
	lb := newLogBuf()
	stopFlush := d.flusher(id, lb)

	sha, err := d.deploy(d.ctx, st, target, lb)
	stopFlush()

	now := time.Now().UTC()
	p := db.FinishParams{Status: db.StatusSuccess, DeployAt: &now}
	if sha != "" {
		p.SHA = &sha
	}
	if err != nil {
		p.Status = db.StatusFailed
		p.Error = runner.Scrub(err.Error(), lb.secrets...)
		fmt.Fprintf(lb, "\n! deploy failed: %s\n", err)
		log.Warn("deploy failed", "err", p.Error)
	} else {
		fmt.Fprintf(lb, "\ndeploy succeeded at %s\n", sha)
		log.Info("deploy succeeded", "sha", sha)
	}
	p.Log = lb.String()
	if err := d.db.FinishRun(bg, id, p); err != nil {
		log.Error("finish run", "err", err)
	}
}

// deploy performs the actual work; it returns the synced sha (if sync
// succeeded) or, for inline stacks, the ContentSHA of the deployed file.
// target pins the commit to deploy ("" = branch head).
func (d *Deployer) deploy(ctx context.Context, st *db.Stack, target string, lb *logBuf) (string, error) {
	env, err := d.decryptEnv(st.EnvEnc)
	if err != nil {
		return "", errors.New("cannot decrypt env (APP_SECRET changed?)")
	}
	auths, secrets, err := d.registryAuths(ctx)
	if err != nil {
		return "", err
	}
	lb.addSecrets(secrets...)

	stackDir := StackDir(d.dataDir, st.Name)
	if err := os.MkdirAll(stackDir, 0o700); err != nil {
		return "", err
	}

	var sha, composeFile, projDir string
	var cenv []string
	if st.Source == db.SourceInline {
		if composeFile, projDir, sha, err = writeInline(stackDir, st.ComposeContent, lb); err != nil {
			return "", err
		}
	} else {
		token, err := d.box.DecryptString(st.TokenEnc)
		if err != nil {
			return "", errors.New("cannot decrypt token (APP_SECRET changed?)")
		}
		lb.addSecrets(gitops.Secrets(token)...)
		repoDir := RepoDir(d.dataDir, st.Name)
		if sha, err = d.git.Sync(ctx, repoDir, st.RepoURL, st.Branch, token, target, lb); err != nil {
			return "", err
		}
		if composeFile, projDir, err = resolveCompose(repoDir, st.ComposePath); err != nil {
			return sha, err
		}
		cenv = commitEnv(sha)
		fmt.Fprintf(lb, "COMMIT_SHA=%s COMMIT_SHORT_SHA=%s\n", sha, sha[:7])
	}
	if err := writeEnvFile(stackDir, projDir, env, lb); err != nil {
		return sha, err
	}

	dockerCfg, cleanup, err := makeDockerConfig(auths)
	if err != nil {
		return sha, err
	}
	defer cleanup()

	cenv = append([]string{"DOCKER_CONFIG=" + dockerCfg}, cenv...)
	base := composeBase(st.Name, composeFile, projDir)
	steps := [][]string{
		append(append([]string{}, base...), "down", "--remove-orphans"),
		append(append([]string{}, base...), "pull"),
		append(append([]string{}, base...), "up", "-d", "--remove-orphans"),
	}
	for _, args := range steps {
		if err := d.docker(ctx, projDir, cenv, lb, args); err != nil {
			return sha, err
		}
	}
	return sha, nil
}

// InlineDir is DATA_DIR/stacks/<name>/inline, the project directory of inline stacks.
func InlineDir(dataDir, name string) string { return filepath.Join(StackDir(dataDir, name), "inline") }

// InlineFile is the name of the compose file written for inline stacks.
const InlineFile = "docker-compose.yml"

// ContentSHA identifies a revision of an inline compose file (stored as last_sha).
func ContentSHA(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// writeInline writes an inline stack's compose file into its project directory.
func writeInline(stackDir, content string, out io.Writer) (file, projDir, sha string, err error) {
	if strings.TrimSpace(content) == "" {
		return "", "", "", errors.New("the compose file is empty")
	}
	projDir = filepath.Join(stackDir, "inline")
	if err := os.MkdirAll(projDir, 0o700); err != nil {
		return "", "", "", err
	}
	file = filepath.Join(projDir, InlineFile)
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		return "", "", "", fmt.Errorf("write compose file: %w", err)
	}
	sha = ContentSHA(content)
	fmt.Fprintf(out, "wrote inline compose file (revision %s)\n", sha[:12])
	return file, projDir, sha, nil
}

// commitEnv exposes the deployed commit to compose interpolation, e.g.
// `image: ghcr.io/acme/app:${COMMIT_SHA}`. Process env takes precedence over .env.
func commitEnv(sha string) []string {
	if len(sha) < 7 {
		return nil
	}
	return []string{"COMMIT_SHA=" + sha, "COMMIT_SHORT_SHA=" + sha[:7]}
}

func composeBase(project, composeFile, projDir string) []string {
	return []string{"compose", "-p", project, "-f", composeFile, "--project-directory", projDir}
}

func (d *Deployer) docker(ctx context.Context, dir string, env []string, out io.Writer, args []string) error {
	fmt.Fprintf(out, "\n$ docker %s\n", strings.Join(args, " "))
	cctx, cancel := context.WithTimeout(ctx, d.cmdTimeout)
	defer cancel()
	err := d.run.Run(cctx, runner.Cmd{Name: "docker", Args: args, Dir: dir, Env: env}, out)
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("docker %s: timed out after %s", stepName(args), d.cmdTimeout)
		}
		return fmt.Errorf("docker %s: %w", stepName(args), err)
	}
	return nil
}

// stepName returns the compose subcommand (e.g. "compose pull").
func stepName(args []string) string {
	for i, a := range args {
		if a == "down" || a == "pull" || a == "up" {
			return "compose " + args[i]
		}
	}
	return strings.Join(args, " ")
}

// resolveCompose returns the absolute compose file path and its directory,
// ensuring (including via symlinks) that it stays inside repoDir.
func resolveCompose(repoDir, composePath string) (string, string, error) {
	file := filepath.Join(repoDir, filepath.FromSlash(composePath))
	realRepo, err := filepath.EvalSymlinks(repoDir)
	if err != nil {
		return "", "", err
	}
	realFile, err := filepath.EvalSymlinks(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("compose file %q not found in repository", composePath)
		}
		return "", "", err
	}
	rel, err := filepath.Rel(realRepo, realFile)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("compose file %q resolves outside the repository", composePath)
	}
	fi, err := os.Stat(realFile)
	if err != nil {
		return "", "", err
	}
	if !fi.Mode().IsRegular() {
		return "", "", fmt.Errorf("compose file %q is not a regular file", composePath)
	}
	return file, filepath.Dir(file), nil
}

// envMarker records that we wrote a .env so it can be removed when the env is cleared.
func envMarker(stackDir string) string { return filepath.Join(stackDir, "env-managed") }

// writeEnvFile writes <projDir>/.env when env is non-empty, overwriting any
// .env committed in the repo. When env is empty, a .env we wrote previously is
// removed (a repo-provided .env is left alone).
func writeEnvFile(stackDir, projDir string, env map[string]string, out io.Writer) error {
	path := filepath.Join(projDir, ".env")
	marker := envMarker(stackDir)
	if len(env) == 0 {
		if b, err := os.ReadFile(marker); err == nil {
			prev := strings.TrimSpace(string(b))
			if prev != "" {
				_ = os.Remove(prev)
			}
			_ = os.Remove(marker)
		}
		return nil
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, []byte(FormatEnv(env)), 0o600); err != nil {
		return fmt.Errorf("write .env: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	fmt.Fprintf(out, "wrote %d variable(s) to .env\n", len(env))
	return os.WriteFile(marker, []byte(path), 0o600)
}

// FormatEnv renders env as a docker compose .env file with sorted keys.
// Values are single-quoted (literal, no interpolation); values containing a
// single quote are double-quoted with \ " and $ escaped.
func FormatEnv(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := env[k]
		if !strings.Contains(v, "'") {
			fmt.Fprintf(&b, "%s='%s'\n", k, v)
			continue
		}
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`)
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, r.Replace(v))
	}
	return b.String()
}

type authEntry struct {
	Auth string `json:"auth"`
}

// registryKey maps Docker Hub aliases to the key the docker CLI expects.
func registryKey(u string) string {
	u = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://"), "/")
	switch u {
	case "docker.io", "index.docker.io", "registry-1.docker.io", "index.docker.io/v1":
		return "https://index.docker.io/v1/"
	}
	return u
}

func (d *Deployer) registryAuths(ctx context.Context) (map[string]authEntry, []string, error) {
	regs, err := d.db.ListRegistries(ctx)
	if err != nil {
		return nil, nil, err
	}
	auths := map[string]authEntry{}
	var secrets []string
	for _, r := range regs {
		pw, err := d.box.DecryptString(r.PasswordEnc)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot decrypt password for registry %s", r.URL)
		}
		enc := base64.StdEncoding.EncodeToString([]byte(r.Username + ":" + pw))
		auths[registryKey(r.URL)] = authEntry{Auth: enc}
		if pw != "" {
			secrets = append(secrets, pw, enc)
		}
	}
	return auths, secrets, nil
}

// makeDockerConfig creates a temporary DOCKER_CONFIG directory holding
// config.json with the registry auths. The caller must invoke cleanup.
func makeDockerConfig(auths map[string]authEntry) (string, func(), error) {
	dir, err := os.MkdirTemp("", "cd-docker-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	body, _ := json.Marshal(map[string]any{"auths": auths})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), body, 0o600); err != nil {
		cleanup()
		return "", func() {}, err
	}
	// Keep CLI plugins (e.g. compose installed per-user) reachable.
	if orig := origDockerConfigDir(); orig != "" {
		if fi, err := os.Stat(filepath.Join(orig, "cli-plugins")); err == nil && fi.IsDir() {
			_ = os.Symlink(filepath.Join(orig, "cli-plugins"), filepath.Join(dir, "cli-plugins"))
		}
	}
	return dir, cleanup, nil
}

func origDockerConfigDir() string {
	if v := os.Getenv("DOCKER_CONFIG"); v != "" {
		return v
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".docker")
	}
	return ""
}

func (d *Deployer) execStop(id int64) {
	lock := d.stackLock(id)
	lock.Lock()
	defer lock.Unlock()

	bg := context.Background()
	st, err := d.db.GetStack(bg, id)
	if err != nil {
		return
	}
	log := d.log.With("stack", st.Name)
	log.Info("stop started")
	if err := d.db.StartRun(bg, id, db.StatusQueued); err != nil {
		log.Error("start run", "err", err)
		return
	}
	lb := newLogBuf()
	err = d.down(d.ctx, st, lb)
	p := db.FinishParams{Status: db.StatusStopped}
	if err != nil {
		p.Status = db.StatusFailed
		p.Error = err.Error()
		fmt.Fprintf(lb, "\n! stop failed: %s\n", err)
		log.Warn("stop failed", "err", err)
	} else {
		fmt.Fprintln(lb, "\nstack stopped")
		log.Info("stack stopped")
	}
	p.Log = lb.String()
	if err := d.db.FinishRun(bg, id, p); err != nil {
		log.Error("finish run", "err", err)
	}
}

// Down runs `docker compose down` for st synchronously (used before delete).
// It waits for any in-flight operation on the stack to finish first.
func (d *Deployer) Down(ctx context.Context, st *db.Stack) error {
	lock := d.stackLock(st.ID)
	lock.Lock()
	defer lock.Unlock()
	return d.down(ctx, st, newLogBuf())
}

func (d *Deployer) down(ctx context.Context, st *db.Stack, out io.Writer) error {
	args := []string{"compose", "-p", st.Name, "down", "--remove-orphans"}
	dir := ""
	var env []string
	if st.Source == db.SourceInline {
		projDir := InlineDir(d.dataDir, st.Name)
		file := filepath.Join(projDir, InlineFile)
		if _, err := os.Stat(file); err == nil {
			args = append(composeBase(st.Name, file, projDir), "down", "--remove-orphans")
			dir = projDir
		}
	} else if file, projDir, err := resolveCompose(RepoDir(d.dataDir, st.Name), st.ComposePath); err == nil {
		args = append(composeBase(st.Name, file, projDir), "down", "--remove-orphans")
		dir = projDir
		// Same interpolation as at deploy time (e.g. ${COMMIT_SHA:?} must resolve).
		env = commitEnv(st.LastSHA)
	}
	return d.docker(ctx, dir, env, out, args)
}

// ValidateCompose runs `docker compose config --quiet` on a draft compose file
// (with the given env written to .env, as at deploy time). It returns the
// command output; err is non-nil when the file is invalid or docker failed.
func (d *Deployer) ValidateCompose(ctx context.Context, content string, env map[string]string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", errors.New("the compose file is empty")
	}
	tmpRoot := filepath.Join(d.dataDir, "tmp")
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(tmpRoot, "validate-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, InlineFile)
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		return "", err
	}
	if len(env) > 0 {
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(FormatEnv(env)), 0o600); err != nil {
			return "", err
		}
	}
	var out bytes.Buffer
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"compose", "-p", "validate", "-f", file, "--project-directory", dir, "config", "--quiet"}
	err = d.run.Run(cctx, runner.Cmd{Name: "docker", Args: args, Dir: dir}, &out)
	// Don't leak the temp path into messages shown in the UI.
	msg := strings.ReplaceAll(out.String(), dir+string(filepath.Separator), "")
	msg = strings.TrimSpace(strings.ReplaceAll(msg, dir, "."))
	if err != nil {
		if msg == "" {
			msg = err.Error()
		}
		return msg, errors.New(msg)
	}
	return msg, nil
}
