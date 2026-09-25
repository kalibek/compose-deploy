package gitops

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compose-deploy/api/internal/runner"
)

type fake struct {
	cmds   []runner.Cmd
	output string
}

func (f *fake) Run(ctx context.Context, c runner.Cmd, out io.Writer) error {
	f.cmds = append(f.cmds, c)
	fmt.Fprint(out, f.output)
	return nil
}

func TestLsRemote(t *testing.T) {
	sha := strings.Repeat("a", 40)
	f := &fake{output: sha + "\trefs/heads/main\n"}
	g := &Git{Runner: f}
	got, err := g.LsRemote(context.Background(), "https://github.com/o/r", "main", "ghp_SECRET")
	if err != nil || got != sha {
		t.Fatalf("got %q %v", got, err)
	}
	c := f.cmds[0]
	if strings.Join(c.Args, " ") != "ls-remote --exit-code https://github.com/o/r refs/heads/main" {
		t.Fatalf("args %v", c.Args)
	}
	env := strings.Join(c.Env, "\n")
	if !strings.Contains(env, "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader") ||
		!strings.Contains(env, "GIT_CONFIG_VALUE_0=Authorization: Basic "+BasicAuthValue("ghp_SECRET")) ||
		!strings.Contains(env, "GIT_TERMINAL_PROMPT=0") {
		t.Fatalf("env %v", c.Env)
	}
	for _, a := range c.Args {
		if strings.Contains(a, "ghp_SECRET") {
			t.Fatal("token leaked into args")
		}
	}
}

func TestAuthEnvNoToken(t *testing.T) {
	for _, e := range AuthEnv("https://github.com/o/r", "") {
		if strings.HasPrefix(e, "GIT_CONFIG") {
			t.Fatalf("unexpected %s", e)
		}
	}
}

// TestSyncRealGit exercises Sync against a local repository with the real git binary.
func TestSyncRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	gitc := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	os.MkdirAll(src, 0o700)
	gitc("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(src, "docker-compose.yml"), []byte("services: {}\n"), 0o600)
	os.WriteFile(filepath.Join(src, ".gitignore"), []byte(".env\n"), 0o600)
	gitc("add", ".")
	gitc("commit", "-qm", "one")
	sha1 := gitc("rev-parse", "HEAD")

	g := &Git{Runner: runner.Exec{}}
	ctx := context.Background()
	if got, err := g.LsRemote(ctx, src, "main", ""); err != nil || got != sha1 {
		t.Fatalf("ls-remote %q %v", got, err)
	}
	dst := filepath.Join(tmp, "stacks", "app", "repo")
	var log strings.Builder
	got, err := g.Sync(ctx, dst, src, "main", "", "", &log)
	if err != nil || got != sha1 {
		t.Fatalf("clone sync %q %v\n%s", got, err, log.String())
	}
	// local modifications, untracked and ignored files
	os.WriteFile(filepath.Join(dst, "docker-compose.yml"), []byte("dirty"), 0o600)
	os.WriteFile(filepath.Join(dst, "junk.txt"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(dst, ".env"), []byte("A=1"), 0o600)

	os.WriteFile(filepath.Join(src, "new.txt"), []byte("n"), 0o600)
	gitc("add", ".")
	gitc("commit", "-qm", "two")
	sha2 := gitc("rev-parse", "HEAD")
	got, err = g.Sync(ctx, dst, src, "main", "", "", &log)
	if err != nil || got != sha2 {
		t.Fatalf("fetch sync %q %v\n%s", got, err, log.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "docker-compose.yml")); string(b) != "services: {}\n" {
		t.Fatal("local modification not reset")
	}
	if _, err := os.Stat(filepath.Join(dst, "junk.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked file not cleaned")
	}
	if _, err := os.Stat(filepath.Join(dst, ".env")); err != nil {
		t.Fatal("ignored .env should be preserved")
	}
	if _, err := g.LsRemote(ctx, src, "nope", ""); err == nil {
		t.Fatal("expected error for missing branch")
	}

	// pinned to an older commit of the branch (existing checkout and fresh clone)
	if got, err := g.Sync(ctx, dst, src, "main", "", sha1, &log); err != nil || got != sha1 {
		t.Fatalf("pinned sync %q %v\n%s", got, err, log.String())
	}
	fresh := filepath.Join(tmp, "stacks", "fresh", "repo")
	if got, err := g.Sync(ctx, fresh, src, "main", "", sha1, &log); err != nil || got != sha1 {
		t.Fatalf("pinned clone %q %v\n%s", got, err, log.String())
	}
	// a commit that is not on the branch is refused
	gitc("checkout", "-qb", "other")
	os.WriteFile(filepath.Join(src, "other.txt"), []byte("o"), 0o600)
	gitc("add", ".")
	gitc("commit", "-qm", "other")
	shaOther := gitc("rev-parse", "HEAD")
	if _, err := g.Sync(ctx, dst, src, "main", "", shaOther, &log); err == nil || !strings.Contains(err.Error(), "not on branch main") {
		t.Fatalf("expected not-on-branch error, got %v", err)
	}
	if _, err := g.Sync(ctx, dst, src, "main", "", "--help", &log); err == nil {
		t.Fatal("expected invalid target error")
	}
}
