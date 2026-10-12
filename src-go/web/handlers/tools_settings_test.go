package handlers_test

// browser-tools + computer-use settings round-trips (per-key app_metadata).

import "testing"

func TestBrowserToolsSettings(t *testing.T) {
	srv := newTestServer(t)
	_, def := call(t, srv, "get_browser_tools_settings", map[string]any{})
	if def["enabled"] != false || def["eval"] != false {
		t.Fatalf("default browser tools = %v", def)
	}
	_, upd := call(t, srv, "set_browser_tools_settings", map[string]any{"settings": map[string]any{"enabled": true, "eval": true}})
	if upd["enabled"] != true || upd["eval"] != true {
		t.Fatalf("set browser tools = %v", upd)
	}
	_, got := call(t, srv, "get_browser_tools_settings", map[string]any{})
	if got["enabled"] != true {
		t.Fatalf("persisted browser tools = %v", got)
	}
}

func TestComputerToolsSettings(t *testing.T) {
	srv := newTestServer(t)
	_, def := call(t, srv, "get_computer_tools_settings", map[string]any{})
	for _, k := range []string{"enabled", "grant_ttl_minutes", "blocklist", "blocklist_removed",
		"blocklist_defaults", "stop_shortcut", "show_indicator", "allow_foreground",
		"default_delivery", "launch_enabled", "clipboard_enabled", "screen_enabled"} {
		if _, ok := def[k]; !ok {
			t.Fatalf("ComputerToolsSettings missing %q: %v", k, def)
		}
	}
	if def["enabled"] != false {
		t.Fatalf("default enabled = %v", def["enabled"])
	}
	if def["grant_ttl_minutes"].(float64) != 30 {
		t.Fatalf("default grant_ttl = %v", def["grant_ttl_minutes"])
	}
	if def["show_indicator"] != true {
		t.Fatalf("default show_indicator = %v", def["show_indicator"])
	}
	if def["default_delivery"] != "background" {
		t.Fatalf("default delivery = %v", def["default_delivery"])
	}

	// enabled toggle via its own command.
	_, en := call(t, srv, "set_computer_tools_enabled", map[string]any{"enabled": true})
	if en["enabled"] != true {
		t.Fatalf("set_enabled = %v", en)
	}

	// preferences partial write (never touches enabled).
	_, pr := call(t, srv, "set_computer_tools_preferences", map[string]any{
		"grant_ttl_minutes": 15, "default_delivery": "foreground", "blocklist": []any{"Passwords", "Passwords", " "},
	})
	if pr["enabled"] != true {
		t.Fatalf("preferences should not clear enabled: %v", pr["enabled"])
	}
	if pr["grant_ttl_minutes"].(float64) != 15 {
		t.Fatalf("grant_ttl = %v", pr["grant_ttl_minutes"])
	}
	if pr["default_delivery"] != "foreground" {
		t.Fatalf("delivery = %v", pr["default_delivery"])
	}
	bl, _ := pr["blocklist"].([]any)
	if len(bl) != 1 || bl[0] != "Passwords" {
		t.Fatalf("blocklist normalization = %v", pr["blocklist"])
	}

	// full settings write.
	_, full := call(t, srv, "set_computer_tools_settings", map[string]any{"settings": map[string]any{
		"enabled": true, "grant_ttl_minutes": 60, "show_indicator": false, "clipboard_enabled": true,
	}})
	if full["grant_ttl_minutes"].(float64) != 60 || full["show_indicator"] != false || full["clipboard_enabled"] != true {
		t.Fatalf("full settings = %v", full)
	}
}
