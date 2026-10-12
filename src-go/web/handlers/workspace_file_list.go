package handlers

// list_directory_with_files (immediate children for the "attach server file"
// picker) and list_workspace_files (flat gitignore-aware walk for fuzzy file
// search), mirroring commands/folders.rs. Pure filesystem reads.
//
// Fidelity note: upstream uses the Rust `ignore` crate (full .gitignore
// semantics + authorized folder-link expansion). This port carries a
// self-contained gitignore matcher covering the common patterns (basename,
// anchored, directory-only, negation, * / ** / ? globs) stacked per directory
// from in-tree .gitignore/.ignore/.git/info/exclude — plus the hardcoded
// FILE_TREE_IGNORED_DIRS and .DS_Store skips. folder-link expansion is not
// replicated (the folder_links subsystem is not built), so symlinked projects
// are reported as a single dir entry and not descended into.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"yoyocode/src-go/web"
)

var fileTreeIgnoredDirs = map[string]bool{".git": true, "__pycache__": true}

// entryKind classifies a dir entry, following symlinks only for the dir/file
// decision (never for traversal), matching upstream entry_presents_as_dir.
func entryKind(dir string, e os.DirEntry) (isDir, isSymlink bool) {
	if e.Type()&os.ModeSymlink != 0 {
		if info, err := os.Stat(filepath.Join(dir, e.Name())); err == nil && info.IsDir() {
			return true, true
		}
		return false, true
	}
	return e.IsDir(), false
}

// ─── gitignore matcher ──────────────────────────────────────────────────────

type ignoreRule struct {
	re      *regexp.Regexp
	negated bool
	dirOnly bool
	baseRel string // dir (rel to root, forward-slash) this rule is anchored under
}

func compileIgnoreLine(line, baseRel string) (ignoreRule, bool) {
	t := strings.TrimRight(line, " ")
	if t == "" || strings.HasPrefix(t, "#") {
		return ignoreRule{}, false
	}
	negated := false
	if strings.HasPrefix(t, "!") {
		negated = true
		t = t[1:]
	}
	dirOnly := false
	if strings.HasSuffix(t, "/") {
		dirOnly = true
		t = strings.TrimSuffix(t, "/")
	}
	anchored := false
	if strings.HasPrefix(t, "/") {
		anchored = true
		t = strings.TrimPrefix(t, "/")
	}
	if strings.Contains(t, "/") {
		anchored = true
	}
	if t == "" {
		return ignoreRule{}, false
	}
	re, err := regexp.Compile(globToRegex(t, anchored))
	if err != nil {
		return ignoreRule{}, false
	}
	return ignoreRule{re: re, negated: negated, dirOnly: dirOnly, baseRel: baseRel}, true
}

// globToRegex translates a gitignore glob (no leading !, no leading/trailing
// slash) into an anchored regex over a forward-slash subpath.
func globToRegex(pat string, anchored bool) string {
	var b strings.Builder
	if anchored {
		b.WriteString("^")
	} else {
		b.WriteString("(^|.*/)")
	}
	for i := 0; i < len(pat); {
		c := pat[i]
		switch c {
		case '*':
			if i+1 < len(pat) && pat[i+1] == '*' {
				if i+2 < len(pat) && pat[i+2] == '/' {
					b.WriteString("(.*/)?")
					i += 3
					continue
				}
				b.WriteString(".*")
				i += 2
				continue
			}
			b.WriteString("[^/]*")
			i++
		case '?':
			b.WriteString("[^/]")
			i++
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	b.WriteString("$")
	return b.String()
}

// ignoredByRules evaluates the stacked rules for a path (rel to root), last
// match wins (gitignore semantics with negation).
func ignoredByRules(rules []ignoreRule, relPath string, isDir bool) bool {
	ignored := false
	for _, r := range rules {
		if r.dirOnly && !isDir {
			continue
		}
		sub := relPath
		if r.baseRel != "" {
			if relPath == r.baseRel {
				continue
			}
			if !strings.HasPrefix(relPath, r.baseRel+"/") {
				continue
			}
			sub = strings.TrimPrefix(relPath, r.baseRel+"/")
		}
		if r.re.MatchString(sub) {
			ignored = !r.negated
		}
	}
	return ignored
}

// loadDirIgnores reads the in-tree ignore files in dir and compiles their rules
// anchored at dirRel.
func loadDirIgnores(dir, dirRel string) []ignoreRule {
	out := []ignoreRule{}
	sources := []string{
		filepath.Join(dir, ".gitignore"),
		filepath.Join(dir, ".ignore"),
	}
	if dirRel == "" {
		sources = append(sources, filepath.Join(dir, ".git", "info", "exclude"))
	}
	for _, src := range sources {
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimRight(line, "\r")
			if rule, ok := compileIgnoreLine(line, dirRel); ok {
				out = append(out, rule)
			}
		}
	}
	return out
}

// ─── commands ───────────────────────────────────────────────────────────────

// RegisterWorkspaceFileList wires list_directory_with_files + list_workspace_files.
func RegisterWorkspaceFileList(reg *Registry) {
	reg.Register("list_directory_with_files", func(c *web.Context) (any, error) {
		path := GetString(c, "path")
		if path == "" {
			return nil, web.ErrInvalidInput("path required")
		}
		return listDirectoryWithFiles(path)
	})
	reg.Register("list_workspace_files", func(c *web.Context) (any, error) {
		path := GetString(c, "path")
		if path == "" {
			return nil, web.ErrInvalidInput("path required")
		}
		return listWorkspaceFiles(path), nil
	})
}

func listDirectoryWithFiles(path string) (any, error) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil, web.ErrIO("Path is not a directory").WithDetail(path)
	}
	items, err := os.ReadDir(path)
	if err != nil {
		return nil, web.ErrIO("Failed to read directory").WithDetail(err.Error())
	}
	type row struct {
		rec   map[string]any
		isDir bool
		name  string
	}
	rows := []row{}
	for _, item := range items {
		name := item.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		isDir, _ := entryKind(path, item)
		abs := filepath.Join(path, name)
		var size any
		hasChildren := false
		if isDir {
			hasChildren = dirHasVisibleChild(abs)
		} else if fi, e := item.Info(); e == nil {
			size = fi.Size()
		}
		rows = append(rows, row{
			rec:   map[string]any{"name": name, "path": abs, "isDir": isDir, "hasChildren": hasChildren, "size": size},
			isDir: isDir, name: name,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].isDir != rows[j].isDir {
			return rows[i].isDir
		}
		return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name)
	})
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.rec)
	}
	return out, nil
}

func dirHasVisibleChild(dir string) bool {
	items, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, it := range items {
		if !strings.HasPrefix(it.Name(), ".") {
			return true
		}
	}
	return false
}

func listWorkspaceFiles(root string) []map[string]any {
	entries := []map[string]any{}
	var walk func(dir, rel string, rules []ignoreRule)
	walk = func(dir, rel string, rules []ignoreRule) {
		combined := append(append([]ignoreRule{}, rules...), loadDirIgnores(dir, rel)...)
		items, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name() < items[j].Name() })
		for _, item := range items {
			name := item.Name()
			childRel := name
			if rel != "" {
				childRel = rel + "/" + name
			}
			isDir, isSymlink := entryKind(dir, item)
			if isDir && fileTreeIgnoredDirs[name] {
				continue
			}
			if !isDir && name == ".DS_Store" {
				continue
			}
			if ignoredByRules(combined, childRel, isDir) {
				continue
			}
			kind := "file"
			if isDir {
				kind = "dir"
			}
			entries = append(entries, map[string]any{"name": name, "path": childRel, "kind": kind})
			if isDir && !isSymlink {
				walk(filepath.Join(dir, name), childRel, combined)
			}
		}
	}
	walk(root, "", nil)
	return entries
}
