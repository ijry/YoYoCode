package app

// changes_test.go covers the git-changes panel: it lists the working-tree
// changes and shows the selected file's diff. Responses are pre-seeded so the
// view renders without a git repo on disk.

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

func changesShellForTest(t *testing.T) *Shell {
	t.Helper()
	s := New(newBackendForTest(t))
	s.stream = newStreamForTest()
	s.workbench = ""
	s.detailTab = "changes"
	s.selectedFolder = "1"
	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
	s.cache["folders"] = json.RawMessage(`[{"id":1,"name":"demo","path":"/tmp/demo"}]`)
	s.cache["gitstatus:/tmp/demo"] = json.RawMessage(
		`[{"status":"M","file":"main.go"},{"status":"??","file":"new.txt"}]`)
	s.cache["gitdiff:main.go"] = json.RawMessage(`"@@ -1 +1 @@\n-old\n+new"`)
	return s
}

func TestChangesPanelListsAndDiffs(t *testing.T) {
	s := changesShellForTest(t)
	tt := ui.NewTester(s.View, 1240, 820)
	for _, want := range []string{"Changes", "main.go", "new.txt", "??", "Commit all"} {
		if !tt.HasText(want) {
			t.Errorf("changes panel missing %q; texts = %v", want, tt.Texts())
		}
	}
	// Clicking a changed file shows its diff (git_diff).
	if err := tt.Click("main.go"); err != nil {
		t.Fatalf("click changed file: %v", err)
	}
	tt.Frame()
	if s.diffSel != "main.go" {
		t.Errorf("diffSel = %q, want main.go", s.diffSel)
	}
	if !tt.HasText("+new") {
		t.Errorf("diff missing content; texts = %v", tt.Texts())
	}
	// The selected file's write actions are present.
	if !tt.HasText("Stage") || !tt.HasText("Discard") {
		t.Errorf("write actions missing; texts = %v", tt.Texts())
	}
}

// TestChangesDiscardConfirmStep locks the two-step discard: the first click only
// arms the confirmation, and Cancel backs out — nothing is discarded.
func TestChangesDiscardConfirmStep(t *testing.T) {
	s := changesShellForTest(t)
	tt := ui.NewTester(s.View, 1240, 820)
	if err := tt.Click("main.go"); err != nil {
		t.Fatalf("click file: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Discard"); err != nil {
		t.Fatalf("click discard: %v", err)
	}
	tt.Frame()
	if s.discardConfirm != "main.go" {
		t.Errorf("discardConfirm = %q, want main.go", s.discardConfirm)
	}
	if !tt.HasText("Confirm discard") {
		t.Errorf("confirm step missing; texts = %v", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("click cancel: %v", err)
	}
	tt.Frame()
	if s.discardConfirm != "" {
		t.Errorf("discardConfirm = %q, want empty", s.discardConfirm)
	}
}

func TestChangesPanelEmptyAndNoFolder(t *testing.T) {
	// No folder selected → a hint, no crash.
	s := New(newBackendForTest(t))
	s.stream = newStreamForTest()
	s.detailTab = "changes"
	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
	tt := ui.NewTester(s.View, 1240, 820)
	if !tt.HasText("Select a folder") {
		t.Errorf("no-folder hint missing; texts = %v", tt.Texts())
	}
}
