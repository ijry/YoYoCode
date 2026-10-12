package handlers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitRemoveWorktree(t *testing.T) {
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

	wt := filepath.Join(t.TempDir(), "feature-wt")
	if code, _ := call(t, srv, "git_worktree_add", map[string]any{
		"path": repo, "branchName": "feature-x", "worktreePath": wt,
	}); code != 200 {
		t.Fatalf("worktree add status = %d", code)
	}

	// main tree refusal: branch "main" hosts the repo's own working tree
	if code, _ := call(t, srv, "git_remove_worktree", map[string]any{
		"path": repo, "branchName": "main", "sourceFolderId": 0,
		"deleteBranch": false, "force": false,
	}); code == 200 {
		t.Fatalf("removing the main working tree should be refused")
	}

	// remove the feature worktree and delete its branch
	code, out := call(t, srv, "git_remove_worktree", map[string]any{
		"path": repo, "branchName": "feature-x", "sourceFolderId": 0,
		"deleteBranch": true, "force": false,
	})
	if code != 200 {
		t.Fatalf("remove worktree status = %d (%v)", code, out)
	}
	if out["worktree_path"] == nil {
		t.Fatalf("expected worktree_path in result: %v", out)
	}
	if out["branch_deleted"] != true {
		t.Fatalf("expected branch_deleted true: %v", out)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone, stat err = %v", err)
	}
	// branch should no longer resolve
	if err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "refs/heads/feature-x").Run(); err == nil {
		t.Fatalf("feature-x branch should have been deleted")
	}

	// idempotent retry: no hosting worktree, branch already gone → still ok
	if code, out := call(t, srv, "git_remove_worktree", map[string]any{
		"path": repo, "branchName": "feature-x", "sourceFolderId": 0,
		"deleteBranch": true, "force": false,
	}); code != 200 {
		t.Fatalf("idempotent retry should succeed, got %d (%v)", code, out)
	}
}
