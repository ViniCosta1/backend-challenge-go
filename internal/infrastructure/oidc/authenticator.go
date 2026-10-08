package oidc

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/vinicosta1/backend-challenge-go/internal/auth"
)

type Authenticator struct{ verifier *coreoidc.IDTokenVerifier }

func NewAuthenticator(ctx context.Context, issuer, audience string) (*Authenticator, error) {
	if issuer == "" || audience == "" {
		return nil, fmt.Errorf("OIDC issuer and audience are required")
	}
	// Bound discovery and subsequent JWKS refresh requests even when the
	// caller's context has no deadline. The library preserves this client.
	ctx = coreoidc.ClientContext(ctx, &http.Client{Timeout: 5 * time.Second})
	provider, err := coreoidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	return &Authenticator{verifier: provider.Verifier(&coreoidc.Config{
		ClientID: audience, SupportedSigningAlgs: []string{coreoidc.RS256},
	})}, nil
}

func (a *Authenticator) Authenticate(ctx context.Context, raw string) (auth.Identity, error) {
	// go-oidc validates exact issuer, audience, exp and signature using cached
	// remote JWKS with refresh on key rotation. No verification is disabled.
	token, err := a.verifier.Verify(ctx, raw)
	if err != nil {
		return auth.Identity{}, fmt.Errorf("verify bearer token: %w: %v", auth.ErrInvalidToken, err)
	}
	var claims struct {
		AuthorizedParty string `json:"azp"`
		Type            string `json:"typ"`
		RealmAccess     struct {
			Roles []string `json:"roles"`
		} `json:"realm_access"`
	}
	if err := token.Claims(&claims); err != nil || claims.AuthorizedParty == "" || claims.Type != "Bearer" {
		return auth.Identity{}, auth.ErrInvalidToken
	}
	return auth.Identity{ClientID: claims.AuthorizedParty,
		Provider: slices.Contains(claims.RealmAccess.Roles, "provider"),
		Internal: slices.Contains(claims.RealmAccess.Roles, "internal")}, nil
}

var _ auth.Authenticator = (*Authenticator)(nil)
