package handlers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOpenWorktreeFolder(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := newTestServer(t)
	repo := t.TempDir()
	runGitTest(t, repo, "init", "-b", "main")
	runGitTest(t, repo, "config", "user.name", "Tester")
	runGitTest(t, repo, "config", "user.email", "t@e.st")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "a.txt")
	runGitTest(t, repo, "commit", "-m", "first")

	// register the source (repo) folder
	_, src := call(t, srv, "open_folder", map[string]any{"path": repo})
	srcID, _ := src["id"].(float64)
	if srcID == 0 {
		t.Fatalf("source folder not registered: %v", src)
	}

	// create a worktree on a feature branch
	wt := filepath.Join(t.TempDir(), "feature-wt")
	if code, _ := call(t, srv, "git_worktree_add", map[string]any{"path": repo, "branchName": "feature-x", "worktreePath": wt}); code != 200 {
		t.Fatalf("worktree add failed")
	}

	// open the worktree folder parented under the source
	code, det := call(t, srv, "open_worktree_folder", map[string]any{"path": wt, "sourceFolderId": int(srcID)})
	if code != 200 {
		t.Fatalf("open_worktree_folder status = %d (%v)", code, det)
	}
	if det["path"] == nil {
		t.Fatalf("no folder detail returned: %v", det)
	}
	// alias seeded from the branch
	if det["alias"] != "feature-x" {
		t.Fatalf("alias should be seeded to branch, got %v", det["alias"])
	}
	// parented under the source (its parent_id, which is null → so parent = srcID)
	if pid, _ := det["parent_id"].(float64); pid != srcID {
		t.Fatalf("worktree folder parent should be source id %v, got %v", srcID, det["parent_id"])
	}

	// re-open must not clobber a user-chosen alias
	fid := det["id"].(float64)
	_, _ = call(t, srv, "update_folder_alias", map[string]any{"folderId": int(fid), "alias": "my-name"})
	_, det2 := call(t, srv, "open_worktree_folder", map[string]any{"path": wt, "sourceFolderId": int(srcID)})
	if det2["alias"] != "my-name" {
		t.Fatalf("re-open should not clobber user alias, got %v", det2["alias"])
	}
}
