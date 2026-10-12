package handlers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runGitTest runs git for test setup; skips the whole test if git is absent.
func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Tester", "GIT_AUTHOR_EMAIL=t@e.st",
		"GIT_COMMITTER_NAME=Tester", "GIT_COMMITTER_EMAIL=t@e.st",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestGitHandlers(t *testing.T) {
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

	// get_git_branch → "main"
	{
		body, _ := jsonBody(map[string]any{"path": repo})
		code, raw := rawPost(t, srv, "get_git_branch", body)
		if code != 200 {
			t.Fatalf("get_git_branch status %d: %s", code, raw)
		}
		if raw != "\"main\"" && raw != "\"main\"\n" {
			t.Fatalf("branch = %q", raw)
		}
	}

	// git_log → one entry, has files, pushed=false (no remote).
	{
		_, out := call(t, srv, "git_log", map[string]any{"path": repo})
		entries, _ := out["entries"].([]any)
		if len(entries) != 1 {
			t.Fatalf("git_log entries = %d: %v", len(entries), out)
		}
		e0 := entries[0].(map[string]any)
		if e0["message"] != "first commit" {
			t.Fatalf("message = %v", e0["message"])
		}
		if e0["pushed"] != false {
			t.Fatalf("pushed = %v (want false, no remote)", e0["pushed"])
		}
		files := e0["files"].([]any)
		if len(files) != 1 || files[0].(map[string]any)["path"] != "a.txt" {
			t.Fatalf("files = %v", files)
		}
		if out["has_upstream"] != false {
			t.Fatalf("has_upstream = %v", out["has_upstream"])
		}
	}

	// Dirty the tree → git_status reports the modified file.
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	{
		_, arr := callArr(t, srv, "git_status", map[string]any{"path": repo})
		if len(arr) != 1 {
			t.Fatalf("git_status len = %d: %v", len(arr), arr)
		}
		e := arr[0].(map[string]any)
		if e["file"] != "a.txt" || e["status"] != "M" {
			t.Fatalf("status entry = %v", e)
		}
	}

	// git_diff non-empty for the modified file.
	{
		_, out := call(t, srv, "git_diff", map[string]any{"path": repo})
		_ = out // string result decoded as map fails; use raw instead
	}
	{
		body, _ := jsonBody(map[string]any{"path": repo})
		code, raw := rawPost(t, srv, "git_diff", body)
		if code != 200 {
			t.Fatalf("git_diff status %d", code)
		}
		if len(raw) < 5 {
			t.Fatalf("git_diff empty: %q", raw)
		}
	}

	// New branch + checkout + list.
	{
		if code, _ := call(t, srv, "git_new_branch", map[string]any{"path": repo, "branchName": "feature"}); code != 200 {
			t.Fatalf("git_new_branch status %d", code)
		}
		_, bl := call(t, srv, "git_list_all_branches", map[string]any{"path": repo})
		local := bl["local"].([]any)
		if len(local) != 2 {
			t.Fatalf("local branches = %v", local)
		}
		if code, _ := call(t, srv, "git_checkout", map[string]any{"path": repo, "branchName": "main"}); code != 200 {
			t.Fatalf("git_checkout status %d", code)
		}
	}

	// Non-repo path → not_a_git_repository (404).
	{
		code, raw := call(t, srv, "git_status", map[string]any{"path": t.TempDir()})
		// not_a_git_repository maps to 422 (upstream status_for_app_error_code).
		if code != 422 || raw["code"] != "not_a_git_repository" {
			t.Fatalf("expected 422 not_a_git_repository, got %d %v", code, raw)
		}
	}
}
