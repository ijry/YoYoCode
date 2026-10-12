package web

import "sync"

// ShutdownSignal mirrors upstream src-tauri/src/web/shutdown.rs: a
// broadcast-style latch that WS connections select on so they can emit a final
// `detached{reason:"server_shutdown"}` frame before the socket closes.
type ShutdownSignal struct {
	mu      sync.Mutex
	ch      chan struct{}
	trigger bool
}

func NewShutdownSignal() *ShutdownSignal {
	return &ShutdownSignal{ch: make(chan struct{})}
}

// Trigger latches the signal. Idempotent — repeat calls are no-ops.
func (s *ShutdownSignal) Trigger() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.trigger {
		s.trigger = true
		close(s.ch)
	}
}

// Wait returns a channel that is closed once Trigger fires.
func (s *ShutdownSignal) Wait() <-chan struct{} { return s.ch }

// IsTriggered reports whether shutdown already fired — used by the late
// handshake guard so a connection accepted during shutdown exits before
// subscribing to anything.
func (s *ShutdownSignal) IsTriggered() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trigger
}
