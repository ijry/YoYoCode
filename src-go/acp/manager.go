// Package acp mirrors upstream src-tauri/src/acp: the Agent Client Protocol
// engine (agent listing, connections, session state, event stream).
package acp

import (
	"encoding/json"
	"sync"
	"time"

	"yoyocode/src-go/db"
)

// EventSink is the narrow contract the ACP engine needs to publish onto the
// legacy web firehose. It exists to keep this package independent of
// `yoyocode/src-go/web` — upstream's acp module is likewise unaware of the web
// layer, and the dependency would otherwise form an import cycle (web needs acp
// for the attach protocol).
//
// *web.Broadcaster satisfies this interface; appstate wires the two together.
type EventSink interface {
	Send(channel string, payload any)
	SendRaw(channel string, payload json.RawMessage)
}

// Manager is the Go analog of upstream ConnectionManager.
type Manager struct {
	mu   sync.RWMutex
	conn map[string]*Connection
	// agents registered (mirrors upstream agent registry)
	agents []*AgentInfo
	db     *db.Database
	bc     EventSink
	seq    uint64

	// Live-session machinery (wired by connection.go). sessions is the shared
	// registry the WS attach protocol reads; live holds the running agent
	// process + driver per connection. spawner/resolver are injectable so tests
	// can point at a fake agent instead of a real npx/uvx launch.
	sessions *SessionRegistry
	live     map[string]*liveSession
	spawner  func(SpawnConfig) (*AgentProcess, error)
	resolver func(agentType, cwd string, env map[string]string) (SpawnConfig, bool)
	// probeLocks serializes concurrent probes per agent type (upstream's
	// probe_locks). Different agent types still probe in parallel; the same
	// agent type is bounded to one in-flight probe so rapid settings-UI clicks
	// fan out to a single real CLI process instead of one per click.
	probeLocks sync.Map
}

// Connection is one live agent session.
type Connection struct {
	ID             string    `json:"id"`
	AgentType      string    `json:"agentType"`
	Status         string    `json:"status"` // connected | idle | disconnected | error
	FolderPath     string    `json:"folderPath"`
	ConversationID int64     `json:"conversationId"`
	CreatedAt      time.Time `json:"createdAt"`
}

// AgentInfo describes a supported agent (mirrors upstream registry entries).
type AgentInfo struct {
	AgentType string            `json:"agentType"`
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Env       map[string]string `json:"env,omitempty"`
}

func NewManager(database *db.Database, bc EventSink) *Manager {
	return &Manager{
		conn:     map[string]*Connection{},
		agents:   []*AgentInfo{},
		db:       database,
		bc:       bc,
		live:     map[string]*liveSession{},
		spawner:  SpawnAgent,
		resolver: ResolveSpawn,
	}
}

func (m *Manager) RegisterAgent(a *AgentInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.agents {
		if existing.AgentType == a.AgentType {
			*existing = *a
			return
		}
	}
	m.agents = append(m.agents, a)
}

func (m *Manager) ListAgents() []*AgentInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*AgentInfo, len(m.agents))
	copy(out, m.agents)
	return out
}

func (m *Manager) ListConnections() []*Connection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Connection, 0, len(m.conn))
	for _, c := range m.conn {
		out = append(out, c)
	}
	return out
}

func (m *Manager) Connect(agentType, folderPath string, conversationID int64) (*Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := uuidV4()
	c := &Connection{
		ID:             id,
		AgentType:      agentType,
		Status:         "connected",
		FolderPath:     folderPath,
		ConversationID: conversationID,
		CreatedAt:      time.Now(),
	}
	m.conn[id] = c
	m.bc.Send("acp://connection", map[string]any{"operation": "connected", "connection": c})
	return c, nil
}

func (m *Manager) Disconnect(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.conn[id]; ok {
		c.Status = "disconnected"
		m.bc.Send("acp://event", map[string]any{"connectionID": id, "status": "disconnected"})
	}
}

func (m *Manager) Get(id string) *Connection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.conn[id]
}

// Touch is the keepalive probe behind acp_touch_connection: it reports whether
// the connection is still known (live) so a client can refresh its idle timer.
func (m *Manager) Touch(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.conn[id]
	return ok
}

func (m *Manager) NextSeq() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	return m.seq
}
