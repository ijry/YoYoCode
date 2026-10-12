package handlers_test

import "testing"

func TestAcpDetectAgentLocalVersion(t *testing.T) {
	srv := newTestServer(t)
	// Requires an agentType.
	if code, _ := call(t, srv, "acp_detect_agent_local_version", map[string]any{}); code == 200 {
		t.Fatalf("missing agentType should be rejected")
	}
	// A known agent: returns 200 with either a version string or null — never
	// an error — regardless of whether the package is globally installed.
	code, body := rawPostJSON(t, srv, "acp_detect_agent_local_version", map[string]any{"agentType": "claude_code"})
	if code != 200 {
		t.Fatalf("detect status = %d: %s", code, body)
	}
	// Unknown agent → no npx spec → null (previous stored value), still 200.
	code2, body2 := rawPostJSON(t, srv, "acp_detect_agent_local_version", map[string]any{"agentType": "not_an_agent"})
	if code2 != 200 {
		t.Fatalf("unknown agent detect status = %d: %s", code2, body2)
	}
}

func TestPackageNameFromSpecViaDetect(t *testing.T) {
	// Indirect coverage: a scoped + versioned agent still resolves to 200.
	srv := newTestServer(t)
	for _, agent := range []string{"codex", "gemini", "open_code"} {
		if code, _ := call(t, srv, "acp_detect_agent_local_version", map[string]any{"agentType": agent}); code != 200 {
			t.Fatalf("%s detect should be 200, got %d", agent, code)
		}
	}
}
