package handlers_test

import "testing"

func TestAppearanceModeRoundTrip(t *testing.T) {
	srv := newTestServer(t)

	// default is system
	if _, got := call(t, srv, "get_appearance_mode", map[string]any{}); got["mode"] != "system" {
		t.Fatalf("default mode should be system, got %v", got["mode"])
	}
	// set dark
	if code, _ := call(t, srv, "update_appearance_mode", map[string]any{"mode": "dark"}); code != 200 {
		t.Fatalf("update_appearance_mode status = %d", code)
	}
	if _, got := call(t, srv, "get_appearance_mode", map[string]any{}); got["mode"] != "dark" {
		t.Fatalf("mode should persist as dark, got %v", got["mode"])
	}
	// set light
	_, _ = call(t, srv, "update_appearance_mode", map[string]any{"mode": "light"})
	if _, got := call(t, srv, "get_appearance_mode", map[string]any{}); got["mode"] != "light" {
		t.Fatalf("mode should be light, got %v", got["mode"])
	}
	// an unknown value normalizes back to system
	_, _ = call(t, srv, "update_appearance_mode", map[string]any{"mode": "purple"})
	if _, got := call(t, srv, "get_appearance_mode", map[string]any{}); got["mode"] != "system" {
		t.Fatalf("unknown mode should normalize to system, got %v", got["mode"])
	}
}
