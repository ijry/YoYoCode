package acp

// fork_test.go covers the session/fork two-row layout: the current row is
// re-pointed to the forked session (S2) with a `[Fork] ` title, and a sibling
// row preserving the pre-fork session (S1) is inserted.

import (
	"encoding/json"
	"testing"
	"time"

	"yoyocode/src-go/db"
)

func forkDB(t *testing.T) *db.Database {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { database.Conn.Close() })
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := database.Conn.Exec(
		`INSERT INTO conversation (id, folder_id, title, agent_type, status, kind, title_locked, message_count, created_at, updated_at)
		 VALUES (1, 10, 'My chat', 'claude_code', 'active', 'regular', 0, 3, ?, ?)`, now, now); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	return database
}

func TestPersistForkOutcomeTwoRowLayout(t *testing.T) {
	database := forkDB(t)
	sibling, err := persistForkOutcome(database, 1, "sess-2", "sess-1")
	if err != nil {
		t.Fatalf("persistForkOutcome: %v", err)
	}
	if sibling == 0 || sibling == 1 {
		t.Fatalf("sibling id = %d, want a new row", sibling)
	}

	var title, externalID string
	var locked int
	if err := database.Conn.QueryRow(
		`SELECT title, COALESCE(external_id,''), COALESCE(title_locked,0) FROM conversation WHERE id = 1`).
		Scan(&title, &externalID, &locked); err != nil {
		t.Fatal(err)
	}
	if title != "[Fork] My chat" || externalID != "sess-2" || locked != 1 {
		t.Errorf("current row = %q/%q/%d, want [Fork] My chat/sess-2/1", title, externalID, locked)
	}

	var sTitle, sExternal, sStatus string
	if err := database.Conn.QueryRow(
		`SELECT title, COALESCE(external_id,''), status FROM conversation WHERE id = ?`, sibling).
		Scan(&sTitle, &sExternal, &sStatus); err != nil {
		t.Fatal(err)
	}
	if sTitle != "My chat" || sExternal != "sess-1" || sStatus != "pending_review" {
		t.Errorf("sibling row = %q/%q/%q, want My chat/sess-1/pending_review", sTitle, sExternal, sStatus)
	}
}

// TestPersistForkOutcomeAdoptsExistingS1 locks the UNIQUE (external_id,
// agent_type) guard: a row already holding S1 is adopted, not duplicated.
func TestPersistForkOutcomeAdoptsExistingS1(t *testing.T) {
	database := forkDB(t)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := database.Conn.Exec(
		`INSERT INTO conversation (folder_id, title, agent_type, status, kind, external_id, message_count, created_at, updated_at)
		 VALUES (10, 'sibling', 'claude_code', 'pending_review', 'regular', 'sess-1', 0, ?, ?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	existing, _ := res.LastInsertId()

	got, err := persistForkOutcome(database, 1, "sess-2", "sess-1")
	if err != nil {
		t.Fatalf("persistForkOutcome: %v", err)
	}
	if got != existing {
		t.Errorf("adopted id = %d, want the existing row %d", got, existing)
	}
}

// TestForkSessionEndToEnd drives a real handshake against a fake agent that
// answers session/fork, and asserts the result + the persisted rows.
func TestForkSessionEndToEnd(t *testing.T) {
	client, agent, done := pipePair(t)
	defer done()
	fakeAgent(agent)
	agent.HandleRequest("session/fork", func(json.RawMessage) (any, error) {
		return map[string]any{"sessionId": "sess-2"}, nil
	})
	database := forkDB(t)

	mgr := NewManager(database, nil)
	mgr.SetSpawner(func(SpawnConfig) (*AgentProcess, error) {
		return &AgentProcess{Peer: client, CloseFn: func() {}}, nil
	})
	mgr.SetResolver(func(agentType, cwd string, env map[string]string) (SpawnConfig, bool) {
		return SpawnConfig{Command: "fake", Cwd: cwd}, true
	})

	conn, err := mgr.ConnectLive("claude_code", t.TempDir(), 1, ConnectOptions{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	res, err := mgr.ForkSession(conn.ID, 1, 10)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if res.ForkedSessionID != "sess-2" || res.OriginalSessionID != "sess-1" || res.SiblingConversationID == 0 {
		t.Errorf("fork result = %+v", res)
	}
	// The live session adopted S2.
	if id := mgr.LiveSession(conn.ID).SessionID(); id != "sess-2" {
		t.Errorf("live session id = %q, want sess-2", id)
	}
}

// TestForkSessionUnknownConnection maps a missing connection to the typed error.
func TestForkSessionUnknownConnection(t *testing.T) {
	mgr := NewManager(nil, nil)
	if _, err := mgr.ForkSession("nope", 1, 10); err == nil {
		t.Fatal("expected a connection-unknown error")
	}
}
