package db

import (
	"context"
	"errors"
	"testing"
)

func TestUnimplemented(t *testing.T) {
	var manager Manager = Unimplemented{}
	ctx := context.Background()
	if _, err := manager.Provision(ctx, "hello", "europe-west1"); !errors.Is(err, ErrNotImplemented) {
		t.Fatal(err)
	}
	if _, err := manager.Backup(ctx, "hello"); !errors.Is(err, ErrNotImplemented) {
		t.Fatal(err)
	}
	if _, err := manager.ListBackups(ctx, "hello"); !errors.Is(err, ErrNotImplemented) {
		t.Fatal(err)
	}
	if err := manager.Restore(ctx, "hello", "b1"); !errors.Is(err, ErrNotImplemented) {
		t.Fatal(err)
	}
}
