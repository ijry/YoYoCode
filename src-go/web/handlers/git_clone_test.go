package handlers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCloneRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := newTestServer(t)

	// a local source repo serves as the clone URL (file path remote)
	src := t.TempDir()
	runGitTest(t, src, "init", "-b", "main")
	runGitTest(t, src, "config", "user.name", "Tester")
	runGitTest(t, src, "config", "user.email", "t@e.st")
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, src, "add", "a.txt")
	runGitTest(t, src, "commit", "-m", "first")

	target := filepath.Join(t.TempDir(), "cloned")
	if code, out := call(t, srv, "clone_repository", map[string]any{
		"url": src, "targetDir": target,
	}); code != 200 {
		t.Fatalf("clone status = %d (%v)", code, out)
	}
	if _, err := os.Stat(filepath.Join(target, "a.txt")); err != nil {
		t.Fatalf("cloned file missing: %v", err)
	}

	// cloning again into the same non-empty dir → already exists
	if code, _ := call(t, srv, "clone_repository", map[string]any{
		"url": src, "targetDir": target,
	}); code == 200 {
		t.Fatalf("clone into existing non-empty dir should fail")
	}

	// missing url/target → invalid input
	if code, _ := call(t, srv, "clone_repository", map[string]any{"url": "", "targetDir": ""}); code == 200 {
		t.Fatalf("empty url/target should fail")
	}
}
