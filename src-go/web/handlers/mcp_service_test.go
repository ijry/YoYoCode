package handlers_test

import "testing"

func TestYoyocodeMcpServiceStatus(t *testing.T) {
	srv := newTestServer(t)
	_, st := call(t, srv, "get_codeg_mcp_service_status", map[string]any{})
	if st["state"] != "stopped" {
		t.Errorf("state = %v, want stopped (no broker in this build)", st["state"])
	}
	if st["can_start"] != false || st["listening"] != false {
		t.Errorf("can_start/listening should be false: %v", st)
	}
	groups, ok := st["tool_groups"].([]any)
	if !ok || len(groups) != 9 {
		t.Fatalf("tool_groups = %v (want 9)", st["tool_groups"])
	}
	byKey := map[string]map[string]any{}
	for _, g := range groups {
		m := g.(map[string]any)
		byKey[m["key"].(string)] = m
	}
	// browser_eval is a dependent switch gated on browser.
	if be, ok := byKey["browser_eval"]; !ok || be["requires"] != "browser" {
		t.Errorf("browser_eval requires browser: %v", byKey["browser_eval"])
	}
	// ask (question) defaults ON.
	if byKey["ask"]["enabled"] != true {
		t.Errorf("ask default should be true: %v", byKey["ask"])
	}
}

func TestSetYoyocodeMcpToolGroup(t *testing.T) {
	srv := newTestServer(t)
	// Enable the computer tool group through the popover toggle.
	if code, _ := call(t, srv, "set_codeg_mcp_tool_group", map[string]any{"key": "computer", "enabled": true}); code != 200 {
		t.Fatalf("set tool group code %d", code)
	}
	_, st := call(t, srv, "get_codeg_mcp_service_status", map[string]any{})
	for _, g := range st["tool_groups"].([]any) {
		m := g.(map[string]any)
		if m["key"] == "computer" && m["enabled"] != true {
			t.Errorf("computer group should be enabled after toggle: %v", m)
		}
	}
	// It is also visible through the settings page (same app_metadata key).
	_, cs := call(t, srv, "get_computer_tools_settings", map[string]any{})
	if cs["enabled"] != true {
		t.Errorf("computer tools settings should reflect the toggle: %v", cs)
	}
	// Unknown slug rejected.
	if code, _ := call(t, srv, "set_codeg_mcp_tool_group", map[string]any{"key": "bogus", "enabled": true}); code == 200 {
		t.Error("unknown tool group should fail")
	}
}
