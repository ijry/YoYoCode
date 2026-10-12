package acp

import (
	"bytes"
	"encoding/json"
	"sync"
)

// Mirrors the capacity constants in upstream src-tauri/src/acp/event_stream.rs.

// BroadcastCapacity is the per-connection broadcast channel size. Sized to
// absorb a brief burst when a slow subscriber lags; past capacity the oldest
// events are dropped and the subscriber is surfaced as `lagged`, which the
// client converts into a re-attach.
const BroadcastCapacity = 4096

// RecentBufferMaxBytes caps the byte total retained in the recent-events ring
// buffer. Oversized images push past this bound and force a snapshot fallback
// on the next attach.
const RecentBufferMaxBytes = 128 * 1024

// RecentBufferMaxCount is a hard cap on event count regardless of byte total.
const RecentBufferMaxCount = 128

// RecentEventMaxBytes is the single-event threshold above which we refuse to
// store the event: replaying it would blow past WS frame budgets, so the next
// attach falls through to a snapshot instead.
const RecentEventMaxBytes = 64 * 1024

// EventEnvelope mirrors upstream `acp::types::EventEnvelope`. `payload` is
// flattened by serde in Rust, so on the wire the envelope's own fields (`seq`,
// `connection_id`) sit alongside the event's fields in one JSON object.
type EventEnvelope struct {
	Seq          uint64
	ConnectionID string
	Payload      json.RawMessage
}

// MarshalJSON flattens Payload into the envelope object, matching serde's
// `#[serde(flatten)]`.
func (e EventEnvelope) MarshalJSON() ([]byte, error) {
	base := map[string]json.RawMessage{
		"seq":           mustJSON(e.Seq),
		"connection_id": mustJSON(e.ConnectionID),
	}
	var payload map[string]json.RawMessage
	if len(e.Payload) > 0 && e.Payload[0] == '{' {
		_ = json.Unmarshal(e.Payload, &payload)
	}
	for k, v := range payload {
		base[k] = v
	}
	return json.Marshal(base)
}

// UnmarshalJSON pulls the two known envelope fields out and keeps the rest as
// the flattened payload.
func (e *EventEnvelope) UnmarshalJSON(data []byte) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	if v, ok := all["seq"]; ok {
		_ = json.Unmarshal(v, &e.Seq)
		delete(all, "seq")
	}
	if v, ok := all["connection_id"]; ok {
		_ = json.Unmarshal(v, &e.ConnectionID)
		delete(all, "connection_id")
	}
	if len(all) == 0 {
		e.Payload = nil
		return nil
	}
	rest, err := json.Marshal(all)
	if err != nil {
		return err
	}
	e.Payload = rest
	return nil
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

// ─── Recent-events ring buffer ─────────────────────────────────────────────

// RecentEvents is the ring buffer backing `replay`. It is bounded by BOTH a
// byte total and an event count (the count cap defends against a flood of tiny
// events, each carrying seq/connection_id overhead the byte total ignores).
type RecentEvents struct {
	events []*EventEnvelope
	bytes  int
}

// Push records an envelope, returning how many events were evicted. An
// oversized single event clears the whole buffer (mirroring upstream: it forces
// the next attach to fall through to a snapshot).
func (r *RecentEvents) Push(e *EventEnvelope) (evicted int) {
	size := len(e.Payload) + 64 // seq + connection_id overhead
	if size > RecentEventMaxBytes || e.Payload == nil {
		evicted = len(r.events)
		r.events = nil
		r.bytes = 0
		return evicted
	}
	r.events = append(r.events, e)
	r.bytes += size
	for len(r.events) > RecentBufferMaxCount || r.bytes > RecentBufferMaxBytes {
		head := r.events[0]
		r.events = r.events[1:]
		r.bytes -= len(head.Payload) + 64
		evicted++
	}
	if r.bytes < 0 {
		r.bytes = 0
	}
	return evicted
}

// RangeAfter returns every stored event with seq > sinceSeq. The bool reports
// whether the buffer still covers the cursor: false means the caller is too far
// behind (events were evicted) and must fall back to a snapshot. Mirrors
// upstream `RecentEventsBuffer::range_after`.
func (r *RecentEvents) RangeAfter(sinceSeq uint64) ([]*EventEnvelope, bool) {
	if len(r.events) == 0 {
		// Empty buffer with a cursor strictly behind head: cannot satisfy.
		return nil, false
	}
	oldest := r.events[0].Seq
	// The cursor is "covered" when it is at/after the oldest retained seq.
	// A cursor of 0 with events starting at 1 is covered (nothing missing).
	if sinceSeq+1 < oldest {
		return nil, false
	}
	out := make([]*EventEnvelope, 0, len(r.events))
	for _, e := range r.events {
		if e.Seq > sinceSeq {
			out = append(out, e)
		}
	}
	return out, true
}

func (r *RecentEvents) Len() int { return len(r.events) }

// ─── Per-connection broadcast stream ───────────────────────────────────────

// subscriber is one live broadcast receiver with its own buffered queue.
type subscriber struct {
	ch      chan *EventEnvelope
	dropped int
	closed  bool
}

// ConnectionEventStream is the per-connection broadcaster + subscriber registry
// (upstream `ConnectionEventStream`). Emission happens through
// SessionState.Emit, which increments event_seq and records into the ring
// buffer before broadcasting — so a subscriber registered after a snapshot read
// can never miss the events that follow.
type ConnectionEventStream struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

func NewConnectionEventStream() *ConnectionEventStream {
	return &ConnectionEventStream{subs: map[*subscriber]struct{}{}}
}

// Subscribe registers a receiver. Must be called under the owning
// SessionState's lock so no broadcast can slip between the snapshot read and
// registration.
func (s *ConnectionEventStream) Subscribe() *StreamReceiver {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := &subscriber{ch: make(chan *EventEnvelope, BroadcastCapacity)}
	s.subs[sub] = struct{}{}
	return &StreamReceiver{stream: s, sub: sub}
}

func (s *ConnectionEventStream) unsubscribe(sub *subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[sub]; ok {
		delete(s.subs, sub)
		sub.closed = true
		close(sub.ch)
	}
}

// Broadcast fans an envelope out to every subscriber. A full subscriber queue
// marks the receiver as lagged (mirroring `RecvError::Lagged`) and drops the
// event — the client is expected to re-attach with its last applied seq.
func (s *ConnectionEventStream) Broadcast(e *EventEnvelope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sub := range s.subs {
		if sub.closed {
			continue
		}
		select {
		case sub.ch <- e:
		default:
			sub.dropped++
		}
	}
}

// SubscriberCount is exposed for the debug/event_metrics endpoint.
func (s *ConnectionEventStream) SubscriberCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

// StreamReceiver is the subscriber handle; Close releases the broadcast slot.
type StreamReceiver struct {
	stream *ConnectionEventStream
	sub    *subscriber
}

// Events returns the receive channel.
func (r *StreamReceiver) Events() <-chan *EventEnvelope { return r.sub.ch }

// Lagged reports how many events this receiver missed. Once non-zero the caller
// must detach (upstream maps this to `DetachReason::Lagged`).
func (r *StreamReceiver) Lagged() int { return r.sub.dropped }

func (r *StreamReceiver) Close() { r.stream.unsubscribe(r.sub) }

// EnvelopeEqual compares two envelopes by serialized bytes — used in tests to
// assert replay payloads survive a round-trip unchanged.
func EnvelopeEqual(a, b *EventEnvelope) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Seq != b.Seq || a.ConnectionID != b.ConnectionID {
		return false
	}
	return bytes.Equal(bytes.TrimSpace(a.Payload), bytes.TrimSpace(b.Payload))
}
