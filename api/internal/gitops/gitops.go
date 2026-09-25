// Package gitops wraps the git CLI. Credentials are passed only through
// environment variables (GIT_CONFIG_COUNT/KEY/VALUE), never in arguments or
// stored remote URLs.
package gitops

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/compose-deploy/api/internal/runner"
)

type Git struct {
	Runner  runner.Runner
	Timeout time.Duration // per command; 0 = no extra timeout
}

// BasicAuthValue returns the base64 credential used in the Authorization header.
func BasicAuthValue(token string) string {
	return base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
}

// Secrets returns the strings that must be scrubbed from any output for token.
func Secrets(token string) []string {
	if token == "" {
		return nil
	}
	return []string{token, BasicAuthValue(token)}
}

// AuthEnv builds the environment for a git invocation against repoURL.
func AuthEnv(repoURL, token string) []string {
	env := []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS="}
	if token == "" {
		return env
	}
	host := "github.com"
	if u, err := url.Parse(repoURL); err == nil && u.Host != "" {
		host = u.Host
	}
	return append(env,
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://"+host+"/.extraheader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+BasicAuthValue(token),
	)
}

func (g *Git) run(ctx context.Context, dir string, env []string, out io.Writer, args ...string) error {
	if g.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.Timeout)
		defer cancel()
	}
	return g.Runner.Run(ctx, runner.Cmd{Name: "git", Args: args, Dir: dir, Env: env}, out)
}

// LsRemote returns the commit sha that refs/heads/<branch> points to.
func (g *Git) LsRemote(ctx context.Context, repoURL, branch, token string) (string, error) {
	var buf bytes.Buffer
	ref := "refs/heads/" + branch
	err := g.run(ctx, "", AuthEnv(repoURL, token), &buf, "ls-remote", "--exit-code", repoURL, ref)
	if err != nil {
		msg := strings.TrimSpace(runner.Scrub(buf.String(), Secrets(token)...))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git ls-remote: %s", msg)
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == ref && isSHA(f[0]) {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("git ls-remote: branch %q not found", branch)
}

func isSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func logStep(out io.Writer, args ...string) {
	fmt.Fprintf(out, "$ git %s\n", strings.Join(args, " "))
}

// Sync makes dir an up-to-date checkout of origin/<branch>: clone if missing,
// otherwise fetch + checkout --force -B + clean -fd. Ignored files (such as a
// .env or bind-mounted data directories) are preserved. Returns HEAD's sha.
// Command headers and output are written to out.
//
// If target is non-empty, that commit is checked out instead of the branch
// head; it must be part of the branch's history.
func (g *Git) Sync(ctx context.Context, dir, repoURL, branch, token, target string, out io.Writer) (string, error) {
	if target != "" && !isSHA(target) {
		return "", fmt.Errorf("invalid target commit %q", target)
	}
	env := AuthEnv(repoURL, token)
	step := func(wd string, args ...string) error {
		logStep(out, args...)
		var last lastLine
		if err := g.run(ctx, wd, env, io.MultiWriter(out, &last), args...); err != nil {
			if msg := runner.Scrub(last.String(), Secrets(token)...); msg != "" {
				return fmt.Errorf("git %s: %s", args[0], msg)
			}
			return fmt.Errorf("git %s: %w", args[0], err)
		}
		return nil
	}

	remote := "refs/remotes/origin/" + branch
	// checkoutTarget pins the checkout to target after verifying it's on the branch.
	checkoutTarget := func() error {
		logStep(out, "merge-base", "--is-ancestor", target, remote)
		if err := g.run(ctx, dir, env, out, "merge-base", "--is-ancestor", target, remote); err != nil {
			return fmt.Errorf("commit %s is not on branch %s (force-pushed?)", target[:7], branch)
		}
		return step(dir, "checkout", "--force", "-B", branch, target)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		// Missing or partial checkout: start fresh.
		if err := os.RemoveAll(dir); err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return "", err
		}
		if err := step(filepath.Dir(dir), "clone", "--branch", branch, "--single-branch", "--", repoURL, dir); err != nil {
			return "", err
		}
		if target != "" {
			if err := checkoutTarget(); err != nil {
				return "", err
			}
		}
	} else {
		if err := step(dir, "remote", "set-url", "origin", repoURL); err != nil {
			return "", err
		}
		if err := step(dir, "fetch", "--prune", "--no-tags", "origin", "+refs/heads/"+branch+":"+remote); err != nil {
			return "", err
		}
		if target != "" {
			if err := checkoutTarget(); err != nil {
				return "", err
			}
		} else if err := step(dir, "checkout", "--force", "-B", branch, remote); err != nil {
			return "", err
		}
		if err := step(dir, "clean", "-fd"); err != nil {
			return "", err
		}
	}

	var buf bytes.Buffer
	if err := g.run(ctx, dir, env, &buf, "rev-parse", "HEAD"); err != nil {
		return "", fmt.Errorf("git rev-parse: %w: %s", err, strings.TrimSpace(buf.String()))
	}
	sha := strings.TrimSpace(buf.String())
	if !isSHA(sha) {
		return "", fmt.Errorf("git rev-parse returned unexpected output %q", sha)
	}
	fmt.Fprintf(out, "HEAD is %s\n", sha)
	return sha, nil
}

// lastLine remembers the last non-empty output line (for error messages).
type lastLine struct{ buf []byte }

func (l *lastLine) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	if len(l.buf) > 8192 {
		l.buf = l.buf[len(l.buf)-4096:]
	}
	return len(p), nil
}

func (l *lastLine) String() string {
	lines := strings.Split(strings.TrimSpace(string(l.buf)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
