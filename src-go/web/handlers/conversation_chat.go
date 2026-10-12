package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// RegisterConversationChat wires the no-folder "chat mode" endpoints:
//   - create_chat_dir            → mint a dated scratch dir (no DB rows)
//   - create_chat_conversation   → hidden "Chat" folder + chat conversation
//
// Mirrors upstream commands/conversations.rs create_chat_dir_core /
// create_chat_conversation_core. The ACP cwd is pinned the instant the user
// picks no-folder mode (create_chat_dir), then the folder + conversation rows
// are minted lazily on first send (create_chat_conversation reusing that dir).
func RegisterConversationChat(reg *Registry) {
	// create_chat_dir: pure filesystem, no DB rows. Result is camelCase {path}.
	reg.web.Register("create_chat_dir", func(c *web.Context) (any, error) {
		dir, err := createChatDir(appDataDir(c))
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": dir}, nil
	})

	// create_chat_conversation: hidden folder + chat conversation. Result is
	// camelCase {conversationId, folderId, folder:FolderDetail(snake_case)}.
	reg.web.Register("create_chat_conversation", func(c *web.Context) (any, error) {
		d := stateDB(c)
		if d == nil || d.Conn == nil {
			return nil, web.ErrDatabase("db unavailable")
		}
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		if agentType == "" {
			agentType = "claude_code"
		}
		title := strVal(c.Body["title"])
		existingDir := strVal(c.Body["existingDir"], c.Body["existing_dir"])

		var path string
		var err error
		if existingDir != "" {
			// Reuse the eagerly-minted scratch dir so the ACP cwd stays put.
			if err = os.MkdirAll(existingDir, 0o755); err != nil {
				return nil, web.ErrIO(err.Error())
			}
			path = existingDir
		} else if path, err = createChatDir(appDataDir(c)); err != nil {
			return nil, err
		}

		folder, ferr := addChatFolder(d, path)
		if ferr != nil {
			return nil, ferr
		}
		folderID, _ := folder["id"].(int64)

		// No transaction spans the two inserts (plain conn), so compensate on
		// conversation-insert failure by soft-deleting the orphan chat folder.
		now := nowRFC3339()
		var titleArg any
		if title != "" {
			titleArg = title
		}
		res, cerr := d.Conn.Exec(
			`INSERT INTO conversation (folder_id, title, agent_type, status, kind, title_locked, message_count, created_at, updated_at)
			 VALUES (?,?,?,?,?,0,0,?,?)`,
			folderID, titleArg, agentType, "in_progress", "chat", now, now)
		if cerr != nil {
			_, _ = d.Conn.Exec(`UPDATE folder SET deleted_at = ? WHERE id = ?`, now, folderID)
			return nil, web.ErrDatabase(cerr.Error())
		}
		convID, _ := res.LastInsertId()
		EmitConversationChanged(c, convID)
		return map[string]any{
			"conversationId": convID,
			"folderId":       folderID,
			"folder":         folder,
		}, nil
	})
}

// newChatUUID returns a 32-char hex id (v4-UUID "simple" shape, no dashes),
// matching upstream's uuid::Uuid::new_v4().simple() scratch-dir leaf.
func newChatUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	// Stamp the v4 / RFC-4122 variant bits so the leaf is a well-formed UUID.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:])
}

// createChatDir mints <dataDir>/chat-sessions/<YYYY-MM-DD>/<uuid>/ and returns
// its absolute path. Pure filesystem — writes no DB rows.
func createChatDir(dataDir string) (string, error) {
	if dataDir == "" {
		dataDir = "."
	}
	date := time.Now().Format("2006-01-02")
	dir := filepath.Join(dataDir, "chat-sessions", date, newChatUUID())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", web.ErrIO(err.Error())
	}
	abs, aerr := filepath.Abs(dir)
	if aerr != nil {
		return dir, nil
	}
	return abs, nil
}

// nextFolderSortOrder mirrors folder_service::next_sort_order: one past the max
// sort_order across folders and folder groups (the two share a sort space).
func nextFolderSortOrder(d *db.Database) int64 {
	var maxFolder, maxGroup int64
	_ = d.Conn.QueryRow(`SELECT COALESCE(MAX(sort_order),0) FROM folder`).Scan(&maxFolder)
	_ = d.Conn.QueryRow(`SELECT COALESCE(MAX(sort_order),0) FROM folder_group`).Scan(&maxGroup)
	if maxGroup > maxFolder {
		maxFolder = maxGroup
	}
	return maxFolder + 1
}

// addChatFolder inserts a hidden "Chat" folder (kind='chat', is_open=1, color
// 'inherit') and returns its FolderDetail. Mirrors folder_service::add_chat_folder.
func addChatFolder(d *db.Database, path string) (map[string]any, error) {
	now := nowRFC3339()
	order := nextFolderSortOrder(d)
	res, err := d.Conn.Exec(
		`INSERT INTO folder (name, path, git_branch, default_agent_type, last_opened_at, created_at, updated_at, is_open, sort_order, color, parent_id, kind, alias, folder_group_id, is_chat)
		 VALUES ('Chat', ?, NULL, NULL, ?, ?, ?, 1, ?, 'inherit', NULL, 'chat', NULL, NULL, 1)`,
		path, now, now, now, order)
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	id, _ := res.LastInsertId()
	return folderDetailByID(d, id)
}
