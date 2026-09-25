package deployer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/compose-deploy/api/internal/crypto"
	"github.com/compose-deploy/api/internal/db"
	"github.com/compose-deploy/api/internal/gitops"
	"github.com/compose-deploy/api/internal/runner"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

type fakeRunner struct {
	mu         sync.Mutex
	cmds       []runner.Cmd
	configJSON string
	failOn     string // compose subcommand to fail
	block      chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context, c runner.Cmd, out io.Writer) error {
	f.mu.Lock()
	f.cmds = append(f.cmds, c)
	block := f.block
	f.mu.Unlock()
	if c.Name == "git" {
		switch c.Args[0] {
		case "clone":
			dir := c.Args[len(c.Args)-1]
			os.MkdirAll(filepath.Join(dir, ".git"), 0o700)
			os.MkdirAll(filepath.Join(dir, "deploy"), 0o700)
			os.WriteFile(filepath.Join(dir, "deploy", "compose.yml"), []byte("services: {}\n"), 0o600)
			fmt.Fprintln(out, "Cloning into... token=ghp_SECRETTOKEN")
		case "rev-parse":
			fmt.Fprintln(out, testSHA)
		}
		return nil
	}
	if c.Name == "docker" {
		if block != nil {
			<-block
		}
		for _, e := range c.Env {
			if v, ok := strings.CutPrefix(e, "DOCKER_CONFIG="); ok && contains(c.Args, "pull") {
				b, _ := os.ReadFile(filepath.Join(v, "config.json"))
				f.mu.Lock()
				f.configJSON = string(b)
				f.mu.Unlock()
			}
		}
		if f.failOn != "" && contains(c.Args, f.failOn) {
			fmt.Fprintln(out, "boom")
			return errors.New("exit status 1")
		}
		fmt.Fprintln(out, "ok")
	}
	return nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func setup(t *testing.T, f *fakeRunner) (*Deployer, *db.DB, *crypto.Box, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	box, _ := crypto.New("secret")
	dep := New(Options{DB: d, Box: box, Runner: f, DataDir: dir, FlushEvery: 10 * time.Millisecond})
	t.Cleanup(dep.Shutdown)
	return dep, d, box, dir
}

func createStack(t *testing.T, d *db.DB, box *crypto.Box) *db.Stack {
	tok, _ := box.EncryptString("ghp_SECRETTOKEN")
	envJSON, _ := json.Marshal(map[string]string{"B": "two", "A": "it's"})
	env, _ := box.Encrypt(envJSON)
	st := &db.Stack{Name: "app", RepoURL: "https://github.com/o/r", Branch: "main", ComposePath: "deploy/compose.yml",
		AutoDeploy: true, TokenEnc: tok, EnvEnc: env}
	if err := d.CreateStack(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	pw, _ := box.EncryptString("regpass")
	d.CreateRegistry(context.Background(), &db.Registry{URL: "ghcr.io", Username: "bob", PasswordEnc: pw})
	return st
}

func waitStatus(t *testing.T, d *db.DB, id int64, want ...string) *db.Stack {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, _ := d.GetStack(context.Background(), id)
		for _, w := range want {
			if st.Status == w {
				return st
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, _ := d.GetStack(context.Background(), id)
	t.Fatalf("timeout waiting for %v, status=%s err=%s log=%s", want, st.Status, st.LastError, st.LastLog)
	return nil
}

func TestDeployCommands(t *testing.T) {
	f := &fakeRunner{}
	dep, d, box, dataDir := setup(t, f)
	st := createStack(t, d, box)

	dep.Enqueue(st.ID, true)
	got := waitStatus(t, d, st.ID, db.StatusSuccess, db.StatusFailed)
	if got.Status != db.StatusSuccess {
		t.Fatalf("status %s: %s\n%s", got.Status, got.LastError, got.LastLog)
	}
	if got.LastSHA != testSHA || got.LastDeployAt == nil {
		t.Fatalf("bad result %+v", got)
	}

	repo := filepath.Join(dataDir, "stacks", "app", "repo")
	composeFile := filepath.Join(repo, "deploy", "compose.yml")
	projDir := filepath.Join(repo, "deploy")
	base := []string{"compose", "-p", "app", "-f", composeFile, "--project-directory", projDir}
	want := []struct {
		name string
		args []string
	}{
		{"git", []string{"clone", "--branch", "main", "--single-branch", "--", "https://github.com/o/r", repo}},
		{"git", []string{"rev-parse", "HEAD"}},
		{"docker", append(append([]string{}, base...), "down", "--remove-orphans")},
		{"docker", append(append([]string{}, base...), "pull")},
		{"docker", append(append([]string{}, base...), "up", "-d", "--remove-orphans")},
	}
	if len(f.cmds) != len(want) {
		t.Fatalf("got %d cmds: %+v", len(f.cmds), f.cmds)
	}
	basic := gitops.BasicAuthValue("ghp_SECRETTOKEN")
	for i, w := range want {
		c := f.cmds[i]
		if c.Name != w.name || strings.Join(c.Args, "\x00") != strings.Join(w.args, "\x00") {
			t.Errorf("cmd %d: got %s %q want %s %q", i, c.Name, c.Args, w.name, w.args)
		}
		for _, a := range c.Args {
			if strings.Contains(a, "ghp_SECRETTOKEN") || strings.Contains(a, basic) {
				t.Errorf("token in args of cmd %d", i)
			}
		}
		env := strings.Join(c.Env, "\n")
		if c.Name == "git" {
			if !strings.Contains(env, "GIT_CONFIG_VALUE_0=Authorization: Basic "+basic) ||
				!strings.Contains(env, "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader") ||
				!strings.Contains(env, "GIT_TERMINAL_PROMPT=0") {
				t.Errorf("git env missing auth: %v", c.Env)
			}
		} else {
			if !strings.HasPrefix(env, "DOCKER_CONFIG=") {
				t.Errorf("docker env missing DOCKER_CONFIG: %v", c.Env)
			}
			if !strings.Contains(env, "COMMIT_SHA="+testSHA) || !strings.Contains(env, "COMMIT_SHORT_SHA="+testSHA[:7]) {
				t.Errorf("docker env missing COMMIT_SHA: %v", c.Env)
			}
			if strings.Contains(env, "ghp_SECRETTOKEN") {
				t.Error("token leaked into docker env")
			}
			if c.Dir != projDir {
				t.Errorf("docker dir %s", c.Dir)
			}
		}
	}

	// registry auth written to temp config and removed afterwards
	var cfg struct {
		Auths map[string]struct{ Auth string } `json:"auths"`
	}
	if err := json.Unmarshal([]byte(f.configJSON), &cfg); err != nil {
		t.Fatalf("config.json %q: %v", f.configJSON, err)
	}
	if cfg.Auths["ghcr.io"].Auth != base64.StdEncoding.EncodeToString([]byte("bob:regpass")) {
		t.Fatalf("bad auths %v", cfg.Auths)
	}
	for _, e := range f.cmds[2].Env {
		if v, ok := strings.CutPrefix(e, "DOCKER_CONFIG="); ok {
			if _, err := os.Stat(v); !os.IsNotExist(err) {
				t.Error("temp DOCKER_CONFIG not removed")
			}
		}
	}

	// .env written with quoting
	b, err := os.ReadFile(filepath.Join(projDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "A=\"it's\"\nB='two'\n" {
		t.Fatalf(".env = %q", b)
	}

	// log contains step headers and is scrubbed
	if !strings.Contains(got.LastLog, "$ docker compose -p app") || !strings.Contains(got.LastLog, "$ git clone") {
		t.Fatalf("log missing headers: %s", got.LastLog)
	}
	if strings.Contains(got.LastLog, "ghp_SECRETTOKEN") || !strings.Contains(got.LastLog, "token=***") {
		t.Fatalf("log not scrubbed: %s", got.LastLog)
	}
}

func TestDeployFailureAndStop(t *testing.T) {
	f := &fakeRunner{failOn: "pull"}
	dep, d, box, _ := setup(t, f)
	st := createStack(t, d, box)
	dep.Enqueue(st.ID, true)
	got := waitStatus(t, d, st.ID, db.StatusFailed, db.StatusSuccess)
	if got.Status != db.StatusFailed || !strings.Contains(got.LastError, "compose pull") {
		t.Fatalf("got %s %q", got.Status, got.LastError)
	}
	if got.LastSHA != testSHA {
		t.Fatal("sha should be recorded after successful sync")
	}

	f.mu.Lock()
	f.failOn = ""
	f.cmds = nil
	f.mu.Unlock()
	dep.Stop(st.ID)
	got = waitStatus(t, d, st.ID, db.StatusStopped)
	last := f.cmds[len(f.cmds)-1]
	if last.Args[len(last.Args)-2] != "down" {
		t.Fatalf("stop ran %v", last.Args)
	}

	// automatic deploy on stopped stack is ignored
	f.mu.Lock()
	f.cmds = nil
	f.mu.Unlock()
	dep.Enqueue(st.ID, false)
	time.Sleep(100 * time.Millisecond)
	if len(f.cmds) != 0 {
		t.Fatal("auto deploy ran on stopped stack")
	}
	// manual deploy clears stopped
	dep.Enqueue(st.ID, true)
	waitStatus(t, d, st.ID, db.StatusSuccess)
}

func TestCoalescing(t *testing.T) {
	f := &fakeRunner{block: make(chan struct{})}
	dep, d, box, _ := setup(t, f)
	st := createStack(t, d, box)
	dep.Enqueue(st.ID, true)
	waitStatus(t, d, st.ID, db.StatusDeploying)
	for i := 0; i < 5; i++ {
		dep.Enqueue(st.ID, true)
	}
	close(f.block)
	waitStatus(t, d, st.ID, db.StatusSuccess)
	// wait for the queue to drain
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		dep.mu.Lock()
		n := len(dep.queues)
		dep.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ups := 0
	for _, c := range f.cmds {
		if contains(c.Args, "up") {
			ups++
		}
	}
	if ups != 2 {
		t.Fatalf("expected exactly 2 deploys (running + one coalesced), got %d", ups)
	}
}

func TestFormatEnv(t *testing.T) {
	got := FormatEnv(map[string]string{"X": `a"b'$c\`, "Y": "plain $HOME"})
	want := "X=\"a\\\"b'\\$c\\\\\"\nY='plain $HOME'\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDeployPinnedSHA(t *testing.T) {
	f := &fakeRunner{}
	dep, d, box, dataDir := setup(t, f)
	st := createStack(t, d, box)
	d.SetWaiting(context.Background(), st.ID, testSHA, "waiting for build")

	dep.EnqueueSHA(st.ID, testSHA)
	got := waitStatus(t, d, st.ID, db.StatusSuccess, db.StatusFailed)
	if got.Status != db.StatusSuccess {
		t.Fatalf("status %s: %s\n%s", got.Status, got.LastError, got.LastLog)
	}
	if got.WaitingSHA != "" || got.WaitingReason != "" {
		t.Fatalf("waiting state not cleared: %+v", got)
	}
	repo := filepath.Join(dataDir, "stacks", "app", "repo")
	var gitCmds []string
	for _, c := range f.cmds {
		if c.Name == "git" {
			gitCmds = append(gitCmds, strings.Join(c.Args, " "))
		}
	}
	want := []string{
		"clone --branch main --single-branch -- https://github.com/o/r " + repo,
		"merge-base --is-ancestor " + testSHA + " refs/remotes/origin/main",
		"checkout --force -B main " + testSHA,
		"rev-parse HEAD",
	}
	if strings.Join(gitCmds, "\n") != strings.Join(want, "\n") {
		t.Fatalf("git cmds:\n%s\nwant:\n%s", strings.Join(gitCmds, "\n"), strings.Join(want, "\n"))
	}
}
