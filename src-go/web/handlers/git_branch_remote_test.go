package handlers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitBranchRemoteHandlers covers the credential-free local git ops:
// current_user, search_authors, remote add/set/remove, merge, rebase, and
// delete_branch — all against a real throwaway repo.
func TestGitBranchRemoteHandlers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := newTestServer(t)
	repo := t.TempDir()

	runGitTest(t, repo, "init", "-b", "main")
	runGitTest(t, repo, "config", "user.name", "Tester")
	runGitTest(t, repo, "config", "user.email", "t@e.st")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "a.txt")
	runGitTest(t, repo, "commit", "-m", "first commit")

	// git_current_user → "Tester"
	{
		body, _ := jsonBody(map[string]any{"path": repo})
		code, raw := rawPost(t, srv, "git_current_user", body)
		if code != 200 || strings.TrimSpace(raw) != "\"Tester\"" {
			t.Fatalf("git_current_user status %d raw %q", code, raw)
		}
	}

	// git_search_authors query "Tes" → ["Tester"]
	{
		code, authors := callArr(t, srv, "git_search_authors", map[string]any{"path": repo, "query": "Tes"})
		if code != 200 || len(authors) != 1 || authors[0] != "Tester" {
			t.Fatalf("git_search_authors = %v (status %d)", authors, code)
		}
	}
	// Empty query short-circuits to [].
	{
		code, authors := callArr(t, srv, "git_search_authors", map[string]any{"path": repo, "query": "  "})
		if code != 200 || len(authors) != 0 {
			t.Fatalf("git_search_authors empty = %v (status %d)", authors, code)
		}
	}

	// Remote add / set-url / remove.
	{
		code, _ := call(t, srv, "git_add_remote", map[string]any{"path": repo, "name": "origin", "url": "https://example.com/x.git"})
		if code != 200 {
			t.Fatalf("git_add_remote status %d", code)
		}
		code, _ = call(t, srv, "git_set_remote_url", map[string]any{"path": repo, "name": "origin", "url": "https://example.com/y.git"})
		if code != 200 {
			t.Fatalf("git_set_remote_url status %d", code)
		}
		out, _ := exec.Command("git", "-C", repo, "remote", "get-url", "origin").Output()
		if strings.TrimSpace(string(out)) != "https://example.com/y.git" {
			t.Fatalf("remote url = %q", strings.TrimSpace(string(out)))
		}
		code, _ = call(t, srv, "git_remove_remote", map[string]any{"path": repo, "name": "origin"})
		if code != 200 {
			t.Fatalf("git_remove_remote status %d", code)
		}
	}

	// git_merge: feature branch with one commit merges cleanly.
	runGitTest(t, repo, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "b.txt")
	runGitTest(t, repo, "commit", "-m", "feature commit")
	runGitTest(t, repo, "checkout", "main")
	{
		code, res := call(t, srv, "git_merge", map[string]any{"path": repo, "branchName": "feature"})
		if code != 200 {
			t.Fatalf("git_merge status %d: %v", code, res)
		}
		if mc, _ := res["merged_commits"].(float64); mc != 1 {
			t.Fatalf("merged_commits = %v, want 1", res["merged_commits"])
		}
		if res["conflict"] != nil {
			t.Fatalf("conflict = %v, want null", res["conflict"])
		}
	}

	// git_rebase: already up to date → message set, conflict null.
	{
		code, res := call(t, srv, "git_rebase", map[string]any{"path": repo, "branchName": "feature"})
		if code != 200 {
			t.Fatalf("git_rebase status %d: %v", code, res)
		}
		if _, ok := res["message"]; !ok {
			t.Fatalf("git_rebase missing message: %v", res)
		}
		if res["conflict"] != nil {
			t.Fatalf("rebase conflict = %v, want null", res["conflict"])
		}
	}

	// git_delete_branch feature (merged) → stdout mentions the branch.
	{
		body, _ := jsonBody(map[string]any{"path": repo, "branchName": "feature", "force": true})
		code, raw := rawPost(t, srv, "git_delete_branch", body)
		if code != 200 || !strings.Contains(raw, "feature") {
			t.Fatalf("git_delete_branch status %d raw %q", code, raw)
		}
	}
}
