package acp

// Live connection lifecycle: spawn a real agent, run the ACP handshake, register
// the session with the event-stream registry (so WS attach streams it), and
// drive prompt/permission/cancel. Mirrors the manager side of upstream
// acp::connection + acp::manager.

import (
	"context"
	"os"
	"time"
)

type liveSession struct {
	conn    *Connection
	proc    *AgentProcess
	session *AgentSession
	state   *SessionState
	cancel  context.CancelFunc
}

// SetSessionRegistry wires the shared registry the WS attach protocol reads.
func (m *Manager) SetSessionRegistry(r *SessionRegistry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions = r
}

// SetSpawner overrides the agent spawner (tests inject a fake agent).
func (m *Manager) SetSpawner(fn func(SpawnConfig) (*AgentProcess, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spawner = fn
}

// SetResolver overrides the agent-type → SpawnConfig resolver.
func (m *Manager) SetResolver(fn func(agentType, cwd string, env map[string]string) (SpawnConfig, bool)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resolver = fn
}

// handshakeTimeout bounds initialize + session/new; a hung agent must not wedge
// the connect call forever.
const handshakeTimeout = 60 * time.Second

// ConnectLive spawns the agent, runs the handshake, registers the session, and
// returns the connection. On any failure it tears the child down and returns a
// typed error (dependency_missing when the agent can't be launched at all).
func (m *Manager) ConnectLive(agentType, folderPath string, conversationID int64) (*Connection, error) {
	m.mu.Lock()
	resolver := m.resolver
	spawner := m.spawner
	sessions := m.sessions
	bc := m.bc
	m.mu.Unlock()

	cwd := folderPath
	if cwd == "" {
		cwd = os.TempDir()
	}
	cfg, ok := resolver(agentType, cwd, nil)
	if !ok {
		return nil, &AgentUnavailableError{AgentType: agentType}
	}

	proc, err := spawner(cfg)
	if err != nil {
		return nil, &SpawnError{AgentType: agentType, Err: err}
	}

	id := uuidV4()
	state := NewSessionState(id)
	if conversationID > 0 {
		cid := conversationID
		state.SetConversation(&cid, nil, "")
	}
	sess := NewAgentSession(proc.Peer, state, agentType)

	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	if err := sess.Initialize(ctx); err != nil {
		cancel()
		_ = proc.Close()
		return nil, &SpawnError{AgentType: agentType, Err: err, Stderr: proc.StderrTail()}
	}
	if _, err := sess.NewSession(ctx, cwd, nil); err != nil {
		cancel()
		_ = proc.Close()
		return nil, &SpawnError{AgentType: agentType, Err: err, Stderr: proc.StderrTail()}
	}
	cancel()

	state.SetStatus(StatusConnected)
	conn := &Connection{
		ID:             id,
		AgentType:      agentType,
		Status:         "connected",
		FolderPath:     folderPath,
		ConversationID: conversationID,
		CreatedAt:      time.Now(),
	}
	ls := &liveSession{conn: conn, proc: proc, session: sess, state: state}

	m.mu.Lock()
	m.conn[id] = conn
	m.live[id] = ls
	m.mu.Unlock()
	if sessions != nil {
		sessions.Insert(state)
	}
	if bc != nil {
		bc.Send("acp://connection", map[string]any{"operation": "connected", "connection": conn})
	}
	return conn, nil
}

// PromptLive drives a prompt turn in the background; session/update events
// stream to the connection's event buffer as they arrive. Returns immediately
// with the connection unknown error if there is no live session.
func (m *Manager) PromptLive(connID string, blocks []any) error {
	m.mu.Lock()
	ls := m.live[connID]
	m.mu.Unlock()
	if ls == nil {
		return &ConnectionUnknownError{ID: connID}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	ls.cancel = cancel
	m.mu.Unlock()
	go func() {
		defer cancel()
		_, _ = ls.session.Prompt(ctx, blocks)
	}()
	return nil
}

// RespondPermission answers a pending permission request on a live session.
func (m *Manager) RespondPermission(connID, requestID, optionID string) bool {
	m.mu.Lock()
	ls := m.live[connID]
	m.mu.Unlock()
	if ls == nil {
		return false
	}
	return ls.session.ResolvePermission(requestID, optionID)
}

// CancelLive cancels the in-flight turn (ACP session/cancel + ctx cancel).
func (m *Manager) CancelLive(connID string) bool {
	m.mu.Lock()
	ls := m.live[connID]
	m.mu.Unlock()
	if ls == nil {
		return false
	}
	_ = ls.proc.Peer.Notify("session/cancel", map[string]any{"sessionId": ls.session.SessionID()})
	if ls.cancel != nil {
		ls.cancel()
	}
	return true
}

// GoalControlLive forwards a goal-control request (codex-acp
// `_codex/session/goal_control`, action "pause"|"clear") to the live adapter.
// Returns false when there is no live session for connID. Mirrors the manager
// side of upstream acp::manager::goal_control; the out-of-band interrupt reply
// is adapter-specific and not awaited here.
func (m *Manager) GoalControlLive(connID, action string) bool {
	m.mu.Lock()
	ls := m.live[connID]
	m.mu.Unlock()
	if ls == nil {
		return false
	}
	_ = ls.proc.Peer.Notify("_codex/session/goal_control", map[string]any{
		"sessionId": ls.session.SessionID(),
		"action":    action,
	})
	return true
}

// DisconnectLive tears down a live session (child + registry entry).
func (m *Manager) DisconnectLive(connID string) {
	m.mu.Lock()
	ls := m.live[connID]
	delete(m.live, connID)
	sessions := m.sessions
	if c, ok := m.conn[connID]; ok {
		c.Status = "disconnected"
	}
	m.mu.Unlock()
	if ls == nil {
		return
	}
	if ls.cancel != nil {
		ls.cancel()
	}
	_ = ls.proc.Close()
	if sessions != nil {
		sessions.Remove(connID)
	}
	if m.bc != nil {
		m.bc.Send("acp://event", map[string]any{"connectionID": connID, "status": "disconnected"})
	}
}

// LiveSession returns the agent session driver for a connection (nil if none).
func (m *Manager) LiveSession(connID string) *AgentSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ls := m.live[connID]; ls != nil {
		return ls.session
	}
	return nil
}

// ─── typed errors ────────────────────────────────────────────────────────────

// AgentUnavailableError: the agent can't be launched here (binary agent with no
// cached binary, or unknown type). Maps to dependency_missing (422).
type AgentUnavailableError struct{ AgentType string }

func (e *AgentUnavailableError) Error() string {
	return "agent not available for launch: " + e.AgentType
}

// SpawnError: the agent process failed to start or complete the handshake.
type SpawnError struct {
	AgentType string
	Err       error
	Stderr    []string
}

func (e *SpawnError) Error() string {
	msg := "failed to start agent " + e.AgentType + ": " + e.Err.Error()
	return msg
}

// ConnectionUnknownError: no such live connection.
type ConnectionUnknownError struct{ ID string }

func (e *ConnectionUnknownError) Error() string { return "connection not found: " + e.ID }

// FindConnectionForConversation returns the live connection bound to a
// conversation (by conversation id), with its current event seq. The optional
// session id is a pre-first-prompt fallback matched against external_id.
func (m *Manager) FindConnectionForConversation(conversationID int64, sessionID string) (connID string, eventSeq uint64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, ls := range m.live {
		if ls.state == nil {
			continue
		}
		snap := ls.state.Snapshot()
		if snap.ConversationID != nil && *snap.ConversationID == conversationID {
			return id, ls.state.EventSeqNow(), true
		}
		if sessionID != "" && snap.ExternalID == sessionID {
			return id, ls.state.EventSeqNow(), true
		}
	}
	return "", 0, false
}

// SnapshotByConversation returns the live snapshot for a conversation's
// connection, or nil when none is live.
func (m *Manager) SnapshotByConversation(conversationID int64) *LiveSessionSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ls := range m.live {
		if ls.state == nil {
			continue
		}
		snap := ls.state.Snapshot()
		if snap.ConversationID != nil && *snap.ConversationID == conversationID {
			return snap
		}
	}
	return nil
}
