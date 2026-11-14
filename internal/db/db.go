// Package db will attach one database to each app and take backups of it.
//
// TODO: choose an engine on the operator's own cloud account (Cloud SQL, or
// a provider-native Postgres) and implement provision, backup, and restore.
// Nothing in this skeleton creates a database or a backup.
package db

import (
	"context"
	"errors"
	"time"
)

// ErrNotImplemented means database provisioning is not built yet.
var ErrNotImplemented = errors.New("db: per-app database is not implemented")

// Database is the connection information for one app.
type Database struct {
	App      string
	Engine   string
	Region   string
	Endpoint string
}

// Backup is one stored snapshot.
type Backup struct {
	ID        string
	App       string
	CreatedAt time.Time
}

// Manager provisions a database per app and backs it up.
type Manager interface {
	Provision(ctx context.Context, app, region string) (*Database, error)
	Backup(ctx context.Context, app string) (*Backup, error)
	ListBackups(ctx context.Context, app string) ([]Backup, error)
	Restore(ctx context.Context, app, backupID string) error
}

// Unimplemented is the skeleton database manager.
type Unimplemented struct{}

func (Unimplemented) Provision(context.Context, string, string) (*Database, error) {
	return nil, ErrNotImplemented
}

func (Unimplemented) Backup(context.Context, string) (*Backup, error) {
	return nil, ErrNotImplemented
}

func (Unimplemented) ListBackups(context.Context, string) ([]Backup, error) {
	return nil, ErrNotImplemented
}

func (Unimplemented) Restore(context.Context, string, string) error {
	return ErrNotImplemented
}
