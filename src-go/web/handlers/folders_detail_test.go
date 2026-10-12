package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFolderDetailAndGroups(t *testing.T) {
	srv := newTestServer(t)
	proj := t.TempDir()

	// open_folder → faithful FolderDetail shape.
	code, det := call(t, srv, "open_folder", map[string]any{"path": proj})
	if code != 200 {
		t.Fatalf("open_folder status %d: %v", code, det)
	}
	for _, k := range []string{"id", "name", "path", "git_branch", "default_agent_type", "last_opened_at", "sort_order", "color", "parent_id", "kind", "alias", "group_id"} {
		if _, ok := det[k]; !ok {
			t.Fatalf("FolderDetail missing %q: %v", k, det)
		}
	}
	if det["color"] != "inherit" {
		t.Fatalf("default color = %v", det["color"])
	}
	if det["kind"] != "regular" {
		t.Fatalf("default kind = %v", det["kind"])
	}
	if det["default_agent_type"] != nil {
		t.Fatalf("default_agent_type should be null: %v", det["default_agent_type"])
	}
	fid := det["id"]

	// list_open_folder_details returns the faithful shape too.
	{
		_, arr := callArr(t, srv, "list_open_folder_details", map[string]any{})
		if len(arr) != 1 {
			t.Fatalf("open folder details len %d", len(arr))
		}
		if _, ok := arr[0].(map[string]any)["default_agent_type"]; !ok {
			t.Fatalf("list detail not faithful shape: %v", arr[0])
		}
	}

	// update alias / color / default_agent.
	{
		_, out := call(t, srv, "update_folder_alias", map[string]any{"folderId": fid, "alias": "My Proj"})
		if out["alias"] != "My Proj" {
			t.Fatalf("alias: %v", out)
		}
		_, out = call(t, srv, "update_folder_color", map[string]any{"folderId": fid, "color": "#ff0000"})
		if out["color"] != "#ff0000" {
			t.Fatalf("color: %v", out)
		}
		_, out = call(t, srv, "update_folder_default_agent", map[string]any{"folderId": fid, "defaultAgentType": "codex"})
		if out["default_agent_type"] != "codex" {
			t.Fatalf("default_agent_type: %v", out)
		}
	}

	// folder groups: create → list → set membership → delete.
	{
		_, grp := call(t, srv, "create_folder_group", map[string]any{"name": "Work", "color": "#00ff00"})
		if grp["name"] != "Work" || grp["color"] != "#00ff00" {
			t.Fatalf("create group: %v", grp)
		}
		gid := grp["id"]
		_, groups := callArr(t, srv, "list_folder_groups", map[string]any{})
		if len(groups) != 1 {
			t.Fatalf("groups len %d", len(groups))
		}
		if code, _ := call(t, srv, "set_folder_group", map[string]any{"folderId": fid, "groupId": gid}); code != 200 {
			t.Fatalf("set_folder_group status")
		}
		_, d2 := call(t, srv, "get_folder", map[string]any{"folderId": fid})
		if d2["group_id"] == nil {
			t.Fatalf("group_id should be set after set_folder_group: %v", d2)
		}
		if code, _ := call(t, srv, "delete_folder_group", map[string]any{"groupId": gid}); code != 200 {
			t.Fatalf("delete group status")
		}
		_, d3 := call(t, srv, "get_folder", map[string]any{"folderId": fid})
		if d3["group_id"] != nil {
			t.Fatalf("group_id should be null after group delete: %v", d3)
		}
	}

	// create_directory under the project.
	{
		code, raw := rawPostJSON(t, srv, "create_directory", map[string]any{"parentPath": proj, "name": "newdir"})
		if code != 200 {
			t.Fatalf("create_directory status %d: %s", code, raw)
		}
		if _, err := os.Stat(filepath.Join(proj, "newdir")); err != nil {
			t.Fatalf("newdir not created: %v", err)
		}
	}
}
