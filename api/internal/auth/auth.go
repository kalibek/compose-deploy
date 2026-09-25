// Package auth implements single-admin password login with HMAC-signed
// session cookies.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/compose-deploy/api/internal/httpx"
)

const (
	CookieName = "cd_session"
	SessionTTL = 7 * 24 * time.Hour

	loginMaxFailures = 10
	loginWindow      = 5 * time.Minute
)

type Auth struct {
	passwordHash [32]byte
	key          []byte
	secure       bool
	now          func() time.Time
	limiter      *limiter
}

// New creates an Auth. The session signing key is derived from secret
// independently from the encryption key, and also from the password so that
// changing ADMIN_PASSWORD invalidates all existing sessions.
func New(password, secret string, cookieSecure bool) *Auth {
	pw := sha256.Sum256([]byte(password))
	k := sha256.Sum256(append([]byte("session:"+secret+"\x00"), pw[:]...))
	return &Auth{
		passwordHash: sha256.Sum256([]byte(password)),
		key:          k[:],
		secure:       cookieSecure,
		now:          time.Now,
		limiter:      newLimiter(loginMaxFailures, loginWindow),
	}
}

// CheckPassword compares in constant time (hashing first so lengths match).
func (a *Auth) CheckPassword(pw string) bool {
	h := sha256.Sum256([]byte(pw))
	return subtle.ConstantTimeCompare(h[:], a.passwordHash[:]) == 1
}

func (a *Auth) mac(payload string) []byte {
	m := hmac.New(sha256.New, a.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// Sign returns a token "base64url(expiryUnix).base64url(hmac)".
func (a *Auth) Sign(expiry time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(expiry.Unix(), 10)))
	return payload + "." + base64.RawURLEncoding.EncodeToString(a.mac(payload))
}

// Verify checks signature and expiry.
func (a *Auth) Verify(token string) bool {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || payload == "" || sig == "" {
		return false
	}
	gotSig, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	if !hmac.Equal(gotSig, a.mac(payload)) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return false
	}
	exp, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return false
	}
	return a.now().Unix() < exp
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// Login handles POST /api/auth/login.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !a.limiter.allow(ip, a.now()) {
		w.Header().Set("Retry-After", strconv.Itoa(int(loginWindow.Seconds())))
		httpx.WriteError(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.CheckPassword(body.Password) {
		a.limiter.fail(ip, a.now())
		httpx.WriteError(w, http.StatusUnauthorized, "invalid password")
		return
	}
	a.limiter.reset(ip)
	exp := a.now().Add(SessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    a.Sign(exp),
		Path:     "/",
		Expires:  exp,
		MaxAge:   int(SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// Logout handles POST /api/auth/logout.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// Me handles GET /api/auth/me (mounted behind Middleware).
func (a *Auth) Me(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

// Middleware rejects requests without a valid session cookie with 401.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil || !a.Verify(c.Value) {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- limiter

type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) prune(ip string, now time.Time) []time.Time {
	h := l.hits[ip]
	i := 0
	for i < len(h) && now.Sub(h[i]) >= l.window {
		i++
	}
	h = h[i:]
	if len(h) == 0 {
		delete(l.hits, ip)
	} else {
		l.hits[ip] = h
	}
	return h
}

func (l *limiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	// opportunistic global cleanup to bound memory
	if len(l.hits) > 10000 {
		for k := range l.hits {
			l.prune(k, now)
		}
	}
	return len(l.prune(ip, now)) < l.max
}

func (l *limiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits[ip] = append(l.prune(ip, now), now)
}

func (l *limiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, ip)
}
