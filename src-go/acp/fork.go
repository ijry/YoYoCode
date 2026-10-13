package acp

// fork.go ports upstream acp::manager::fork_session's DB half
// (persist_fork_outcome) and ties it to the ACP `session/fork` round-trip. The
// full upstream path also resolves a per-agent fork point from the parsed
// transcript and serializes against the connection command loop; this build
// forks at the tail (no fork point) and has no command loop, so both are
// documented rather than ported.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"yoyocode/src-go/db"
)

// ForkResultInfo mirrors upstream ForkResultInfo.
type ForkResultInfo struct {
	ForkedSessionID       string `json:"forkedSessionId"`
	OriginalSessionID     string `json:"originalSessionId"`
	SiblingConversationID int64  `json:"siblingConversationId"`
}

// TurnInProgressError: a fork was requested while a turn is in flight (409).
type TurnInProgressError struct{}

func (e *TurnInProgressError) Error() string { return "a turn is already in progress" }

// ForkSession branches the live session bound to connID via the ACP
// `session/fork`, persists the two-row layout and returns the result.
//
// conversationID / folderID link the current row on demand when the connection
// has not linked one yet (a resumed history connection forks before its first
// prompt); both must be positive to link.
func (m *Manager) ForkSession(connID string, conversationID, folderID int64) (*ForkResultInfo, error) {
	m.mu.Lock()
	ls := m.live[connID]
	m.mu.Unlock()
	if ls == nil {
		return nil, &ConnectionUnknownError{ID: connID}
	}
	snap := ls.state.Snapshot()
	if snap.ConversationID == nil && conversationID > 0 && folderID > 0 {
		cid, fid := conversationID, folderID
		ls.state.SetConversation(&cid, &fid, snap.ExternalID)
		snap = ls.state.Snapshot()
	}
	if snap.ConversationID == nil {
		return nil, errors.New("fork_session requires a linked conversation row")
	}
	rowID := *snap.ConversationID

	ctx, cancel := context.WithTimeout(context.Background(), acpControlTimeout)
	defer cancel()
	forkedID, originalID, err := ls.session.Fork(ctx)
	if err != nil {
		return nil, err
	}
	siblingID, err := persistForkOutcome(m.db, rowID, forkedID, originalID)
	if err != nil {
		return nil, err
	}
	// Re-point the live session's state at S2 so a later snapshot/reconnect sees
	// the forked id.
	ls.state.SetConversation(&rowID, snap.FolderID, forkedID)
	if m.bc != nil {
		m.bc.Send("conversation://changed", map[string]any{"conversationId": rowID})
		m.bc.Send("conversation://changed", map[string]any{"conversationId": siblingID})
	}
	return &ForkResultInfo{ForkedSessionID: forkedID, OriginalSessionID: originalID, SiblingConversationID: siblingID}, nil
}

// persistForkOutcome applies the atomic two-row layout (upstream
// persist_fork_outcome): the current row is re-pointed to the forked session
// (S2) with a `[Fork] ` title, and a sibling row preserving the pre-fork
// history (S1) is inserted — or an existing S1 row adopted, since
// (external_id, agent_type) is UNIQUE.
func persistForkOutcome(d *db.Database, conversationID int64, forkedSessionID, originalSessionID string) (int64, error) {
	if d == nil || d.Conn == nil {
		return 0, errors.New("fork: no database")
	}
	tx, err := d.Conn.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339)

	// WRITE FIRST: claim the live row, so a deleted/absent row is rejected
	// before any derived state is written.
	res, err := tx.Exec(`UPDATE conversation SET updated_at = ? WHERE id = ? AND deleted_at IS NULL`, now, conversationID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, fmt.Errorf("conversation %d not found or already deleted", conversationID)
	}

	var (
		title       sql.NullString
		titleLocked int
		folderID    sql.NullInt64
		agentType   string
		gitBranch   sql.NullString
		kind        sql.NullString
	)
	if err := tx.QueryRow(
		`SELECT title, COALESCE(title_locked,0), folder_id, agent_type, git_branch, kind FROM conversation WHERE id = ?`,
		conversationID).Scan(&title, &titleLocked, &folderID, &agentType, &gitBranch, &kind); err != nil {
		return 0, err
	}

	// Strip any existing `[Fork]` prefix so forks don't stack it.
	cleanTitle := ""
	if title.Valid {
		cleanTitle = strings.TrimSpace(strings.TrimPrefix(title.String, "[Fork]"))
	}

	// Current row → S2 (+ `[Fork] ` title, locked, when it had a title).
	if title.Valid {
		if _, err := tx.Exec(
			`UPDATE conversation SET title = ?, title_locked = 1, external_id = ?, updated_at = ? WHERE id = ?`,
			"[Fork] "+cleanTitle, forkedSessionID, now, conversationID); err != nil {
			return 0, err
		}
	} else if _, err := tx.Exec(
		`UPDATE conversation SET external_id = ?, updated_at = ? WHERE id = ?`,
		forkedSessionID, now, conversationID); err != nil {
		return 0, err
	}

	// Adopt a row already holding S1 (the UNIQUE (external_id, agent_type) index
	// forbids a second), else insert the sibling.
	var existingID int64
	err = tx.QueryRow(
		`SELECT id FROM conversation WHERE external_id = ? AND agent_type = ? AND id != ? AND deleted_at IS NULL LIMIT 1`,
		originalSessionID, agentType, conversationID).Scan(&existingID)
	if err == nil {
		return existingID, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	siblingKind := kind.String
	if siblingKind == "" || siblingKind == "delegate" {
		siblingKind = "regular"
	}
	var siblingTitle any
	if title.Valid {
		siblingTitle = cleanTitle
	}
	ins, err := tx.Exec(
		`INSERT INTO conversation (folder_id, title, title_locked, agent_type, status, kind, git_branch, external_id, message_count, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		folderID, siblingTitle, titleLocked, agentType, "pending_review", siblingKind, gitBranch, originalSessionID, 0, now, now)
	if err != nil {
		return 0, err
	}
	siblingID, _ := ins.LastInsertId()
	// The sibling keeps the pre-fork history under the original's name, so it
	// keeps the original's tag links too.
	_, _ = tx.Exec(
		`INSERT INTO conversation_tag_link (conversation_id, tag_id, created_at) SELECT ?, tag_id, ? FROM conversation_tag_link WHERE conversation_id = ?`,
		siblingID, now, conversationID)
	return siblingID, tx.Commit()
}
