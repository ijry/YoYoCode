package handlers

// Mirror upstream web/handlers/quick_messages.rs + db/service/quick_message_service.rs.
// Quick messages are reusable composer snippets; mcode-app lists/creates/edits them.

import (
	"database/sql"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// quickMessageInfo mirrors models::QuickMessageInfo:
// { id, title, content, sort_order, created_at, updated_at }.
func scanQuickMessage(row interface{ Scan(...any) error }) (map[string]any, error) {
	var (
		id, sortOrder        int64
		title, content       string
		createdAt, updatedAt string
	)
	if err := row.Scan(&id, &title, &content, &sortOrder, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	return map[string]any{
		"id":         id,
		"title":      title,
		"content":    content,
		"sort_order": sortOrder,
		"created_at": createdAt,
		"updated_at": updatedAt,
	}, nil
}

func quickMessageByID(d *db.Database, id int64) (map[string]any, error) {
	row := d.Conn.QueryRow(
		`SELECT id, title, content, sort_order, created_at, updated_at FROM quick_message WHERE id = ?`, id)
	rec, err := scanQuickMessage(row)
	if err == sql.ErrNoRows {
		return nil, web.ErrNotFound("quick message not found")
	}
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	return rec, nil
}

// RegisterQuickMessages wires the quick-message CRUD endpoints.
func RegisterQuickMessages(reg *Registry) {
	reg.Register("quick_messages_list", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		rows, err := d.Conn.Query(
			`SELECT id, title, content, sort_order, created_at, updated_at FROM quick_message ORDER BY sort_order ASC`)
		if err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			rec, err := scanQuickMessage(rows)
			if err != nil {
				continue
			}
			out = append(out, rec)
		}
		return out, nil
	})

	reg.Register("quick_messages_create", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		title := GetString(c, "title")
		content := GetString(c, "content")
		var maxOrder sql.NullInt64
		_ = d.Conn.QueryRow(`SELECT MAX(sort_order) FROM quick_message`).Scan(&maxOrder)
		next := int64(0)
		if maxOrder.Valid {
			next = maxOrder.Int64 + 1
		}
		now := nowRFC3339()
		res, err := d.Conn.Exec(
			`INSERT INTO quick_message (title, content, sort_order, created_at, updated_at) VALUES (?,?,?,?,?)`,
			title, content, next, now, now)
		if err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		id, _ := res.LastInsertId()
		return quickMessageByID(d, id)
	})

	reg.Register("quick_messages_update", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, err := GetIntOrErr(c, "id")
		if err != nil {
			return nil, err
		}
		if _, err := quickMessageByID(d, id); err != nil {
			return nil, err
		}
		now := nowRFC3339()
		if _, ok := c.Body["title"]; ok {
			if _, err := d.Conn.Exec(`UPDATE quick_message SET title = ?, updated_at = ? WHERE id = ?`, GetString(c, "title"), now, id); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		if _, ok := c.Body["content"]; ok {
			if _, err := d.Conn.Exec(`UPDATE quick_message SET content = ?, updated_at = ? WHERE id = ?`, GetString(c, "content"), now, id); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		return quickMessageByID(d, id)
	})

	reg.Register("quick_messages_delete", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, err := GetIntOrErr(c, "id")
		if err != nil {
			return nil, err
		}
		if _, err := d.Conn.Exec(`DELETE FROM quick_message WHERE id = ?`, id); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		return EmptyResult(), nil
	})

	reg.Register("quick_messages_reorder", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		ids := GetIntSlice(c, "ids")
		if len(ids) == 0 {
			return EmptyResult(), nil
		}
		now := nowRFC3339()
		for pos, id := range ids {
			if _, err := d.Conn.Exec(`UPDATE quick_message SET sort_order = ?, updated_at = ? WHERE id = ?`, pos, now, id); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		return EmptyResult(), nil
	})
}
