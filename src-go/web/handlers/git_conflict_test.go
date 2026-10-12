package handlers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildConflictRepo makes a repo whose "feature" branch and "main" both edit
// a.txt divergently, so merging feature into main conflicts.
func buildConflictRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitTest(t, repo, "init", "-b", "main")
	runGitTest(t, repo, "config", "user.name", "Tester")
	runGitTest(t, repo, "config", "user.email", "t@e.st")
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("line1\n")
	runGitTest(t, repo, "add", "a.txt")
	runGitTest(t, repo, "commit", "-m", "base")
	runGitTest(t, repo, "checkout", "-b", "feature")
	write("feature-change\n")
	runGitTest(t, repo, "add", "a.txt")
	runGitTest(t, repo, "commit", "-m", "feature edit")
	runGitTest(t, repo, "checkout", "main")
	write("main-change\n")
	runGitTest(t, repo, "add", "a.txt")
	runGitTest(t, repo, "commit", "-m", "main edit")
	return repo
}

func TestGitConflictFamily(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := newTestServer(t)
	repo := buildConflictRepo(t)

	// Merge feature → main conflicts (reported as a conflict object, not error).
	code, res := call(t, srv, "git_merge", map[string]any{"path": repo, "branchName": "feature"})
	if code != 200 {
		t.Fatalf("git_merge status %d: %v", code, res)
	}
	conflict, ok := res["conflict"].(map[string]any)
	if !ok {
		t.Fatalf("expected conflict object, got %v", res["conflict"])
	}
	if conflict["operation"] != "merge" || conflict["has_conflicts"] != true {
		t.Fatalf("conflict shape: %v", conflict)
	}
	cf, _ := conflict["conflicted_files"].([]any)
	if len(cf) != 1 || cf[0] != "a.txt" {
		t.Fatalf("conflicted_files = %v", conflict["conflicted_files"])
	}

	// Merge in progress.
	code, mh := callBool(t, srv, "git_has_merge_head", map[string]any{"path": repo})
	if code != 200 || !mh {
		t.Fatalf("git_has_merge_head = %v (status %d), want true", mh, code)
	}

	// list_conflicts → ["a.txt"].
	code, conflicts := callArr(t, srv, "git_list_conflicts", map[string]any{"path": repo})
	if code != 200 || len(conflicts) != 1 || conflicts[0] != "a.txt" {
		t.Fatalf("git_list_conflicts = %v (status %d)", conflicts, code)
	}

	// conflict_file_versions: ours has main-change, theirs feature-change, merged
	// has conflict markers.
	code, ver := call(t, srv, "git_conflict_file_versions", map[string]any{"path": repo, "file": "a.txt"})
	if code != 200 {
		t.Fatalf("git_conflict_file_versions status %d: %v", code, ver)
	}
	if !strings.Contains(ver["ours"].(string), "main-change") {
		t.Fatalf("ours = %q", ver["ours"])
	}
	if !strings.Contains(ver["theirs"].(string), "feature-change") {
		t.Fatalf("theirs = %q", ver["theirs"])
	}
	if !strings.Contains(ver["merged"].(string), "<<<<<<<") {
		t.Fatalf("merged missing conflict markers: %q", ver["merged"])
	}

	// Resolve + continue.
	if code, _ := call(t, srv, "git_resolve_conflict", map[string]any{"path": repo, "file": "a.txt", "content": "resolved\n"}); code != 200 {
		t.Fatalf("git_resolve_conflict status %d", code)
	}
	code, after := callArr(t, srv, "git_list_conflicts", map[string]any{"path": repo})
	if code != 200 || len(after) != 0 {
		t.Fatalf("conflicts after resolve = %v", after)
	}
	if code, _ := call(t, srv, "git_continue_operation", map[string]any{"path": repo, "operation": "merge"}); code != 200 {
		t.Fatalf("git_continue_operation status %d", code)
	}
	code, mh2 := callBool(t, srv, "git_has_merge_head", map[string]any{"path": repo})
	if code != 200 || mh2 {
		t.Fatalf("git_has_merge_head after continue = %v, want false", mh2)
	}

	// Fresh repo: start_pull_merge then abort.
	repo2 := buildConflictRepo(t)
	// Point an "upstream" at the feature commit so start_pull_merge has a target.
	featSha, _ := exec.Command("git", "-C", repo2, "rev-parse", "feature").Output()
	code, _ = call(t, srv, "git_start_pull_merge", map[string]any{"path": repo2, "upstreamCommit": strings.TrimSpace(string(featSha))})
	if code != 200 {
		t.Fatalf("git_start_pull_merge status %d", code)
	}
	code, mh3 := callBool(t, srv, "git_has_merge_head", map[string]any{"path": repo2})
	if code != 200 || !mh3 {
		t.Fatalf("start_pull_merge did not start a merge: %v", mh3)
	}
	if code, _ := call(t, srv, "git_abort_operation", map[string]any{"path": repo2, "operation": "merge"}); code != 200 {
		t.Fatalf("git_abort_operation status %d", code)
	}
	code, mh4 := callBool(t, srv, "git_has_merge_head", map[string]any{"path": repo2})
	if code != 200 || mh4 {
		t.Fatalf("git_has_merge_head after abort = %v, want false", mh4)
	}

	// Unknown operation → invalid_input.
	code, errBody := call(t, srv, "git_abort_operation", map[string]any{"path": repo2, "operation": "bogus"})
	if code != 400 || errBody["code"] != "invalid_input" {
		t.Fatalf("abort bogus: status %d body %v", code, errBody)
	}
}
