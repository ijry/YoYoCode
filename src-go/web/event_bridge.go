// Package web mirrors upstream src-tauri/src/web (the HTTP/WebSocket server).
package web

import (
	"encoding/json"
	"sync"
)

// WebEvent is the legacy firehose envelope: {"channel": "...", "payload": ...}
// Mirrors upstream `WebEvent` serialize output exactly.
type WebEvent struct {
	Channel string          `json:"channel"`
	Payload json.RawMessage `json:"payload"`
}

// Broadcaster is the Go analog of upstream `WebEventBroadcaster`.
type Broadcaster struct {
	mu   sync.RWMutex
	subs map[*chan WebEvent]struct{}
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: map[*chan WebEvent]struct{}{}}
}

// Subscribe registers a channel receiver. Returns an unsubscribe func.
func (b *Broadcaster) Subscribe() (chan WebEvent, func()) {
	ch := make(chan WebEvent, 4096)
	b.mu.Lock()
	b.subs[&ch] = struct{}{}
	b.mu.Unlock()
	unsub := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[&ch]; ok {
			delete(b.subs, &ch)
			close(ch)
		}
	}
	return ch, unsub
}

// Send broadcasts a value to all subscribers (non-blocking, drops on slow
// consumers to match upstream's broadcast channel).
func (b *Broadcaster) Send(channel string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	evt := WebEvent{Channel: channel, Payload: json.RawMessage(data)}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case *ch <- evt:
		default:
		}
	}
}

// SendRaw broadcasts a pre-serialized payload.
func (b *Broadcaster) SendRaw(channel string, payload json.RawMessage) {
	evt := WebEvent{Channel: channel, Payload: payload}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case *ch <- evt:
		default:
		}
	}
}

// SubscriberCount returns number of live subscribers.
func (b *Broadcaster) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
