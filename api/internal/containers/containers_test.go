package containers

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/compose-deploy/api/internal/runner"
)

const (
	idA = "aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111"
	idB = "bbbbbbbbbbbb2222222222222222222222222222222222222222222222222222"
)

type fakeRunner struct {
	out  string
	cmds []runner.Cmd
}

func (f *fakeRunner) Run(ctx context.Context, c runner.Cmd, out io.Writer) error {
	f.cmds = append(f.cmds, c)
	io.WriteString(out, f.out)
	return nil
}

func TestListParsesAndFilters(t *testing.T) {
	f := &fakeRunner{out: strings.Join([]string{
		idB + "\tapp-web-1\tnginx:1.27\trunning\tUp 3 minutes (healthy)\t0.0.0.0:8080->80/tcp, [::]:8080->80/tcp\tweb\t2026-09-25 10:00:00 +0000 UTC",
		idA + "\tapp-db-1\tpostgres:17\texited\tExited (0) 2 minutes ago\t\tdb\t2026-09-25 09:59:00 +0000 UTC",
		"garbage line",
		"",
	}, "\n")}
	c := &Client{Runner: f, DockerCommand: "docker"}
	list, err := c.List(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"ps", "--all", "--no-trunc", "--filter", "label=com.docker.compose.project=app", "--format", psFormat}
	if f.cmds[0].Name != "docker" || !reflect.DeepEqual(f.cmds[0].Args, wantArgs) {
		t.Fatalf("args = %v", f.cmds[0].Args)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].Service != "db" || list[0].State != "exited" || list[0].Health != "" {
		t.Errorf("db = %+v", list[0])
	}
	w := list[1]
	if w.Service != "web" || w.Health != "healthy" || w.Ports != "0.0.0.0:8080->80/tcp, [::]:8080->80/tcp" || w.Image != "nginx:1.27" {
		t.Errorf("web = %+v", w)
	}

	got, err := c.Find(context.Background(), "app", idB[:12])
	if err != nil || got.Name != "app-web-1" {
		t.Fatalf("find = %+v, %v", got, err)
	}
	if _, err := c.Find(context.Background(), "app", "cccccccccccc"); err != ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
	if _, err := c.Find(context.Background(), "app", "--help"); err != ErrNotFound {
		t.Fatalf("invalid id must be rejected, got %v", err)
	}
}

func TestLogsArgs(t *testing.T) {
	f := &fakeRunner{}
	c := &Client{Runner: f, DockerCommand: "docker"}
	c.Logs(context.Background(), idA, 200, true, io.Discard)
	c.Logs(context.Background(), idA, 50, false, io.Discard)
	if want := []string{"logs", "--timestamps", "--tail", "200", "--follow", idA}; !reflect.DeepEqual(f.cmds[0].Args, want) {
		t.Errorf("follow args = %v", f.cmds[0].Args)
	}
	if want := []string{"logs", "--timestamps", "--tail", "50", idA}; !reflect.DeepEqual(f.cmds[1].Args, want) {
		t.Errorf("args = %v", f.cmds[1].Args)
	}
}

func TestHealth(t *testing.T) {
	for in, want := range map[string]string{
		"Up 1 second (health: starting)": "starting",
		"Up 5 minutes (unhealthy)":       "unhealthy",
		"Up 5 minutes (healthy)":         "healthy",
		"Up 5 minutes":                   "",
	} {
		if got := health(in); got != want {
			t.Errorf("health(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestStartShellPTY runs a fake `docker` that drops the `exec -it -e TERM=… <id>`
// prefix and runs the shell locally under the PTY.
func TestStartShellPTY(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nshift 5\nexec \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sh, err := (&Client{DockerCommand: "docker"}).StartShell(ctx, idA, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	if err := sh.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	io.WriteString(sh, "stty size; echo \"term=$TERM\"; exit\n")
	out, _ := io.ReadAll(sh) // ends with EIO/EOF once the shell exits
	s := string(out)
	if !strings.Contains(s, "40 120") || !strings.Contains(s, "term=xterm-256color") {
		t.Fatalf("output = %q", s)
	}
}
