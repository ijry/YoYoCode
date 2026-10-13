package app

// shell_test.go covers the root shell's information architecture: there is no
// outer navigation rail — the leftmost column IS the workspace sidebar — and
// Settings / Pet are reached from the bottom chrome (the single-window
// stand-in for the separate routes upstream opens).

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

func shellForTest(t *testing.T) *Shell {
	t.Helper()
	s := New(newBackendForTest(t))
	s.stream = newStreamForTest()
	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
	s.cache["folders"] = json.RawMessage(`[]`)
	return s
}

func TestShellBootsIntoTheWorkspaceSidebar(t *testing.T) {
	s := shellForTest(t)
	tt := ui.NewTester(s.View, 1240, 820)
	if s.section != "workspace" {
		t.Fatalf("boot section = %q", s.section)
	}
	// The workspace sidebar is the leftmost column: its nav rows render.
	for _, want := range []string{"New chat", "Automations", "To-dos", "Repositories", "Canvas"} {
		if !tt.HasText(want) {
			t.Errorf("workspace sidebar missing %q; texts = %v", want, tt.Texts())
		}
	}
	// The settings page is not mounted until the section is switched.
	if tt.HasText("Appearance") {
		t.Errorf("settings page should not be mounted on boot; texts = %v", tt.Texts())
	}
	// The section switcher lives on the bottom chrome.
	for _, want := range []string{"Workspace", "Settings", "Pet"} {
		if !tt.HasText(want) {
			t.Errorf("status bar missing the %q switcher; texts = %v", want, tt.Texts())
		}
	}
}

func TestStatusBarSwitchesSections(t *testing.T) {
	s := shellForTest(t)
	tt := ui.NewTester(s.View, 1240, 820)

	if err := tt.Click("Settings"); err != nil {
		t.Fatalf("click Settings: %v", err)
	}
	if s.section != "settings" {
		t.Fatalf("section = %q", s.section)
	}
	tt = ui.NewTester(s.View, 1240, 820)
	if !tt.HasText("Appearance") {
		t.Errorf("settings page not rendered; texts = %v", tt.Texts())
	}

	if err := tt.Click("Pet"); err != nil {
		t.Fatalf("click Pet: %v", err)
	}
	if s.section != "pet" {
		t.Fatalf("section = %q", s.section)
	}

	if err := tt.Click("Workspace"); err != nil {
		t.Fatalf("click Workspace: %v", err)
	}
	if s.section != "workspace" {
		t.Fatalf("section = %q", s.section)
	}
	tt = ui.NewTester(s.View, 1240, 820)
	if !tt.HasText("New chat") {
		t.Errorf("workspace not restored; texts = %v", tt.Texts())
	}
}
