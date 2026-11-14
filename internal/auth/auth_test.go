package auth

import (
	"context"
	"errors"
	"testing"
)

func TestUnimplemented(t *testing.T) {
	var gate Gate = Unimplemented{}
	if _, err := gate.Login(context.Background()); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("login err = %v", err)
	}
	if err := gate.Authorize(context.Background(), &Session{Email: "a@example.com"}, "example.com"); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("authorize err = %v", err)
	}
}
