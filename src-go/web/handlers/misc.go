package handlers

// Small OS/misc + conversation-children endpoints.
//   get_home_directory        → the user's home dir (string)
//   probe_terminal_shell_path → whether a shell path exists (bool)
//   pet_list_active_sessions  → live pet sessions (empty until the ACP engine
//                               tracks connections; shape is faithful)
//   list_child_conversations  → delegation children of a conversation

import (
	"os"

	"yoyocode/src-go/web"
)

// RegisterMisc wires the small standalone endpoints.
func RegisterMisc(reg *Registry) {
	reg.Register("get_home_directory", func(c *web.Context) (any, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return nil, web.ErrIO("could not resolve home directory")
		}
		return home, nil
	})

	reg.Register("probe_terminal_shell_path", func(c *web.Context) (any, error) {
		path := GetString(c, "path")
		if path == "" {
			return false, nil
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return false, nil
		}
		return true, nil
	})

	// Until the ACP engine tracks live connections, no pet sessions are active.
	reg.Register("pet_list_active_sessions", func(c *web.Context) (any, error) {
		return map[string]any{
			"running_count": 0,
			"waiting_count": 0,
			"error_count":   0,
			"sessions":      []any{},
		}, nil
	})

	reg.Register("list_child_conversations", func(c *web.Context) (any, error) {
		d := stateDB(c)
		parentID := intVal(c.Body["parentConversationId"], c.Body["parent_conversation_id"])
		if parentID <= 0 {
			return []map[string]any{}, nil
		}
		if d == nil || d.Conn == nil {
			return []map[string]any{}, nil
		}
		rows, err := d.Conn.Query(
			"SELECT id, title, agent_type, status, model, git_branch, external_id, message_count, created_at, updated_at, last_message_at, folder_id, parent_id FROM conversation WHERE deleted_at IS NULL AND parent_id = ? ORDER BY COALESCE(last_message_at, updated_at) DESC",
			parentID)
		if err != nil {
			return []map[string]any{}, nil
		}
		defer rows.Close()
		return scanConversationSummaries(rows), nil
	})
}
