package handlers_test

import "testing"

func TestComputerAvailable(t *testing.T) {
	srv := newTestServer(t)
	_, out := call(t, srv, "computer_available", map[string]any{})
	if out["available"] != false {
		t.Fatalf("headless server should report computer use unavailable: %v", out)
	}
	plat, _ := out["platform"].(string)
	if plat != "macos" && plat != "windows" && plat != "linux" {
		t.Fatalf("unexpected platform: %q", plat)
	}
}

func TestComputerOpsRefused(t *testing.T) {
	srv := newTestServer(t)
	for _, cmd := range []string{
		"computer_status", "computer_shared_state", "computer_stop_key_status",
		"computer_driver_info", "computer_list_shareable_windows", "computer_stop",
	} {
		code, _ := call(t, srv, cmd, map[string]any{})
		if code == 200 {
			t.Fatalf("%s should be refused on a headless server", cmd)
		}
	}
}
