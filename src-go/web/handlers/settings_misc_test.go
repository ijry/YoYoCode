package handlers_test

import "testing"

func TestMiscSettings(t *testing.T) {
	srv := newTestServer(t)

	// question defaults ON; toggle OFF persists.
	{
		_, def := call(t, srv, "get_question_settings", map[string]any{})
		if def["enabled"] != true {
			t.Fatalf("question default should be true: %v", def)
		}
		_, set := call(t, srv, "set_question_settings", map[string]any{"settings": map[string]any{"enabled": false}})
		if set["enabled"] != false {
			t.Fatalf("set question: %v", set)
		}
		_, got := call(t, srv, "get_question_settings", map[string]any{})
		if got["enabled"] != false {
			t.Fatalf("persisted question: %v", got)
		}
	}

	// feedback + session_info default OFF.
	for _, cmd := range []string{"get_feedback_settings", "get_session_info_settings"} {
		_, def := call(t, srv, cmd, map[string]any{})
		if def["enabled"] != false {
			t.Fatalf("%s default should be false: %v", cmd, def)
		}
	}

	// chat_authoring two toggles.
	{
		_, def := call(t, srv, "get_chat_authoring_settings", map[string]any{})
		if def["automations_enabled"] != false || def["work_tasks_enabled"] != false {
			t.Fatalf("chat_authoring default: %v", def)
		}
		_, set := call(t, srv, "set_chat_authoring_settings", map[string]any{
			"settings": map[string]any{"automations_enabled": true, "work_tasks_enabled": false},
		})
		if set["automations_enabled"] != true || set["work_tasks_enabled"] != false {
			t.Fatalf("set chat_authoring: %v", set)
		}
	}

	// delegation defaults + depth clamp.
	{
		_, def := call(t, srv, "get_delegation_settings", map[string]any{})
		if def["enabled"] != false || def["depth_limit"].(float64) != 1 || def["completed_cache_max_mb"].(float64) != 512 {
			t.Fatalf("delegation default: %v", def)
		}
		_, set := call(t, srv, "set_delegation_settings", map[string]any{
			"settings": map[string]any{"enabled": true, "depth_limit": 99},
		})
		if set["depth_limit"].(float64) != 8 {
			t.Fatalf("depth should clamp to 8: %v", set)
		}
	}
}

func TestMiscEndpoints(t *testing.T) {
	srv := newTestServer(t)

	// get_home_directory → non-empty string.
	{
		body, _ := jsonBody(map[string]any{})
		code, raw := rawPost(t, srv, "get_home_directory", body)
		if code != 200 || len(raw) < 3 {
			t.Fatalf("get_home_directory: %d %q", code, raw)
		}
	}

	// probe_terminal_shell_path: a bogus path is false.
	{
		code, _ := call(t, srv, "probe_terminal_shell_path", map[string]any{"path": "/no/such/shell/xyz"})
		if code != 200 {
			t.Fatalf("probe status %d", code)
		}
	}

	// pet_list_active_sessions → empty payload shape.
	{
		_, out := call(t, srv, "pet_list_active_sessions", map[string]any{})
		if out["running_count"].(float64) != 0 {
			t.Fatalf("pet sessions running_count: %v", out)
		}
		if _, ok := out["sessions"].([]any); !ok {
			t.Fatalf("pet sessions missing sessions array: %v", out)
		}
	}

	// list_child_conversations with no parent → empty array.
	{
		_, arr := callArr(t, srv, "list_child_conversations", map[string]any{"parentConversationId": 999})
		if len(arr) != 0 {
			t.Fatalf("child conversations should be empty: %v", arr)
		}
	}
}

func TestSubmitSessionFeedback(t *testing.T) {
	srv := newTestServer(t)
	// Missing required params → error (not 200).
	if code, _ := call(t, srv, "submit_session_feedback", map[string]any{}); code == 200 {
		t.Error("missing connectionId/text should fail")
	}
	// Valid params → recoverable invalid_input (frontend falls back to a prompt).
	code, body := call(t, srv, "submit_session_feedback", map[string]any{
		"connectionId": "conn-1", "text": "please stop",
	})
	if code == 200 {
		t.Fatalf("submit should return a recoverable error, got 200: %v", body)
	}
	if body["code"] != "invalid_input" {
		t.Errorf("error code = %v, want invalid_input", body["code"])
	}
}
