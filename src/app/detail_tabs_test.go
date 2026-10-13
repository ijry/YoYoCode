package app

// detail_tabs_test.go covers the workspace main-area tab strip: the folder-scoped
// panels (files/changes/terminal) live behind tabs, and switching a tab swaps the
// content without touching the sidebar.

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDetailTabsSwitch(t *testing.T) {
	s := New(newBackendForTest(t))
	s.stream = newStreamForTest()
	s.selectedFolder = "1"
	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
	s.cache["folders"] = json.RawMessage(`[{"id":1,"name":"demo","path":"/tmp/demo"}]`)
	s.cache["ft:/tmp/demo"] = json.RawMessage(`[{"kind":"file","name":"main.go","path":"main.go"}]`)

	tt := ui.NewTester(s.View, 1240, 820)
	// The tab strip is present; the default tab is the conversation.
	for _, want := range []string{"Chat", "Files", "Changes", "Terminal"} {
		if !tt.HasText(want) {
			t.Fatalf("tab strip missing %q; texts = %v", want, tt.Texts())
		}
	}
	// Switching to the Files tab renders the folder's files.
	if err := tt.Click("Files"); err != nil {
		t.Fatalf("click Files tab: %v", err)
	}
	tt.Frame()
	if s.detailTab != "files" {
		t.Errorf("detailTab = %q, want files", s.detailTab)
	}
	if !tt.HasText("main.go") {
		t.Errorf("files tab content missing; texts = %v", tt.Texts())
	}
}
