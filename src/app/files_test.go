package app

// files_test.go covers the folder file browser: it lists a directory's entries,
// walks into a sub-directory, and previews the selected file. The tree/preview
// responses are pre-seeded so the view renders without touching the filesystem.

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

func filesShellForTest(t *testing.T) *Shell {
	t.Helper()
	s := New(newBackendForTest(t))
	s.stream = newStreamForTest()
	s.workbench = "files"
	s.selectedFolder = "1"
	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
	s.cache["folders"] = json.RawMessage(`[{"id":1,"name":"demo","path":"/tmp/demo"}]`)
	s.cache["ft:/tmp/demo"] = json.RawMessage(
		`[{"kind":"dir","name":"src","path":"src","children":[]},{"kind":"file","name":"main.go","path":"main.go"}]`)
	s.cache["filepreview:main.go"] = json.RawMessage(`{"path":"main.go","content":"package main"}`)
	return s
}

func TestFilesPanelListsAndPreviews(t *testing.T) {
	s := filesShellForTest(t)
	tt := ui.NewTester(s.View, 1240, 820)
	for _, want := range []string{"Files", "src", "main.go"} {
		if !tt.HasText(want) {
			t.Errorf("files panel missing %q; texts = %v", want, tt.Texts())
		}
	}
	// Clicking a file previews it (read_file_preview).
	if err := tt.Click("main.go"); err != nil {
		t.Fatalf("click file: %v", err)
	}
	tt.Frame()
	if s.fileSel != "main.go" {
		t.Errorf("fileSel = %q, want main.go", s.fileSel)
	}
	if !tt.HasText("package main") {
		t.Errorf("preview missing content; texts = %v", tt.Texts())
	}
}

func TestFilesPanelWalksIntoDirectory(t *testing.T) {
	s := filesShellForTest(t)
	tt := ui.NewTester(s.View, 1240, 820)
	if err := tt.Click("src"); err != nil {
		t.Fatalf("click dir: %v", err)
	}
	tt.Frame()
	if s.fileDir != "src" {
		t.Errorf("fileDir = %q, want src", s.fileDir)
	}
	// The browser is now at /src; walking up returns to the root.
	s.fileUp()
	if s.fileDir != "" {
		t.Errorf("after up, fileDir = %q, want empty", s.fileDir)
	}
}
