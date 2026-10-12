package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func mkfile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListWorkspaceFilesGitignore(t *testing.T) {
	srv := newTestServer(t)
	root := t.TempDir()
	mkfile(t, root, ".gitignore", "node_modules/\n*.log\n")
	mkfile(t, root, "a.txt", "hi")
	mkfile(t, root, "src/main.go", "package main")
	mkfile(t, root, "node_modules/pkg/index.js", "x")
	mkfile(t, root, "build.log", "noise")
	mkfile(t, root, ".git/config", "[core]")
	mkfile(t, root, ".env", "SECRET=1")

	code, list := callArr(t, srv, "list_workspace_files", map[string]any{"path": root})
	if code != 200 {
		t.Fatalf("list_workspace_files status %d", code)
	}
	paths := map[string]string{}
	for _, e := range list {
		m := e.(map[string]any)
		paths[m["path"].(string)] = m["kind"].(string)
	}
	// Kept.
	for _, want := range []string{"a.txt", "src", "src/main.go", ".env"} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("expected %q in workspace files, got %v", want, paths)
		}
	}
	if paths["src"] != "dir" || paths["src/main.go"] != "file" {
		t.Fatalf("kind mismatch: %v", paths)
	}
	// Excluded by gitignore / hardcoded skips.
	for _, bad := range []string{"node_modules", "node_modules/pkg/index.js", "build.log", ".git", ".git/config"} {
		if _, ok := paths[bad]; ok {
			t.Fatalf("%q should be ignored, got %v", bad, paths)
		}
	}
}

func TestListDirectoryWithFiles(t *testing.T) {
	srv := newTestServer(t)
	root := t.TempDir()
	mkfile(t, root, "a.txt", "hello")
	mkfile(t, root, "zeta.txt", "z")
	mkfile(t, root, "src/main.go", "package main")
	mkfile(t, root, ".hidden", "x")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, list := callArr(t, srv, "list_directory_with_files", map[string]any{"path": root})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	// Dirs first (empty, src), then files (a.txt, zeta.txt); dotfiles excluded.
	var names []string
	byName := map[string]map[string]any{}
	for _, e := range list {
		m := e.(map[string]any)
		names = append(names, m["name"].(string))
		byName[m["name"].(string)] = m
	}
	if len(names) != 4 {
		t.Fatalf("expected 4 entries (dotfile excluded), got %v", names)
	}
	if names[0] != "empty" || names[1] != "src" || names[2] != "a.txt" || names[3] != "zeta.txt" {
		t.Fatalf("sort order wrong: %v", names)
	}
	if byName["src"]["isDir"] != true || byName["src"]["hasChildren"] != true {
		t.Fatalf("src dir shape: %v", byName["src"])
	}
	if byName["empty"]["hasChildren"] != false {
		t.Fatalf("empty should have no children: %v", byName["empty"])
	}
	if byName["a.txt"]["isDir"] != false {
		t.Fatalf("a.txt isDir: %v", byName["a.txt"])
	}
	if sz, _ := byName["a.txt"]["size"].(float64); sz != 5 {
		t.Fatalf("a.txt size = %v, want 5", byName["a.txt"]["size"])
	}
	if byName["src"]["size"] != nil {
		t.Fatalf("dir size should be null: %v", byName["src"]["size"])
	}
}
