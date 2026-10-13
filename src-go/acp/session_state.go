package acp

import (
	"encoding/json"
	"sync"
	"time"
)

// ConnectionStatus mirrors upstream `acp::types::ConnectionStatus` with
// serde(rename_all="snake_case").
type ConnectionStatus string

const (
	StatusConnecting   ConnectionStatus = "connecting"
	StatusConnected    ConnectionStatus = "connected"
	StatusPrompting    ConnectionStatus = "prompting"
	StatusDisconnected ConnectionStatus = "disconnected"
	StatusError        ConnectionStatus = "error"
)

// ToolCallStatus mirrors upstream ToolCallStatus (snake_case).
type ToolCallStatus string

const (
	ToolCallPending  ToolCallStatus = "pending"
	ToolCallRunning  ToolCallStatus = "running"
	ToolCallSuccess  ToolCallStatus = "success"
	ToolCallFailed   ToolCallStatus = "failed"
	ToolCallRejected ToolCallStatus = "rejected"
)

// ─── Snapshot sub-structs ─────────────────────────────────────────────────

// LiveContentBlock mirrors upstream `LiveContentBlock`, a serde
// tag="kind" enum with snake_case variants.
type LiveContentBlock struct {
	Kind            string `json:"kind"` // text | thinking | tool_call_ref | plan
	Text            string `json:"text,omitempty"`
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`
	ToolCallID      string `json:"tool_call_id,omitempty"`
	Entries         any    `json:"entries,omitempty"`
}

// LiveMessage is the in-flight streaming turn.
type LiveMessage struct {
	ID        string             `json:"id"`
	Role      string             `json:"role"`
	Content   []LiveContentBlock `json:"content"`
	StartedAt time.Time          `json:"started_at"`
}

// ToolCallState is one live tool call. Optional fields use omitempty to match
// upstream's skip_serializing_if, keeping the common case byte-identical.
type ToolCallState struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Label     string          `json:"label"`
	Status    ToolCallStatus  `json:"status"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	Content   string          `json:"content,omitempty"`
	Locations json.RawMessage `json:"locations,omitempty"`
	Meta      json.RawMessage `json:"meta,omitempty"`
}

// PendingPermissionState is an unresolved permission request.
type PendingPermissionState struct {
	RequestID  string          `json:"request_id"`
	ToolCallID string          `json:"tool_call_id"`
	ToolCall   json.RawMessage `json:"tool_call"`
	Options    json.RawMessage `json:"options"`
	CreatedAt  time.Time       `json:"created_at"`
	Queued     uint32          `json:"queued"`
}

// PendingQuestionState is an in-flight `ask_user_question`.
type PendingQuestionState struct {
	RequestID string          `json:"request_id"`
	Questions json.RawMessage `json:"questions"`
	CreatedAt time.Time       `json:"created_at,omitempty"`
}

// PendingUserMessage is the in-flight user prompt for the current turn.
type PendingUserMessage struct {
	MessageID string          `json:"message_id"`
	Blocks    json.RawMessage `json:"blocks"`
}

// LiveSessionSnapshot mirrors upstream `LiveSessionSnapshot`. It is the payload
// of the WS `snapshot` frame — the cold-start / large-gap catch-up shape.
//
// Optional fields carry omitempty to match upstream skip_serializing_if so a
// connection with no pending question / no delegations stays byte-identical to
// the pre-feature shape.
type LiveSessionSnapshot struct {
	ConnectionID        string                  `json:"connection_id"`
	ConversationID      *int64                  `json:"conversation_id,omitempty"`
	FolderID            *int64                  `json:"folder_id,omitempty"`
	Status              ConnectionStatus        `json:"status"`
	ExternalID          string                  `json:"external_id,omitempty"`
	LiveMessage         *LiveMessage            `json:"live_message,omitempty"`
	ActiveToolCalls     []ToolCallState         `json:"active_tool_calls"`
	PendingPermission   *PendingPermissionState `json:"pending_permission,omitempty"`
	PendingQuestion     *PendingQuestionState   `json:"pending_question,omitempty"`
	PendingPlanApproval json.RawMessage         `json:"pending_plan_approval,omitempty"`
	PendingUserMessage  *PendingUserMessage     `json:"pending_user_message,omitempty"`
	ActiveDelegations   json.RawMessage         `json:"active_delegations,omitempty"`
	Feedback            json.RawMessage         `json:"feedback,omitempty"`
}

// ─── SessionState ─────────────────────────────────────────────────────────

// SessionState is the authoritative per-connection live state (upstream
// `SessionState`, held as Arc<RwLock<..>> per AgentConnection). Only the
// current turn's in-flight data lives here; completed turns are read back from
// the agent's JSONL transcript by the parsers.
type SessionState struct {
	mu sync.RWMutex

	ConnectionID   string
	ConversationID *int64
	FolderID       *int64
	ExternalID     string
	Status         ConnectionStatus

	LiveMessage        *LiveMessage
	ActiveToolCalls    []ToolCallState
	PendingPermission  *PendingPermissionState
	PendingQuestion    *PendingQuestionState
	PendingUserMessage *PendingUserMessage

	// Selector catalog populated by session/update events during a probe or a
	// live session's handshake. Mirrors upstream SessionState's
	// modes/config_options/prompt_capabilities/available_commands/selectors_ready/last_error.
	Modes              *SessionModeStateInfo
	ConfigOptions      []SessionConfigOptionInfo
	AvailableCommands  []AvailableCommandInfo
	PromptCapabilities *PromptCapabilitiesInfo
	SelectorsReady     bool
	LastError          *SessionLastError

	// EventSeq is the high-water mark; the next emitted envelope takes
	// EventSeq+1 and every `event` frame delivered after a snapshot/replay
	// carries seq > the value the client was given.
	EventSeq     uint64
	recentEvents RecentEvents
	stream       *ConnectionEventStream
}

func NewSessionState(connectionID string) *SessionState {
	return &SessionState{
		ConnectionID:    connectionID,
		Status:          StatusConnecting,
		ActiveToolCalls: []ToolCallState{},
		stream:          NewConnectionEventStream(),
	}
}

// Snapshot produces the LiveSessionSnapshot under the read lock.
func (s *SessionState) Snapshot() *LiveSessionSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	toolCalls := make([]ToolCallState, len(s.ActiveToolCalls))
	copy(toolCalls, s.ActiveToolCalls)
	return &LiveSessionSnapshot{
		ConnectionID:       s.ConnectionID,
		ConversationID:     s.ConversationID,
		FolderID:           s.FolderID,
		Status:             s.Status,
		ExternalID:         s.ExternalID,
		LiveMessage:        s.LiveMessage,
		ActiveToolCalls:    toolCalls,
		PendingPermission:  s.PendingPermission,
		PendingQuestion:    s.PendingQuestion,
		PendingUserMessage: s.PendingUserMessage,
	}
}

// EventSeqNow returns the current high-water mark.
func (s *SessionState) EventSeqNow() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.EventSeq
}

// Emit records an envelope into the ring buffer and broadcasts it. Must be the
// only emission path: holding the write lock across "increment seq → push to
// buffer → broadcast" is what guarantees a subscriber registered under the read
// lock cannot miss an event that follows its snapshot.
func (s *SessionState) Emit(payload json.RawMessage) *EventEnvelope {
	s.mu.Lock()
	s.EventSeq++
	env := &EventEnvelope{
		Seq:          s.EventSeq,
		ConnectionID: s.ConnectionID,
		Payload:      payload,
	}
	s.recentEvents.Push(env)
	stream := s.stream
	s.mu.Unlock()

	stream.Broadcast(env)
	return env
}

// Subscribe registers a live receiver. Callers must hold the read lock (see
// Attach) so registration is ordered against Emit's write lock.
func (s *SessionState) Subscribe() *StreamReceiver { return s.stream.Subscribe() }

// RecentEventsAfter returns events with seq > sinceSeq for a `replay`. The bool
// reports whether the ring buffer still covers the cursor — false means the
// client fell too far behind and must take a snapshot instead.
func (s *SessionState) RecentEventsAfter(sinceSeq uint64) ([]*EventEnvelope, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recentEvents.RangeAfter(sinceSeq)
}

// SetStatus updates the connection status under the write lock.
func (s *SessionState) SetStatus(st ConnectionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = st
}

// SetConversation binds the session to a persisted conversation.
func (s *SessionState) SetConversation(conversationID, folderID *int64, externalID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ConversationID = conversationID
	s.FolderID = folderID
	if externalID != "" {
		s.ExternalID = externalID
	}
}

// ─── Selector catalog (probe + live handshake) ──────────────────────────────

// SetModes records the agent's mode catalog (session_modes event).
func (s *SessionState) SetModes(m *SessionModeStateInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Modes = m
}

// SetConfigOptions records the agent's config option catalog
// (session_config_options event).
func (s *SessionState) SetConfigOptions(opts []SessionConfigOptionInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ConfigOptions = opts
}

// SetAvailableCommands records the agent's advertised slash commands
// (available_commands event).
func (s *SessionState) SetAvailableCommands(cmds []AvailableCommandInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AvailableCommands = cmds
}

// SetPromptCapabilities records what the agent accepts in a prompt
// (prompt_capabilities event).
func (s *SessionState) SetPromptCapabilities(p *PromptCapabilitiesInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PromptCapabilities = p
}

// MarkSelectorsReady latches once when the agent signals its initial selector
// set is complete (selectors_ready event). The probe waits on this before
// snapshotting.
func (s *SessionState) MarkSelectorsReady() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SelectorsReady = true
}

// SetLastError captures the agent's own error text (error event), surfaced by
// the probe path when the handshake fails.
func (s *SessionState) SetLastError(e *SessionLastError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastError = e
}

// IsSelectorsReady reports whether the agent has published its selectors.
func (s *SessionState) IsSelectorsReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SelectorsReady
}

// LastErrorSnapshot returns the captured agent error, if any.
func (s *SessionState) LastErrorSnapshot() *SessionLastError {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LastError
}

// AgentOptions returns a read-only snapshot of the modes + config options the
// agent advertised — mirroring upstream AgentOptionsSnapshot /
// SessionState::to_snapshot. Slices are always non-nil so the wire shape is a
// JSON array (never null), matching upstream's Vec contract.
func (s *SessionState) AgentOptions() *AgentOptionsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var modes *SessionModeStateInfo
	if s.Modes != nil {
		cp := *s.Modes
		modes = &cp
	}
	var pc *PromptCapabilitiesInfo
	if s.PromptCapabilities != nil {
		cp := *s.PromptCapabilities
		pc = &cp
	}
	opts := s.ConfigOptions
	if opts == nil {
		opts = []SessionConfigOptionInfo{}
	}
	cmds := s.AvailableCommands
	if cmds == nil {
		cmds = []AvailableCommandInfo{}
	}
	return &AgentOptionsSnapshot{
		Modes:              modes,
		ConfigOptions:      opts,
		AvailableCommands:  cmds,
		PromptCapabilities: pc,
	}
}

// ─── Session registry ─────────────────────────────────────────────────────

// SessionRegistry maps connection_id → live SessionState. Upstream holds these
// on the ConnectionManager (`manager.get_state(&connection_id)`).
type SessionRegistry struct {
	mu      sync.RWMutex
	byID    map[string]*SessionState
	metrics *EventBusMetrics
}

func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{byID: map[string]*SessionState{}, metrics: NewEventBusMetrics()}
}

// GetState returns the live session state, or nil when the connection is
// unknown (already GC'd, or never existed).
func (r *SessionRegistry) GetState(connectionID string) *SessionState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byID[connectionID]
}

// Insert registers a new session state.
func (r *SessionRegistry) Insert(s *SessionState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[s.ConnectionID] = s
}

// Remove drops a session state (connection gone).
func (r *SessionRegistry) Remove(connectionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, connectionID)
}

// Metrics exposes the event-bus counters for the debug/event_metrics endpoint.
func (r *SessionRegistry) Metrics() *EventBusMetrics { return r.metrics }
