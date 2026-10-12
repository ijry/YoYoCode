package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"yoyocode/src-go/web"
)

func TestNormalizeGitAndNumstatPaths(t *testing.T) {
	if got := normalizeGitStatusPath("old name.txt -> new name.txt"); got != "new name.txt" {
		t.Errorf("rename status path = %q", got)
	}
	if got := normalizeNumstatPath("src/{old => new}/f.go"); got != "src/new/f.go" {
		t.Errorf("brace numstat = %q", got)
	}
	if got := normalizeNumstatPath("a => b"); got != "b" {
		t.Errorf("arrow numstat = %q", got)
	}
	if parseNumstatInt("-") != 0 || parseNumstatInt("42") != 42 {
		t.Errorf("numstat int parse wrong")
	}
}

func TestBuildWorkspaceTreeDepth(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755)
	os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755)

	tree := buildWorkspaceTree(root, "", 1)
	if len(tree) != 2 {
		t.Fatalf("top-level nodes = %d, want 2 (node_modules ignored): %v", len(tree), tree)
	}
	// Dirs sort first.
	sub := tree[0].(map[string]any)
	if sub["kind"] != "dir" || sub["name"] != "sub" {
		t.Fatalf("first node = %v, want dir sub", sub)
	}
	children := sub["children"].([]any)
	// b.txt + deep (dir, depth-capped children empty).
	if len(children) != 2 {
		t.Fatalf("sub children = %v", children)
	}
	var deep map[string]any
	for _, ch := range children {
		m := ch.(map[string]any)
		if m["name"] == "deep" {
			deep = m
		}
	}
	if deep == nil || deep["kind"] != "dir" {
		t.Fatalf("deep dir missing: %v", children)
	}
	if dc := deep["children"].([]any); len(dc) != 0 {
		t.Errorf("depth cap: deep children should be empty, got %v", dc)
	}
	a := tree[1].(map[string]any)
	if a["kind"] != "file" || a["path"] != "a.txt" {
		t.Errorf("file node = %v", a)
	}
}

func TestWorkspaceStreamLifecycle(t *testing.T) {
	root := t.TempDir()
	ctx := func() *web.Context { return &web.Context{Body: map[string]any{"rootPath": root}} }

	// get before start → not found.
	if _, err := getWorkspaceSnapshot(ctx()); err == nil {
		t.Error("expected not-found before stream started")
	}
	// start → full snapshot.
	res, err := startWorkspaceStateStream(ctx())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	snap := res.(map[string]any)
	if snap["full"] != true || snap["version"] != workspaceStateProtocolVersion {
		t.Fatalf("snapshot shape = %v", snap)
	}
	if _, ok := snap["tree_snapshot"].([]any); !ok {
		t.Errorf("tree_snapshot should be a list: %v", snap["tree_snapshot"])
	}
	if d, ok := snap["deltas"].([]any); !ok || len(d) != 0 {
		t.Errorf("deltas should be empty: %v", snap["deltas"])
	}
	// get after start → ok.
	if _, err := getWorkspaceSnapshot(ctx()); err != nil {
		t.Fatalf("get after start: %v", err)
	}
	// stop → null, then get → not found again.
	if r, err := stopWorkspaceStateStream(ctx()); err != nil || r != nil {
		t.Fatalf("stop = %v, %v", r, err)
	}
	if _, err := getWorkspaceSnapshot(ctx()); err == nil {
		t.Error("expected not-found after stop")
	}
}

func TestWorkspaceSnapshotLightweight(t *testing.T) {
	root := t.TempDir()
	// wantsTreeGit=false → tree/git snapshots omitted (nil).
	snap := buildWorkspaceSnapshot(root, 0, false)
	if snap["tree_snapshot"] != nil || snap["git_snapshot"] != nil {
		t.Errorf("lightweight subscriber should omit tree/git: %v", snap)
	}
	if snap["is_git_repo"] != false {
		t.Errorf("temp dir is not a git repo: %v", snap["is_git_repo"])
	}
}
