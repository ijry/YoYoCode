package handlers_test

import "testing"

func TestAcpAgentSettingsAndStatus(t *testing.T) {
	srv := newTestServer(t)

	// get_agent_status defaults: enabled=true, installed_version=null, adapter flag.
	{
		_, out := call(t, srv, "acp_get_agent_status", map[string]any{"agentType": "claude_code"})
		if out["agent_type"] != "claude_code" {
			t.Fatalf("status agent_type: %v", out)
		}
		if out["enabled"] != true {
			t.Fatalf("default enabled should be true: %v", out)
		}
		if out["installed_version"] != nil {
			t.Fatalf("default installed_version should be null: %v", out)
		}
		if out["is_acp_adapter"] != true {
			t.Fatalf("claude_code is an acp adapter: %v", out)
		}
		if _, ok := out["available"]; !ok {
			t.Fatalf("status missing available: %v", out)
		}
	}

	// update_agent_env persists enabled=false.
	{
		code, _ := call(t, srv, "acp_update_agent_env", map[string]any{
			"agentType": "codex", "enabled": false, "env": map[string]any{"FOO": "bar"},
		})
		if code != 200 {
			t.Fatalf("update_agent_env status %d", code)
		}
		_, st := call(t, srv, "acp_get_agent_status", map[string]any{"agentType": "codex"})
		if st["enabled"] != false {
			t.Fatalf("codex should be disabled after update: %v", st)
		}
	}

	// reorder_agents is accepted.
	if code, _ := call(t, srv, "acp_reorder_agents", map[string]any{"agentTypes": []any{"codex", "claude_code"}}); code != 200 {
		t.Fatalf("reorder status %d", code)
	}

	// find_connection_for_conversation with no live conn → null.
	{
		body, _ := jsonBody(map[string]any{"conversationId": 123, "agentType": "codex"})
		code, raw := rawPost(t, srv, "acp_find_connection_for_conversation", body)
		if code != 200 || (raw != "null" && raw != "null\n") {
			t.Fatalf("expected null, got %d %q", code, raw)
		}
	}
	{
		body, _ := jsonBody(map[string]any{"conversationId": 123})
		code, raw := rawPost(t, srv, "acp_get_session_snapshot_by_conversation", body)
		if code != 200 || (raw != "null" && raw != "null\n") {
			t.Fatalf("expected null snapshot, got %d %q", code, raw)
		}
	}

	// download_agent_binary: a non-binary agent is rejected as invalid input.
	{
		code, out := call(t, srv, "acp_download_agent_binary", map[string]any{"agentType": "claude_code", "taskId": "t1"})
		if code != 400 || out["code"] != "invalid_input" {
			t.Fatalf("expected 400 invalid_input for an npx agent, got %d %v", code, out)
		}
	}
	// A binary agent with a custom version is refused BEFORE any download (the
	// built-ins publish no version-templated URL) → external_command_failed
	// (500). No network is touched.
	{
		code, out := call(t, srv, "acp_download_agent_binary", map[string]any{"agentType": "cursor", "taskId": "t1", "version": "1.2.3"})
		if code != 500 || out["code"] != "external_command_failed" {
			t.Fatalf("expected 500 external_command_failed for a custom version, got %d %v", code, out)
		}
	}

	// uninstall_agent is accepted (clears recorded install).
	if code, _ := call(t, srv, "acp_uninstall_agent", map[string]any{"agentType": "codex", "taskId": "t2"}); code != 200 {
		t.Fatalf("uninstall status %d", code)
	}
}
