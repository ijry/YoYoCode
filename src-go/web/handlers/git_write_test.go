package handlers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitWriteOps(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := newTestServer(t)
	repo := t.TempDir()

	// git_init
	if code, _ := call(t, srv, "git_init", map[string]any{"path": repo}); code != 200 {
		t.Fatalf("git_init status %d", code)
	}
	runGitTest(t, repo, "config", "user.name", "Tester")
	runGitTest(t, repo, "config", "user.email", "t@e.st")
	runGitTest(t, repo, "config", "commit.gpgsign", "false")

	// add + commit a file via handlers.
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, srv, "git_add_files", map[string]any{"path": repo, "files": []any{"f.txt"}}); code != 200 {
		t.Fatalf("git_add_files status %d", code)
	}
	{
		code, out := call(t, srv, "git_commit", map[string]any{"path": repo, "message": "init", "files": []any{"f.txt"}})
		if code != 200 {
			t.Fatalf("git_commit status %d: %v", code, out)
		}
		if out["committed_files"].(float64) != 1 {
			t.Fatalf("committed_files: %v", out)
		}
	}

	// stash: modify, stash, verify clean, pop, verify dirty, list.
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, srv, "git_stash_push", map[string]any{"path": repo, "message": "wip"}); code != 200 {
		t.Fatalf("stash push status %d", code)
	}
	{
		_, arr := callArr(t, srv, "git_status", map[string]any{"path": repo})
		if len(arr) != 0 {
			t.Fatalf("tree should be clean after stash: %v", arr)
		}
		_, stashes := callArr(t, srv, "git_stash_list", map[string]any{"path": repo})
		if len(stashes) != 1 {
			t.Fatalf("expected 1 stash, got %d", len(stashes))
		}
		e := stashes[0].(map[string]any)
		if e["ref_name"] != "stash@{0}" {
			t.Fatalf("stash ref: %v", e)
		}
	}
	if code, _ := call(t, srv, "git_stash_pop", map[string]any{"path": repo}); code != 200 {
		t.Fatalf("stash pop status %d", code)
	}
	{
		_, arr := callArr(t, srv, "git_status", map[string]any{"path": repo})
		if len(arr) != 1 {
			t.Fatalf("tree should be dirty after pop: %v", arr)
		}
	}
	// restore the modified file so push has a clean tree.
	runGitTest(t, repo, "checkout", "--", "f.txt")

	// push to a local bare remote (no credentials needed for a file path remote).
	bare := t.TempDir()
	runGitTest(t, bare, "init", "--bare")
	runGitTest(t, repo, "remote", "add", "origin", bare)
	{
		code, out := call(t, srv, "git_push", map[string]any{"path": repo, "remote": "origin"})
		if code != 200 {
			t.Fatalf("git_push status %d: %v", code, out)
		}
		if out["upstream_set"] != true {
			t.Fatalf("first push should set upstream: %v", out)
		}
	}
	// fetch should now succeed.
	if code, out := call(t, srv, "git_fetch", map[string]any{"path": repo}); code != 200 {
		t.Fatalf("git_fetch status %d: %v", code, out)
	}
}
