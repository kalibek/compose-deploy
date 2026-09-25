package deployer

import (
	"bytes"
	"sync"

	"github.com/compose-deploy/api/internal/runner"
)

const (
	logTail   = 64 << 10 // bytes kept in last_log
	logRawCap = 1 << 20  // raw buffer cap before trimming
)

// logBuf collects combined command output. Secrets are scrubbed on read and
// only the tail is returned.
type logBuf struct {
	mu      sync.Mutex
	buf     []byte
	secrets []string
	dirty   bool
}

func newLogBuf(secrets ...string) *logBuf { return &logBuf{secrets: secrets} }

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	if len(l.buf) > logRawCap {
		cut := len(l.buf) - logRawCap/2
		// cut at a newline so a secret is never split across the boundary
		if i := bytes.IndexByte(l.buf[cut:], '\n'); i >= 0 {
			cut += i + 1
		}
		l.buf = append([]byte(nil), l.buf[cut:]...)
	}
	l.dirty = true
	return len(p), nil
}

func (l *logBuf) addSecrets(s ...string) {
	l.mu.Lock()
	l.secrets = append(l.secrets, s...)
	l.mu.Unlock()
}

// String returns the scrubbed tail (at most logTail bytes).
func (l *logBuf) String() string {
	l.mu.Lock()
	s := runner.Scrub(string(l.buf), l.secrets...)
	l.mu.Unlock()
	if len(s) > logTail {
		s = s[len(s)-logTail:]
		if i := bytes.IndexByte([]byte(s), '\n'); i >= 0 && i < 1024 {
			s = s[i+1:]
		}
		s = "...(truncated)...\n" + s
	}
	return s
}

// takeDirty reports whether new output arrived since the last call.
func (l *logBuf) takeDirty() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.dirty
	l.dirty = false
	return d
}
