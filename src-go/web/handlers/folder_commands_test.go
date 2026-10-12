package handlers_test

// folder_commands CRUD + reorder + package.json bootstrap, through the real
// Router and a real on-disk SQLite DB.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFolderCommands(t *testing.T) {
	srv := newTestServer(t)
	proj := t.TempDir()
	_, det := call(t, srv, "open_folder", map[string]any{"path": proj})
	fid := det["id"]

	// empty list.
	_, arr := callArr(t, srv, "list_folder_commands", map[string]any{"folderId": fid})
	if len(arr) != 0 {
		t.Fatalf("list should be empty, got %d", len(arr))
	}

	// create two.
	_, c1 := call(t, srv, "create_folder_command", map[string]any{"folderId": fid, "name": "build", "command": "make"})
	for _, k := range []string{"id", "folder_id", "name", "command", "sort_order", "created_at", "updated_at"} {
		if _, ok := c1[k]; !ok {
			t.Fatalf("FolderCommandInfo missing %q: %v", k, c1)
		}
	}
	if c1["name"] != "build" || c1["command"] != "make" {
		t.Fatalf("create c1 = %v", c1)
	}
	if c1["sort_order"].(float64) != 0 {
		t.Fatalf("first sort_order = %v, want 0", c1["sort_order"])
	}
	_, c2 := call(t, srv, "create_folder_command", map[string]any{"folderId": fid, "name": "test", "command": "go test"})
	if c2["sort_order"].(float64) != 1 {
		t.Fatalf("second sort_order = %v, want 1", c2["sort_order"])
	}
	id1 := c1["id"]
	id2 := c2["id"]

	// update name + command.
	_, up := call(t, srv, "update_folder_command", map[string]any{"id": id1, "name": "compile", "command": "make all"})
	if up["name"] != "compile" || up["command"] != "make all" {
		t.Fatalf("update = %v", up)
	}

	// reorder: id2 before id1.
	if code, _ := call(t, srv, "reorder_folder_commands", map[string]any{"folderId": fid, "ids": []any{id2, id1}}); code != 200 {
		t.Fatalf("reorder status %d", code)
	}
	_, arr = callArr(t, srv, "list_folder_commands", map[string]any{"folderId": fid})
	if len(arr) != 2 {
		t.Fatalf("list len %d", len(arr))
	}
	if first := arr[0].(map[string]any); first["id"] != id2 {
		t.Fatalf("after reorder first = %v, want id2", first["id"])
	}

	// delete id1.
	if code, _ := call(t, srv, "delete_folder_command", map[string]any{"id": id1}); code != 200 {
		t.Fatalf("delete status %d", code)
	}
	_, arr = callArr(t, srv, "list_folder_commands", map[string]any{"folderId": fid})
	if len(arr) != 1 {
		t.Fatalf("after delete len %d", len(arr))
	}
}

func TestFolderCommandsBootstrap(t *testing.T) {
	srv := newTestServer(t)
	proj := t.TempDir()
	_, det := call(t, srv, "open_folder", map[string]any{"path": proj})
	fid := det["id"]

	// package.json with scripts + a pnpm lockfile → "pnpm run <name>".
	pkg := `{"name":"demo","scripts":{"dev":"vite","build":"vite build","lint":"eslint ."}}`
	if err := os.WriteFile(filepath.Join(proj, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "pnpm-lock.yaml"), []byte("lockfileVersion: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, arr := callArr(t, srv, "bootstrap_folder_commands_from_package_json", map[string]any{"folderId": fid, "folderPath": proj})
	if len(arr) != 3 {
		t.Fatalf("bootstrap created %d commands, want 3: %v", len(arr), arr)
	}
	// file order preserved: dev, build, lint.");
	names := []string{}
	cmds := map[string]string{}
	for _, it := range arr {
		m := it.(map[string]any)
		names = append(names, m["name"].(string))
		cmds[m["name"].(string)] = m["command"].(string)
	}
	if names[0] != "dev" || names[1] != "build" || names[2] != "lint" {
		t.Fatalf("script order not preserved: %v", names)
	}
	if cmds["build"] != "pnpm run build" {
		t.Fatalf("command = %q, want \"pnpm run build\"", cmds["build"])
	}

	// second bootstrap is a no-op (returns existing).
	_, arr2 := callArr(t, srv, "bootstrap_folder_commands_from_package_json", map[string]any{"folderId": fid, "folderPath": proj})
	if len(arr2) != 3 {
		t.Fatalf("second bootstrap len %d, want 3 (no dup)", len(arr2))
	}
}
