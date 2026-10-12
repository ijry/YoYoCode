package handlers

// Workspace state snapshots (B11, workspace_state module), mirroring
// commands/workspace_state.rs + the workspace_state subsystem: the file-tree +
// git-status snapshot the editor's workspace panel subscribes to.
//
// Fidelity note: upstream keeps a live notify(FS)-watcher per root that pushes
// incremental deltas on workspace-state://<root>. The Go server has no such
// watcher, so start/get return a freshly-scanned FULL snapshot (full=true,
// empty deltas) and there is no live delta stream — the frontend already
// re-fetches get_workspace_snapshot after every write (create/delete/upload/
// rename), which is the same path it uses when the watcher misses an event, so
// the panel stays correct without the push channel.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"yoyocode/src-go/web"
)

const workspaceStateProtocolVersion = 1
const workspaceTreeMaxDepth = 2

type wsStream struct{ seq uint64 }

var (
	wsStreamsMu sync.Mutex
	wsStreams   = map[string]*wsStream{}
)

func wsKey(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return filepath.ToSlash(filepath.Clean(abs))
}

func RegisterWorkspaceState(reg *Registry) {
	reg.Register("start_workspace_state_stream", startWorkspaceStateStream)
	reg.Register("stop_workspace_state_stream", stopWorkspaceStateStream)
	reg.Register("get_workspace_snapshot", getWorkspaceSnapshot)
}

func startWorkspaceStateStream(c *web.Context) (any, error) {
	root, err := GetStringOrErr(c, "rootPath", "root_path")
	if err != nil {
		return nil, err
	}
	wantsTreeGit := true
	if v, ok := c.Body["wantsTreeGit"].(bool); ok {
		wantsTreeGit = v
	} else if v, ok := c.Body["wants_tree_git"].(bool); ok {
		wantsTreeGit = v
	}
	key := wsKey(root)
	wsStreamsMu.Lock()
	st := wsStreams[key]
	if st == nil {
		st = &wsStream{}
		wsStreams[key] = st
	}
	seq := st.seq
	wsStreamsMu.Unlock()
	return buildWorkspaceSnapshot(root, seq, wantsTreeGit), nil
}

func stopWorkspaceStateStream(c *web.Context) (any, error) {
	root, err := GetStringOrErr(c, "rootPath", "root_path")
	if err != nil {
		return nil, err
	}
	wsStreamsMu.Lock()
	delete(wsStreams, wsKey(root))
	wsStreamsMu.Unlock()
	return nil, nil
}

func getWorkspaceSnapshot(c *web.Context) (any, error) {
	root, err := GetStringOrErr(c, "rootPath", "root_path")
	if err != nil {
		return nil, err
	}
	key := wsKey(root)
	wsStreamsMu.Lock()
	st := wsStreams[key]
	var seq uint64
	if st != nil {
		seq = st.seq
	}
	wsStreamsMu.Unlock()
	if st == nil {
		return nil, web.ErrNotFound("Workspace stream is not running for this root")
	}
	return buildWorkspaceSnapshot(root, seq, true), nil
}

// buildWorkspaceSnapshot scans the tree (depth-2) and git status on demand.
func buildWorkspaceSnapshot(root string, seq uint64, wantsTreeGit bool) map[string]any {
	isRepo := workspaceIsGitRepo(root)
	resp := map[string]any{
		"root_path":     root,
		"seq":           seq,
		"version":       workspaceStateProtocolVersion,
		"full":          true,
		"tree_snapshot": nil,
		"git_snapshot":  nil,
		"deltas":        []any{},
		"degraded":      false,
		"is_git_repo":   isRepo,
	}
	if wantsTreeGit {
		resp["tree_snapshot"] = buildWorkspaceTree(root, "", 1)
		if isRepo {
			resp["git_snapshot"] = collectWorkspaceGitSnapshot(root)
		} else {
			resp["git_snapshot"] = []any{}
		}
	}
	return resp
}

var workspaceIgnoredNames = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true, ".DS_Store": true,
}

// buildWorkspaceTree walks the directory to workspaceTreeMaxDepth, returning
// FileTreeNode objects {kind, name, path[, children, symlink]}.
func buildWorkspaceTree(root, rel string, level int) []any {
	dir := root
	if rel != "" {
		dir = filepath.Join(root, filepath.FromSlash(rel))
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return []any{}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].IsDir() != items[j].IsDir() {
			return items[i].IsDir()
		}
		return items[i].Name() < items[j].Name()
	})
	out := []any{}
	for _, item := range items {
		name := item.Name()
		if workspaceIgnoredNames[name] {
			continue
		}
		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		isSymlink := item.Type()&os.ModeSymlink != 0
		if item.IsDir() || (isSymlink && dirLike(filepath.Join(dir, name))) {
			node := map[string]any{"kind": "dir", "name": name, "path": childRel}
			if isSymlink {
				node["symlink"] = true
				node["children"] = []any{}
			} else if level < workspaceTreeMaxDepth {
				node["children"] = buildWorkspaceTree(root, childRel, level+1)
			} else {
				node["children"] = []any{}
			}
			out = append(out, node)
		} else {
			out = append(out, map[string]any{"kind": "file", "name": name, "path": childRel})
		}
	}
	return out
}

func dirLike(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func workspaceIsGitRepo(root string) bool {
	out, _, ok := gitRun(root, "rev-parse", "--is-inside-work-tree")
	return ok && strings.TrimSpace(out) == "true"
}

// ─── git snapshot ───────────────────────────────────────────────────────────

func normalizeGitStatusPath(path string) string {
	n := strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
	if i := strings.LastIndex(n, " -> "); i >= 0 {
		n = n[i+4:]
	}
	return strings.TrimRight(strings.TrimSpace(n), "/")
}

func normalizeNumstatPath(path string) string {
	t := strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
	open := strings.Index(t, "{")
	close := strings.LastIndex(t, "}")
	if open >= 0 && close > open {
		prefix, suffix, inner := t[:open], t[close+1:], t[open+1:close]
		if idx := strings.Index(inner, " => "); idx >= 0 {
			return prefix + inner[idx+4:] + suffix
		}
	}
	if idx := strings.LastIndex(t, " => "); idx >= 0 {
		return t[idx+4:]
	}
	return t
}

// gitNumstatMap runs git diff --numstat HEAD (fallback --cached) → path→(add,del).
func gitNumstatMap(root string) map[string][2]int {
	run := func(args ...string) (map[string][2]int, bool) {
		out, _, ok := gitRun(root, args...)
		if !ok {
			return nil, false
		}
		m := map[string][2]int{}
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) < 3 {
				continue
			}
			p := normalizeNumstatPath(parts[2])
			if p == "" {
				continue
			}
			m[p] = [2]int{parseNumstatInt(parts[0]), parseNumstatInt(parts[1])}
		}
		return m, true
	}
	if m, ok := run("--no-optional-locks", "diff", "--numstat", "HEAD"); ok {
		return m
	}
	if m, ok := run("--no-optional-locks", "diff", "--numstat", "--cached"); ok {
		return m
	}
	return map[string][2]int{}
}

func parseNumstatInt(raw string) int {
	n := 0
	for _, r := range strings.TrimSpace(raw) {
		if r < '0' || r > '9' {
			return 0 // "-" (binary) or malformed → 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// collectWorkspaceGitSnapshot merges porcelain status with numstat counts into
// sorted WorkspaceGitEntry {path, status, additions, deletions}.
func collectWorkspaceGitSnapshot(root string) []any {
	out, _, ok := gitRun(root, "--no-optional-locks", "-c", "core.quotePath=false", "status", "--porcelain=v1", "--untracked-files=all")
	if !ok {
		return []any{}
	}
	stats := gitNumstatMap(root)
	type entry struct {
		path, status         string
		additions, deletions int
	}
	entries := []entry{}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 3 {
			continue
		}
		status := strings.TrimSpace(line[:2])
		p := normalizeGitStatusPath(line[3:])
		if p == "" {
			continue
		}
		add, del := 0, 0
		if v, ok := stats[p]; ok {
			add, del = v[0], v[1]
		}
		entries = append(entries, entry{p, status, add, del})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		li, lj := strings.ToLower(entries[i].path), strings.ToLower(entries[j].path)
		if li != lj {
			return li < lj
		}
		return entries[i].path < entries[j].path
	})
	result := make([]any, 0, len(entries))
	for _, e := range entries {
		result = append(result, map[string]any{
			"path": e.path, "status": e.status, "additions": e.additions, "deletions": e.deletions,
		})
	}
	return result
}
