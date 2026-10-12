package handlers

import (
	"database/sql"
	"encoding/base64"
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
		d := stateDB(c)
		folderID := int64Num(c.Body["folderId"])
		if folderID == 0 {
			folderID = int64Num(c.Body["folder_id"])
		}
		if d == nil || d.Conn == nil {
			return map[string]any{}, nil
		}
		_, _ = d.Conn.Exec(`UPDATE folder SET deleted_at = ? WHERE id = ?`, nowRFC3339(), folderID)
		return map[string]any{"ok": true}, nil
	})
	reg.web.Register("get_file_tree", func(c *web.Context) (any, error) {
		d := stateDB(c)
		folderID := int64Num(c.Body["folderId"])
		if folderID == 0 {
			folderID = int64Num(c.Body["folder_id"])
		}
		if folderID > 0 {
			if path := folderPath(d, folderID); path != "" {
				return buildFileTree(path, c.Body), nil
			}
		}
		if path := strVal(c.Body["path"]); path != "" {
			return buildFileTree(path, c.Body), nil
		}
		return nil, web.ErrInvalidInput("folderId or path required")
	})
	reg.web.Register("list_directory_entries", func(c *web.Context) (any, error) {
		path := strVal(c.Body["path"])
		if path == "" {
			return nil, web.ErrInvalidInput("path required")
		}
		return listDirectoryEntries(path), nil
	})
	reg.web.Register("read_file_preview", func(c *web.Context) (any, error) {
		path := strVal(c.Body["path"])
		if path == "" {
			return nil, web.ErrInvalidInput("path required")
		}
		return readFilePreview(path, c.Body)
	})
	reg.web.Register("read_file_base64", func(c *web.Context) (any, error) {
		path := strVal(c.Body["path"], c.Body["filePath"])
		if path == "" {
			return nil, web.ErrInvalidInput("path required")
		}
		return readFileBase64(path)
	})
	reg.web.Register("save_file_copy", func(c *web.Context) (any, error) {
		return nil, web.ErrNotImplemented(c.Command)
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

func addFolder(d *db.Database, path string) (map[string]any, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, web.ErrInvalidInput(err.Error())
	}
	name := filepath.Base(abs)
	now := nowRFC3339()
	res, err := d.Conn.Exec(
		`INSERT INTO folder (name, path, last_opened_at, created_at, updated_at, is_open)
		 VALUES (?,?,?,?,?,1)
		 ON CONFLICT(path) DO UPDATE SET name=excluded.name, last_opened_at=excluded.last_opened_at, updated_at=excluded.updated_at, deleted_at=NULL`,
		name, abs, now, now, now)
	if err != nil {
		return nil, web.ErrIO(err.Error())
	}
	id, _ := res.LastInsertId()
	return map[string]any{"id": id, "name": name, "path": abs}, nil
}

// buildFileTree mirrors upstream get_file_tree: recursively listed relative
// paths for a folder.
func buildFileTree(root string, body map[string]any) map[string]any {
	depth := 3
	if v, ok := body["depth"].(float64); ok {
		depth = int(v)
	}
	showHidden := boolVal(body["showHidden"], false)
	entries := []map[string]any{}
	var walk func(dir, rel string, level int)
	walk = func(dir, rel string, level int) {
		if level > depth {
			return
		}
		items, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		sort.Slice(items, func(i, j int) bool {
			// application-friendly: directories first, then files
			if items[i].IsDir() != items[j].IsDir() {
				return items[i].IsDir()
			}
			return items[i].Name() < items[j].Name()
		})
		for _, item := range items {
			if !showHidden && strings.HasPrefix(item.Name(), ".") {
				continue
			}
			childRel := item.Name()
			if rel != "" {
				childRel = rel + "/" + item.Name()
			}
			rec := map[string]any{"path": childRel, "name": item.Name(), "type": "file"}
			if item.IsDir() {
				rec["type"] = "directory"
			}
			entries = append(entries, rec)
			if item.IsDir() && level < depth {
				walk(filepath.Join(dir, item.Name()), childRel, level+1)
			}
		}
	}
	walk(root, "", 1)
	return map[string]any{"entries": entries}
}

func listDirectoryEntries(path string) []map[string]any {
	out := []map[string]any{}
	items, err := os.ReadDir(path)
	if err != nil {
		return out
	}
	for _, item := range items {
		rec := map[string]any{"name": item.Name(), "type": "file"}
		if item.IsDir() {
			rec["type"] = "directory"
		}
		out = append(out, rec)
	}
	return out
}

func readFilePreview(path string, body map[string]any) (map[string]any, error) {
	maxBytes := int64(200_000)
	if v, ok := body["maxBytes"].(float64); ok && v > 0 {
		maxBytes = int64(v)
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, web.ErrNotFound("file not found")
		}
		return nil, web.ErrIO(err.Error())
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, web.ErrIO(err.Error())
	}
	defer f.Close()
	buf := make([]byte, maxBytes)
	n, _ := f.Read(buf)
	buf = buf[:n]
	truncated := false
	if n == int(maxBytes) {
		if info.Size() > maxBytes {
			truncated = true
		}
	}
	return map[string]any{
		"content": string(buf), "truncated": truncated, "size": info.Size(),
	}, nil
}

func readFileBase64(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, web.ErrNotFound("file not found")
		}
		return nil, web.ErrIO(err.Error())
	}
	return map[string]any{"data": base64.StdEncoding.EncodeToString(b), "size": len(b)}, nil
}
