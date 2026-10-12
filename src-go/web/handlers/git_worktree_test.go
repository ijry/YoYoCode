package handlers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitWorktreeAdd(t *testing.T) {
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
	code, _ := call(t, srv, "git_worktree_add", map[string]any{
		"path":         repo,
		"branchName":   "feature-x",
		"worktreePath": wt,
	})
	if code != 200 {
		t.Fatalf("worktree add status = %d", code)
	}
	if _, err := os.Stat(filepath.Join(wt, "a.txt")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}
	// branch now exists → adding again rejects (branch already exists)
	code2, _ := call(t, srv, "git_worktree_add", map[string]any{
		"path":         repo,
		"branchName":   "feature-x",
		"worktreePath": filepath.Join(t.TempDir(), "another"),
	})
	if code2 == 200 {
		t.Fatalf("re-adding existing branch should fail")
	}
	// existing directory → rejects
	code3, _ := call(t, srv, "git_worktree_add", map[string]any{
		"path":         repo,
		"branchName":   "feature-y",
		"worktreePath": wt,
	})
	if code3 == 200 {
		t.Fatalf("existing worktree dir should fail")
	}
}

func TestGitUpdateBranchNotCheckedOut(t *testing.T) {
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
	runGitTest(t, repo, "branch", "topic")

	// 'topic' has no configured upstream → invalid_input error, not a panic.
	code, out := call(t, srv, "git_update_branch", map[string]any{
		"path":     repo,
		"branch":   "topic",
		"isRemote": false,
	})
	if code == 200 {
		t.Fatalf("update of upstream-less branch should fail cleanly, got %v", out)
	}
}
