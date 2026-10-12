package handlers_test

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitShowFileBase64AndCommitFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := newTestServer(t)
	repo := t.TempDir()
	runGitTest(t, repo, "init", "-b", "main")
	runGitTest(t, repo, "config", "user.name", "Tester")
	runGitTest(t, repo, "config", "user.email", "t@e.st")

	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 5000)
	if err := os.WriteFile(filepath.Join(repo, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "a.txt", "big.txt")
	runGitTest(t, repo, "commit", "-m", "add files")

	// git_commit_files HEAD → both files, status A.
	code, files := callArr(t, srv, "git_commit_files", map[string]any{"path": repo, "commit": "HEAD"})
	if code != 200 || len(files) != 2 {
		t.Fatalf("git_commit_files = %v (status %d)", files, code)
	}
	seen := map[string]string{}
	for _, f := range files {
		m := f.(map[string]any)
		seen[m["path"].(string)] = m["status"].(string)
	}
	if seen["a.txt"] != "A" || seen["big.txt"] != "A" {
		t.Fatalf("commit file statuses = %v", seen)
	}

	// git_show_file_base64 a.txt → exists, decodes to content.
	code, blob := call(t, srv, "git_show_file_base64", map[string]any{"path": repo, "file": "a.txt"})
	if code != 200 || blob["exists"] != true || blob["too_large"] != false {
		t.Fatalf("show_file_base64 a.txt = %v (status %d)", blob, code)
	}
	dec, derr := base64.StdEncoding.DecodeString(blob["data"].(string))
	if derr != nil || string(dec) != "hello world\n" {
		t.Fatalf("decoded = %q (err %v)", string(dec), derr)
	}
	if bs, _ := blob["byte_size"].(float64); bs != 12 {
		t.Fatalf("byte_size = %v, want 12", blob["byte_size"])
	}

	// maxBytes below big.txt size → too_large (data empty, size reported).
	code, tooBig := call(t, srv, "git_show_file_base64", map[string]any{"path": repo, "file": "big.txt", "maxBytes": 4096})
	if code != 200 || tooBig["too_large"] != true || tooBig["exists"] != true {
		t.Fatalf("show_file_base64 big.txt = %v", tooBig)
	}
	if tooBig["data"] != "" {
		t.Fatalf("too_large should carry no data: %v", tooBig["data"])
	}
	if bs, _ := tooBig["byte_size"].(float64); bs != 5000 {
		t.Fatalf("big byte_size = %v, want 5000", tooBig["byte_size"])
	}

	// Missing path at a valid ref → exists false, ref_missing false.
	code, noPath := call(t, srv, "git_show_file_base64", map[string]any{"path": repo, "file": "nope.txt"})
	if code != 200 || noPath["exists"] != false || noPath["ref_missing"] != false {
		t.Fatalf("missing path = %v", noPath)
	}

	// Missing revision → exists false, ref_missing true.
	code, noRef := call(t, srv, "git_show_file_base64", map[string]any{"path": repo, "file": "a.txt", "refName": "no-such-branch"})
	if code != 200 || noRef["exists"] != false || noRef["ref_missing"] != true {
		t.Fatalf("missing ref = %v", noRef)
	}
}
