package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileEditorCommands(t *testing.T) {
	srv := newTestServer(t)
	root := t.TempDir()

	// create_file_tree_entry: file at root.
	{
		code, raw := rawPostJSON(t, srv, "create_file_tree_entry", map[string]any{
			"rootPath": root, "path": "", "name": "a.txt", "kind": "file",
		})
		if code != 200 {
			t.Fatalf("create file status %d: %s", code, raw)
		}
		if raw != "\"a.txt\"" && raw != "\"a.txt\"\n" {
			t.Fatalf("create returned %q", raw)
		}
	}
	// create_file_tree_entry: dir, then a nested file.
	{
		if code, _ := call(t, srv, "create_file_tree_entry", map[string]any{
			"rootPath": root, "path": "", "name": "sub", "kind": "dir",
		}); code != 200 {
			t.Fatalf("create dir status %d", code)
		}
		if code, _ := call(t, srv, "create_file_tree_entry", map[string]any{
			"rootPath": root, "path": "sub", "name": "b.txt", "kind": "file",
		}); code != 200 {
			t.Fatalf("create nested status %d", code)
		}
	}

	// Seed content for a.txt, then read_file_for_edit.
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var etag string
	{
		code, out := call(t, srv, "read_file_for_edit", map[string]any{"rootPath": root, "path": "a.txt"})
		if code != 200 {
			t.Fatalf("read_for_edit status %d: %v", code, out)
		}
		if out["content"] != "line1\nline2\n" {
			t.Fatalf("content = %q", out["content"])
		}
		if out["line_ending"] != "lf" {
			t.Fatalf("line_ending = %v", out["line_ending"])
		}
		e, ok := out["etag"].(string)
		if !ok || e == "" {
			t.Fatalf("etag missing: %v", out)
		}
		etag = e
	}

	// save_file_content with stale etag → invalid_input (409-ish 400).
	{
		code, out := call(t, srv, "save_file_content", map[string]any{
			"rootPath": root, "path": "a.txt", "content": "x", "expectedEtag": "deadbeef",
		})
		if code != 400 || out["code"] != "invalid_input" {
			t.Fatalf("stale save expected 400 invalid_input, got %d %v", code, out)
		}
	}
	// save_file_content with correct etag → ok, new etag.
	{
		code, out := call(t, srv, "save_file_content", map[string]any{
			"rootPath": root, "path": "a.txt", "content": "new content\n", "expectedEtag": etag,
		})
		if code != 200 {
			t.Fatalf("save status %d: %v", code, out)
		}
		b, _ := os.ReadFile(filepath.Join(root, "a.txt"))
		if string(b) != "new content\n" {
			t.Fatalf("file on disk = %q", string(b))
		}
	}

	// rename a.txt → renamed.txt
	{
		code, raw := rawPostJSON(t, srv, "rename_file_tree_entry", map[string]any{
			"rootPath": root, "path": "a.txt", "newName": "renamed.txt",
		})
		if code != 200 {
			t.Fatalf("rename status %d: %s", code, raw)
		}
		if _, err := os.Stat(filepath.Join(root, "renamed.txt")); err != nil {
			t.Fatalf("renamed file missing: %v", err)
		}
	}

	// move renamed.txt into sub/
	{
		code, raw := rawPostJSON(t, srv, "move_file_tree_entry", map[string]any{
			"rootPath": root, "sourcePath": "renamed.txt", "destDir": "sub",
		})
		if code != 200 {
			t.Fatalf("move status %d: %s", code, raw)
		}
		if _, err := os.Stat(filepath.Join(root, "sub", "renamed.txt")); err != nil {
			t.Fatalf("moved file missing: %v", err)
		}
		if raw != "\"sub/renamed.txt\"" && raw != "\"sub/renamed.txt\"\n" {
			t.Fatalf("move returned %q (want forward-slash rel)", raw)
		}
	}

	// delete sub/ recursively
	{
		if code, _ := call(t, srv, "delete_file_tree_entry", map[string]any{"rootPath": root, "path": "sub"}); code != 200 {
			t.Fatalf("delete status")
		}
		if _, err := os.Stat(filepath.Join(root, "sub")); !os.IsNotExist(err) {
			t.Fatalf("sub still exists")
		}
	}

	// Path traversal is rejected.
	{
		code, out := call(t, srv, "read_file_for_edit", map[string]any{"rootPath": root, "path": "../etc/passwd"})
		if code != 400 || out["code"] != "invalid_input" {
			t.Fatalf("traversal expected 400 invalid_input, got %d %v", code, out)
		}
	}
}
