// Package audit will record who deployed, connected a provider, or changed a
// secret, so IT can review it later.
//
// TODO: persist an append-only log with retention and an admin viewer.
// MemoryLogger exists so the deploy path can be wired and tested. It forgets
// everything when the process exits and is not a compliance record.
package audit

import (
	"context"
	"sync"
	"time"
)

// Event is one operator action.
type Event struct {
	Time     time.Time
	Action   string
	Provider string
	App      string
	Message  string
}

// Logger records and lists events.
type Logger interface {
	Record(ctx context.Context, event Event) error
	List(ctx context.Context, app string) ([]Event, error)
}

// MemoryLogger keeps events in process memory.
type MemoryLogger struct {
	mu     sync.Mutex
	events []Event
}

// NewMemory returns an empty logger.
func NewMemory() *MemoryLogger {
	return &MemoryLogger{}
}

// Record appends an event. A zero time is replaced with the current time.
func (l *MemoryLogger) Record(_ context.Context, event Event) error {
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
	return nil
}

// List returns events, optionally filtered by app. The order is chronological.
func (l *MemoryLogger) List(_ context.Context, app string) ([]Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, 0, len(l.events))
	for _, event := range l.events {
		if app == "" || event.App == app {
			out = append(out, event)
		}
	}
	return out, nil
}
