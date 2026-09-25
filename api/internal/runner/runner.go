// Package runner abstracts external command execution so callers can be
// tested with a fake. Commands are always executed with explicit argument
// slices, never through a shell.
package runner

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Cmd describes one command invocation.
type Cmd struct {
	Name string
	Args []string
	Dir  string
	// Env is appended to the (filtered) parent environment.
	Env []string
}

// String renders the command for logs (arguments are never secret; secrets
// are passed through Env only).
func (c Cmd) String() string {
	return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
}

// Runner runs a command, streaming combined stdout/stderr to out.
type Runner interface {
	Run(ctx context.Context, c Cmd, out io.Writer) error
}

// Exec runs real processes via os/exec.
type Exec struct{}

// blockedEnv are variables from our own process that must never leak into
// child processes (docker compose would make them available for
// interpolation inside untrusted compose files).
var blockedEnv = map[string]bool{
	"ADMIN_PASSWORD": true,
	"APP_SECRET":     true,
}

// BaseEnv returns the parent environment minus sensitive / conflicting vars.
func BaseEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if blockedEnv[k] || strings.HasPrefix(k, "GIT_CONFIG_") || k == "DOCKER_CONFIG" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func (Exec) Run(ctx context.Context, c Cmd, out io.Writer) error {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(BaseEnv(), c.Env...)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = 10 * time.Second
	return cmd.Run()
}

// Scrub replaces every occurrence of each non-empty secret in s with "***".
func Scrub(s string, secrets ...string) string {
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		s = strings.ReplaceAll(s, sec, "***")
	}
	return s
}
