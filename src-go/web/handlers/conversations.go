package handlers

import (
	"database/sql"

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
		return listAllConversations(stateDB(c), listAllOptions{
			folderIDs:       int64Slice(c.Body["folderIds"], c.Body["folder_ids"]),
			agentType:       strVal(c.Body["agentType"], c.Body["agent_type"]),
			search:          strVal(c.Body["search"]),
			sortBy:          strVal(c.Body["sortBy"], c.Body["sort_by"]),
			status:          strVal(c.Body["status"]),
			includeChildren: GetBool(c, "includeChildren", "include_children"),
		}), nil
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
		id, err := createConversation(d, c.Body)
		if err != nil {
			return nil, err
		}
		return id, nil
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

// listAllOptions is the upstream ListAllConversationsParams, resolved.
type listAllOptions struct {
	folderIDs       []int64
	agentType       string
	search          string
	sortBy          string
	status          string
	includeChildren bool
}

// conversationStatuses are upstream's ConversationStatus values. A `status`
// filter that is not one of these is ignored rather than rejected, matching
// upstream's `if let Ok(...)` around the deserialization.
var conversationStatuses = map[string]bool{
	"in_progress":    true,
	"pending_review": true,
	"completed":      true,
	"cancelled":      true,
}

// listAllConversations mirrors upstream db::service::conversation_service::list_all.
// Every filter is applied in SQL so ordering stays server-side:
//
//   - loop-engineering runs never surface here (their entry point is the loops
//     workbench);
//   - delegation children are hidden unless includeChildren is set;
//   - an absent/empty folderIds means "every folder that still exists", not
//     "every folder" — conversations in a removed folder stay hidden;
//   - search is a substring match on the title;
//   - sortBy "oldest" orders by updated_at ascending, anything else descending.
func listAllConversations(d *db.Database, opts listAllOptions) []map[string]any {
	if d == nil || d.Conn == nil {
		return []map[string]any{}
	}
	query := `SELECT ` + conversationSummaryColumns + `
	 FROM conversation c WHERE c.deleted_at IS NULL AND COALESCE(c.kind, 'regular') != 'loop'`
	var args []any
	if !opts.includeChildren {
		query += " AND c.parent_id IS NULL"
	}
	if len(opts.folderIDs) > 0 {
		query += " AND c.folder_id IN (" + placeholders(len(opts.folderIDs)) + ")"
		for _, id := range opts.folderIDs {
			args = append(args, id)
		}
	} else {
		query += " AND c.folder_id IN (SELECT id FROM folder WHERE deleted_at IS NULL)"
	}
	if opts.agentType != "" {
		query += " AND c.agent_type = ?"
		args = append(args, opts.agentType)
	}
	if opts.search != "" {
		query += " AND c.title LIKE ?"
		args = append(args, "%"+opts.search+"%")
	}
	if opts.status != "" && conversationStatuses[opts.status] {
		query += " AND c.status = ?"
		args = append(args, opts.status)
	}
	if opts.sortBy == "oldest" {
		query += " ORDER BY c.updated_at ASC"
	} else {
		query += " ORDER BY c.updated_at DESC"
	}
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
		if rec, ok := scanConversationSummary(rows); ok {
			out = append(out, rec)
		}
	}
	return out
}

// conversationSummaryColumns is the shared SELECT list for a conversation
// summary, in the order scanConversationSummary expects. The table must be
// aliased `c`. `child_count` is computed rather than stored — upstream's
// fill_summary_extras does the same, with one aggregate over the returned set.
const conversationSummaryColumns = `c.id, c.folder_id, c.title, c.agent_type, c.status, c.model,
	c.git_branch, c.external_id, c.message_count, c.created_at, c.updated_at, c.last_message_at,
	c.parent_id, c.kind, c.title_locked, c.pinned_at,
	(SELECT COUNT(*) FROM conversation cc WHERE cc.parent_id = c.id AND cc.deleted_at IS NULL)`

// rowScanner is the common slice of *sql.Row and *sql.Rows, so one scanner
// serves both the list queries and the single-row lookup.
type rowScanner interface{ Scan(dest ...any) error }

// nullableString maps a NULL column to JSON null and a value to its string,
// matching upstream's Option<String>. An empty string would be a different
// value to the client, which distinguishes "untitled" from "titled empty".
func nullableString(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

// scanConversationSummary builds upstream's DbConversationSummary.
// `pinned_at` is always present (null when unset) so the client's
// `string | null` type holds; `parent_id` is omitted when absent, as upstream
// skips it.
func scanConversationSummary(s rowScanner) (map[string]any, bool) {
	var (
		id, messageCount, folderID, childCount int64
		titleLocked                            int64
		parentID                               sql.NullInt64
		title, model, gitBranch, externalID    sql.NullString
		kind, pinnedAt, lastMessageAt          sql.NullString
		updatedAt                              sql.NullString
		agentType, status, createdAt           string
	)
	if err := s.Scan(&id, &folderID, &title, &agentType, &status, &model, &gitBranch, &externalID,
		&messageCount, &createdAt, &updatedAt, &lastMessageAt, &parentID, &kind, &titleLocked,
		&pinnedAt, &childCount); err != nil {
		return nil, false
	}
	rec := map[string]any{
		"id":              id,
		"folder_id":       folderID,
		"title":           nullableString(title),
		"title_locked":    titleLocked != 0,
		"agent_type":      agentType,
		"status":          status,
		"kind":            strVal(kind.String, "regular"),
		"model":           nullableString(model),
		"git_branch":      nullableString(gitBranch),
		"external_id":     nullableString(externalID),
		"message_count":   messageCount,
		"child_count":     childCount,
		"created_at":      createdAt,
		"updated_at":      updatedAt.String,
		"pinned_at":       nullableString(pinnedAt),
		"last_message_at": lastMessageAt.String,
	}
	if parentID.Valid {
		rec["parent_id"] = parentID.Int64
	}
	return rec, true
}

// listConversations lists one folder's conversations, newest activity first.
func listConversations(d *db.Database, folderID int64) []map[string]any {
	if d == nil || d.Conn == nil {
		return []map[string]any{}
	}
	rows, err := d.Conn.Query(
		`SELECT `+conversationSummaryColumns+`
		 FROM conversation c WHERE c.deleted_at IS NULL AND c.folder_id = ?
		 ORDER BY COALESCE(c.last_message_at, c.updated_at) DESC`, folderID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	return scanConversationSummaries(rows)
}

// getConversation reads one conversation as upstream's DbConversationSummary.
func getConversation(d *db.Database, id int64) (map[string]any, error) {
	if d == nil || d.Conn == nil {
		return nil, web.ErrDatabase("db unavailable")
	}
	row := d.Conn.QueryRow(
		`SELECT `+conversationSummaryColumns+` FROM conversation c WHERE c.id = ? AND c.deleted_at IS NULL`, id)
	rec, ok := scanConversationSummary(row)
	if !ok {
		return nil, web.ErrNotFound("conversation not found")
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
