package app

import "testing"

// newStreamForTest builds a Stream without dialing, for handle() unit tests.
func newStreamForTest() *Stream {
	return &Stream{onChange: func(string, string) {}, byConn: map[string]*connStream{}, attached: map[string]bool{}}
}

func TestStreamHandleAccumulatesEvents(t *testing.T) {
	s := newStreamForTest()
	// snapshot sets the resume cursor.
	s.handle(&wsFrame{Type: "snapshot", ConnectionID: "c1", EventSeq: 3})
	// an event envelope lands under its connection_id.
	s.handle(&wsFrame{Type: "event", Envelope: map[string]any{"connection_id": "c1", "seq": float64(4), "type": "content_delta", "text": "hello"}})
	s.handle(&wsFrame{Type: "event", Envelope: map[string]any{"connection_id": "c1", "seq": float64(5), "type": "content_delta", "text": " world"}})
	evs := s.Events("c1")
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs))
	}
	if evs[0]["text"] != "hello" || evs[0]["type"] != "content_delta" {
		t.Fatalf("event0 = %v", evs[0])
	}
	// The accumulated envelopes fold into one assistant bubble.
	if rows := buildTranscript(evs); len(rows) != 1 || rows[0].kind != "message" || rows[0].text != "hello world" {
		t.Fatalf("rows = %+v", rows)
	}
	if s.byConn["c1"].lastSeq != 5 {
		t.Fatalf("lastSeq = %d, want 5", s.byConn["c1"].lastSeq)
	}
}

func TestStreamHandleReplayAndFirehose(t *testing.T) {
	s := newStreamForTest()
	s.handle(&wsFrame{Type: "replay", ConnectionID: "c2", Events: []map[string]any{
		{"connection_id": "c2", "seq": float64(1), "type": "tool_call"},
		{"connection_id": "c2", "seq": float64(2), "type": "tool_call"},
	}, HighWaterSeq: 2})
	if len(s.Events("c2")) != 2 {
		t.Fatalf("replay should accumulate 2")
	}
	// A firehose frame (channel set) must not panic or create a connection.
	s.handle(&wsFrame{Channel: "conversation://changed"})
	if s.Events("missing") != nil {
		t.Fatal("unknown connection should have no events")
	}
}
