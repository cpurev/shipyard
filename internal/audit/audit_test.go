package audit

import (
	"context"
	"testing"
)

func TestMemoryLogger(t *testing.T) {
	logger := NewMemory()
	ctx := context.Background()
	if err := logger.Record(ctx, Event{Action: "deploy", App: "hello", Provider: "fake"}); err != nil {
		t.Fatal(err)
	}
	if err := logger.Record(ctx, Event{Action: "deploy", App: "other", Provider: "fake"}); err != nil {
		t.Fatal(err)
	}
	all, err := logger.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("len = %d", len(all))
	}
	hello, err := logger.List(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(hello) != 1 || hello[0].App != "hello" {
		t.Fatalf("got %#v", hello)
	}
}
