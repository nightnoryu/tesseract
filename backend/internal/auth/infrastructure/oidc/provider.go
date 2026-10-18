package oidc

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type Config struct {
	Enabled                                                   bool
	Issuer, DiscoveryURL, ClientID, ClientSecret, RedirectURI string
	Scopes                                                    []string
	InsecureDevelopment                                       bool
}
type Provider struct {
	Issuer   string
	OAuth    oauth2.Config
	verifier *coreoidc.IDTokenVerifier
	client   *http.Client
}

func validURL(raw string, insecure bool) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Host != "" && u.User == nil && u.Fragment == "" && (u.Scheme == "https" || insecure && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))
}
func New(ctx context.Context, c Config) (*Provider, error) {
	if !c.Enabled {
		return nil, nil
	}
	if !validURL(c.Issuer, c.InsecureDevelopment) || !validURL(c.RedirectURI, c.InsecureDevelopment) || c.ClientID == "" || c.ClientSecret == "" {
		return nil, errors.New("invalid_oidc_config")
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx = coreoidc.ClientContext(ctx, client)
	var provider *coreoidc.Provider
	var err error
	if c.DiscoveryURL == "" {
		provider, err = coreoidc.NewProvider(ctx, c.Issuer)
	} else {
		if !validURL(c.DiscoveryURL, c.InsecureDevelopment) {
			return nil, errors.New("invalid_oidc_config")
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, c.DiscoveryURL, nil)
		if e != nil {
			return nil, errors.New("invalid_oidc_config")
		}
		// Use the library discovery decoder through a narrowly scoped transport rewrite.
		discoveryClient := *client
		discoveryClient.Transport = discoveryTransport{base: http.DefaultTransport, from: strings.TrimSuffix(c.Issuer, "/") + "/.well-known/openid-configuration", to: req.URL}
		provider, err = coreoidc.NewProvider(coreoidc.ClientContext(ctx, &discoveryClient), c.Issuer)
	}
	if err != nil {
		return nil, errors.New("oidc_discovery_failed")
	}
	var metadata struct {
		JWKS string `json:"jwks_uri"`
	}
	if provider.Claims(&metadata) != nil || !validURL(metadata.JWKS, c.InsecureDevelopment) || !validURL(provider.Endpoint().AuthURL, c.InsecureDevelopment) || !validURL(provider.Endpoint().TokenURL, c.InsecureDevelopment) {
		return nil, errors.New("invalid_oidc_metadata")
	}
	scopes := []string{coreoidc.ScopeOpenID}
	for _, scope := range c.Scopes {
		if scope != "openid" && scope != "" {
			scopes = append(scopes, scope)
		}
	}
	return &Provider{Issuer: c.Issuer, OAuth: oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, RedirectURL: c.RedirectURI, Endpoint: provider.Endpoint(), Scopes: scopes}, verifier: provider.Verifier(&coreoidc.Config{ClientID: c.ClientID}), client: client}, nil
}

type discoveryTransport struct {
	base http.RoundTripper
	from string
	to   *url.URL
}

func (t discoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() == t.from {
		r = r.Clone(r.Context())
		r.URL = t.to
	}
	return t.base.RoundTrip(r)
}
func (p *Provider) AuthorizationURL(state, nonce, verifier string) string {
	return p.OAuth.AuthCodeURL(state, coreoidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
}
func (p *Provider) Subject(ctx context.Context, code, nonce, verifier string) (string, error) {
	ctx = coreoidc.ClientContext(ctx, p.client)
	token, err := p.OAuth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", errors.New("oidc_login_failed")
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return "", errors.New("oidc_login_failed")
	}
	id, err := p.verifier.Verify(ctx, raw)
	if err != nil || id.Nonce != nonce || id.Subject == "" || len(id.Subject) > 255 {
		return "", errors.New("oidc_login_failed")
	}
	return id.Subject, nil
}
