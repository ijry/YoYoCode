package handlers_test

// System proxy + terminal settings round-trips and the default-shell picker.

import "testing"

func TestSystemProxySettings(t *testing.T) {
	srv := newTestServer(t)

	// default: disabled, no url.
	_, def := call(t, srv, "get_system_proxy_settings", map[string]any{})
	if def["enabled"] != false {
		t.Fatalf("default enabled = %v", def["enabled"])
	}

	// enabling without a url → configuration_missing.");
	if code, out := call(t, srv, "update_system_proxy_settings", map[string]any{
		"settings": map[string]any{"enabled": true, "proxy_url": ""},
	}); code != 422 || out["code"] != "configuration_missing" {
		t.Fatalf("enable w/o url: %d %v", code, out)
	}

	// enable with a bare host:port → scheme added, no_proxy canonicalized.");
	_, upd := call(t, srv, "update_system_proxy_settings", map[string]any{
		"settings": map[string]any{"enabled": true, "proxy_url": "127.0.0.1:7890", "no_proxy": " a.com , , b.com "},
	})
	if upd["proxy_url"] != "http://127.0.0.1:7890" {
		t.Fatalf("proxy_url not normalized: %v", upd["proxy_url"])
	}
	if upd["no_proxy"] != "a.com,b.com" {
		t.Fatalf("no_proxy not canonicalized: %v", upd["no_proxy"])
	}

	// persisted + healed on read.
	_, got := call(t, srv, "get_system_proxy_settings", map[string]any{})
	if got["enabled"] != true || got["proxy_url"] != "http://127.0.0.1:7890" {
		t.Fatalf("persisted proxy = %v", got)
	}

	// disable clears to disabled.
	_, off := call(t, srv, "update_system_proxy_settings", map[string]any{"settings": map[string]any{"enabled": false}})
	if off["enabled"] != false {
		t.Fatalf("disable = %v", off)
	}
}

func TestSystemTerminalSettings(t *testing.T) {
	srv := newTestServer(t)

	_, def := call(t, srv, "get_system_terminal_settings", map[string]any{})
	if def["colorize_command_output"] != false {
		t.Fatalf("default colorize = %v", def["colorize_command_output"])
	}
	if def["default_shell"] != nil {
		t.Fatalf("default_shell should be null: %v", def["default_shell"])
	}

	_, upd := call(t, srv, "update_system_terminal_settings", map[string]any{
		"settings": map[string]any{"default_shell": "pwsh.exe", "colorize_command_output": true},
	})
	if upd["default_shell"] != "pwsh.exe" || upd["colorize_command_output"] != true {
		t.Fatalf("update terminal = %v", upd)
	}
	_, got := call(t, srv, "get_system_terminal_settings", map[string]any{})
	if got["default_shell"] != "pwsh.exe" {
		t.Fatalf("persisted terminal = %v", got)
	}

	// available shells: has a system option and a custom option; resolved set.
	_, shells := call(t, srv, "get_available_terminal_shells", map[string]any{})
	opts, _ := shells["options"].([]any)
	if len(opts) < 2 {
		t.Fatalf("expected >=2 shell options, got %d", len(opts))
	}
	if _, ok := shells["resolved_shell"].(string); !ok {
		t.Fatalf("resolved_shell missing: %v", shells)
	}
	first := opts[0].(map[string]any)
	if first["id"] != "system" {
		t.Fatalf("first option should be system: %v", first)
	}
	last := opts[len(opts)-1].(map[string]any)
	if last["id"] != "custom" || last["accepts_custom_path"] != true {
		t.Fatalf("last option should be custom w/ accepts_custom_path: %v", last)
	}
}
