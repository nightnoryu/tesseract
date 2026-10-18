package httpapi

import (
	"context"
	auth "github.com/nightnoryu/tesseract/internal/auth/app"
	"github.com/nightnoryu/tesseract/internal/auth/domain"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type authStore struct {
	account  domain.Account
	tokens   map[string]time.Time
	attempts int
}

func (s *authStore) Account(context.Context, string) (domain.Account, error) { return s.account, nil }
func (s *authStore) Allow(context.Context, string, int) (bool, error) {
	s.attempts++
	return s.attempts <= 40, nil
}
func (s *authStore) Issue(_ context.Context, a domain.Account, token, old string, expires time.Time) error {
	delete(s.tokens, old)
	s.tokens[token] = expires
	return nil
}
func (s *authStore) Session(_ context.Context, token string) (domain.Session, error) {
	expires, ok := s.tokens[token]
	if !ok || !expires.After(time.Now()) || s.account.Disabled {
		return domain.Session{}, domain.ErrCredentials
	}
	return domain.Session{ID: s.account.ID, Email: s.account.Email, Expires: expires}, nil
}
func (s *authStore) Revoke(_ context.Context, token string) error {
	delete(s.tokens, token)
	return nil
}
func (s *authStore) Change(_ context.Context, _, old, hash string) error {
	if old != s.account.Hash {
		return domain.ErrCredentials
	}
	s.account.Hash = hash
	clear(s.tokens)
	return nil
}
func TestPasswordSessionHTTP(t *testing.T) {
	hash, _ := auth.Hash("correct horse battery staple")
	store := &authStore{account: domain.Account{ID: "00000000-0000-0000-0000-000000000001", Email: "user@example.com", Hash: hash}, tokens: map[string]time.Time{}}
	server := New(func(context.Context) error { return nil }, &capture{}, telemetry.New(), CatalogConfig{Auth: &AuthConfig{Service: auth.New(store), Origin: "https://app.example.com"}})
	call := func(method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	login := `{"email":"user@example.com","password":"correct horse battery staple"}`
	if w := call("POST", "/api/v1/auth/login", login, "https://evil.example", nil); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/api/v1/auth/login", `{"email":"user@example.com","password":"wrong"}`, "https://app.example.com", nil); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := call("POST", "/api/v1/auth/login", login, "https://app.example.com", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || len(c.Value) != 52 {
		t.Fatal("insecure cookie", c)
	}
	if w := call("GET", "/api/v1/auth/session", "", "", c); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("POST", "/api/v1/auth/login", login, "https://app.example.com", c)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	next := w.Result().Cookies()[0]
	if w := call("GET", "/api/v1/auth/session", "", "", c); w.Code != 401 {
		t.Fatal("old session survived rotation")
	}
	store.tokens[auth.Digest(next.Value)] = time.Now().Add(-time.Second)
	if w := call("GET", "/api/v1/auth/session", "", "", next); w.Code != 401 {
		t.Fatal("expired session accepted")
	}
	w = call("POST", "/api/v1/auth/login", login, "https://app.example.com", nil)
	c = w.Result().Cookies()[0]
	if w := call("POST", "/api/v1/auth/password", `{"current_password":"correct horse battery staple","new_password":"replacement password"}`, "https://app.example.com", c); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(store.tokens) != 0 {
		t.Fatal("password change did not revoke sessions")
	}
	w = call("POST", "/api/v1/auth/login", `{"email":"user@example.com","password":"replacement password"}`, "https://app.example.com", nil)
	c = w.Result().Cookies()[0]
	if w := call("POST", "/api/v1/auth/logout", "", "https://app.example.com", c); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("GET", "/api/v1/auth/session", "", "", c); w.Code != 401 {
		t.Fatal("logged out session accepted")
	}
	store.attempts = 40
	if w := call("POST", "/api/v1/auth/login", login, "https://app.example.com", nil); w.Code != 429 {
		t.Fatal("rate limit not enforced", w.Code)
	}
}
func TestOriginConfiguration(t *testing.T) {
	for _, tc := range []struct {
		origin     string
		dev, valid bool
	}{{"https://app.example.com", false, true}, {"http://app.example.com", true, false}, {"http://127.0.0.1:5173", true, true}, {"http://127.0.0.1:5173", false, false}, {"", false, false}} {
		if err := (AuthConfig{Origin: tc.origin, InsecureDevelopment: tc.dev}).Validate(); (err == nil) != tc.valid {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}
