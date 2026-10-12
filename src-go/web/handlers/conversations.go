package handlers

import (
	"database/sql"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// RegisterConversations wires conversation CRUD endpoints.
func RegisterConversations(reg *Registry) {
	reg.web.Register("list_conversations", func(c *web.Context) (any, error) {
		d := stateDB(c)
		folderID := int64Num(c.Body["folderId"])
		fid := int64Num(c.Body["folder_id"])
		if fid > 0 {
			folderID = fid
		}
		return listConversations(d, folderID), nil
	})
	reg.web.Register("list_all_conversations", func(c *web.Context) (any, error) {
		d := stateDB(c)
		folderIDs := int64Slice(c.Body["folderIds"], c.Body["folder_ids"])
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		return listAllConversations(d, folderIDs, agentType), nil
	})
	reg.web.Register("get_conversation", func(c *web.Context) (any, error) {
		d := stateDB(c)
		id := intVal(c.Body["conversationId"], c.Body["conversation_id"], c.Body["id"])
		if id <= 0 {
			return nil, web.ErrInvalidInput("conversationId required")
		}
		return getConversation(d, int64(id))
	})
	reg.web.Register("list_folders", func(c *web.Context) (any, error) {
		d := stateDB(c)
		return computeFolders(d), nil
	})
	reg.web.Register("get_folder_conversation", func(c *web.Context) (any, error) {
		// Upstream's params are {conversationId, tailTurns?, fromIndex?} and it
		// answers with the conversation DETAIL (summary + a windowed turn
		// slice) — not a folder. folderId stays accepted for in-app callers.
		convID := intVal(c.Body["conversationId"], c.Body["conversation_id"])
		if convID <= 0 {
			convID = intVal(c.Body["folderId"], c.Body["folder_id"])
		}
		if convID <= 0 {
			return nil, web.ErrInvalidInput("conversationId required")
		}
		return getFolderConversation(stateDB(c), convID,
			int(int64Num(c.Body["tailTurns"])), int(int64Num(c.Body["fromIndex"])))
	})
	reg.web.Register("create_conversation", func(c *web.Context) (any, error) {
		d := stateDB(c)
		return createConversation(d, c.Body)
	})
	reg.web.Register("update_conversation_title", func(c *web.Context) (any, error) {
		d := stateDB(c)
		id := intVal(c.Body["conversationId"], c.Body["conversation_id"], c.Body["id"])
		if id <= 0 {
			return nil, web.ErrInvalidInput("conversationId required")
		}
		title := strVal(c.Body["title"])
		// A manual rename locks the title so the auto-title backfill leaves it be.
		if _, err := d.Conn.Exec(
			`UPDATE conversation SET title = ?, title_locked = 1, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
			title, nowRFC3339(), id); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		EmitConversationChanged(c, id)
		return EmptyResult(), nil
	})
	reg.web.Register("update_conversation_status", func(c *web.Context) (any, error) {
		d := stateDB(c)
		id := intVal(c.Body["conversationId"], c.Body["conversation_id"], c.Body["id"])
		if id <= 0 {
			return nil, web.ErrInvalidInput("conversationId required")
		}
		status := strVal(c.Body["status"])
		if status == "" {
			return nil, web.ErrInvalidInput("status required")
		}
		if _, err := d.Conn.Exec(
			`UPDATE conversation SET status = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
			status, nowRFC3339(), id); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		EmitConversationChanged(c, id)
		return EmptyResult(), nil
	})
	reg.web.Register("update_conversation_pinned", func(c *web.Context) (any, error) {
		d := stateDB(c)
		id := intVal(c.Body["conversationId"], c.Body["conversation_id"], c.Body["id"])
		if id <= 0 {
			return nil, web.ErrInvalidInput("conversationId required")
		}
		pinned := false
		if v, ok := c.Body["pinned"].(bool); ok {
			pinned = v
		}
		var pinnedAt any = nil
		if pinned {
			pinnedAt = nowRFC3339()
		}
		if _, err := d.Conn.Exec(
			`UPDATE conversation SET pinned_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
			pinnedAt, nowRFC3339(), id); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		EmitConversationChanged(c, id)
		return EmptyResult(), nil
	})
	reg.web.Register("delete_conversation", func(c *web.Context) (any, error) {
		d := stateDB(c)
		id := intVal(c.Body["conversationId"], c.Body["conversation_id"], c.Body["id"])
		if id <= 0 {
			return nil, web.ErrInvalidInput("conversationId required")
		}
		// Soft delete (upstream sets deleted_at). Also purge any tab rows that
		// pointed at it so a ghost tab can't resurrect.
		if _, err := d.Conn.Exec(
			`UPDATE conversation SET deleted_at = ? WHERE id = ?`, nowRFC3339(), id); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		_, _ = d.Conn.Exec(`DELETE FROM opened_tab WHERE conversation_id = ?`, id)
		EmitConversationChanged(c, id)
		return EmptyResult(), nil
	})
	reg.web.Register("get_stats", func(c *web.Context) (any, error) {
		d := stateDB(c)
		return computeStats(d), nil
	})
	reg.web.Register("get_sidebar_data", func(c *web.Context) (any, error) {
		d := stateDB(c)
		return getSidebarData(d), nil
	})
}

func intVal(values ...any) int64 {
	var out int64
	for _, v := range values {
		if x, ok := v.(float64); ok && x > 0 {
			out = int64(x)
		}
	}
	return out
}

func int64Num(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		var out int64
		for _, r := range t {
			if r >= '0' && r <= '9' {
				out = out*10 + int64(r-'0')
			}
		}
		return out
	}
	return 0
}

func int64Slice(names ...any) []int64 {
	var out []int64
	for _, n := range names {
		if arr, ok := n.([]any); ok {
			for _, item := range arr {
				if x, ok := item.(float64); ok && x > 0 {
					out = append(out, int64(x))
				}
			}
		}
	}
	return out
}

func strVal(values ...any) string {
	for _, v := range values {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// listConversations mirrors upstream list_conversations_core returning
// DbConversationSummary[] — the mobile "conversations" tab for a folder.
func listConversations(d *db.Database, folderID int64) []map[string]any {
	if d == nil || d.Conn == nil {
		return []map[string]any{}
	}
	rows, err := d.Conn.Query(
		`SELECT id, title, agent_type, status, model, git_branch, external_id, message_count,
		        created_at, updated_at, last_message_at, folder_id, parent_id
		 FROM conversation WHERE deleted_at IS NULL AND folder_id = ?
		 ORDER BY COALESCE(last_message_at, updated_at) DESC`, folderID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	return scanConversationSummaries(rows)
}

func listAllConversations(d *db.Database, folderIDs []int64, agentType string) []map[string]any {
	if d == nil || d.Conn == nil {
		return []map[string]any{}
	}
	query := `SELECT id, title, agent_type, status, model, git_branch, external_id, message_count,
	       created_at, updated_at, last_message_at, folder_id, parent_id
	 FROM conversation WHERE deleted_at IS NULL`
	var args []any
	if len(folderIDs) > 0 {
		placeholders := make([]string, len(folderIDs))
		for i, id := range folderIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query += " AND folder_id IN (" + strings.Join(placeholders, ", ") + ")"
	}
	if agentType != "" {
		query += " AND agent_type = ?"
		args = append(args, agentType)
	}
	query += " ORDER BY COALESCE(last_message_at, updated_at) DESC"
	rows, err := d.Conn.Query(query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	return scanConversationSummaries(rows)
}

func scanConversationSummaries(rows *sql.Rows) []map[string]any {
	out := []map[string]any{}
	for rows.Next() {
		var (
			id, messageCount, folderID   int64
			parentID                     sql.NullInt64
			title, updatedAt             sql.NullString
			lastMessageAt                sql.NullString
			model, gitBranch, externalID sql.NullString
			agentType, status, createdAt string
		)
		if err := rows.Scan(&id, &title, &agentType, &status, &model, &gitBranch, &externalID,
			&messageCount, &createdAt, &updatedAt, &lastMessageAt, &folderID, &parentID); err != nil {
			continue
		}
		rec := map[string]any{
			"id":              id,
			"folder_id":       folderID,
			"title":           title.String,
			"agent_type":      agentType,
			"status":          status,
			"model":           model.String,
			"git_branch":      gitBranch.String,
			"external_id":     externalID.String,
			"message_count":   messageCount,
			"created_at":      createdAt,
			"updated_at":      updatedAt.String,
			"last_message_at": lastMessageAt.String,
		}
		if parentID.Valid {
			rec["parent_id"] = parentID.Int64
		}
		out = append(out, rec)
	}
	return out
}

func getConversation(d *db.Database, id int64) (map[string]any, error) {
	if d == nil || d.Conn == nil {
		return nil, web.ErrDatabase("db unavailable")
	}
	row := d.Conn.QueryRow(
		`SELECT id, folder_id, title, agent_type, status, model, git_branch, external_id, parent_id, message_count, created_at, updated_at FROM conversation WHERE id = ? AND deleted_at IS NULL`, id)
	var (
		convID, folderID, messageCount int64
		parentID                       sql.NullInt64
		title                          sql.NullString
		model, gitBranch, externalID   sql.NullString
		agentType, status              string
		createdAt, updatedAt           sql.NullString
	)
	err := row.Scan(&convID, &folderID, &title, &agentType, &status, &model, &gitBranch, &externalID, &parentID, &messageCount, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, web.ErrNotFound("conversation not found")
	}
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	rec := map[string]any{
		"id": convID, "folder_id": folderID, "title": title.String, "agent_type": agentType,
		"status": status, "model": model.String, "git_branch": gitBranch.String, "external_id": externalID.String,
		"message_count": messageCount, "created_at": createdAt.String, "updated_at": updatedAt.String,
	}
	if parentID.Valid {
		rec["parent_id"] = parentID.Int64
	}
	return rec, nil
}

// getFolderConversation mirrors upstream commands::get_folder_conversation_with_live_core:
// the conversation's summary plus its turns, sliced to the requested window.
// `tailTurns` and `fromIndex` are mutually exclusive (upstream rejects both);
// either one turns on the window metadata, whose presence is what tells the
// client to merge a page into its transcript instead of replacing it.
//
// Upstream's DbConversationDetail also carries session_stats, a transcript
// watermark and in-flight turn ids. All of those are `skip_serializing_if`
// optional upstream, so leaving them out stays wire-legal.
func getFolderConversation(d *db.Database, convID int64, tailTurns, fromIndex int) (map[string]any, error) {
	if tailTurns > 0 && fromIndex > 0 {
		return nil, web.ErrInvalidInput("tailTurns and fromIndex are mutually exclusive")
	}
	summary, err := getConversation(d, convID)
	if err != nil {
		return nil, err
	}
	turns := loadConversationTurns(d, convID)
	offset := 0
	switch {
	case tailTurns > 0 && tailTurns < len(turns):
		offset = len(turns) - tailTurns
	case fromIndex > 0:
		offset = fromIndex
		if offset > len(turns) {
			offset = len(turns)
		}
	}
	page := make([]any, 0, len(turns)-offset)
	for _, t := range turns[offset:] {
		page = append(page, t.raw)
	}
	out := map[string]any{"summary": summary, "turns": page}
	if tailTurns > 0 || fromIndex > 0 {
		meta := computeWindowMeta(turns, offset)
		out["turns_offset"] = meta.offset
		out["turns_total"] = meta.total
		out["assistant_turns_before_offset"] = meta.assistantBefore
	}
	return out, nil
}
