package handlers_test

import (
	"os/exec"
	"testing"
)

func TestForgeSettings(t *testing.T) {
	srv := newTestServer(t)

	// default store.
	_, store := call(t, srv, "forge_settings_get", map[string]any{})
	g, _ := store["global"].(map[string]any)
	if g == nil || g["writeback_default"] != true {
		t.Fatalf("default global: %v", store)
	}
	if _, ok := store["folders"].(map[string]any); !ok {
		t.Fatalf("folders missing: %v", store)
	}

	// set global scenario.
	_, s2 := call(t, srv, "forge_settings_set", map[string]any{
		"settings": map[string]any{"default_issue_scenario": "  plan_first ", "writeback_default": false},
	})
	g2 := s2["global"].(map[string]any)
	if g2["default_issue_scenario"] != "plan_first" || g2["writeback_default"] != false {
		t.Fatalf("global set/trim: %v", g2)
	}

	// set a folder scope, then drop it.
	_, s3 := call(t, srv, "forge_settings_set", map[string]any{
		"folderId": 7, "settings": map[string]any{"default_pr_scenario": "fix"},
	})
	folders := s3["folders"].(map[string]any)
	if _, ok := folders["7"]; !ok {
		t.Fatalf("folder scope not stored: %v", folders)
	}
	_, s4 := call(t, srv, "forge_settings_set", map[string]any{"folderId": 7})
	if _, ok := s4["folders"].(map[string]any)["7"]; ok {
		t.Fatalf("folder scope should be dropped when settings absent: %v", s4)
	}
}

func TestFolderForgeRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := newTestServer(t)
	repo := t.TempDir()
	runGitTest(t, repo, "init")
	runGitTest(t, repo, "remote", "add", "origin", "git@github.com:foo/bar.git")

	// register the folder via open_folder.
	code, det := call(t, srv, "open_folder", map[string]any{"path": repo})
	if code != 200 {
		t.Fatalf("open_folder status %d", code)
	}
	fid := det["id"]

	_, rem := call(t, srv, "folder_forge_remote", map[string]any{"folderId": fid})
	if rem["server_host"] != "github.com" {
		t.Fatalf("host: %v", rem)
	}
	if rem["owner_repo"] != "foo/bar" {
		t.Fatalf("owner_repo: %v", rem)
	}
	if rem["provider"] != "github" || rem["supported"] != true {
		t.Fatalf("provider/supported: %v", rem)
	}

	// Switch to an unrecognized host → provider defaults github, supported false.
	runGitTest(t, repo, "remote", "set-url", "origin", "https://git.corp.internal/team/app.git")
	_, rem2 := call(t, srv, "folder_forge_remote", map[string]any{"folderId": fid})
	if rem2["server_host"] != "git.corp.internal" || rem2["supported"] != false {
		t.Fatalf("unrecognized host: %v", rem2)
	}
	if rem2["owner_repo"] != "team/app" {
		t.Fatalf("owner_repo2: %v", rem2)
	}
}
