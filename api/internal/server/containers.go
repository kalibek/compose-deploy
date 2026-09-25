package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/compose-deploy/api/internal/containers"
	"github.com/compose-deploy/api/internal/db"
	"github.com/compose-deploy/api/internal/httpx"
)

// shellSession is an interactive terminal session (a PTY-backed docker exec).
type shellSession interface {
	io.ReadWriter
	Resize(cols, rows uint16) error
	Close() error
}

func startDockerShell(ctx context.Context, id string, cols, rows uint16) (shellSession, error) {
	return containers.StartShell(ctx, id, cols, rows)
}

func (s *Server) listContainers(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStack(w, r)
	if !ok {
		return
	}
	list, err := s.ctr.List(r.Context(), st.Name)
	if err != nil {
		s.log.Warn("list containers", "stack", st.Name, "err", err)
		httpx.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// loadContainer resolves {id} (stack) and {cid} (container of that stack).
func (s *Server) loadContainer(w http.ResponseWriter, r *http.Request) (*db.Stack, *containers.Container, bool) {
	st, ok := s.loadStack(w, r)
	if !ok {
		return nil, nil, false
	}
	c, err := s.ctr.Find(r.Context(), st.Name, chi.URLParam(r, "cid"))
	if errors.Is(err, containers.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "container not found")
		return nil, nil, false
	}
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, err.Error())
		return nil, nil, false
	}
	return st, c, true
}

// queryInt reads an integer query parameter clamped to [lo, hi].
func queryInt(r *http.Request, key string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	return max(lo, min(v, hi))
}

// containerLogs streams `docker logs` as Server-Sent Events: one `data:` event
// per log line, and a final `end` event (data = error message or empty) when
// the stream finishes on the server side.
func (s *Server) containerLogs(w http.ResponseWriter, r *http.Request) {
	_, c, ok := s.loadContainer(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	tail := queryInt(r, "tail", 500, 1, 10000)
	follow := r.URL.Query().Get("follow") != "false"

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // nginx: don't buffer the stream
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sw := &sseWriter{w: w, f: flusher}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go sw.keepAlive(ctx, 20*time.Second)

	err := s.ctr.Logs(ctx, c.ID, tail, follow, sw)
	if r.Context().Err() != nil {
		return // client went away
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	sw.end(msg)
}

// sseWriter turns a byte stream into one SSE `data:` event per line.
type sseWriter struct {
	mu  sync.Mutex
	w   io.Writer
	f   http.Flusher
	buf []byte
}

func (s *sseWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	wrote := false
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		if err := s.event("", string(s.buf[:i])); err != nil {
			return 0, err
		}
		s.buf = s.buf[i+1:]
		wrote = true
	}
	// Don't hold an unterminated line forever (e.g. progress output).
	if len(s.buf) > 16<<10 {
		if err := s.event("", string(s.buf)); err != nil {
			return 0, err
		}
		s.buf = s.buf[:0]
		wrote = true
	}
	if wrote {
		s.f.Flush()
	}
	return len(p), nil
}

// event writes one SSE event; the caller holds mu and flushes.
func (s *sseWriter) event(name, data string) error {
	var b strings.Builder
	if name != "" {
		b.WriteString("event: " + name + "\n")
	}
	data = strings.TrimSuffix(data, "\r")
	for _, l := range strings.Split(data, "\n") {
		b.WriteString("data: " + l + "\n")
	}
	b.WriteString("\n")
	_, err := io.WriteString(s.w, b.String())
	return err
}

func (s *sseWriter) end(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) > 0 {
		s.event("", string(s.buf))
		s.buf = nil
	}
	s.event("end", msg)
	s.f.Flush()
}

// keepAlive sends SSE comments so idle streams survive proxy timeouts.
func (s *sseWriter) keepAlive(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			io.WriteString(s.w, ": ping\n\n")
			s.f.Flush()
			s.mu.Unlock()
		}
	}
}

// containerExec opens an interactive shell in a running container over a
// WebSocket. Binary messages carry terminal bytes in both directions; text
// messages from the client are JSON control messages:
//
//	{"type":"resize","cols":120,"rows":40}
//
// The WebSocket library rejects cross-origin handshakes (Origin must match
// Host), which together with the session cookie guards against CSWSH.
func (s *Server) containerExec(w http.ResponseWriter, r *http.Request) {
	st, c, ok := s.loadContainer(w, r)
	if !ok {
		return
	}
	if c.State != "running" {
		httpx.WriteError(w, http.StatusConflict, "container is not running")
		return
	}
	cols := uint16(queryInt(r, "cols", 80, 10, 1000))
	rows := uint16(queryInt(r, "rows", 24, 5, 1000))

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)

	// Not r.Context(): after hijacking, its lifetime is not tied to the socket.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sh, err := s.shell(ctx, c.ID, cols, rows)
	if err != nil {
		s.log.Warn("exec start", "stack", st.Name, "container", c.Name, "err", err)
		conn.Close(websocket.StatusInternalError, truncate("exec failed: "+err.Error(), 120))
		return
	}
	s.log.Info("exec session started", "stack", st.Name, "container", c.Name)

	// terminal → client
	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		buf := make([]byte, 32<<10)
		for {
			n, err := sh.Read(buf)
			if n > 0 {
				wctx, wcancel := context.WithTimeout(ctx, 30*time.Second)
				werr := conn.Write(wctx, websocket.MessageBinary, buf[:n])
				wcancel()
				if werr != nil {
					return
				}
			}
			if err != nil {
				return // EOF/EIO: the process exited
			}
		}
	}()

	// client → terminal
	inDone := make(chan struct{})
	go func() {
		defer close(inDone)
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if _, err := sh.Write(data); err != nil {
					return
				}
				continue
			}
			var msg struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
				sh.Resize(uint16(min(msg.Cols, 1000)), uint16(min(msg.Rows, 1000)))
			}
		}
	}()

	select {
	case <-outDone:
		sh.Close()
		conn.Close(websocket.StatusNormalClosure, "session ended")
	case <-inDone:
		sh.Close() // client disconnected: kill the exec
	}
	cancel()
	s.log.Info("exec session ended", "stack", st.Name, "container", c.Name)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
