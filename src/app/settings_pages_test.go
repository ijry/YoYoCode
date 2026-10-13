package app

// settings_pages_test.go covers the settings sub-pages that read real backend
// data (browser) and the honest stub pages (science, office-tools). Responses
// are pre-seeded so the views render without touching the network.

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

func settingsShellForTest(t *testing.T, page string) *Shell {
	t.Helper()
	s := New(newBackendForTest(t))
	s.stream = newStreamForTest()
	s.section = "settings"
	s.settingsPage = page
	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
	return s
}

func TestSettingsBrowserPage(t *testing.T) {
	s := settingsShellForTest(t, "browser")
	s.cache["browser-tools"] = json.RawMessage(`{"enabled":true,"eval":false}`)
	s.cache["browser-bridge"] = json.RawMessage(`{"enabled":false,"ports":[],"public_host":null,"host_pattern":null}`)
	tt := ui.NewTester(s.View, 1240, 820)
	for _, want := range []string{"Browser", "Browser tools", "Enabled", "Allow eval", "Bridge", "stopped"} {
		if !tt.HasText(want) {
			t.Errorf("browser page missing %q; texts = %v", want, tt.Texts())
		}
	}
	// The enabled toggle reflects the seeded settings.
	if !tt.HasText("on") || !tt.HasText("off") {
		t.Errorf("toggle states missing; texts = %v", tt.Texts())
	}
}

func TestSettingsSciencePage(t *testing.T) {
	s := settingsShellForTest(t, "science")
	s.cache["science-list"] = json.RawMessage(`[]`)
	tt := ui.NewTester(s.View, 1240, 820)
	for _, want := range []string{"Science", "No science packs"} {
		if !tt.HasText(want) {
			t.Errorf("science page missing %q; texts = %v", want, tt.Texts())
		}
	}
}

func TestSettingsOfficeToolsPage(t *testing.T) {
	s := settingsShellForTest(t, "office-tools")
	s.cache["officecli"] = json.RawMessage(`{"installed":false,"version":null,"path":null,"runtime_error":null}`)
	tt := ui.NewTester(s.View, 1240, 820)
	for _, want := range []string{"Office Tools", "not installed"} {
		if !tt.HasText(want) {
			t.Errorf("office page missing %q; texts = %v", want, tt.Texts())
		}
	}
}
