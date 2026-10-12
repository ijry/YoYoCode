package handlers

// Mirror upstream web/handlers/files.rs + commands/folders.rs file editor
// commands. All paths are workspace-relative (forward slashes from the client);
// resolveTreePath rejects absolute paths and any ".." the same way upstream's
// resolve_tree_path does, and ensureNavigable asserts the result stays under
// the workspace root.
//
// etag is an opaque optimistic-concurrency token: the client reads it from
// read_file_for_edit and echoes it back as expectedEtag on save. It only has to
// be self-consistent within this server (never compared across implementations),
// so a stable FNV hash of content+size+mtime stands in for upstream's hasher.

import (
	"encoding/base64"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"yoyocode/src-go/web"
)

const (
	fileOpenHardLimit = 50000000
	fileSaveHardLimit = 50000000
)

// resolveTreePath mirrors commands::folders::resolve_tree_path: reject absolute
// paths and any ".." component, then join under root. Accepts "/" and "\" as
// separators (clients send forward slashes; be lenient).
func resolveTreePath(root, rel string) (string, error) {
	if rel == "" {
		return root, nil
	}
	norm := strings.ReplaceAll(rel, "\\", "/")
	if strings.HasPrefix(norm, "/") || filepath.IsAbs(rel) {
		return "", web.ErrInvalidInput("Path must be relative")
	}
	parts := []string{}
	for _, seg := range strings.Split(norm, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			return "", web.ErrInvalidInput("Path cannot contain '..'")
		}
		parts = append(parts, seg)
	}
	return filepath.Join(append([]string{root}, parts...)...), nil
}

// ensureNavigable asserts target lies under root (the user-navigable boundary).
func ensureNavigable(root, target string) error {
	ra, err := filepath.Abs(root)
	if err != nil {
		return web.ErrIO(err.Error())
	}
	ta, err := filepath.Abs(target)
	if err != nil {
		return web.ErrIO(err.Error())
	}
	rel, err := filepath.Rel(ra, ta)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return web.ErrInvalidInput("Path is outside workspace root")
	}
	return nil
}

func requireFolderDir(root string) error {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return web.ErrNotFound("Folder does not exist")
	}
	return nil
}

func computeETag(content []byte, info os.FileInfo) string {
	h := fnv.New64a()
	h.Write(content)
	var sz [8]byte
	n := info.Size()
	for i := 0; i < 8; i++ {
		sz[i] = byte(n >> (8 * i))
	}
	h.Write(sz[:])
	if ms, ok := fileMtimeMs(info); ok {
		var mb [8]byte
		for i := 0; i < 8; i++ {
			mb[i] = byte(ms >> (8 * i))
		}
		h.Write(mb[:])
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

func fileMtimeMs(info os.FileInfo) (int64, bool) {
	t := info.ModTime()
	if t.IsZero() {
		return 0, false
	}
	return t.UnixMilli(), true
}

// isReadonly reports whether no write permission bit is set (owner/group/other).
func isReadonly(info os.FileInfo) bool {
	return info.Mode().Perm()&0o222 == 0
}

func mtimeMsAny(info os.FileInfo) any {
	if ms, ok := fileMtimeMs(info); ok {
		return ms
	}
	return nil
}

func detectLineEnding(b []byte) string {
	hasLF := false
	hasCRLF := false
	for i := 0; i < len(b); i++ {
		if b[i] != '\n' {
			continue
		}
		if i > 0 && b[i-1] == '\r' {
			hasCRLF = true
		} else {
			hasLF = true
		}
		if hasLF && hasCRLF {
			return "mixed"
		}
	}
	switch {
	case hasCRLF:
		return "crlf"
	case hasLF:
		return "lf"
	default:
		return "none"
	}
}

func looksBinary(b []byte) bool {
	lim := len(b)
	if lim > 2048 {
		lim = 2048
	}
	for i := 0; i < lim; i++ {
		if b[i] == 0 {
			return true
		}
	}
	return false
}

// RegisterFiles wires the file-editor commands.
func RegisterFiles(reg *Registry) {
	reg.Register("read_file_for_edit", func(c *web.Context) (any, error) {
		root := GetString(c, "rootPath", "root_path")
		rel := GetString(c, "path")
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
			return nil, web.ErrInvalidInput("File is too large to open in editor")
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return nil, web.ErrIO(err.Error())
		}
		if looksBinary(data) {
			return nil, web.ErrInvalidInput("Binary files are not supported in preview")
		}
		return map[string]any{
			"path":        rel,
			"content":     string(data),
			"etag":        computeETag(data, info),
			"mtime_ms":    mtimeMsAny(info),
			"readonly":    isReadonly(info),
			"line_ending": detectLineEnding(data),
		}, nil
	})

	reg.Register("save_file_content", func(c *web.Context) (any, error) {
		root := GetString(c, "rootPath", "root_path")
		rel := GetString(c, "path")
		content := GetString(c, "content")
		if _, ok := c.Body["content"]; ok {
			content = asString(c.Body["content"])
		}
		expectedEtag := GetString(c, "expectedEtag", "expected_etag")
		if root == "" {
			return nil, web.ErrInvalidInput("rootPath required")
		}
		if err := requireFolderDir(root); err != nil {
			return nil, err
		}
		if len(content) > fileSaveHardLimit {
			return nil, web.ErrInvalidInput("File is too large to save in editor")
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
		if li, lerr := os.Lstat(target); lerr == nil && li.Mode()&os.ModeSymlink != 0 {
			return nil, web.ErrInvalidInput("Saving symlink targets is not supported")
		}
		if isReadonly(info) {
			return nil, web.ErrPermissionDenied("File is read-only")
		}
		cur, err := os.ReadFile(target)
		if err != nil {
			return nil, web.ErrIO(err.Error())
		}
		if looksBinary(cur) {
			return nil, web.ErrInvalidInput("Binary files are not supported in editor")
		}
		if expectedEtag != "" && expectedEtag != computeETag(cur, info) {
			return nil, web.ErrInvalidInput("File has changed on disk. Reload the file before saving.")
		}
		if err := atomicWriteText(target, []byte(content)); err != nil {
			return nil, err
		}
		after, err := os.Stat(target)
		if err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return map[string]any{
			"path":        rel,
			"etag":        computeETag([]byte(content), after),
			"mtime_ms":    mtimeMsAny(after),
			"readonly":    isReadonly(after),
			"line_ending": detectLineEnding([]byte(content)),
		}, nil
	})

	reg.Register("read_workspace_file_base64", func(c *web.Context) (any, error) {
		root := GetString(c, "rootPath", "root_path")
		rel := GetString(c, "path")
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
		if err := ensureNavigable(root, target); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return nil, web.ErrNotFound("File does not exist")
		}
		if maxBytes := GetInt(c, "maxBytes", "max_bytes"); maxBytes > 0 && int64(len(data)) > maxBytes {
			data = data[:maxBytes]
		}
		return base64.StdEncoding.EncodeToString(data), nil
	})

	reg.Register("create_file_tree_entry", func(c *web.Context) (any, error) {
		root := GetString(c, "rootPath", "root_path")
		rel := GetString(c, "path")
		name := strings.TrimSpace(GetString(c, "name"))
		kind := GetString(c, "kind")
		if root == "" {
			return nil, web.ErrInvalidInput("rootPath required")
		}
		if err := requireFolderDir(root); err != nil {
			return nil, err
		}
		if err := validateNewName(name); err != nil {
			return nil, err
		}
		parentDir := root
		if rel != "" {
			resolved, err := resolveTreePath(root, rel)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(resolved)
			if err != nil {
				return nil, web.ErrNotFound("Parent path does not exist")
			}
			if info.IsDir() {
				parentDir = resolved
			} else {
				parentDir = filepath.Dir(resolved)
			}
		}
		target := filepath.Join(parentDir, name)
		if _, err := os.Stat(target); err == nil {
			return nil, web.ErrAlreadyExists("A file or directory with this name already exists")
		}
		switch kind {
		case "file":
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				return nil, web.ErrIO(err.Error())
			}
			f.Close()
		case "dir":
			if err := os.Mkdir(target, 0o755); err != nil {
				return nil, web.ErrIO(err.Error())
			}
		default:
			return nil, web.ErrInvalidInput("Kind must be 'file' or 'dir'")
		}
		return relUnderRoot(root, target)
	})

	reg.Register("rename_file_tree_entry", func(c *web.Context) (any, error) {
		root := GetString(c, "rootPath", "root_path")
		rel := GetString(c, "path")
		newName := GetString(c, "newName", "new_name")
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
		if _, err := os.Lstat(target); err != nil {
			return nil, web.ErrNotFound("Target file does not exist")
		}
		if err := ensureEntryInWorkspace(root, target, "rename"); err != nil {
			return nil, err
		}
		if err := validateNewName(newName); err != nil {
			return nil, err
		}
		next := filepath.Join(filepath.Dir(target), strings.TrimSpace(newName))
		if next == target {
			return rel, nil
		}
		if _, err := os.Lstat(next); err == nil {
			return nil, web.ErrAlreadyExists("A file with this name already exists")
		}
		if err := os.Rename(target, next); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return relUnderRoot(root, next)
	})

	reg.Register("move_file_tree_entry", func(c *web.Context) (any, error) {
		root := GetString(c, "rootPath", "root_path")
		sourcePath := GetString(c, "sourcePath", "source_path")
		destDir := GetString(c, "destDir", "dest_dir")
		if root == "" {
			return nil, web.ErrInvalidInput("rootPath required")
		}
		if err := requireFolderDir(root); err != nil {
			return nil, err
		}
		source, err := resolveTreePath(root, sourcePath)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(source); err != nil {
			return nil, web.ErrNotFound("Source file does not exist")
		}
		if err := ensureEntryInWorkspace(root, source, "move"); err != nil {
			return nil, err
		}
		dest, err := resolveTreePath(root, destDir)
		if err != nil {
			return nil, err
		}
		di, err := os.Stat(dest)
		if err != nil || !di.IsDir() {
			return nil, web.ErrInvalidInput("Destination is not a directory")
		}
		if err := ensureNavigable(root, dest); err != nil {
			return nil, web.ErrInvalidInput("Destination resolves outside the workspace")
		}
		if dest == source || strings.HasPrefix(dest, source+string(filepath.Separator)) {
			return nil, web.ErrInvalidInput("Cannot move a directory into itself")
		}
		target := filepath.Join(dest, filepath.Base(source))
		if target == source {
			return sourcePath, nil
		}
		if _, err := os.Lstat(target); err == nil {
			return nil, web.ErrAlreadyExists("An entry with this name already exists in the destination")
		}
		if err := os.Rename(source, target); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return relUnderRoot(root, target)
	})

	reg.Register("delete_file_tree_entry", func(c *web.Context) (any, error) {
		root := GetString(c, "rootPath", "root_path")
		rel := GetString(c, "path")
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
		li, err := os.Lstat(target)
		if err != nil {
			return nil, web.ErrNotFound("Target file does not exist")
		}
		if err := ensureEntryInWorkspace(root, target, "delete"); err != nil {
			return nil, err
		}
		if li.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(target); err != nil {
				return nil, web.ErrIO(err.Error())
			}
		} else if li.IsDir() {
			if err := os.RemoveAll(target); err != nil {
				return nil, web.ErrIO(err.Error())
			}
		} else {
			if err := os.Remove(target); err != nil {
				return nil, web.ErrIO(err.Error())
			}
		}
		return EmptyResult(), nil
	})
}

func validateNewName(name string) error {
	t := strings.TrimSpace(name)
	if t == "" {
		return web.ErrInvalidInput("New name cannot be empty")
	}
	if t == "." || t == ".." {
		return web.ErrInvalidInput("Invalid file name")
	}
	if strings.ContainsAny(t, "/\\") {
		return web.ErrInvalidInput("New name cannot contain path separators")
	}
	return nil
}

func ensureEntryInWorkspace(root, target, action string) error {
	ra, _ := filepath.Abs(root)
	ta, _ := filepath.Abs(target)
	if ta == ra {
		return web.ErrInvalidInput("Cannot " + action + " workspace root")
	}
	return ensureNavigable(root, target)
}

func relUnderRoot(root, target string) (any, error) {
	ra, _ := filepath.Abs(root)
	ta, _ := filepath.Abs(target)
	rel, err := filepath.Rel(ra, ta)
	if err != nil {
		return nil, web.ErrInvalidInput("Failed to compute relative path")
	}
	return filepath.ToSlash(rel), nil
}

func atomicWriteText(path string, data []byte) error {
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return web.ErrNotFound("Parent directory does not exist")
	}
	tmp, err := os.CreateTemp(dir, ".yoyocode-edit-*.tmp")
	if err != nil {
		return web.ErrIO(err.Error())
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return web.ErrIO(err.Error())
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return web.ErrIO(err.Error())
	}
	tmp.Close()
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return web.ErrIO(err.Error())
	}
	return nil
}
