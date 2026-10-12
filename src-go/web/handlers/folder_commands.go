package handlers

// Folder-scoped slash commands (B11): list/create/update/delete/reorder plus a
// package.json bootstrap. Mirrors commands/folder_commands.rs +
// db/service/folder_command_service.rs. FolderCommandInfo wire shape is
// {id, folder_id, name, command, sort_order, created_at, updated_at}.

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

func scanFolderCommand(row interface{ Scan(...any) error }) (map[string]any, error) {
	var id, folderID, sortOrder int64
	var name, command, createdAt, updatedAt string
	if err := row.Scan(&id, &folderID, &name, &command, &sortOrder, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "folder_id": folderID, "name": name, "command": command,
		"sort_order": sortOrder, "created_at": createdAt, "updated_at": updatedAt,
	}, nil
}

const folderCommandCols = `SELECT id, folder_id, name, command, sort_order, created_at, updated_at FROM folder_command`

func listFolderCommands(d *db.Database, folderID int64) []map[string]any {
	out := []map[string]any{}
	if d == nil || d.Conn == nil {
		return out
	}
	rows, err := d.Conn.Query(folderCommandCols+` WHERE folder_id = ? ORDER BY sort_order ASC`, folderID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		if rec, err := scanFolderCommand(rows); err == nil {
			out = append(out, rec)
		}
	}
	return out
}

func folderCommandByID(d *db.Database, id int64) (map[string]any, error) {
	row := d.Conn.QueryRow(folderCommandCols+` WHERE id = ?`, id)
	rec, err := scanFolderCommand(row)
	if err == sql.ErrNoRows {
		return nil, web.ErrNotFound("folder command not found")
	}
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	return rec, nil
}

func nextFolderCommandOrder(d *db.Database, folderID int64) int64 {
	var maxOrder sql.NullInt64
	_ = d.Conn.QueryRow(`SELECT MAX(sort_order) FROM folder_command WHERE folder_id = ?`, folderID).Scan(&maxOrder)
	if maxOrder.Valid {
		return maxOrder.Int64 + 1
	}
	return 0
}

func RegisterFolderCommands(reg *Registry) {
	reg.Register("list_folder_commands", func(c *web.Context) (any, error) {
		return listFolderCommands(stateDB(c), GetInt(c, "folderId", "folder_id")), nil
	})

	reg.Register("create_folder_command", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		folderID := GetInt(c, "folderId", "folder_id")
		if folderID == 0 {
			return nil, web.ErrInvalidInput("folderId required")
		}
		name := GetString(c, "name")
		command := GetString(c, "command")
		now := nowRFC3339()
		res, e := d.Conn.Exec(
			`INSERT INTO folder_command (folder_id, name, command, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
			folderID, name, command, nextFolderCommandOrder(d, folderID), now, now)
		if e != nil {
			return nil, web.ErrDatabase(e.Error())
		}
		id, _ := res.LastInsertId()
		return folderCommandByID(d, id)
	})

	reg.Register("update_folder_command", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		if _, err := folderCommandByID(d, id); err != nil {
			return nil, err
		}
		now := nowRFC3339()
		if _, ok := c.Body["name"]; ok {
			_, _ = d.Conn.Exec(`UPDATE folder_command SET name = ?, updated_at = ? WHERE id = ?`, GetString(c, "name"), now, id)
		}
		if _, ok := c.Body["command"]; ok {
			_, _ = d.Conn.Exec(`UPDATE folder_command SET command = ?, updated_at = ? WHERE id = ?`, GetString(c, "command"), now, id)
		}
		if _, ok := c.Body["sortOrder"]; ok {
			_, _ = d.Conn.Exec(`UPDATE folder_command SET sort_order = ?, updated_at = ? WHERE id = ?`, GetInt(c, "sortOrder", "sort_order"), now, id)
		} else if _, ok := c.Body["sort_order"]; ok {
			_, _ = d.Conn.Exec(`UPDATE folder_command SET sort_order = ?, updated_at = ? WHERE id = ?`, GetInt(c, "sortOrder", "sort_order"), now, id)
		}
		return folderCommandByID(d, id)
	})

	reg.Register("delete_folder_command", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, e := GetIntOrErr(c, "id")
		if e != nil {
			return nil, e
		}
		_, _ = d.Conn.Exec(`DELETE FROM folder_command WHERE id = ?`, id)
		return nil, nil
	})

	reg.Register("reorder_folder_commands", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		folderID := GetInt(c, "folderId", "folder_id")
		ids := GetIntSlice(c, "ids")
		now := nowRFC3339()
		for idx, id := range ids {
			_, _ = d.Conn.Exec(`UPDATE folder_command SET sort_order = ?, updated_at = ? WHERE folder_id = ? AND id = ?`, idx, now, folderID, id)
		}
		return nil, nil
	})

	reg.Register("bootstrap_folder_commands_from_package_json", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		folderID := GetInt(c, "folderId", "folder_id")
		existing := listFolderCommands(d, folderID)
		if len(existing) > 0 {
			return existing, nil
		}
		folderPathArg := GetString(c, "folderPath", "folder_path")
		cmds := loadPackageScriptsAsCommands(folderPathArg)
		if len(cmds) == 0 {
			return existing, nil
		}
		now := nowRFC3339()
		base := nextFolderCommandOrder(d, folderID)
		for i, pair := range cmds {
			_, _ = d.Conn.Exec(
				`INSERT INTO folder_command (folder_id, name, command, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
				folderID, pair[0], pair[1], base+int64(i), now, now)
		}
		return listFolderCommands(d, folderID), nil
	})
}

// loadPackageScriptsAsCommands reads package.json "scripts" (in file order) and
// maps each to a "<pm> run <name>" command, choosing the package manager from
// the lockfile present. Mirrors folder_commands.rs::load_package_scripts_as_commands.
func loadPackageScriptsAsCommands(folderPath string) [][2]string {
	out := [][2]string{}
	if folderPath == "" {
		return out
	}
	entries, err := os.ReadDir(folderPath)
	if err != nil {
		return out
	}
	hasPackageJSON, hasPnpm, hasYarn, hasBun := false, false, false, false
	for _, e := range entries {
		switch e.Name() {
		case "package.json":
			hasPackageJSON = true
		case "pnpm-lock.yaml":
			hasPnpm = true
		case "yarn.lock":
			hasYarn = true
		case "bun.lockb", "bun.lock":
			hasBun = true
		}
	}
	if !hasPackageJSON {
		return out
	}
	raw, err := os.ReadFile(filepath.Join(folderPath, "package.json"))
	if err != nil {
		return out
	}
	pm := "npm"
	switch {
	case hasPnpm:
		pm = "pnpm"
	case hasYarn:
		pm = "yarn"
	case hasBun:
		pm = "bun"
	}
	for _, name := range jsonObjectKeysInOrder(raw, "scripts") {
		if strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, [2]string{name, pm + " run " + name})
	}
	return out
}

// jsonObjectKeysInOrder returns the keys of the top-level object property
// `field` in document order (only keys whose value is a string), using a
// streaming decoder so package.json script order is preserved.
func jsonObjectKeysInOrder(raw []byte, field string) []string {
	out := []string{}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	// Expect top-level object.
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return out
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return out
		}
		key, _ := keyTok.(string)
		if key == field {
			return decodeStringObjectKeys(dec)
		}
		// Skip this value.
		if err := skipJSONValue(dec); err != nil {
			return out
		}
	}
	return out
}

func decodeStringObjectKeys(dec *json.Decoder) []string {
	out := []string{}
	t, err := dec.Token()
	if err != nil || t != json.Delim('{') {
		return out
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return out
		}
		key, _ := keyTok.(string)
		valTok, err := dec.Token()
		if err != nil {
			return out
		}
		if _, isStr := valTok.(string); isStr {
			out = append(out, key)
		} else if d, ok := valTok.(json.Delim); ok && (d == '{' || d == '[') {
			_ = skipRemainingContainer(dec)
		}
	}
	return out
}

// skipJSONValue consumes one JSON value (scalar or container) from dec.
func skipJSONValue(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := t.(json.Delim); ok && (d == '{' || d == '[') {
		return skipRemainingContainer(dec)
	}
	return nil
}

// skipRemainingContainer consumes tokens until the currently-open container
// closes (the opening delim was already read).
func skipRemainingContainer(dec *json.Decoder) error {
	depth := 1
	for depth > 0 {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}
