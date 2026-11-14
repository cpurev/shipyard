// Package credentials stores provider tokens on the operator's machine.
// The default backend is a file created with mode 0600. The OS keychain is
// an interface with a stub implementation.
package credentials

import (
	"context"
	"errors"

	"github.com/cpurev/shipyard/internal/providers"
)

// ErrKeychainNotSupported is returned by the OS keychain stub.
var ErrKeychainNotSupported = errors.New("os keychain is not implemented")

// Store persists credentials for one operator.
type Store interface {
	Save(ctx context.Context, provider string, creds providers.Credentials) error
	Load(ctx context.Context, provider string) (providers.Credentials, bool, error)
	LoadAll(ctx context.Context) (map[string]providers.Credentials, error)
}

// OSKeyring is the keychain backend.
//
// TODO: implement macOS Keychain and Linux libsecret. FileStore is the
// default so a 0600 file works in CI and on machines without a keychain.
type OSKeyring struct{}

func (OSKeyring) Save(context.Context, string, providers.Credentials) error {
	return ErrKeychainNotSupported
}

func (OSKeyring) Load(context.Context, string) (providers.Credentials, bool, error) {
	return providers.Credentials{}, false, ErrKeychainNotSupported
}

func (OSKeyring) LoadAll(context.Context) (map[string]providers.Credentials, error) {
	return nil, ErrKeychainNotSupported
}
