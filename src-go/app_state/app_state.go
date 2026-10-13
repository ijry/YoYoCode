// Package appstate mirrors upstream src-tauri/src/app_state.rs.
package appstate

import (
	"sync"

	"yoyocode/src-go/acp"
	"yoyocode/src-go/db"
	"yoyocode/src-go/terminal"
	"yoyocode/src-go/web"
)

// ServerState tracks the web server lifecycle (port, token, running flag).
type ServerState struct {
	mu      sync.Mutex
	running bool
	port    uint16
	token   string
	host    string
}

func NewServerState() *ServerState { return &ServerState{} }

func (s *ServerState) MarkRunning(host string, port uint16, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running, s.port, s.token, s.host = true, port, token, host
}

func (s *ServerState) MarkStopped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
}

func (s *ServerState) IsRunning() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.running }
func (s *ServerState) Port() uint16    { s.mu.Lock(); defer s.mu.Unlock(); return s.port }
func (s *ServerState) Token() string   { s.mu.Lock(); defer s.mu.Unlock(); return s.token }
func (s *ServerState) Host() string    { s.mu.Lock(); defer s.mu.Unlock(); return s.host }

// AppState is the root of shared state in the process.
type AppState struct {
	DB       *db.Database
	ACP      *acp.Manager
	Session  *acp.SessionRegistry
	Server   ServerState
	bc       *web.Broadcaster
	DataDir  string
	Terminal *terminal.Manager
}

// Broadcaster exposes the shared web event broadcaster (used by web.Router).
func (s *AppState) Broadcaster() *web.Broadcaster { return s.bc }

// Sessions exposes the live ACP session registry the WS attach protocol reads.
// Satisfies the web.Sessions interface.
func (s *AppState) Sessions() *acp.SessionRegistry { return s.Session }

var (
	once   sync.Once
	global *AppState
)

// Get returns the process-global AppState, initializing on first use.
func Get() *AppState {
	once.Do(func() { global = &AppState{bc: web.NewBroadcaster()} })
	return global
}

// Init wires the global AppState with concrete services.
func Init(database *db.Database, dataDir string) *AppState {
	st := Get()
	st.DB = database
	st.DataDir = dataDir
	// Point the ACP binary cache at the data dir so ResolveSpawn can launch a
	// downloaded binary agent (cursor/antigravity) without a request context.
	acp.SetBinaryCacheRoot(dataDir)
	st.Session = acp.NewSessionRegistry()
	st.ACP = acp.NewManager(database, st.bc)
	// Share the attach registry with the engine so a live agent session's
	// events reach WS subscribers.
	st.ACP.SetSessionRegistry(st.Session)
	st.Terminal = terminal.NewManager(st.bc)
	return st
}
