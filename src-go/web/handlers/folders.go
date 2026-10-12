package handlers

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// RegisterFolders wires folder endpoints.
func RegisterFolders(reg *Registry) {
	reg.web.Register("list_open_folders", func(c *web.Context) (any, error) {
		d := stateDB(c)
		if d == nil || d.Conn == nil {
			return []map[string]any{}, nil
		}
		rows, err := d.Conn.Query(`SELECT id, name, path FROM folder WHERE deleted_at IS NULL ORDER BY last_opened_at DESC`)
		if err != nil {
			return []map[string]any{}, nil
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id int64
			var name, path string
			if rows.Scan(&id, &name, &path) == nil {
				out = append(out, map[string]any{"id": id, "name": name, "path": path})
			}
		}
		return out, nil
	})
	reg.web.Register("list_open_folder_details", func(c *web.Context) (any, error) {
		return listOpenFolderDetails(stateDB(c)), nil
	})
	reg.web.Register("list_all_folder_details", func(c *web.Context) (any, error) {
		return listOpenFolderDetails(stateDB(c)), nil
	})
	reg.web.Register("add_folder_to_history", func(c *web.Context) (any, error) {
		d := stateDB(c)
		path := strVal(c.Body["path"])
		if path == "" {
			return nil, web.ErrInvalidInput("path required")
		}
		return addFolder(d, path)
	})
	reg.web.Register("remove_folder_from_history", func(c *web.Context) (any, error) {
		// Upstream takes AddFolderParams {path} and answers `null`. A folder id
		// is accepted too, as a convenience for in-app callers.
		path := GetString(c, "path")
		d := stateDB(c)
		if path == "" {
			folderID := int64Num(c.Body["folderId"])
			if folderID == 0 {
				folderID = int64Num(c.Body["folder_id"])
			}
			path = folderPath(d, folderID)
		}
		if path == "" {
			return nil, web.ErrInvalidInput("path required")
		}
		if d == nil || d.Conn == nil {
			return EmptyResult(), nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, web.ErrInvalidInput(err.Error())
		}
		if _, err := d.Conn.Exec(
			`UPDATE folder SET deleted_at = ? WHERE path = ?`, nowRFC3339(), abs); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return EmptyResult(), nil
	})
	reg.web.Register("get_file_tree", func(c *web.Context) (any, error) {
		// Upstream's params are {path, maxDepth}. A folder id is also accepted,
		// as a convenience for the in-app callers.
		path := GetString(c, "path")
		if path == "" {
			folderID := int64Num(c.Body["folderId"])
			if folderID == 0 {
				folderID = int64Num(c.Body["folder_id"])
			}
			if folderID > 0 {
				path = folderPath(stateDB(c), folderID)
			}
		}
		if path == "" {
			return nil, web.ErrInvalidInput("path required")
		}
		return buildFileTree(path, maxDepthParam(c.Body)), nil
	})
	reg.web.Register("list_directory_entries", func(c *web.Context) (any, error) {
		path := strVal(c.Body["path"])
		if path == "" {
			return nil, web.ErrInvalidInput("path required")
		}
		return listDirectoryEntries(path)
	})
	reg.web.Register("read_file_preview", func(c *web.Context) (any, error) {
		return readFilePreview(GetString(c, "rootPath", "root_path"), GetString(c, "path"))
	})
	reg.web.Register("read_file_base64", func(c *web.Context) (any, error) {
		return readFileBase64(GetString(c, "path", "filePath"), c.Body)
	})
	reg.web.Register("save_file_copy", func(c *web.Context) (any, error) {
		content := GetString(c, "content")
		if _, ok := c.Body["content"]; ok {
			content = asString(c.Body["content"])
		}
		return saveFileCopy(GetString(c, "rootPath", "root_path"), GetString(c, "path"), content)
	})
}

// listOpenFolderDetails mirrors upstream: folder details (id, name, path,
// gitBranch) for open folders plus conversation counts.
func listOpenFolderDetails(d *db.Database) []map[string]any {
	if d == nil || d.Conn == nil {
		return []map[string]any{}
	}
	rows, err := d.Conn.Query(
		`SELECT f.id, f.name, f.path, f.git_branch,
		        (SELECT COUNT(*) FROM conversation c WHERE c.folder_id = f.id AND c.deleted_at IS NULL),
		        (SELECT COUNT(*) FROM conversation c WHERE c.folder_id = f.id AND c.deleted_at IS NULL AND c.status IN ('in_progress','pending_review'))
		 FROM folder f WHERE f.deleted_at IS NULL ORDER BY f.last_opened_at DESC`)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, total, active int64
		var name, path string
		var branch sql.NullString
		if rows.Scan(&id, &name, &path, &branch, &total, &active) == nil {
			rec := map[string]any{
				"id": id, "name": name, "path": path,
				"totalSessions": total, "activeSessions": active,
			}
			if branch.Valid {
				rec["gitBranch"] = branch.String
			}
			out = append(out, rec)
		}
	}
	return out
}

func folderPath(d *db.Database, folderID int64) string {
	if d == nil || d.Conn == nil {
		return ""
	}
	var path string
	_ = d.Conn.QueryRow(`SELECT path FROM folder WHERE id = ? AND deleted_at IS NULL`, folderID).Scan(&path)
	return path
}

// addFolder upserts the folder into the workspace (is_open=1) and returns its
// FolderHistoryEntry {id, path, name, last_opened_at} — the shape upstream's
// add_folder_to_history answers with.
func addFolder(d *db.Database, path string) (map[string]any, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, web.ErrInvalidInput(err.Error())
	}
	name := filepath.Base(abs)
	now := nowRFC3339()
	_, err = d.Conn.Exec(
		`INSERT INTO folder (name, path, last_opened_at, created_at, updated_at, is_open)
		 VALUES (?,?,?,?,?,1)
		 ON CONFLICT(path) DO UPDATE SET name=excluded.name, last_opened_at=excluded.last_opened_at, updated_at=excluded.updated_at, deleted_at=NULL`,
		name, abs, now, now, now)
	if err != nil {
		return nil, web.ErrIO(err.Error())
	}
	// The upsert may have hit an existing row, so LastInsertId is not the id to
	// report — read it back by path.
	var id int64
	if err := d.Conn.QueryRow(`SELECT id FROM folder WHERE path = ?`, abs).Scan(&id); err != nil {
		return nil, web.ErrIO(err.Error())
	}
	return map[string]any{
		"id": id, "path": abs, "name": name, "last_opened_at": now,
	}, nil
}

// maxDepthParam reads the optional maxDepth (upstream's camelCase field; the
// snake_case spelling is accepted too). 0 means unlimited, matching an omitted
// Option<usize> upstream.
func maxDepthParam(body map[string]any) int {
	for _, k := range []string{"maxDepth", "max_depth"} {
		if v, ok := body[k].(float64); ok && v > 0 {
			return int(v)
		}
	}
	return 0
}

// fileTreeNode mirrors upstream `FileTreeNode`, a serde tag="kind" snake_case
// enum. The wire shape is per-variant — a dir always carries `children` (an
// empty array when it has none) and only carries `symlink` when true — so
// MarshalJSON writes each variant out explicitly rather than leaning on struct
// tags, which cannot express "present-but-empty on one variant only".
type fileTreeNode struct {
	kind     string
	name     string
	path     string
	children []*fileTreeNode
	symlink  bool
}

func (n *fileTreeNode) MarshalJSON() ([]byte, error) {
	if n.kind == "dir" {
		children := n.children
		if children == nil {
			children = []*fileTreeNode{}
		}
		return json.Marshal(struct {
			Kind     string          `json:"kind"`
			Name     string          `json:"name"`
			Path     string          `json:"path"`
			Children []*fileTreeNode `json:"children"`
			Symlink  bool            `json:"symlink,omitempty"`
		}{Kind: "dir", Name: n.name, Path: n.path, Children: children, Symlink: n.symlink})
	}
	return json.Marshal(struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		Path string `json:"path"`
	}{Kind: "file", Name: n.name, Path: n.path})
}

// buildFileTree mirrors upstream commands::build_file_tree: the root's children
// as nested nodes with root-relative, forward-slashed paths.
//
// `.git` and `__pycache__` directories and `.DS_Store` files are skipped. A
// symlinked directory is reported as an empty dir flagged `symlink` — the walk
// deliberately does not descend through links, so a `x -> ..` loop or a pnpm
// symlink farm cannot recurse forever; the panel fills such a dir in lazily.
// Unreadable entries are skipped rather than failing the whole tree. maxDepth
// <= 0 means unlimited.
func buildFileTree(root string, maxDepth int) []*fileTreeNode {
	return walkFileTree(root, "", 1, maxDepth)
}

func walkFileTree(dir, rel string, level, maxDepth int) []*fileTreeNode {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var dirs, files []*fileTreeNode
	for _, it := range items {
		name := it.Name()
		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		full := filepath.Join(dir, name)

		// A symlink pointing at a directory counts as a directory for the
		// ignore check, the way upstream's `entry_presents_as_dir` does.
		isDir, link := entryKind(dir, it)
		if isDir {
			if fileTreeIgnoredDirs[name] {
				continue
			}
		} else if name == ".DS_Store" {
			continue
		}

		if link {
			if isDir {
				dirs = append(dirs, &fileTreeNode{kind: "dir", name: name, path: childRel, symlink: true})
			} else {
				files = append(files, &fileTreeNode{kind: "file", name: name, path: childRel})
			}
			continue
		}
		if isDir {
			var children []*fileTreeNode
			if maxDepth <= 0 || level < maxDepth {
				children = walkFileTree(full, childRel, level+1, maxDepth)
			}
			dirs = append(dirs, &fileTreeNode{kind: "dir", name: name, path: childRel, children: children})
			continue
		}
		files = append(files, &fileTreeNode{kind: "file", name: name, path: childRel})
	}
	sortTreeNodes(dirs)
	sortTreeNodes(files)
	return append(dirs, files...)
}

// sortTreeNodes orders nodes case-insensitively by name, as upstream does.
func sortTreeNodes(nodes []*fileTreeNode) {
	sort.Slice(nodes, func(i, j int) bool {
		return strings.ToLower(nodes[i].name) < strings.ToLower(nodes[j].name)
	})
}

// listDirectoryEntries mirrors upstream commands::list_directory_entries: the
// immediate SUBDIRECTORIES of `path` (files are not listed at all), each with
// its absolute path and whether it holds a non-hidden subdirectory. Hidden
// directories are skipped and the result is sorted case-insensitively by name.
func listDirectoryEntries(path string) ([]map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil, web.ErrIO("Path is not a directory").WithDetail(path)
	}
	items, err := os.ReadDir(path)
	if err != nil {
		return nil, web.ErrIO("Failed to read directory").WithDetail(err.Error())
	}
	type row struct {
		rec  map[string]any
		name string
	}
	rows := []row{}
	for _, item := range items {
		isDir, _ := entryKind(path, item)
		if !isDir {
			continue
		}
		name := item.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		abs := filepath.Join(path, name)
		rows = append(rows, row{
			rec: map[string]any{
				"name":        name,
				"path":        abs,
				"hasChildren": dirHasVisibleSubdir(abs),
			},
			name: name,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name)
	})
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.rec)
	}
	return out, nil
}

// dirHasVisibleSubdir reports whether dir holds a non-hidden subdirectory —
// the `has_children` upstream computes for list_directory_entries, which counts
// directories only (unlike the any-entry test list_directory_with_files uses).
func dirHasVisibleSubdir(dir string) bool {
	items, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, it := range items {
		if strings.HasPrefix(it.Name(), ".") {
			continue
		}
		if isDir, _ := entryKind(dir, it); isDir {
			return true
		}
	}
	return false
}

// readFilePreview mirrors upstream commands::read_file_preview: `rel` is
// workspace-relative and resolved under rootPath. The whole text comes back (up
// to the 50 MB editor limit — no truncation), and a file whose first 2 KiB hold
// a NUL is refused as binary. The response echoes the caller's relative path.
func readFilePreview(root, rel string) (map[string]any, error) {
	if root == "" {
		return nil, web.ErrInvalidInput("rootPath required")
	}
	if err := requireFolderDir(root); err != nil {
		return nil, err
	}
	target, err := resolveTreePath(root, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, web.ErrNotFound("File does not exist")
	}
	if info.IsDir() {
		return nil, web.ErrInvalidInput("Path is not a file")
	}
	if err := ensureNavigable(root, target); err != nil {
		return nil, err
	}
	if info.Size() > fileOpenHardLimit {
		return nil, web.ErrInvalidInput("File is too large to open in editor").
			WithDetail(fmt.Sprintf("size=%d, limit=%d", info.Size(), fileOpenHardLimit))
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, web.ErrIO(err.Error())
	}
	if looksBinary(data) {
		return nil, web.ErrInvalidInput("Binary files are not supported in preview")
	}
	return map[string]any{"path": rel, "content": string(data)}, nil
}

// fileBase64MinBytes is the lower clamp for read_file_base64's maxBytes; the
// default and upper bound live beside the other base64 readers
// (git_show_base64.go) as fileBase64DefaultMaxBytes / fileBase64MaxBytes.
const fileBase64MinBytes = 4_096

// readFileBase64 mirrors upstream commands::read_file_base64: an ABSOLUTE path
// (no workspace root), answered with the bare base64 string — not a wrapper
// object.
func readFileBase64(path string, body map[string]any) (any, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, web.ErrInvalidInput("Path cannot be empty")
	}
	info, err := os.Stat(trimmed)
	if err != nil {
		return nil, web.ErrNotFound("File does not exist")
	}
	if info.IsDir() {
		return nil, web.ErrInvalidInput("Path is not a file")
	}
	limit := int64(fileBase64DefaultMaxBytes)
	for _, k := range []string{"maxBytes", "max_bytes"} {
		if v, ok := body[k].(float64); ok && v > 0 {
			limit = int64(v)
			break
		}
	}
	if limit < fileBase64MinBytes {
		limit = fileBase64MinBytes
	}
	if limit > fileBase64MaxBytes {
		limit = fileBase64MaxBytes
	}
	if info.Size() > limit {
		return nil, web.ErrInvalidInput("File is too large to attach").
			WithDetail(fmt.Sprintf("max_bytes=%d", limit))
	}
	data, err := os.ReadFile(trimmed)
	if err != nil {
		return nil, web.ErrIO(err.Error())
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// saveFileCopy mirrors upstream commands::save_file_copy: `content` is written
// to a NEW sibling of `path` named `<stem>.local.<ext>` (then `.local.2`,
// `.local.3` …), leaving the original untouched. The response is the created
// file's FileSaveResult, with a root-relative path.
func saveFileCopy(root, rel, content string) (map[string]any, error) {
	if root == "" {
		return nil, web.ErrInvalidInput("rootPath required")
	}
	if err := requireFolderDir(root); err != nil {
		return nil, err
	}
	if len(content) > fileSaveHardLimit {
		return nil, web.ErrInvalidInput("File is too large to save in editor")
	}
	source, err := resolveTreePath(root, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, web.ErrNotFound("File does not exist")
	}
	if info.IsDir() {
		return nil, web.ErrInvalidInput("Path is not a file")
	}
	if err := ensureNavigable(root, source); err != nil {
		return nil, err
	}
	if li, lerr := os.Lstat(source); lerr == nil && li.Mode()&os.ModeSymlink != 0 {
		return nil, web.ErrInvalidInput("Saving symlink targets is not supported")
	}
	parent := filepath.Dir(source)
	if err := ensureNavigable(root, parent); err != nil {
		return nil, err
	}

	sourceName := filepath.Base(source)
	created := ""
	for attempt := 1; attempt <= 9999; attempt++ {
		candidate := filepath.Join(parent, localCopyName(sourceName, attempt))
		if _, serr := os.Stat(candidate); serr == nil {
			continue
		}
		created = candidate
		break
	}
	if created == "" {
		return nil, web.ErrAlreadyExists("Unable to create copy file: too many existing local copies")
	}
	if err := atomicWriteText(created, []byte(content)); err != nil {
		return nil, err
	}
	after, err := os.Stat(created)
	if err != nil {
		return nil, web.ErrIO(err.Error())
	}
	relPath, rerr := filepath.Rel(root, created)
	if rerr != nil {
		return nil, web.ErrInvalidInput("Failed to compute relative path for copy").
			WithDetail(rerr.Error())
	}
	return map[string]any{
		"path":        strings.ReplaceAll(relPath, "\\", "/"),
		"etag":        computeETag([]byte(content), after),
		"mtime_ms":    mtimeMsAny(after),
		"readonly":    isReadonly(after),
		"line_ending": detectLineEnding([]byte(content)),
	}, nil
}

// localCopyName mirrors upstream build_local_copy_file_name: the first copy of
// `report.md` is `report.local.md`, the next `report.local.2.md`, and so on.
func localCopyName(original string, attempt int) string {
	ext := filepath.Ext(original)
	stem := strings.TrimSuffix(original, ext)
	if stem == "" {
		// A dotfile like `.gitignore` has no stem/extension split.
		stem, ext = original, ""
	}
	if attempt <= 1 {
		return stem + ".local" + ext
	}
	return stem + fmt.Sprintf(".local.%d", attempt) + ext
}
