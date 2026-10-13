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
	for _, want := range []string{"Changes", "main.go", "new.txt", "??"} {
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
