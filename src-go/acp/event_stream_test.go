package acp

import (
	"encoding/json"
	"testing"
)

func TestRangeAfterCoversCursor(t *testing.T) {
	var buf RecentEvents
	for i := uint64(1); i <= 5; i++ {
		buf.Push(&EventEnvelope{Seq: i, ConnectionID: "c", Payload: json.RawMessage(`{}`)})
	}

	events, ok := buf.RangeAfter(3)
	if !ok {
		t.Fatal("cursor 3 must be covered by a buffer holding seq 1..5")
	}
	if len(events) != 2 || events[0].Seq != 4 || events[1].Seq != 5 {
		t.Fatalf("want seq 4,5 — got %d event(s)", len(events))
	}
}

// A cursor older than the oldest retained seq can't be satisfied: the missing
// events were evicted, so the caller must fall back to a snapshot.
func TestRangeAfterUncoveredCursorFallsBack(t *testing.T) {
	var buf RecentEvents
	// Overflow the count cap so the earliest events are evicted.
	for i := uint64(1); i <= RecentBufferMaxCount+10; i++ {
		buf.Push(&EventEnvelope{Seq: i, ConnectionID: "c", Payload: json.RawMessage(`{}`)})
	}
	if buf.Len() != RecentBufferMaxCount {
		t.Fatalf("buffer must be capped at %d, got %d", RecentBufferMaxCount, buf.Len())
	}
	if _, ok := buf.RangeAfter(1); ok {
		t.Fatal("cursor 1 must be uncovered once seq 1 was evicted")
	}
	// A cursor AT the head is covered with nothing to replay — this is the
	// "client is fully caught up" case, distinct from "client fell behind".
	head := uint64(RecentBufferMaxCount + 10)
	events, ok := buf.RangeAfter(head)
	if !ok {
		t.Fatal("cursor at head must still be covered")
	}
	if len(events) != 0 {
		t.Fatalf("want no events past head, got %d", len(events))
	}
}

// An oversized event clears the whole buffer rather than being stored —
// replaying it would blow past WS frame budgets, so the next attach takes a
// snapshot instead.
func TestOversizedEventClearsBuffer(t *testing.T) {
	var buf RecentEvents
	buf.Push(&EventEnvelope{Seq: 1, ConnectionID: "c", Payload: json.RawMessage(`{}`)})
	big := make([]byte, RecentEventMaxBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	buf.Push(&EventEnvelope{Seq: 2, ConnectionID: "c", Payload: json.RawMessage(`"` + string(big) + `"`)})
	if buf.Len() != 0 {
		t.Fatalf("oversized event must clear the buffer, got %d", buf.Len())
	}
}

func TestBroadcastAndLag(t *testing.T) {
	stream := NewConnectionEventStream()
	recv := stream.Subscribe()
	defer recv.Close()

	stream.Broadcast(&EventEnvelope{Seq: 1, ConnectionID: "c", Payload: json.RawMessage(`{}`)})
	if got := <-recv.Events(); got.Seq != 1 {
		t.Fatalf("want seq 1, got %d", got.Seq)
	}
	if recv.Lagged() != 0 {
		t.Fatalf("fresh receiver must not be lagged, got %d", recv.Lagged())
	}

	// Flood past the receiver's capacity without draining.
	for i := uint64(0); i < BroadcastCapacity+10; i++ {
		stream.Broadcast(&EventEnvelope{Seq: i, ConnectionID: "c", Payload: json.RawMessage(`{}`)})
	}
	if recv.Lagged() == 0 {
		t.Fatal("saturated receiver must be reported as lagged")
	}
}

// A session's Emit must be the single ordering point: seq increments, the
// envelope lands in the ring buffer, and subscribers registered afterwards see
// subsequent events. This is what makes "snapshot then subscribe" race-free.
func TestSessionEmitOrdering(t *testing.T) {
	state := NewSessionState("conn-a")
	recv := state.Subscribe()
	defer recv.Close()

	state.Emit(json.RawMessage(`{"kind":"a"}`))
	state.Emit(json.RawMessage(`{"kind":"b"}`))

	if state.EventSeqNow() != 2 {
		t.Fatalf("want event_seq 2, got %d", state.EventSeqNow())
	}
	if got := <-recv.Events(); got.Seq != 1 {
		t.Fatalf("want first event seq 1, got %d", got.Seq)
	}
	if got := <-recv.Events(); got.Seq != 2 {
		t.Fatalf("want second event seq 2, got %d", got.Seq)
	}

	events, ok := state.RecentEventsAfter(0)
	if !ok || len(events) != 2 {
		t.Fatalf("ring buffer must hold both events, got %d (ok=%v)", len(events), ok)
	}
}

func TestSessionRegistryLifecycle(t *testing.T) {
	reg := NewSessionRegistry()
	if reg.GetState("missing") != nil {
		t.Fatal("unknown connection must resolve to nil (→ detached/connection_gone)")
	}
	s := NewSessionState("conn-b")
	reg.Insert(s)
	if reg.GetState("conn-b") != s {
		t.Fatal("insert must be retrievable")
	}
	reg.Remove("conn-b")
	if reg.GetState("conn-b") != nil {
		t.Fatal("remove must unregister")
	}
}
