package app

// login_test.go covers the token gate: a tokenless shell shows the login
// surface (not the workspace), and the token probe checks /api/health exactly
// like upstream's login page.

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestLoginGateRendersWhenTokenless locks the gate: with no token the shell
// shows the login surface instead of the workspace chrome.
func TestLoginGateRendersWhenTokenless(t *testing.T) {
	b := newBackendForTest(t)
	b.Token = "" // a tokenless boot → the login gate
	s := New(b)
	tt := ui.NewTester(s.View, 900, 600)
	for _, want := range []string{"YoYoCode", "Connect"} {
		if !tt.HasText(want) {
			t.Errorf("login gate missing %q; texts = %v", want, tt.Texts())
		}
	}
	// The workspace nav rail must not render behind the gate.
	if tt.HasText("Workspace") {
		t.Errorf("workspace should be hidden behind the login gate; texts = %v", tt.Texts())
	}
}

// TestLoginProbeHealth locks the token probe against /api/health — the same
// check upstream's login page runs (200 → valid, 401 → invalid).
func TestLoginProbeHealth(t *testing.T) {
	b := newBackendForTest(t)
	if ok, status, err := probeHealth(b.URL, "tok"); err != nil || !ok || status != 200 {
		t.Errorf("probe(valid) = ok=%v status=%d err=%v, want true/200/nil", ok, status, err)
	}
	if ok, status, _ := probeHealth(b.URL, "wrong"); ok || status != 401 {
		t.Errorf("probe(wrong) = ok=%v status=%d, want false/401", ok, status)
	}
	if ok, status, _ := probeHealth(b.URL, ""); ok || status != 401 {
		t.Errorf("probe(empty) = ok=%v status=%d, want false/401", ok, status)
	}
}
