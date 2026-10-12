package handlers

// Mirror upstream web/handlers/conversation_tags.rs + commands/conversation_tags.rs.
// User-defined conversation labels (name + color), optionally folder-scoped,
// plus the single app-wide "branch tag" setting.

import (
	"database/sql"
	"encoding/json"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const branchTagKey = "conversation_branch_tag"
const defaultBranchTagColor = "#6e7781"

// tagDetail mirrors models::ConversationTagDetail — folder_id is null for a
// global tag (serde serializes Option as null, never omitted).
func scanTag(row interface{ Scan(...any) error }) (map[string]any, error) {
	var (
		id, sortOrder int64
		folderID      sql.NullInt64
		name, color   string
	)
	if err := row.Scan(&id, &folderID, &name, &color, &sortOrder); err != nil {
		return nil, err
	}
	rec := map[string]any{
		"id":         id,
		"folder_id":  nil,
		"name":       name,
		"color":      color,
		"sort_order": sortOrder,
	}
	if folderID.Valid {
		rec["folder_id"] = folderID.Int64
	}
	return rec, nil
}

func tagByID(d *db.Database, id int64) (map[string]any, error) {
	row := d.Conn.QueryRow(
		`SELECT id, folder_id, name, color, sort_order FROM conversation_tag WHERE id = ?`, id)
	rec, err := scanTag(row)
	if err == sql.ErrNoRows {
		return nil, web.ErrNotFound("tag not found").WithI18n("errors.tagNotFound", nil)
	}
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	return rec, nil
}

func normalizeColor(color string) (string, bool) {
	c := strings.TrimSpace(strings.ToLower(color))
	if len(c) != 7 || c[0] != '#' {
		return "", false
	}
	for _, r := range c[1:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return "", false
		}
	}
	return c, true
}

// RegisterConversationTags wires the tag endpoints.
func RegisterConversationTags(reg *Registry) {
	reg.Register("list_conversation_tags", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		rows, err := d.Conn.Query(
			`SELECT id, folder_id, name, color, sort_order FROM conversation_tag ORDER BY sort_order ASC, id ASC`)
		if err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			if rec, err := scanTag(rows); err == nil {
				out = append(out, rec)
			}
		}
		return out, nil
	})

	reg.Register("create_conversation_tag", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(GetString(c, "name"))
		if name == "" {
			return nil, web.ErrInvalidInput("name required").WithI18n("errors.emptyName", nil)
		}
		color, ok := normalizeColor(GetString(c, "color"))
		if !ok {
			return nil, web.ErrInvalidInput("invalid color").WithI18n("errors.invalidColor", nil)
		}
		var folderID any = nil
		if v := GetInt(c, "folderId", "folder_id"); v > 0 {
			folderID = v
		}
		// Duplicate check within scope (case-insensitive).
		if tagNameExists(d, folderID, name, 0) {
			return nil, web.ErrAlreadyExists("duplicate tag name").WithI18n("errors.duplicateName", nil)
		}
		var maxOrder sql.NullInt64
		_ = d.Conn.QueryRow(`SELECT MAX(sort_order) FROM conversation_tag`).Scan(&maxOrder)
		next := int64(0)
		if maxOrder.Valid {
			next = maxOrder.Int64 + 1
		}
		now := nowRFC3339()
		res, err := d.Conn.Exec(
			`INSERT INTO conversation_tag (folder_id, name, color, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
			folderID, name, color, next, now, now)
		if err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		id, _ := res.LastInsertId()
		rec, e := tagByID(d, id)
		if e == nil {
			Emit(c, "conversation_tags://changed", map[string]any{})
		}
		return rec, e
	})

	reg.Register("update_conversation_tag", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, err := GetIntOrErr(c, "tagId", "tag_id")
		if err != nil {
			return nil, err
		}
		existing, err := tagByID(d, id)
		if err != nil {
			return nil, err
		}
		now := nowRFC3339()
		if _, ok := c.Body["name"]; ok {
			name := strings.TrimSpace(GetString(c, "name"))
			if name == "" {
				return nil, web.ErrInvalidInput("name required").WithI18n("errors.emptyName", nil)
			}
			var scope any = existing["folder_id"]
			if tagNameExists(d, scope, name, id) {
				return nil, web.ErrAlreadyExists("duplicate tag name").WithI18n("errors.duplicateName", nil)
			}
			if _, err := d.Conn.Exec(`UPDATE conversation_tag SET name = ?, updated_at = ? WHERE id = ?`, name, now, id); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		if _, ok := c.Body["color"]; ok {
			color, ok := normalizeColor(GetString(c, "color"))
			if !ok {
				return nil, web.ErrInvalidInput("invalid color").WithI18n("errors.invalidColor", nil)
			}
			if _, err := d.Conn.Exec(`UPDATE conversation_tag SET color = ?, updated_at = ? WHERE id = ?`, color, now, id); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		rec, e := tagByID(d, id)
		if e == nil {
			Emit(c, "conversation_tags://changed", map[string]any{})
		}
		return rec, e
	})

	reg.Register("delete_conversation_tag", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, err := GetIntOrErr(c, "tagId", "tag_id")
		if err != nil {
			return nil, err
		}
		if _, err := d.Conn.Exec(`DELETE FROM conversation_tag_link WHERE tag_id = ?`, id); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		if _, err := d.Conn.Exec(`DELETE FROM conversation_tag WHERE id = ?`, id); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		Emit(c, "conversation_tags://changed", map[string]any{})
		return EmptyResult(), nil
	})

	reg.Register("reorder_conversation_tags", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		ids := GetIntSlice(c, "tagIds", "tag_ids")
		now := nowRFC3339()
		for pos, id := range ids {
			if _, err := d.Conn.Exec(`UPDATE conversation_tag SET sort_order = ?, updated_at = ? WHERE id = ?`, pos, now, id); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		Emit(c, "conversation_tags://changed", map[string]any{})
		return EmptyResult(), nil
	})

	reg.Register("update_conversation_tags", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		convID, err := GetIntOrErr(c, "conversationId", "conversation_id")
		if err != nil {
			return nil, err
		}
		now := nowRFC3339()
		for _, tagID := range GetIntSlice(c, "add") {
			_, _ = d.Conn.Exec(
				`INSERT OR IGNORE INTO conversation_tag_link (conversation_id, tag_id, created_at) VALUES (?,?,?)`,
				convID, tagID, now)
		}
		for _, tagID := range GetIntSlice(c, "remove") {
			_, _ = d.Conn.Exec(
				`DELETE FROM conversation_tag_link WHERE conversation_id = ? AND tag_id = ?`, convID, tagID)
		}
		EmitConversationChanged(c, convID)
		return conversationSummaryWithTags(d, convID)
	})

	reg.Register("get_conversation_branch_tag", func(c *web.Context) (any, error) {
		return getBranchTag(stateDB(c)), nil
	})

	reg.Register("update_conversation_branch_tag", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		enabled := GetBool(c, "enabled")
		color, ok := normalizeColor(GetString(c, "color"))
		if !ok {
			color = defaultBranchTagColor
		}
		payload := map[string]any{"enabled": enabled, "color": color}
		raw, _ := json.Marshal(payload)
		upsertValue(d, branchTagKey, string(raw), nowRFC3339())
		Emit(c, "conversation_tags://changed", map[string]any{})
		return payload, nil
	})
}

func tagNameExists(d *db.Database, scope any, name string, excludeID int64) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	var query string
	var args []any
	if scope == nil {
		query = `SELECT id, name FROM conversation_tag WHERE folder_id IS NULL`
	} else {
		query = `SELECT id, name FROM conversation_tag WHERE folder_id = ?`
		args = append(args, scope)
	}
	rows, err := d.Conn.Query(query, args...)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n string
		if rows.Scan(&id, &n) != nil {
			continue
		}
		if id == excludeID {
			continue
		}
		if strings.ToLower(strings.TrimSpace(n)) == key {
			return true
		}
	}
	return false
}

func getBranchTag(d *db.Database) map[string]any {
	out := map[string]any{"enabled": false, "color": defaultBranchTagColor}
	raw := valueOrDefault(d, branchTagKey)
	if raw != "" {
		var parsed map[string]any
		if json.Unmarshal([]byte(raw), &parsed) == nil {
			if v, ok := parsed["enabled"].(bool); ok {
				out["enabled"] = v
			}
			if v, ok := parsed["color"].(string); ok && v != "" {
				out["color"] = v
			}
		}
	}
	return out
}

// conversationSummaryWithTags returns the conversation row plus its tag id list.
// Not a byte-for-byte DbConversationSummary (that carries child_count etc.), but
// a functional summary for the non-P0 tag-assignment path.
func conversationSummaryWithTags(d *db.Database, convID int64) (map[string]any, error) {
	rec, err := getConversation(d, convID)
	if err != nil {
		return nil, err
	}
	tagIDs := []int64{}
	rows, err := d.Conn.Query(`SELECT tag_id FROM conversation_tag_link WHERE conversation_id = ?`, convID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				tagIDs = append(tagIDs, id)
			}
		}
	}
	rec["tag_ids"] = tagIDs
	return rec, nil
}
