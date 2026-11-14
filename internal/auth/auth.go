// Package auth will be the login gate in front of a deployed internal app.
//
// TODO: implement OpenID Connect for Google Workspace and Microsoft Entra ID,
// and accept a session only when the email domain matches the company's
// domain from config. This skeleton does not put a gate in front of deploys.
// Provider credentials are the operator's, not the end user's.
package auth

import (
	"context"
	"errors"
)

// ErrNotImplemented means the login gate is not built yet.
var ErrNotImplemented = errors.New("auth: login gate is not implemented")

// IdentityProvider is the company identity system.
type IdentityProvider string

const (
	// IdentityGoogle is Google Workspace.
	IdentityGoogle IdentityProvider = "google"
	// IdentityMicrosoft is Microsoft Entra ID.
	IdentityMicrosoft IdentityProvider = "microsoft"
)

// Session is a signed-in employee.
type Session struct {
	Email    string
	Domain   string
	Provider IdentityProvider
}

// Gate checks that the caller is an employee of the company domain.
type Gate interface {
	Login(ctx context.Context) (*Session, error)
	Authorize(ctx context.Context, session *Session, orgDomain string) error
}

// Unimplemented is the skeleton gate. Every method returns ErrNotImplemented.
type Unimplemented struct{}

func (Unimplemented) Login(context.Context) (*Session, error) {
	return nil, ErrNotImplemented
}

func (Unimplemented) Authorize(context.Context, *Session, string) error {
	return ErrNotImplemented
}
