package httpapi

import (
	"context"
	"encoding/json"
	auth "github.com/nightnoryu/tesseract/internal/auth/app"
	"github.com/nightnoryu/tesseract/internal/auth/domain"
	oidc "github.com/nightnoryu/tesseract/internal/auth/infrastructure/oidc"
	"github.com/nightnoryu/tesseract/internal/auth/infrastructure/oidctest"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type oidcStore struct {
	*authStore
	flow auth.OIDCAttempt
}

func (s *oidcStore) SaveOIDCAttempt(_ context.Context, a auth.OIDCAttempt) error {
	s.flow = a
	return nil
}
func (s *oidcStore) ConsumeOIDCAttempt(_ context.Context, state, browser string) (auth.OIDCAttempt, error) {
	a := s.flow
	if state != a.StateHash || browser != a.BrowserHash || !a.Expires.After(time.Now()) {
		return a, domain.ErrCredentials
	}
	s.flow = auth.OIDCAttempt{}
	return a, nil
}
func (s *oidcStore) IssueOIDC(ctx context.Context, issuer, subject, token, old string, expires time.Time) (domain.Session, error) {
	if subject != "subject" || s.account.Disabled {
		return domain.Session{}, domain.ErrCredentials
	}
	s.Issue(ctx, s.account, token, old, expires)
	return domain.Session{ID: s.account.ID, Email: s.account.Email, Expires: expires}, nil
}
func TestOIDCHTTP(t *testing.T) {
	fake := oidctest.New(t)
	provider, err := oidc.New(context.Background(), oidc.Config{Enabled: true, Issuer: fake.Issuer, ClientID: "client", ClientSecret: "secret", RedirectURI: "http://localhost/api/v1/auth/oidc/callback", InsecureDevelopment: true})
	if err != nil {
		t.Fatal(err)
	}
	store := &oidcStore{authStore: &authStore{account: domain.Account{ID: "00000000-0000-0000-0000-000000000001", Email: "local@example.com"}, tokens: map[string]time.Time{}}}
	server := New(func(context.Context) error { return nil }, &capture{}, telemetry.New(), CatalogConfig{Auth: &AuthConfig{Service: auth.New(store), Origin: "https://app.example", OIDC: provider, OIDCStore: store}})
	call := func(method, path, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/api/v1/auth/oidc/start", "https://evil.example"); w.Code != 403 {
		t.Fatal("start CSRF", w.Code)
	}
	for _, kind := range []string{"success", "state", "browser", "expired", "disabled", "nonce"} {
		t.Run(kind, func(t *testing.T) {
			store.account.Disabled = false
			w := call("POST", "/api/v1/auth/oidc/start", "https://app.example")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var body struct {
				URL string `json:"authorization_url"`
			}
			json.Unmarshal(w.Body.Bytes(), &body)
			u, _ := url.Parse(body.URL)
			state := u.Query().Get("state")
			cookie := w.Result().Cookies()[0]
			if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
				t.Fatal("unsafe transaction cookie")
			}
			fake.Nonce = ""
			callback := fake.Authorize(t, body.URL)
			switch kind {
			case "state":
				state = "wrong"
			case "browser":
				cookie.Value = "wrong"
			case "expired":
				store.flow.Expires = time.Now().Add(-time.Hour)
			case "disabled":
				store.account.Disabled = true
			case "nonce":
				fake.Nonce = "wrong"
			}
			path := "/api/v1/auth/oidc/callback?state=" + url.QueryEscape(state) + "&code=" + url.QueryEscape(callback.Query().Get("code"))
			w = call("GET", path, "", cookie)
			if w.Code != 302 {
				t.Fatal(w.Code, w.Body.String())
			}
			if kind != "success" {
				if w.Header().Get("Location") != "https://app.example/?sso_error=1" {
					t.Fatal("invalid flow accepted")
				}
				return
			}
			var session *http.Cookie
			for _, c := range w.Result().Cookies() {
				if c.Name == "tesseract_session" {
					session = c
				}
			}
			if session == nil {
				t.Fatal("no session")
			}
			w = call("GET", "/api/v1/auth/session", "", session)
			if w.Code != 200 {
				t.Fatal("no local session", w.Code)
			}
			if w = call("GET", path, "", cookie); w.Header().Get("Location") != "https://app.example/?sso_error=1" {
				t.Fatal("replay accepted")
			}
			if w = call("POST", "/api/v1/auth/logout", "https://app.example", session); w.Code != 204 {
				t.Fatal(w.Code)
			}
			if w = call("GET", "/api/v1/auth/session", "", session); w.Code != 401 {
				t.Fatal("logout failed")
			}
		})
	}
}
