package handlers_test

import "testing"

func TestAcpListAgents(t *testing.T) {
	srv := newTestServer(t)
	_, arr := callArr(t, srv, "acp_list_agents", map[string]any{})
	if len(arr) != 15 {
		t.Fatalf("expected 15 agents, got %d", len(arr))
	}
	first := arr[0].(map[string]any)
	if first["agent_type"] != "claude_code" || first["registry_id"] != "claude-acp" {
		t.Fatalf("first agent wrong: %v", first)
	}
	if first["available"] != false {
		t.Fatalf("agent should be unavailable until preflight: %v", first)
	}
}
