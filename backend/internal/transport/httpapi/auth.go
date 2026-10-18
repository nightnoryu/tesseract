package httpapi

import (
	"context"
	"errors"
	"github.com/google/uuid"
	auth "github.com/nightnoryu/tesseract/internal/auth/app"
	"github.com/nightnoryu/tesseract/internal/auth/domain"
	oidc "github.com/nightnoryu/tesseract/internal/auth/infrastructure/oidc"
	"github.com/nightnoryu/tesseract/internal/generated/api"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type AuthConfig struct {
	Service             *auth.Service
	OIDC                *oidc.Provider
	OIDCStore           auth.OIDCStore
	Origin              string
	InsecureDevelopment bool
}

func (c AuthConfig) Validate() error {
	u, err := url.Parse(c.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("invalid_auth_origin")
	}
	if u.Scheme == "https" && !c.InsecureDevelopment {
		return nil
	}
	if c.InsecureDevelopment && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1") {
		return nil
	}
	return errors.New("invalid_auth_origin")
}

type authHTTPKey struct{}
type sessionKey struct{}
type authHTTP struct {
	w http.ResponseWriter
	r *http.Request
}

func authBoundary(next http.Handler, c *AuthConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && c != nil {
				if r.Header.Get("Origin") != c.Origin || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
					respond(w, 403, envelope(r.Context(), "csrf_rejected", "The request origin is not allowed."))
					return
				}
			}
		}
		ctx := context.WithValue(r.Context(), authHTTPKey{}, authHTTP{w, r})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (h *contract) HandleSessionCookie(ctx context.Context, _ api.OperationName, cookie api.SessionCookie) (context.Context, error) {
	if h.auth == nil || h.auth.Service == nil || len(cookie.APIKey) != 52 {
		return ctx, domain.ErrCredentials
	}
	session, err := h.auth.Service.Store.Session(ctx, auth.Digest(cookie.APIKey))
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, sessionKey{}, session), nil
}
func sessionResponse(s domain.Session) *api.Session {
	return &api.Session{UserID: uuid.MustParse(s.ID), Email: s.Email, ExpiresAt: s.Expires}
}
func (h *contract) GetSession(ctx context.Context) (*api.Session, error) {
	return sessionResponse(ctx.Value(sessionKey{}).(domain.Session)), nil
}
func (h *contract) cookie(ctx context.Context, token string, expires time.Time) {
	maxAge := int(time.Until(expires).Seconds())
	if token == "" {
		maxAge = -1
	}
	http.SetCookie(ctx.Value(authHTTPKey{}).(authHTTP).w, &http.Cookie{Name: "tesseract_session", Value: token, Path: "/", HttpOnly: true, Secure: !h.auth.InsecureDevelopment, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: maxAge})
}
func (h *contract) Login(ctx context.Context, req *api.LoginRequest) (*api.Session, error) {
	if h.auth == nil {
		return nil, errors.New("auth_unavailable")
	}
	r := ctx.Value(authHTTPKey{}).(authHTTP).r
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	old := ""
	if c, err := r.Cookie("tesseract_session"); err == nil {
		old = c.Value
	}
	session, token, err := h.auth.Service.Login(ctx, req.Email, req.Password, ip, old)
	if err != nil {
		return nil, err
	}
	h.cookie(ctx, token, session.Expires)
	return sessionResponse(session), nil
}
func (h *contract) Logout(ctx context.Context) error {
	r := ctx.Value(authHTTPKey{}).(authHTTP).r
	c, err := r.Cookie("tesseract_session")
	if err != nil {
		return domain.ErrCredentials
	}
	if err := h.auth.Service.Store.Revoke(ctx, auth.Digest(c.Value)); err != nil {
		return err
	}
	h.cookie(ctx, "", time.Unix(1, 0))
	return nil
}
func (h *contract) ChangePassword(ctx context.Context, req *api.ChangePasswordReq) error {
	session := ctx.Value(sessionKey{}).(domain.Session)
	if err := h.auth.Service.Change(ctx, session, req.CurrentPassword, req.NewPassword); err != nil {
		return err
	}
	h.cookie(ctx, "", time.Unix(1, 0))
	return nil
}
