// Package admin will be the IT view of every internal app and its owner.
//
// TODO: aggregate apps across providers and attach the owner from the login
// gate or the audit log. There is no org-wide store in this skeleton, so the
// directory refuses to answer rather than invent an empty company.
package admin

import (
	"context"
	"errors"
)

// ErrNotImplemented means the admin directory is not built yet.
var ErrNotImplemented = errors.New("admin: app directory is not implemented")

// App is one internal tool and the person responsible for it.
type App struct {
	Name     string
	Provider string
	Owner    string
	URL      string
	Region   string
}

// Directory lists apps for an IT reviewer.
type Directory interface {
	List(ctx context.Context) ([]App, error)
	Owner(ctx context.Context, app string) (string, error)
}

// Unimplemented is the skeleton directory.
type Unimplemented struct{}

func (Unimplemented) List(context.Context) ([]App, error) {
	return nil, ErrNotImplemented
}

func (Unimplemented) Owner(context.Context, string) (string, error) {
	return "", ErrNotImplemented
}
