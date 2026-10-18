// Package oidctest provides a local signed-token provider for authentication tests.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	jose "github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

type Provider struct {
	Server                           *httptest.Server
	Nonce, Issuer, Audience, Subject string
	Expiry                           time.Time
	BadSignature                     bool
}

func New(t *testing.T) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	codes := map[string]url.Values{}
	p := &Provider{Audience: "client", Subject: "subject", Expiry: time.Now().Add(time.Hour)}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration", "/discovery":
			json.NewEncoder(w).Encode(map[string]any{"issuer": p.Issuer, "authorization_endpoint": p.Server.URL + "/authorize", "token_endpoint": p.Server.URL + "/token", "jwks_uri": p.Server.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}}})
		case "/authorize":
			q := r.URL.Query()
			if q.Get("client_id") != "client" || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" || q.Get("state") == "" {
				http.Error(w, "invalid authorization", 400)
				return
			}
			target, err := url.Parse(q.Get("redirect_uri"))
			if err != nil || target.Host != "localhost" {
				http.Error(w, "invalid redirect", 400)
				return
			}
			code := rand.Text()
			mu.Lock()
			codes[code] = q
			mu.Unlock()
			values := target.Query()
			values.Set("code", code)
			values.Set("state", q.Get("state"))
			target.RawQuery = values.Encode()
			http.Redirect(w, r, target.String(), http.StatusFound)
		case "/token":
			r.ParseForm()
			client, secret, _ := r.BasicAuth()
			mu.Lock()
			flow, ok := codes[r.Form.Get("code")]
			delete(codes, r.Form.Get("code"))
			mu.Unlock()
			if !ok || client != "client" || secret != "secret" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != flow.Get("code_challenge") || r.Form.Get("redirect_uri") != flow.Get("redirect_uri") || r.Form.Get("grant_type") != "authorization_code" {
				http.Error(w, "invalid", 400)
				return
			}
			signingKey := key
			if p.BadSignature {
				signingKey = bad
			}
			signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signingKey}, (&jose.SignerOptions{}).WithHeader("kid", "test"))
			if e != nil {
				t.Error(e)
				return
			}
			nonce := flow.Get("nonce")
			if p.Nonce != "" {
				nonce = p.Nonce
			}
			claims, _ := json.Marshal(map[string]any{"iss": p.Issuer, "sub": p.Subject, "aud": p.Audience, "exp": p.Expiry.Unix(), "iat": time.Now().Unix(), "nonce": nonce, "email": "changed@example.com"})
			signed, e := signer.Sign(claims)
			if e != nil {
				t.Error(e)
				return
			}
			raw, e := signed.CompactSerialize()
			if e != nil {
				t.Error(e)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "token_type": "Bearer", "id_token": raw})
		default:
			http.NotFound(w, r)
		}
	}))
	p.Issuer = p.Server.URL
	t.Cleanup(p.Server.Close)
	return p
}

// Authorize follows the browser's outbound redirect but stops at the application callback.
func (p *Provider) Authorize(t *testing.T, authorizationURL string) *url.URL {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatal("authorization failed", response.StatusCode)
	}
	target, err := response.Location()
	if err != nil {
		t.Fatal(err)
	}
	return target
}
