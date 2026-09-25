package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/compose-deploy/api/internal/runner"
)

const (
	webID = "aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111"
	dbID  = "bbbbbbbbbbbb2222222222222222222222222222222222222222222222222222"
)

// fakeDocker answers `docker ps` and `docker logs` for project "my-app".
type fakeDocker struct {
	mu   sync.Mutex
	cmds []runner.Cmd
}

func (f *fakeDocker) Run(ctx context.Context, c runner.Cmd, out io.Writer) error {
	f.mu.Lock()
	f.cmds = append(f.cmds, c)
	f.mu.Unlock()
	switch c.Args[0] {
	case "ps":
		if !strings.Contains(strings.Join(c.Args, " "), "com.docker.compose.project=my-app") {
			return nil
		}
		fmt.Fprintf(out, "%s\tmy-app-web-1\tnginx\trunning\tUp 1 minute (healthy)\t80/tcp\tweb\t2026-09-25 10:00:00 +0000 UTC\n", webID)
		fmt.Fprintf(out, "%s\tmy-app-db-1\tpostgres\texited\tExited (1) 1 minute ago\t\tdb\t2026-09-25 10:00:00 +0000 UTC\n", dbID)
	case "logs":
		io.WriteString(out, "line one\nline two\r\npartial")
	}
	return nil
}

func (e *env) login(t *testing.T) {
	t.Helper()
	if resp, _ := e.do(t, "POST", "/api/auth/login", `{"password":"pw"}`); resp.StatusCode != 204 {
		t.Fatalf("login %d", resp.StatusCode)
	}
	if resp, body := e.do(t, "POST", "/api/stacks", map[string]any{"name": "my-app", "repo_url": "https://github.com/o/r"}); resp.StatusCode != 201 {
		t.Fatalf("create %d %s", resp.StatusCode, body)
	}
}

func TestListContainers(t *testing.T) {
	e := newEnv(t)
	if resp, _ := e.do(t, "GET", "/api/stacks/1/containers", nil); resp.StatusCode != 401 {
		t.Fatalf("unauthenticated: %d", resp.StatusCode)
	}
	e.login(t)
	resp, body := e.do(t, "GET", "/api/stacks/1/containers", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list %d %s", resp.StatusCode, body)
	}
	var list []map[string]any
	json.Unmarshal(body, &list)
	if len(list) != 2 || list[0]["service"] != "db" || list[1]["health"] != "healthy" || list[1]["id"] != webID {
		t.Fatalf("list = %s", body)
	}
}

func TestContainerLogsSSE(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	if resp, _ := e.do(t, "GET", "/api/stacks/1/containers/cccccccccccc/logs", nil); resp.StatusCode != 404 {
		t.Fatalf("foreign container: %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "GET", "/api/stacks/1/containers/--version/logs", nil); resp.StatusCode != 404 {
		t.Fatalf("invalid id: %d", resp.StatusCode)
	}
	resp, body := e.do(t, "GET", "/api/stacks/1/containers/"+webID[:12]+"/logs?tail=99999&follow=false", nil)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("logs %d %v", resp.StatusCode, resp.Header)
	}
	want := "data: line one\n\ndata: line two\n\ndata: partial\n\nevent: end\ndata: \n\n"
	if string(body) != want {
		t.Fatalf("body = %q", body)
	}
	last := e.docker.cmds[len(e.docker.cmds)-1]
	if got := strings.Join(last.Args, " "); got != "logs --timestamps --tail 10000 "+webID {
		t.Fatalf("args = %q", got)
	}
}

// echoShell echoes input back and records resizes.
type echoShell struct {
	pr     *io.PipeReader
	pw     *io.PipeWriter
	mu     sync.Mutex
	sizes  []string
	closed chan struct{}
}

func newEchoShell() *echoShell {
	pr, pw := io.Pipe()
	return &echoShell{pr: pr, pw: pw, closed: make(chan struct{})}
}
func (s *echoShell) Read(p []byte) (int, error)  { return s.pr.Read(p) }
func (s *echoShell) Write(p []byte) (int, error) { return s.pw.Write(p) }
func (s *echoShell) Resize(c, r uint16) error {
	s.mu.Lock()
	s.sizes = append(s.sizes, fmt.Sprintf("%dx%d", c, r))
	s.mu.Unlock()
	return nil
}
func (s *echoShell) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
		s.pw.Close()
	}
	return nil
}

func TestContainerExecWebSocket(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	sh := newEchoShell()
	started := make(chan string, 1)
	e.srv.shell = func(ctx context.Context, id string, cols, rows uint16) (shellSession, error) {
		started <- fmt.Sprintf("%s %dx%d", id, cols, rows)
		return sh, nil
	}

	// exited container → 409, no upgrade
	if resp, _ := e.do(t, "GET", "/api/stacks/1/containers/"+dbID+"/exec", nil); resp.StatusCode != 409 {
		t.Fatalf("exited container: %d", resp.StatusCode)
	}

	wsURL := "ws" + strings.TrimPrefix(e.ts.URL, "http") + "/api/stacks/1/containers/" + webID + "/exec?cols=100&rows=30"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// cross-origin handshake is rejected
	if _, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: e.client, HTTPHeader: http.Header{"Origin": {"http://evil.example"}},
	}); err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("expected 403 for foreign origin, got %v %v", resp, err)
	}

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: e.client})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	select {
	case got := <-started:
		if got != webID+" 100x30" {
			t.Fatalf("shell started with %q", got)
		}
	case <-ctx.Done():
		t.Fatal("shell not started")
	}

	conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":120,"rows":40}`))
	conn.Write(ctx, websocket.MessageBinary, []byte("echo hi\n"))
	typ, data, err := conn.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(data) != "echo hi\n" {
		t.Fatalf("read %v %q %v", typ, data, err)
	}
	sh.mu.Lock()
	sizes := strings.Join(sh.sizes, ",")
	sh.mu.Unlock()
	if sizes != "120x40" {
		t.Fatalf("resizes = %q", sizes)
	}

	// client disconnect kills the shell
	conn.Close(websocket.StatusNormalClosure, "")
	select {
	case <-sh.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("shell not closed after client disconnect")
	}
}

func TestSSEWriterSplitsLines(t *testing.T) {
	var b strings.Builder
	w := &sseWriter{w: &b, f: nopFlusher{}}
	w.Write([]byte("a\nb"))
	w.Write([]byte("c\n"))
	w.end("boom")
	sc := bufio.NewScanner(strings.NewReader(b.String()))
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if got := strings.Join(lines, "|"); got != "data: a||data: bc||event: end|data: boom|" {
		t.Fatalf("got %q", got)
	}
}

type nopFlusher struct{}

func (nopFlusher) Flush() {}
