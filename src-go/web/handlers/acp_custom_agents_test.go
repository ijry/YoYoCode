package handlers_test

import "testing"

// Save → list → delete round trip for a custom npx agent, plus the launchable
// flag and the builtin-id collision rejection.
func TestCustomAgentRoundTrip(t *testing.T) {
	srv := newTestServer(t)

	// empty list
	_, empty := callArr(t, srv, "acp_list_custom_agents", map[string]any{})
	if len(empty) != 0 {
		t.Fatalf("expected no custom agents, got %d", len(empty))
	}

	// save a valid npx agent
	code, _ := call(t, srv, "acp_save_custom_agent", map[string]any{
		"params": map[string]any{
			"registryId":       "qwen-code",
			"name":             "Qwen",
			"distributionKind": "npx",
			"spec": map[string]any{
				"npx": map[string]any{"package": "@qwen-code/qwen-code@0.21.0", "cmd": "qwen"},
			},
		},
	})
	if code != 200 {
		t.Fatalf("save status = %d", code)
	}

	// list returns it with the expected wire shape
	_, arr := callArr(t, srv, "acp_list_custom_agents", map[string]any{})
	if len(arr) != 1 {
		t.Fatalf("expected 1 custom agent, got %d", len(arr))
	}
	info := arr[0].(map[string]any)
	if info["registryId"] != "qwen-code" {
		t.Fatalf("registryId = %v", info["registryId"])
	}
	if info["agentType"] != "custom:qwen-code" {
		t.Fatalf("agentType = %v", info["agentType"])
	}
	if info["launchable"] != true {
		t.Fatalf("expected launchable, got %v (problem=%v)", info["launchable"], info["problem"])
	}
	if info["source"] != "manual" {
		t.Fatalf("new row should default source=manual, got %v", info["source"])
	}
	if info["supportsMcp"] != true {
		t.Fatalf("new row should default supportsMcp=true, got %v", info["supportsMcp"])
	}

	// update (upsert): change the name, source should be preserved
	call(t, srv, "acp_save_custom_agent", map[string]any{
		"params": map[string]any{
			"registryId":       "qwen-code",
			"name":             "Qwen Updated",
			"distributionKind": "npx",
			"spec":             map[string]any{"npx": map[string]any{"package": "@qwen-code/qwen-code@0.22.0"}},
		},
	})
	_, arr2 := callArr(t, srv, "acp_list_custom_agents", map[string]any{})
	if len(arr2) != 1 || arr2[0].(map[string]any)["name"] != "Qwen Updated" {
		t.Fatalf("upsert did not update: %v", arr2)
	}

	// delete
	dc, _ := call(t, srv, "acp_delete_custom_agent", map[string]any{"registryId": "qwen-code"})
	if dc != 200 {
		t.Fatalf("delete status = %d", dc)
	}
	_, after := callArr(t, srv, "acp_list_custom_agents", map[string]any{})
	if len(after) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(after))
	}
	// deleting again is a not-found error
	dc2, _ := call(t, srv, "acp_delete_custom_agent", map[string]any{"registryId": "qwen-code"})
	if dc2 == 200 {
		t.Fatalf("second delete should fail")
	}
}

func TestCustomAgentRejectsBuiltinID(t *testing.T) {
	srv := newTestServer(t)
	// "claude-acp" is a built-in registry id → collision rejection.
	code, _ := call(t, srv, "acp_save_custom_agent", map[string]any{
		"params": map[string]any{
			"registryId":       "claude-acp",
			"name":             "X",
			"distributionKind": "npx",
			"spec":             map[string]any{"npx": map[string]any{"package": "x@1"}},
		},
	})
	if code == 200 {
		t.Fatalf("built-in registry id should be rejected")
	}
	// missing npx package → validation error
	code2, _ := call(t, srv, "acp_save_custom_agent", map[string]any{
		"params": map[string]any{
			"registryId":       "my-agent",
			"name":             "X",
			"distributionKind": "npx",
			"spec":             map[string]any{},
		},
	})
	if code2 == 200 {
		t.Fatalf("missing npx package should be rejected")
	}
}
