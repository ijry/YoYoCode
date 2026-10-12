package handlers

// Faithful FolderDetail surface + folder/group management. Overrides the
// simplified list_open_folder_details/list_all_folder_details from folders.go
// with the upstream FolderDetail shape (id, name, path, git_branch,
// default_agent_type, last_opened_at, sort_order, color, parent_id, kind,
// alias, group_id) so the native UI and other clients see the real fields.

import (
	"database/sql"
	"os"
	"path/filepath"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const folderDetailCols = `SELECT id, name, path, git_branch, default_agent_type, last_opened_at, sort_order, color, parent_id, kind, alias, folder_group_id FROM folder`

func scanFolderDetail(rows interface{ Scan(...any) error }) (map[string]any, error) {
	var (
		id, sortOrder           int64
		name, path              string
		gitBranch, defaultAgent sql.NullString
		lastOpened, color, kind sql.NullString
		alias                   sql.NullString
		parentID, groupID       sql.NullInt64
	)
	if err := rows.Scan(&id, &name, &path, &gitBranch, &defaultAgent, &lastOpened, &sortOrder, &color, &parentID, &kind, &alias, &groupID); err != nil {
		return nil, err
	}
	rec := map[string]any{
		"id":                 id,
		"name":               name,
		"path":               path,
		"git_branch":         nullStr(gitBranch),
		"default_agent_type": nullStr(defaultAgent),
		"last_opened_at":     lastOpened.String,
		"sort_order":         sortOrder,
		"color":              strOr(color, "inherit"),
		"parent_id":          nullInt(parentID),
		"kind":               strOr(kind, "regular"),
		"alias":              nullStr(alias),
		"group_id":           nullInt(groupID),
	}
	return rec, nil
}

func folderDetailByID(d *db.Database, id int64) (map[string]any, error) {
	if d == nil || d.Conn == nil {
		return nil, web.ErrDatabase("db unavailable")
	}
	row := d.Conn.QueryRow(folderDetailCols+` WHERE id = ? AND deleted_at IS NULL`, id)
	rec, err := scanFolderDetail(row)
	if err == sql.ErrNoRows {
		return nil, web.ErrNotFound("folder not found")
	}
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	return rec, nil
}

func folderDetailsWhere(d *db.Database, where string) []map[string]any {
	out := []map[string]any{}
	if d == nil || d.Conn == nil {
		return out
	}
	rows, err := d.Conn.Query(folderDetailCols + " WHERE deleted_at IS NULL " + where)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		if rec, err := scanFolderDetail(rows); err == nil {
			out = append(out, rec)
		}
	}
	return out
}

func folderGroupDetails(d *db.Database) []map[string]any {
	out := []map[string]any{}
	if d == nil || d.Conn == nil {
		return out
	}
	rows, err := d.Conn.Query(`SELECT id, name, color, sort_order FROM folder_group ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, sortOrder int64
		var name string
		var color sql.NullString
		if rows.Scan(&id, &name, &color, &sortOrder) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "color": strOr(color, "inherit"), "sort_order": sortOrder})
		}
	}
	return out
}

func folderGroupByID(d *db.Database, id int64) (map[string]any, error) {
	row := d.Conn.QueryRow(`SELECT id, name, color, sort_order FROM folder_group WHERE id = ?`, id)
	var gid, sortOrder int64
	var name string
	var color sql.NullString
	if err := row.Scan(&gid, &name, &color, &sortOrder); err != nil {
		if err == sql.ErrNoRows {
			return nil, web.ErrNotFound("folder group not found")
		}
		return nil, web.ErrDatabase(err.Error())
	}
	return map[string]any{"id": gid, "name": name, "color": strOr(color, "inherit"), "sort_order": sortOrder}, nil
}

// RegisterFoldersDetail overrides + extends the folder endpoints. Must be
// registered AFTER RegisterFolders so the faithful detail shape wins.
func RegisterFoldersDetail(reg *Registry) {
	reg.Register("list_open_folder_details", func(c *web.Context) (any, error) {
		return folderDetailsWhere(stateDB(c), "AND is_open = 1 ORDER BY sort_order ASC, last_opened_at DESC"), nil
	})
	reg.Register("list_all_folder_details", func(c *web.Context) (any, error) {
		return folderDetailsWhere(stateDB(c), "ORDER BY sort_order ASC, last_opened_at DESC"), nil
	})
	reg.Register("get_folder", func(c *web.Context) (any, error) {
		id := GetInt(c, "folderId", "folder_id")
		if id <= 0 {
			return nil, web.ErrInvalidInput("folderId required")
		}
		return folderDetailByID(stateDB(c), id)
	})
	reg.Register("load_folder_history", func(c *web.Context) (any, error) {
		d := stateDB(c)
		out := []map[string]any{}
		if d == nil || d.Conn == nil {
			return out, nil
		}
		rows, err := d.Conn.Query(`SELECT id, path, name, last_opened_at FROM folder WHERE deleted_at IS NULL ORDER BY last_opened_at DESC`)
		if err != nil {
			return out, nil
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var path, name, lastOpened string
			if rows.Scan(&id, &path, &name, &lastOpened) == nil {
				out = append(out, map[string]any{"id": id, "path": path, "name": name, "last_opened_at": lastOpened})
			}
		}
		return out, nil
	})

	reg.Register("open_folder", func(c *web.Context) (any, error) {
		return openFolderByPath(c, GetString(c, "path"), false)
	})
	reg.Register("open_folder_in_workspace", func(c *web.Context) (any, error) {
		return openFolderByPath(c, GetString(c, "path"), true)
	})
	reg.Register("open_folder_by_id", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id := GetInt(c, "folderId", "folder_id")
		if id <= 0 {
			return nil, web.ErrInvalidInput("folderId required")
		}
		if _, err := d.Conn.Exec(`UPDATE folder SET is_open = 1, last_opened_at = ?, updated_at = ? WHERE id = ?`, nowRFC3339(), nowRFC3339(), id); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		rec, e := folderDetailByID(d, id)
		if e == nil {
			Emit(c, "folders://changed", map[string]any{})
		}
		return rec, e
	})
	reg.Register("remove_folder_from_workspace", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id := GetInt(c, "folderId", "folder_id")
		if _, err := d.Conn.Exec(`UPDATE folder SET is_open = 0, updated_at = ? WHERE id = ?`, nowRFC3339(), id); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		_, _ = d.Conn.Exec(`DELETE FROM opened_tab WHERE folder_id = ?`, id)
		Emit(c, "folders://changed", map[string]any{})
		return EmptyResult(), nil
	})

	reg.Register("update_folder_color", func(c *web.Context) (any, error) {
		return updateFolderField(c, "color", GetString(c, "color"))
	})
	reg.Register("update_folder_alias", func(c *web.Context) (any, error) {
		var alias any = nil
		if v := GetString(c, "alias"); v != "" {
			alias = v
		}
		return updateFolderField(c, "alias", alias)
	})
	reg.Register("update_folder_default_agent", func(c *web.Context) (any, error) {
		var agent any = nil
		if v := GetString(c, "defaultAgentType", "default_agent_type"); v != "" {
			agent = v
		}
		return updateFolderField(c, "default_agent_type", agent)
	})

	reg.Register("list_folder_groups", func(c *web.Context) (any, error) {
		return folderGroupDetails(stateDB(c)), nil
	})
	reg.Register("create_folder_group", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		name := GetString(c, "name")
		color := GetString(c, "color")
		if color == "" {
			color = "inherit"
		}
		var maxOrder sql.NullInt64
		_ = d.Conn.QueryRow(`SELECT MAX(sort_order) FROM folder_group`).Scan(&maxOrder)
		next := int64(0)
		if maxOrder.Valid {
			next = maxOrder.Int64 + 1
		}
		now := nowRFC3339()
		res, err := d.Conn.Exec(`INSERT INTO folder_group (name, color, sort_order, created_at, updated_at) VALUES (?,?,?,?,?)`, name, color, next, now, now)
		if err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		id, _ := res.LastInsertId()
		rec, e := folderGroupByID(d, id)
		if e == nil {
			Emit(c, "folders://changed", map[string]any{})
		}
		return rec, e
	})
	reg.Register("update_folder_group", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id := GetInt(c, "groupId", "group_id")
		if id <= 0 {
			return nil, web.ErrInvalidInput("groupId required")
		}
		now := nowRFC3339()
		if _, ok := c.Body["name"]; ok {
			if _, err := d.Conn.Exec(`UPDATE folder_group SET name = ?, updated_at = ? WHERE id = ?`, GetString(c, "name"), now, id); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		if _, ok := c.Body["color"]; ok {
			if _, err := d.Conn.Exec(`UPDATE folder_group SET color = ?, updated_at = ? WHERE id = ?`, GetString(c, "color"), now, id); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		rec, e := folderGroupByID(d, id)
		if e == nil {
			Emit(c, "folders://changed", map[string]any{})
		}
		return rec, e
	})
	reg.Register("delete_folder_group", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id := GetInt(c, "groupId", "group_id")
		// Ungroup members, then remove the group.
		_, _ = d.Conn.Exec(`UPDATE folder SET folder_group_id = NULL WHERE folder_group_id = ?`, id)
		if _, err := d.Conn.Exec(`DELETE FROM folder_group WHERE id = ?`, id); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		Emit(c, "folders://changed", map[string]any{})
		return EmptyResult(), nil
	})
	reg.Register("set_folder_group", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		folderID := GetInt(c, "folderId", "folder_id")
		var groupID any = nil
		if v := GetInt(c, "groupId", "group_id"); v > 0 {
			groupID = v
		}
		if _, err := d.Conn.Exec(`UPDATE folder SET folder_group_id = ?, updated_at = ? WHERE id = ?`, groupID, nowRFC3339(), folderID); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		Emit(c, "folders://changed", map[string]any{})
		return EmptyResult(), nil
	})
	reg.Register("apply_sidebar_layout", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		entries, _ := c.Body["entries"].([]any)
		now := nowRFC3339()
		folderPos := 0
		groupPos := 0
		for _, raw := range entries {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			kind := asString(pick(m, "kind"))
			id := asInt64(pick(m, "id"))
			switch kind {
			case "group":
				_, _ = d.Conn.Exec(`UPDATE folder_group SET sort_order = ?, updated_at = ? WHERE id = ?`, groupPos, now, id)
				groupPos++
			case "folder":
				var gid any = nil
				if v := asInt64(pick(m, "groupId", "group_id")); v > 0 {
					gid = v
				}
				_, _ = d.Conn.Exec(`UPDATE folder SET sort_order = ?, folder_group_id = ?, updated_at = ? WHERE id = ?`, folderPos, gid, now, id)
				folderPos++
			}
		}
		Emit(c, "folders://changed", map[string]any{})
		return EmptyResult(), nil
	})

	reg.Register("create_folder_directory", func(c *web.Context) (any, error) {
		path := GetString(c, "path")
		if path == "" {
			return nil, web.ErrInvalidInput("path required")
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return EmptyResult(), nil
	})
	reg.Register("create_directory", func(c *web.Context) (any, error) {
		parent := GetString(c, "parentPath", "parent_path")
		name := GetString(c, "name")
		if parent == "" || name == "" {
			return nil, web.ErrInvalidInput("parentPath and name required")
		}
		target := filepath.Join(parent, name)
		if _, err := os.Stat(target); err == nil {
			return nil, web.ErrAlreadyExists("A directory with this name already exists")
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return target, nil
	})
}

// openFolderByPath upserts a folder by absolute path, marks it open, returns detail.
func openFolderByPath(c *web.Context, path string, broadcast bool) (any, error) {
	d, err := requireDB(c)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, web.ErrInvalidInput("path required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, web.ErrInvalidInput(err.Error())
	}
	name := filepath.Base(abs)
	now := nowRFC3339()
	res, err := d.Conn.Exec(
		`INSERT INTO folder (name, path, last_opened_at, created_at, updated_at, is_open, color, kind)
		 VALUES (?,?,?,?,?,1,'inherit','regular')
		 ON CONFLICT(path) DO UPDATE SET name=excluded.name, last_opened_at=excluded.last_opened_at, updated_at=excluded.updated_at, is_open=1, deleted_at=NULL`,
		name, abs, now, now, now)
	if err != nil {
		return nil, web.ErrIO(err.Error())
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		_ = d.Conn.QueryRow(`SELECT id FROM folder WHERE path = ?`, abs).Scan(&id)
	}
	rec, e := folderDetailByID(d, id)
	if e == nil && broadcast {
		Emit(c, "folders://changed", map[string]any{})
	}
	return rec, e
}

func updateFolderField(c *web.Context, col string, val any) (any, error) {
	d, err := requireDB(c)
	if err != nil {
		return nil, err
	}
	id := GetInt(c, "folderId", "folder_id")
	if id <= 0 {
		return nil, web.ErrInvalidInput("folderId required")
	}
	if _, err := d.Conn.Exec(`UPDATE folder SET `+col+` = ?, updated_at = ? WHERE id = ?`, val, nowRFC3339(), id); err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	rec, e := folderDetailByID(d, id)
	if e == nil {
		Emit(c, "folders://changed", map[string]any{})
	}
	return rec, e
}

func nullStr(v sql.NullString) any {
	if v.Valid && v.String != "" {
		return v.String
	}
	return nil
}

func strOr(v sql.NullString, def string) string {
	if v.Valid && v.String != "" {
		return v.String
	}
	return def
}

func nullInt(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}
