package httpapi

import (
	"context"
	"crypto/rand"
	auth "github.com/nightnoryu/tesseract/internal/auth/app"
	"github.com/nightnoryu/tesseract/internal/auth/domain"
	"github.com/nightnoryu/tesseract/internal/generated/api"
	"golang.org/x/oauth2"
	"net"
	"net/http"
	"time"
)

const oidcCookie = "tesseract_oidc"

func (h *contract) GetAuthOptions(context.Context) (*api.GetAuthOptionsOK, error) {
	return &api.GetAuthOptionsOK{Password: true, Oidc: h.auth != nil && h.auth.OIDC != nil}, nil
}
func (h *contract) flowCookie(ctx context.Context, value string, expires time.Time) {
	age := int(time.Until(expires).Seconds())
	if value == "" {
		age = -1
	}
	http.SetCookie(ctx.Value(authHTTPKey{}).(authHTTP).w, &http.Cookie{Name: oidcCookie, Value: value, Path: "/api/v1/auth/oidc", HttpOnly: true, Secure: !h.auth.InsecureDevelopment, SameSite: http.SameSiteLaxMode, MaxAge: age, Expires: expires})
}
func (h *contract) StartOIDC(ctx context.Context) (*api.StartOIDCOK, error) {
	if h.auth == nil || h.auth.OIDC == nil {
		return nil, domain.ErrCredentials
	}
	r := ctx.Value(authHTTPKey{}).(authHTTP).r
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	ok, err := h.auth.Service.Store.Allow(ctx, auth.Digest("oidc:"+ip), 50)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.ErrLimited
	}
	state, browser, nonce := rand.Text()+rand.Text(), rand.Text()+rand.Text(), rand.Text()+rand.Text()
	verifier := oauth2.GenerateVerifier()
	old := ""
	if c, err := r.Cookie("tesseract_session"); err == nil {
		old = c.Value
	}
	expires := time.Now().UTC().Add(5 * time.Minute)
	err = h.auth.OIDCStore.SaveOIDCAttempt(ctx, auth.OIDCAttempt{StateHash: auth.Digest(state), BrowserHash: auth.Digest(browser), Nonce: nonce, Verifier: verifier, OldSessionHash: auth.Digest(old), Expires: expires})
	if err != nil {
		return nil, err
	}
	h.flowCookie(ctx, browser, expires)
	return &api.StartOIDCOK{AuthorizationURL: h.auth.OIDC.AuthorizationURL(state, nonce, verifier)}, nil
}
func (h *contract) CallbackOIDC(ctx context.Context, p api.CallbackOIDCParams) (*api.CallbackOIDCFound, error) {
	if h.auth == nil {
		return nil, domain.ErrCredentials
	}
	failure := &api.CallbackOIDCFound{Location: h.auth.Origin + "/?sso_error=1"}
	if h.auth.OIDC == nil {
		return failure, nil
	}
	h.flowCookie(ctx, "", time.Unix(1, 0))
	r := ctx.Value(authHTTPKey{}).(authHTTP).r
	c, err := r.Cookie(oidcCookie)
	if err != nil || len(c.Value) != 52 || len(p.State.Value) != 52 {
		return failure, nil
	}
	a, err := h.auth.OIDCStore.ConsumeOIDCAttempt(ctx, auth.Digest(p.State.Value), auth.Digest(c.Value))
	if err != nil {
		return failure, nil
	}
	if p.Error.Value != "" || p.Code.Value == "" {
		return failure, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	subject, err := h.auth.OIDC.Subject(ctx, p.Code.Value, a.Nonce, a.Verifier)
	if err != nil {
		return failure, nil
	}
	token := rand.Text() + rand.Text()
	expires := time.Now().UTC().Add(12 * time.Hour)
	session, err := h.auth.OIDCStore.IssueOIDC(ctx, h.auth.OIDC.Issuer, subject, auth.Digest(token), a.OldSessionHash, expires)
	if err != nil {
		return failure, nil
	}
	h.cookie(ctx, token, session.Expires)
	return &api.CallbackOIDCFound{Location: h.auth.Origin + "/"}, nil
}
