package auth

import (
	"context"
	"errors"
)

var ErrInvalidToken = errors.New("invalid access token")

type Identity struct {
	ClientID string
	Provider bool
	Internal bool
}

// Authenticator is the contract consumed by transport. Implementations verify
// credentials before returning an identity; no JWT types enter the domain.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (Identity, error)
}
