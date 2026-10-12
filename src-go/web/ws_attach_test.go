package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"yoyocode/src-go/acp"
)

// dial opens an authenticated WS connection and returns it plus a read helper
// that fails the test instead of hanging.
func dialWS(t *testing.T, base string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(base, "http") + "/ws/events"
	h := map[string][]string{"Sec-WebSocket-Protocol": {strings.Join(BuildWSProtocols("tok"), ", ")}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, h)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn
}

// readFrame decodes the next frame into a generic map so tests can assert on
// `type` without committing to one variant.
func readFrame(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(msg, &out); err != nil {
		t.Fatalf("decode frame %s: %v", msg, err)
	}
	return out
}

// skipReady consumes the mandatory legacy __ready__ frame.
func skipReady(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	frame := readFrame(t, conn)
	if frame["channel"] != ReadyChannel {
		t.Fatalf("want __ready__ channel, got %v", frame["channel"])
	}
	// Upstream sends payload:null — an object here breaks the client handshake.
	if p, ok := frame["payload"]; ok && p != nil {
		t.Fatalf("__ready__ payload must be null, got %#v", p)
	}
}

func TestReadyFramePayloadIsNull(t *testing.T) {
	raw, err := json.Marshal(ReadyFrame())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"channel":"__ready__","payload":null}` {
		t.Fatalf("unexpected ready frame: %s", raw)
	}
}

func TestAttachUnknownConnectionDetachesConnectionGone(t *testing.T) {
	base, _ := startStatefulTestServer(t)
	conn := dialWS(t, base)
	skipReady(t, conn)

	if err := conn.WriteJSON(ClientMsg{Action: "attach", SubscriptionID: "s1", ConnectionID: "nope"}); err != nil {
		t.Fatal(err)
	}
	frame := readFrame(t, conn)
	if frame["type"] != FrameTypeDetached {
		t.Fatalf("want detached, got %#v", frame)
	}
	if frame["subscription_id"] != "s1" {
		t.Fatalf("want subscription_id s1, got %#v", frame["subscription_id"])
	}
	if frame["reason"] != string(DetachConnectionGone) {
		t.Fatalf("want connection_gone, got %#v", frame["reason"])
	}
}

func TestAttachColdStartReturnsSnapshot(t *testing.T) {
	base, st := startStatefulTestServer(t)
	state := acp.NewSessionState("conn-1")
	state.SetStatus(acp.StatusConnected)
	st.sessions.Insert(state)

	conn := dialWS(t, base)
	skipReady(t, conn)

	if err := conn.WriteJSON(ClientMsg{Action: "attach", SubscriptionID: "s1", ConnectionID: "conn-1"}); err != nil {
		t.Fatal(err)
	}
	frame := readFrame(t, conn)
	if frame["type"] != FrameTypeSnapshot {
		t.Fatalf("want snapshot, got %#v", frame)
	}
	if frame["connection_id"] != "conn-1" {
		t.Fatalf("want connection_id conn-1, got %#v", frame["connection_id"])
	}
	if _, ok := frame["event_seq"]; !ok {
		t.Fatalf("snapshot must carry event_seq, got %#v", frame)
	}
	snap, ok := frame["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot missing or wrong shape: %#v", frame["snapshot"])
	}
	if snap["status"] != "connected" {
		t.Fatalf("want status connected, got %#v", snap["status"])
	}
}

func TestAttachReplaysSmallGap(t *testing.T) {
	base, st := startStatefulTestServer(t)
	state := acp.NewSessionState("conn-2")
	st.sessions.Insert(state)

	// Emit three events BEFORE any attach — they land in the ring buffer.
	for i := 0; i < 3; i++ {
		state.Emit(json.RawMessage(`{"kind":"tick","n":` + itoa(i) + `}`))
	}

	conn := dialWS(t, base)
	skipReady(t, conn)

	cursor := uint64(1)
	if err := conn.WriteJSON(ClientMsg{
		Action: "attach", SubscriptionID: "s1", ConnectionID: "conn-2", SinceSeq: &cursor,
	}); err != nil {
		t.Fatal(err)
	}
	frame := readFrame(t, conn)
	if frame["type"] != FrameTypeReplay {
		t.Fatalf("want replay, got %#v", frame)
	}
	events, ok := frame["events"].([]any)
	if !ok || len(events) != 2 {
		t.Fatalf("want 2 replayed events (seq 2,3), got %#v", frame["events"])
	}
	if hw, _ := frame["high_water_seq"].(float64); hw != 3 {
		t.Fatalf("want high_water_seq 3, got %#v", frame["high_water_seq"])
	}
}

func TestAttachFallsBackToSnapshotOnLargeGap(t *testing.T) {
	base, st := startStatefulTestServer(t)
	state := acp.NewSessionState("conn-3")
	st.sessions.Insert(state)

	// Push more than REPLAY_BATCH_THRESHOLD events into the ring buffer.
	for i := 0; i < ReplayBatchThreshold+5; i++ {
		state.Emit(json.RawMessage(`{"kind":"tick"}`))
	}

	conn := dialWS(t, base)
	skipReady(t, conn)

	cursor := uint64(0)
	if err := conn.WriteJSON(ClientMsg{
		Action: "attach", SubscriptionID: "s1", ConnectionID: "conn-3", SinceSeq: &cursor,
	}); err != nil {
		t.Fatal(err)
	}
	frame := readFrame(t, conn)
	if frame["type"] != FrameTypeSnapshot {
		t.Fatalf("large gap must fall back to snapshot, got %#v", frame)
	}
}

func TestAttachForwardsLiveEvents(t *testing.T) {
	base, st := startStatefulTestServer(t)
	state := acp.NewSessionState("conn-4")
	st.sessions.Insert(state)

	conn := dialWS(t, base)
	skipReady(t, conn)

	if err := conn.WriteJSON(ClientMsg{Action: "attach", SubscriptionID: "s1", ConnectionID: "conn-4"}); err != nil {
		t.Fatal(err)
	}
	if frame := readFrame(t, conn); frame["type"] != FrameTypeSnapshot {
		t.Fatalf("want snapshot, got %#v", frame)
	}

	state.Emit(json.RawMessage(`{"kind":"message","text":"hello"}`))

	frame := readFrame(t, conn)
	if frame["type"] != FrameTypeEvent {
		t.Fatalf("want event, got %#v", frame)
	}
	if frame["subscription_id"] != "s1" {
		t.Fatalf("want subscription_id s1, got %#v", frame["subscription_id"])
	}
	env, ok := frame["envelope"].(map[string]any)
	if !ok {
		t.Fatalf("envelope missing: %#v", frame)
	}
	// seq and connection_id sit alongside the flattened payload.
	if env["seq"] != float64(1) {
		t.Fatalf("want seq 1, got %#v", env["seq"])
	}
	if env["connection_id"] != "conn-4" {
		t.Fatalf("want connection_id conn-4, got %#v", env["connection_id"])
	}
	if env["text"] != "hello" {
		t.Fatalf("payload must be flattened into the envelope, got %#v", env)
	}
}

func TestDetachStopsForwarding(t *testing.T) {
	base, st := startStatefulTestServer(t)
	state := acp.NewSessionState("conn-5")
	st.sessions.Insert(state)

	conn := dialWS(t, base)
	skipReady(t, conn)

	if err := conn.WriteJSON(ClientMsg{Action: "attach", SubscriptionID: "s1", ConnectionID: "conn-5"}); err != nil {
		t.Fatal(err)
	}
	readFrame(t, conn) // snapshot

	if err := conn.WriteJSON(ClientMsg{Action: "detach", SubscriptionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	// Grace period so the server has processed the detach before we emit;
	// otherwise the first emit simply races the cancellation and the test
	// measures scheduling, not behaviour.
	time.Sleep(150 * time.Millisecond)
	for i := 0; i < 5; i++ {
		state.Emit(json.RawMessage(`{"kind":"after-detach"}`))
	}

	// Ping must still be answered — and it must arrive without any `event`
	// frame preceding it, proving the forwarder really stopped.
	if err := conn.WriteJSON(ClientMsg{Action: "ping"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		frame := readFrame(t, conn)
		if frame["type"] == FrameTypeEvent {
			t.Fatalf("event forwarded after detach: %#v", frame)
		}
		if frame["type"] == FrameTypePong {
			return
		}
	}
	t.Fatal("never received pong after detach")
}

func TestEventEnvelopeRoundTrip(t *testing.T) {
	in := []byte(`{"seq":7,"connection_id":"c","kind":"x","nested":{"a":1}}`)
	var env acp.EventEnvelope
	if err := json.Unmarshal(in, &env); err != nil {
		t.Fatal(err)
	}
	if env.Seq != 7 || env.ConnectionID != "c" {
		t.Fatalf("bad parse: %+v", env)
	}
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"seq", "connection_id", "kind", "nested"} {
		if _, ok := back[k]; !ok {
			t.Fatalf("re-serialized envelope lost key %q: %s", k, out)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
