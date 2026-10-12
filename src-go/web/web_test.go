package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"yoyocode/src-go/acp"
)

// stubState provides a Broadcaster + ACP session registry for the router.
type stubState struct {
	bc       *Broadcaster
	sessions *acp.SessionRegistry
}

func (s *stubState) Broadcaster() *Broadcaster      { return s.bc }
func (s *stubState) Sessions() *acp.SessionRegistry { return s.sessions }

func newStubState() *stubState {
	return &stubState{bc: NewBroadcaster(), sessions: acp.NewSessionRegistry()}
}

func startTestServer(t *testing.T) string {
	t.Helper()
	r := NewRouter(newStubState())
	r.SetToken("tok")
	r.RegisterPublic("health", func(c *Context) (any, error) {
		return map[string]any{"status": "ok"}, nil
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL
}

// startStatefulTestServer returns the URL plus the state so tests can seed
// ACP sessions and drive event emission.
func startStatefulTestServer(t *testing.T) (string, *stubState) {
	t.Helper()
	st := newStubState()
	r := NewRouter(st)
	r.SetToken("tok")
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL, st
}

func TestAPIHealthOK(t *testing.T) {
	base := startTestServer(t)
	res, err := http.Post(base+"/api/health", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("want 200, got %d", res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("bad body: %v", body)
	}
}

func TestAuthBearerAndWSProtocol(t *testing.T) {
	req, _ := http.NewRequest("POST", "/api/x", nil)
	if Authorized(req, "tok") {
		t.Fatal("no token must not authorize")
	}
	req.Header.Set("authorization", "Bearer tok")
	if !Authorized(req, "tok") {
		t.Fatal("bearer token must authorize")
	}

	req2, _ := http.NewRequest("GET", "/ws/events", nil)
	req2.Header.Set("sec-websocket-protocol", strings.Join(BuildWSProtocols("tok"), ", "))
	if !Authorized(req2, "tok") {
		t.Fatal("ws protocol token must authorize")
	}

	req3, _ := http.NewRequest("GET", "/ws/events", nil)
	req3.Header.Set("sec-websocket-protocol", BuildWSProtocols("bad")[1])
	if Authorized(req3, "tok") {
		t.Fatal("wrong ws token must not authorize")
	}
}

func TestWSProtocolHandshakeAndPing(t *testing.T) {
	base := startTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(base, "http") + "/ws/events"
	h := http.Header{}
	h.Set("Sec-WebSocket-Protocol", strings.Join(BuildWSProtocols("tok"), ", "))
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, h)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// First frame: legacy __ready__ channel.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ready: %v", err)
	}
	var ready WebEvent
	if err := json.Unmarshal(msg, &ready); err != nil {
		t.Fatalf("ready frame: %v", err)
	}
	if ready.Channel != ReadyChannel {
		t.Fatalf("want ready channel, got %q", ready.Channel)
	}

	// Ping → Pong.
	if err := conn.WriteJSON(ClientMsg{Action: "ping"}); err != nil {
		t.Fatal(err)
	}
	_, msg, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("read pong: %v", err)
	}
	if !strings.Contains(string(msg), `"pong"`) {
		t.Fatalf("want pong frame, got %s", msg)
	}
}

func TestWSRejectsBadToken(t *testing.T) {
	base := startTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(base, "http") + "/ws/events"
	h := http.Header{}
	h.Set("Sec-WebSocket-Protocol", strings.Join(BuildWSProtocols("nope"), ", "))
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, h)
	if err == nil {
		t.Fatal("expected handshake failure for bad token")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %v", resp)
	}
}

func TestBuildWSProtocolsShape(t *testing.T) {
	got := BuildWSProtocols("abc")
	if len(got) != 2 || got[0] != "codeg-events" || !strings.HasPrefix(got[1], "codeg-token.") {
		t.Fatalf("bad protocols: %v", got)
	}
}
