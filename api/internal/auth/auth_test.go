package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	a := New("pw", "secret", false)
	now := time.Unix(1_700_000_000, 0)
	a.now = func() time.Time { return now }

	tok := a.Sign(now.Add(time.Hour))
	if !a.Verify(tok) {
		t.Fatal("valid token rejected")
	}
	// expiry
	a.now = func() time.Time { return now.Add(2 * time.Hour) }
	if a.Verify(tok) {
		t.Fatal("expired token accepted")
	}
	a.now = func() time.Time { return now }

	// tampered payload: re-encode a later expiry with old signature
	other := a.Sign(now.Add(100 * time.Hour))
	p1, s1, _ := strings.Cut(tok, ".")
	p2, _, _ := strings.Cut(other, ".")
	if a.Verify(p2 + "." + s1) {
		t.Fatal("tampered payload accepted")
	}
	if a.Verify(p1 + ".AAAA") {
		t.Fatal("bad signature accepted")
	}
	for _, bad := range []string{"", ".", "abc", "a.b.c", "!!.!!"} {
		if a.Verify(bad) {
			t.Fatalf("garbage %q accepted", bad)
		}
	}
	// different secret
	b := New("pw", "other", false)
	b.now = a.now
	if b.Verify(tok) {
		t.Fatal("token from other secret accepted")
	}
}

func TestCheckPassword(t *testing.T) {
	a := New("hunter2", "s", false)
	if !a.CheckPassword("hunter2") || a.CheckPassword("hunter3") || a.CheckPassword("") {
		t.Fatal("password check wrong")
	}
}

func TestLoginAndRateLimit(t *testing.T) {
	a := New("pw", "s", true)
	do := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
		r.RemoteAddr = "1.2.3.4:5555"
		w := httptest.NewRecorder()
		a.Login(w, r)
		return w
	}
	w := do(`{"password":"pw"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("login: %d", w.Code)
	}
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Name != CookieName || !c[0].HttpOnly || !c[0].Secure || c[0].SameSite != http.SameSiteLaxMode || c[0].Path != "/" {
		t.Fatalf("bad cookie %+v", c)
	}
	for i := 0; i < loginMaxFailures; i++ {
		if w := do(`{"password":"no"}`); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	if w := do(`{"password":"pw"}`); w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	a.now = func() time.Time { return time.Now().Add(loginWindow + time.Second) }
	if w := do(`{"password":"pw"}`); w.Code != http.StatusNoContent {
		t.Fatalf("expected success after window, got %d", w.Code)
	}
}

func TestMiddleware(t *testing.T) {
	a := New("pw", "s", false)
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) }))
	r := httptest.NewRequest("GET", "/api/stacks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("got %d", w.Code)
	}
	r.AddCookie(&http.Cookie{Name: CookieName, Value: a.Sign(time.Now().Add(time.Minute))})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 299 {
		t.Fatalf("got %d", w.Code)
	}
}
