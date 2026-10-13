package handlers_test

// acp_fork_test.go locks the route's error mapping: an unknown connection is a
// 404, and a missing connectionId is invalid input.

import "testing"

func TestAcpForkUnknownConnection(t *testing.T) {
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_fork", map[string]any{"connectionId": "nope"})
	if code != 404 || out["code"] != "not_found" {
		t.Fatalf("expected 404 not_found, got %d %v", code, out)
	}
}

func TestAcpForkRequiresConnection(t *testing.T) {
	srv := newTestServer(t)
	code, out := call(t, srv, "acp_fork", map[string]any{})
	if code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("expected 400 invalid_input, got %d %v", code, out)
	}
}
