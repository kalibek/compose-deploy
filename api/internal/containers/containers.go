// Package containers inspects the running containers of a compose project and
// gives access to their logs and an interactive shell, all through the docker
// CLI (so it works against any engine the CLI can talk to, including Podman).
package containers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/creack/pty"

	"github.com/compose-deploy/api/internal/runner"
)

// Container is one container of a compose project.
type Container struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Service   string `json:"service"`
	Image     string `json:"image"`
	State     string `json:"state"`  // created | running | paused | restarting | removing | exited | dead
	Status    string `json:"status"` // human readable, e.g. "Up 2 hours (healthy)"
	Health    string `json:"health"` // healthy | unhealthy | starting | ""
	Ports     string `json:"ports"`
	CreatedAt string `json:"created_at"`
}

var idRe = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

// ValidID reports whether id looks like a (possibly shortened) container ID.
func ValidID(id string) bool { return idRe.MatchString(id) }

// ErrNotFound is returned when a container is not part of the project.
var ErrNotFound = errors.New("container not found")

// psFormat is tab separated so values containing commas/spaces stay intact.
const psFormat = `{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.State}}\t{{.Status}}\t{{.Ports}}\t{{.Label "com.docker.compose.service"}}\t{{.CreatedAt}}`

type Client struct {
	Runner        runner.Runner
	DockerCommand string // container CLI binary, e.g. docker or podman
}

// List returns all containers (including stopped ones) of compose project
// `project`, ordered by service then name.
func (c *Client) List(ctx context.Context, project string) ([]Container, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	err := c.Runner.Run(ctx, runner.Cmd{Name: c.DockerCommand, Args: []string{
		"ps", "--all", "--no-trunc",
		"--filter", "label=com.docker.compose.project=" + project,
		"--format", psFormat,
	}}, &out)
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w: %s", err, lastLine(out.String()))
	}
	return parsePS(out.String()), nil
}

// Find returns the container of `project` whose ID equals or starts with id.
func (c *Client) Find(ctx context.Context, project, id string) (*Container, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	list, err := c.List(ctx, project)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if strings.HasPrefix(list[i].ID, id) {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

func parsePS(s string) []Container {
	list := []Container{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		f := strings.Split(line, "\t")
		if len(f) < 8 || !ValidID(f[0]) {
			continue
		}
		list = append(list, Container{
			ID: f[0], Name: f[1], Image: f[2], State: strings.ToLower(f[3]), Status: f[4],
			Health: health(f[4]), Ports: f[5], Service: f[6], CreatedAt: f[7],
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Service != list[j].Service {
			return list[i].Service < list[j].Service
		}
		return list[i].Name < list[j].Name
	})
	return list
}

func health(status string) string {
	switch {
	case strings.Contains(status, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(status, "(healthy)"):
		return "healthy"
	case strings.Contains(status, "(health: starting)"):
		return "starting"
	}
	return ""
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// Logs streams `docker logs` of container id to out until ctx is done or, when
// follow is false, the existing log has been written.
func (c *Client) Logs(ctx context.Context, id string, tail int, follow bool, out io.Writer) error {
	args := []string{"logs", "--timestamps", "--tail", strconv.Itoa(tail)}
	if follow {
		args = append(args, "--follow")
	}
	args = append(args, id)
	return c.Runner.Run(ctx, runner.Cmd{Name: c.DockerCommand, Args: args}, out)
}

// shellScript starts bash when the image has it, otherwise sh.
const shellScript = `if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi`

// ShellArgs is the docker CLI invocation for an interactive shell in id.
func ShellArgs(id string) []string {
	return []string{"exec", "-it", "-e", "TERM=xterm-256color", id, "sh", "-c", shellScript}
}

// Shell is an interactive shell session running under a pseudo-terminal.
type Shell struct {
	cmd *exec.Cmd
	pty *os.File
}

// StartShell runs `docker exec -it <id> sh` attached to a new PTY of the given
// size. The process is killed when ctx is done.
func (c *Client) StartShell(ctx context.Context, id string, cols, rows uint16) (*Shell, error) {
	cmd := exec.CommandContext(ctx, c.DockerCommand, ShellArgs(id)...)
	cmd.Env = append(runner.BaseEnv(), "TERM=xterm-256color")
	cmd.WaitDelay = 5 * time.Second
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &Shell{cmd: cmd, pty: f}, nil
}

func (s *Shell) Read(p []byte) (int, error)  { return s.pty.Read(p) }
func (s *Shell) Write(p []byte) (int, error) { return s.pty.Write(p) }

// Resize changes the terminal size; the docker CLI forwards it to the container.
func (s *Shell) Resize(cols, rows uint16) error {
	return pty.Setsize(s.pty, &pty.Winsize{Cols: cols, Rows: rows})
}

// Close kills the process (if still running) and releases the PTY.
func (s *Shell) Close() error {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	err := s.cmd.Wait()
	s.pty.Close()
	return err
}
