package app

// Stream is the native UI's client for the backend's /ws/events socket — the
// same multiplexed firehose + attach protocol mcode-app speaks. It keeps the
// workspace live: firehose frames (conversation://changed, acp://*) trigger a
// repaint, and per-connection ACP event envelopes (attach → snapshot/replay/
// event) accumulate so the chat detail can render streaming agent output.

import (
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

type connStream struct {
	subID   string
	lastSeq uint64
	events  []map[string]any
}

// Stream owns the websocket connection and the per-connection event buffers.
type Stream struct {
	b        *Backend
	onChange func(connID, channel string) // repaint request (goroutine-safe); connID "" for firehose, channel set for firehose frames

	mu       sync.Mutex
	conn     *websocket.Conn
	ready    bool
	byConn   map[string]*connStream
	attached map[string]bool
}

func newStream(b *Backend, onChange func(connID, channel string)) *Stream {
	s := &Stream{b: b, onChange: onChange, byConn: map[string]*connStream{}, attached: map[string]bool{}}
	go s.run()
	return s
}

// wsURL turns the backend's http base into a ws:// /ws/events URL.
func (s *Stream) wsURL() string {
	u := s.b.URL
	u = strings.Replace(u, "http://", "ws://", 1)
	u = strings.Replace(u, "https://", "wss://", 1)
	return u + "/ws/events"
}

func (s *Stream) run() {
	hdr := http.Header{}
	hdr.Set("authorization", "Bearer "+s.b.Token)
	conn, _, err := websocket.DefaultDialer.Dial(s.wsURL(), hdr)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.conn = conn
	s.ready = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.ready = false
		s.conn = nil
		s.mu.Unlock()
		conn.Close()
	}()

	for {
		var f wsFrame
		if err := conn.ReadJSON(&f); err != nil {
			return
		}
		s.handle(&f)
	}
}

// wsFrame is the union of a firehose WebEvent ({channel,payload}) and an
// attach-protocol ServerMsg ({type,…}); only one family's keys are set.
type wsFrame struct {
	Channel      string           `json:"channel"`
	Type         string           `json:"type"`
	ConnectionID string           `json:"connection_id"`
	EventSeq     uint64           `json:"event_seq"`
	HighWaterSeq uint64           `json:"high_water_seq"`
	Envelope     map[string]any   `json:"envelope"`
	Events       []map[string]any `json:"events"`
	Snapshot     map[string]any   `json:"snapshot"`
}

func (s *Stream) handle(f *wsFrame) {
	changed := false
	changedConn := ""
	channel := ""
	if f.Channel != "" {
		// Firehose: any change is worth a repaint; the view refetches as needed.
		channel = f.Channel
		changed = true
	} else {
		switch f.Type {
		case "snapshot":
			s.mu.Lock()
			cs := s.ensureConn(f.ConnectionID)
			cs.lastSeq = f.EventSeq
			s.mu.Unlock()
			changedConn = f.ConnectionID
			changed = true
		case "replay":
			changedConn = f.ConnectionID
			s.mu.Lock()
			cs := s.ensureConn(f.ConnectionID)
			for _, ev := range f.Events {
				cs.events = append(cs.events, ev)
				if seq, ok := ev["seq"].(float64); ok {
					cs.lastSeq = uint64(seq)
				}
			}
			if f.HighWaterSeq > cs.lastSeq {
				cs.lastSeq = f.HighWaterSeq
			}
			s.mu.Unlock()
			changed = true
		case "event":
			if f.Envelope != nil {
				connID, _ := f.Envelope["connection_id"].(string)
				s.mu.Lock()
				cs := s.ensureConn(connID)
				cs.events = append(cs.events, f.Envelope)
				if seq, ok := f.Envelope["seq"].(float64); ok {
					cs.lastSeq = uint64(seq)
				}
				s.mu.Unlock()
				changedConn = connID
				changed = true
			}
		}
	}
	if changed && s.onChange != nil {
		s.onChange(changedConn, channel)
	}
}

// ensureConn must be called with s.mu held.
func (s *Stream) ensureConn(connID string) *connStream {
	cs := s.byConn[connID]
	if cs == nil {
		cs = &connStream{subID: "sub-" + connID}
		s.byConn[connID] = cs
	}
	return cs
}

// Attach subscribes to a connection's live event stream (idempotent).
func (s *Stream) Attach(connID string) {
	if connID == "" {
		return
	}
	s.mu.Lock()
	if s.attached[connID] || !s.ready || s.conn == nil {
		s.mu.Unlock()
		return
	}
	cs := s.ensureConn(connID)
	s.attached[connID] = true
	conn := s.conn
	since := cs.lastSeq
	subID := cs.subID
	s.mu.Unlock()

	msg := map[string]any{"action": "attach", "subscription_id": subID, "connection_id": connID}
	if since > 0 {
		msg["since_seq"] = since
	}
	_ = conn.WriteJSON(msg)
}

// Events returns the accumulated event envelopes for a connection.
func (s *Stream) Events(connID string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs := s.byConn[connID]
	if cs == nil {
		return nil
	}
	out := make([]map[string]any, len(cs.events))
	copy(out, cs.events)
	return out
}

// isReady reports whether the websocket is connected.
func (s *Stream) isReady() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// ensureStream lazily starts the websocket client once a Services handle is
// available to repaint through.
func (s *Shell) ensureStream() {
	s.mu.Lock()
	if s.stream != nil {
		s.mu.Unlock()
		return
	}
	svc := s.svc
	sh := s
	s.stream = newStream(s.B, func(connID, channel string) {
		if connID != "" {
			sh.invalidate("snapshot:" + connID)
		}
		// A server-side conversation mutation (create/delete/title/status, incl.
		// the agent's auto-title) refreshes the sidebar lists without a manual
		// Refresh.
		if channel == "conversation://changed" {
			sh.invalidate("folders")
			sh.mu.Lock()
			folder := sh.selectedFolder
			sh.mu.Unlock()
			if folder != "" {
				sh.invalidate("convs:" + folder)
			}
		}
		svc.Invalidate()
	})
	s.mu.Unlock()
}
