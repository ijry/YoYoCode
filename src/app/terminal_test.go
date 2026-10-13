package app

// terminal_test.go covers the terminal panel: the start prompt, the live
// snapshot with its output, and the no-folder hint. Snapshot responses are
// pre-seeded so the view renders without spawning a real PTY.

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

func terminalShellForTest(t *testing.T, termID string) *Shell {
	t.Helper()
	s := New(newBackendForTest(t))
	s.stream = newStreamForTest()
	s.workbench = "terminal"
	s.selectedFolder = "1"
	s.termID = termID
	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
	s.cache["folders"] = json.RawMessage(`[{"id":1,"name":"demo","path":"/tmp/demo"}]`)
	return s
}

func TestTerminalPanelShowsSnapshot(t *testing.T) {
	s := terminalShellForTest(t, "t1")
	s.cache["term:t1"] = json.RawMessage(`{"alive":true,"data":"$ echo hi\nhi\n","seq":5}`)
	tt := ui.NewTester(s.View, 1240, 820)
	for _, want := range []string{"Terminal", "$ echo hi", "Send"} {
		if !tt.HasText(want) {
			t.Errorf("terminal panel missing %q; texts = %v", want, tt.Texts())
		}
	}
}

func TestTerminalPanelStartPrompt(t *testing.T) {
	s := terminalShellForTest(t, "")
	tt := ui.NewTester(s.View, 1240, 820)
	if !tt.HasText("Start terminal") {
		t.Errorf("start prompt missing; texts = %v", tt.Texts())
	}
}

func TestTerminalPanelNoFolder(t *testing.T) {
	s := New(newBackendForTest(t))
	s.stream = newStreamForTest()
	s.workbench = "terminal"
	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
	tt := ui.NewTester(s.View, 1240, 820)
	if !tt.HasText("Select a folder") {
		t.Errorf("no-folder hint missing; texts = %v", tt.Texts())
	}
}
