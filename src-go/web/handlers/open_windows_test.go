package handlers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOpenWindowNavigation(t *testing.T) {
	srv := newTestServer(t)
	cases := []struct {
		cmd  string
		body map[string]any
		want string
	}{
		{"open_settings_window", map[string]any{"section": "mcp"}, "/settings/mcp"},
		{"open_settings_window", map[string]any{"section": "agents", "agentType": "claude_code"}, "/settings/agents?agent=claude_code"},
		{"open_settings_window", map[string]any{}, "/settings/appearance"},
		{"open_commit_window", map[string]any{"folderId": 7}, "/commit?folderId=7"},
		{"open_stash_window", map[string]any{"folderId": 3}, "/stash?folderId=3"},
		{"open_push_window", map[string]any{"folderId": 2, "branch": "feature/x"}, "/push?folderId=2&branch=feature%2Fx"},
		{"open_import_sessions_window", map[string]any{}, "/import-sessions"},
	}
	for _, tc := range cases {
		_, out := call(t, srv, tc.cmd, tc.body)
		if out["path"] != tc.want {
			t.Fatalf("%s(%v) path = %v, want %v", tc.cmd, tc.body, out["path"], tc.want)
		}
	}
}

func TestResolveWorktreeFolder(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := newTestServer(t)
	repo := t.TempDir()
	runGitTest(t, repo, "init", "-b", "main")
	runGitTest(t, repo, "config", "user.name", "Tester")
	runGitTest(t, repo, "config", "user.email", "t@e.st")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "a.txt")
	runGitTest(t, repo, "commit", "-m", "first")

	// a branch not checked out anywhere → path null
	runGitTest(t, repo, "branch", "topic")
	_, none := call(t, srv, "resolve_worktree_folder", map[string]any{"repoPath": repo, "branch": "topic"})
	if none["path"] != nil {
		t.Fatalf("topic not in a worktree should resolve to null path, got %v", none["path"])
	}

	// add a worktree for a new branch → path resolves
	wt := filepath.Join(t.TempDir(), "wt")
	runGitTest(t, repo, "worktree", "add", "-b", "feature", wt)
	_, res := call(t, srv, "resolve_worktree_folder", map[string]any{"repoPath": repo, "branch": "feature"})
	if res["path"] == nil {
		t.Fatalf("feature worktree should resolve a path, got %v", res)
	}
}
