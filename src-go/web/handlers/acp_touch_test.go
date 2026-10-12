package handlers_test

import "testing"

func TestAcpTouchUnknownConnection(t *testing.T) {
	srv := newTestServer(t)
	// An unknown connection id → false (not live), never an error.
	code, body := rawPostJSON(t, srv, "acp_touch_connection", map[string]any{"connectionId": "nope"})
	if code != 200 {
		t.Fatalf("touch status = %d", code)
	}
	if trimmed := trimWS(body); trimmed != "false" {
		t.Fatalf("unknown connection should touch=false, got %q", body)
	}
}

func trimWS(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
