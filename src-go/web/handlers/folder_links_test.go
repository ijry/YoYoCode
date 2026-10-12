package handlers_test

// folder_links: preview a candidate directory, create the link, verify its
// on-disk status, rename it, then remove it. Symlink/junction creation can be
// unprivileged-unavailable; the test skips rather than fails there.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFolderLinks(t *testing.T) {
	srv := newTestServer(t)
	root := t.TempDir()
	targetParent := t.TempDir()
	target := filepath.Join(targetParent, "shared-lib")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	_, det := call(t, srv, "open_folder", map[string]any{"path": root})
	fid := det["id"]

	// empty list.
	_, arr := callArr(t, srv, "list_folder_links", map[string]any{"folderId": fid})
	if len(arr) != 0 {
		t.Fatalf("list should be empty, got %d", len(arr))
	}

	// preview resolves a name.
	_, plans := callArr(t, srv, "preview_folder_links", map[string]any{"folderId": fid, "paths": []any{target}})
	if len(plans) != 1 {
		t.Fatalf("preview len %d", len(plans))
	}
	plan := plans[0].(map[string]any)
	if plan["name"] != "shared-lib" || plan["baseName"] != "shared-lib" {
		t.Fatalf("preview plan = %v", plan)
	}
	if plan["rejection"] != nil {
		t.Fatalf("unexpected rejection: %v", plan["rejection"])
	}

	// create the link.
	_, created := callArr(t, srv, "create_folder_links", map[string]any{
		"folderId": fid, "gitExclude": false,
		"items": []any{map[string]any{"path": target}},
	})
	if len(created) != 1 {
		// Symlink/junction unsupported (unprivileged Windows w/o Dev Mode).
		if _, statErr := os.Lstat(filepath.Join(root, "shared-lib")); statErr != nil {
			t.Skip("symlink creation unavailable in this environment")
		}
		t.Fatalf("create returned %d", len(created))
	}
	c0 := created[0].(map[string]any)
	if c0["name"] != "shared-lib" {
		t.Fatalf("created name = %v", c0["name"])
	}
	if c0["status"] != "ok" {
		t.Fatalf("created status = %v, want ok", c0["status"])
	}
	linkID := c0["id"]

	// the symlink exists on disk and resolves to the target.
	if _, err := os.Stat(filepath.Join(root, "shared-lib")); err != nil {
		t.Fatalf("link not on disk: %v", err)
	}

	// a second preview of the same target reports already_linked.
	_, plans2 := callArr(t, srv, "preview_folder_links", map[string]any{"folderId": fid, "paths": []any{target}})
	if plans2[0].(map[string]any)["rejection"] != "already_linked" {
		t.Fatalf("second preview rejection = %v", plans2[0].(map[string]any)["rejection"])
	}

	// rename.
	_, ren := call(t, srv, "rename_folder_link", map[string]any{"linkId": linkID, "newName": "libs"})
	if ren["name"] != "libs" || ren["status"] != "ok" {
		t.Fatalf("rename = %v", ren)
	}
	if _, err := os.Stat(filepath.Join(root, "libs")); err != nil {
		t.Fatalf("renamed link not on disk: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "shared-lib")); err == nil {
		t.Fatalf("old link name still present after rename")
	}

	// remove (and delete the link entry).
	if code, _ := call(t, srv, "remove_folder_link", map[string]any{"linkId": linkID, "deleteLink": true}); code != 200 {
		t.Fatalf("remove status %d", code)
	}
	_, arr = callArr(t, srv, "list_folder_links", map[string]any{"folderId": fid})
	if len(arr) != 0 {
		t.Fatalf("list after remove = %d", len(arr))
	}
	if _, err := os.Lstat(filepath.Join(root, "libs")); err == nil {
		t.Fatalf("link still on disk after remove")
	}
	// target directory itself is never touched.
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target directory was removed: %v", err)
	}
}
