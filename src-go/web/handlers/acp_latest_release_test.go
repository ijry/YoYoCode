package handlers_test

import "testing"

func TestFetchLatestReleaseNullPaths(t *testing.T) {
	srv := newTestServer(t)
	// missing agentType → rejected
	if code, _ := call(t, srv, "acp_fetch_agent_latest_release", map[string]any{}); code == 200 {
		t.Fatalf("missing agentType should be rejected")
	}
	// unknown agent → 200 null, no network
	code, body := rawPostJSON(t, srv, "acp_fetch_agent_latest_release", map[string]any{"agentType": "not_an_agent"})
	if code != 200 || trimWS(body) != "null" {
		t.Fatalf("unknown agent should be null, got %d %q", code, body)
	}
	// uvx agent (pi) is not downloaded by this build → 200 null, no network
	code2, body2 := rawPostJSON(t, srv, "acp_fetch_agent_latest_release", map[string]any{"agentType": "pi"})
	if code2 != 200 || trimWS(body2) != "null" {
		t.Fatalf("uvx agent should be null, got %d %q", code2, body2)
	}
}
