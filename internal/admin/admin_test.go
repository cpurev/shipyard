package admin

import (
	"context"
	"errors"
	"testing"
)

func TestUnimplemented(t *testing.T) {
	var directory Directory = Unimplemented{}
	if _, err := directory.List(context.Background()); !errors.Is(err, ErrNotImplemented) {
		t.Fatal(err)
	}
	if _, err := directory.Owner(context.Background(), "hello"); !errors.Is(err, ErrNotImplemented) {
		t.Fatal(err)
	}
}
