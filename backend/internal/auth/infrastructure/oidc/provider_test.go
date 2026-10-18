package oidc

import (
	"context"
	"github.com/nightnoryu/tesseract/internal/auth/infrastructure/oidctest"
	"golang.org/x/oauth2"
	"net/url"
	"testing"
	"time"
)

func TestProviderValidation(t *testing.T) {
	for _, kind := range []string{"success", "nonce", "issuer", "audience", "expired", "signature", "subject", "pkce", "custom_discovery"} {
		t.Run(kind, func(t *testing.T) {
			fake := oidctest.New(t)

			config := Config{Enabled: true, Issuer: fake.Issuer, ClientID: "client", ClientSecret: "secret", RedirectURI: "http://localhost/api/v1/auth/oidc/callback", InsecureDevelopment: true}
			if kind == "custom_discovery" {
				config.DiscoveryURL = fake.Server.URL + "/discovery"
			}
			provider, err := New(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(provider.AuthorizationURL("state", "nonce", "verifier"))
			q := u.Query()
			if q.Get("state") != "state" || q.Get("nonce") != "nonce" || q.Get("code_challenge") != oauth2.S256ChallengeFromVerifier("verifier") || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" {
				t.Fatal("missing flow protections", q)
			}
			callback := fake.Authorize(t, u.String())
			verifier := "verifier"
			switch kind {
			case "pkce":
				verifier = "incorrect"
			case "nonce":
				fake.Nonce = "wrong"
			case "issuer":
				fake.Issuer = "https://unexpected.example"
			case "audience":
				fake.Audience = "other"
			case "expired":
				fake.Expiry = time.Now().Add(-time.Hour)
			case "signature":
				fake.BadSignature = true
			case "subject":
				fake.Subject = ""
			}
			subject, err := provider.Subject(context.Background(), callback.Query().Get("code"), "nonce", verifier)
			success := kind == "success" || kind == "custom_discovery"
			if success && (err != nil || subject != "subject") {
				t.Fatal(subject, err)
			}
			if success {
				if _, err := provider.Subject(context.Background(), callback.Query().Get("code"), "nonce", verifier); err == nil {
					t.Fatal("authorization code replay accepted")
				}
			}
			if !success && err == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
	t.Run("discovery issuer mismatch", func(t *testing.T) {
		fake := oidctest.New(t)
		fake.Issuer = "https://other.example"
		if _, err := New(context.Background(), Config{Enabled: true, Issuer: fake.Server.URL, ClientID: "client", ClientSecret: "secret", RedirectURI: "http://localhost/callback", InsecureDevelopment: true}); err == nil {
			t.Fatal("issuer mismatch accepted")
		}
	})
	if p, err := New(context.Background(), Config{}); p != nil || err != nil {
		t.Fatal("disabled SSO")
	}
}
