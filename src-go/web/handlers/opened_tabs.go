package handlers

// Mirror upstream web/handlers/conversations.rs (list_opened_tabs /
// save_opened_tabs) + db/service/tab_service.rs.
//
// The tab set is a workspace-global, compare-and-set-versioned list. The
// version lives in app_metadata under "opened_tabs_version" and advances by one
// on every accepted write; a save whose expectedVersion no longer matches is
// rejected (accepted:false) and handed the current truth to reconcile against.
// Draft tabs (conversation_id == null) are never persisted.

import (
	"strconv"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const openedTabsVersionKey = "opened_tabs_version"

// openedTab is the wire shape (upstream models::OpenedTab, snake_case).
type openedTab struct {
	ID             int64  `json:"id"`
	FolderID       int64  `json:"folder_id"`
	ConversationID *int64 `json:"conversation_id"`
	AgentType      string `json:"agent_type"`
	Position       int64  `json:"position"`
	IsActive       bool   `json:"is_active"`
	IsPinned       bool   `json:"is_pinned"`
}

func tabsVersion(d *db.Database) int64 {
	raw := valueOrDefault(d, openedTabsVersionKey)
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func listAllTabs(d *db.Database) []openedTab {
	out := []openedTab{}
	rows, err := d.Conn.Query(
		`SELECT id, folder_id, conversation_id, agent_type, position, is_active, is_pinned FROM opened_tab ORDER BY position ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var t openedTab
		var conv *int64
		var active, pinned int64
		if rows.Scan(&t.ID, &t.FolderID, &conv, &t.AgentType, &t.Position, &active, &pinned) != nil {
			continue
		}
		t.ConversationID = conv
		t.IsActive = active != 0
		t.IsPinned = pinned != 0
		out = append(out, t)
	}
	return out
}

// parseIncomingTabs reads the save payload tolerantly: upstream OpenedTab is
// snake_case on the wire, but accept camelCase too in case a client sends it.
func parseIncomingTabs(items any) []openedTab {
	arr, ok := items.([]any)
	if !ok {
		return nil
	}
	out := make([]openedTab, 0, len(arr))
	for _, raw := range arr {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		t := openedTab{
			ID:        asInt64(pick(m, "id")),
			FolderID:  asInt64(pick(m, "folder_id", "folderId")),
			AgentType: asString(pick(m, "agent_type", "agentType")),
			Position:  asInt64(pick(m, "position")),
			IsActive:  asBool(pick(m, "is_active", "isActive")),
			IsPinned:  asBool(pick(m, "is_pinned", "isPinned")),
		}
		if cv := pick(m, "conversation_id", "conversationId"); cv != nil {
			if n := asInt64(cv); n != 0 {
				t.ConversationID = &n
			}
		}
		out = append(out, t)
	}
	return out
}

func saveAllTabs(d *db.Database, items []openedTab) error {
	if _, err := d.Conn.Exec(`DELETE FROM opened_tab`); err != nil {
		return err
	}
	now := nowRFC3339()
	activeSeen := false
	for _, item := range items {
		// Skip drafts — never persist a conversation-less tab.
		if item.ConversationID == nil {
			continue
		}
		isActive := 0
		if item.IsActive && !activeSeen {
			activeSeen = true
			isActive = 1
		}
		isPinned := 0
		if item.IsPinned {
			isPinned = 1
		}
		if _, err := d.Conn.Exec(
			`INSERT INTO opened_tab (folder_id, conversation_id, agent_type, position, is_active, is_pinned, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
			item.FolderID, *item.ConversationID, item.AgentType, item.Position, isActive, isPinned, now, now); err != nil {
			return err
		}
	}
	return nil
}

func snapshotPayload(items []openedTab, version int64) map[string]any {
	return map[string]any{"items": items, "version": version}
}

// RegisterOpenedTabs wires the opened-tab CAS endpoints.
func RegisterOpenedTabs(reg *Registry) {
	reg.Register("list_opened_tabs", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		return snapshotPayload(listAllTabs(d), tabsVersion(d)), nil
	})

	reg.Register("save_opened_tabs", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		expected := GetInt(c, "expectedVersion", "expected_version")
		items := parseIncomingTabs(c.Body["items"])
		current := tabsVersion(d)
		if current != expected {
			// Stale write — reject and hand back the current truth.
			return map[string]any{
				"accepted": false,
				"version":  current,
				"tabs":     listAllTabs(d),
			}, nil
		}
		if err := saveAllTabs(d, items); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		next := current + 1
		upsertValue(d, openedTabsVersionKey, strconv.FormatInt(next, 10), nowRFC3339())
		out := map[string]any{
			"accepted": true,
			"version":  next,
			"tabs":     listAllTabs(d),
		}
		// Mirror upstream: broadcast the new snapshot on the tabs side-channel.
		Emit(c, "tabs://changed", snapshotPayload(listAllTabs(d), next))
		return out, nil
	})
}

// ─── tiny value coercers (local to opened tabs) ─────────────────────────────

func pick(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func asInt64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	}
	return 0
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}
