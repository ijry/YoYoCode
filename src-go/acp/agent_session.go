package acp

// AgentSession drives the ACP handshake over a Peer and translates the agent's
// session/update notifications into AcpEvent envelopes published on the
// connection's event stream (SessionState.Emit), which the WS attach protocol
// streams to clients. Mirrors the core of upstream acp::connection.
//
// Field names on the wire are the ACP schema's camelCase (sessionId,
// toolCallId, protocolVersion, clientCapabilities, mcpServers, stopReason,
// sessionUpdate); the published AcpEvent payloads are upstream's snake_case
// (content_delta, thinking, tool_call, tool_call_update, plan_update,
// turn_complete, session_started, permission_request).

import (
	"context"
	"encoding/json"
	"os"
	"sync"
)

// ACPProtocolVersion is the protocol version upstream negotiates.
const ACPProtocolVersion = 1

// AgentSession is one live ACP session bound to a connection's SessionState.
type AgentSession struct {
	peer      *Peer
	state     *SessionState
	agentType string

	mu           sync.Mutex
	sessionID    string
	agentCaps    json.RawMessage
	pendingPerms map[string]chan string
}

// NewAgentSession wires the agent→client handlers (session/update, permission,
// fs read/write) onto the peer and returns the session driver.
func NewAgentSession(peer *Peer, state *SessionState, agentType string) *AgentSession {
	s := &AgentSession{
		peer:         peer,
		state:        state,
		agentType:    agentType,
		pendingPerms: map[string]chan string{},
	}
	peer.HandleNotification("session/update", s.onSessionUpdate)
	peer.HandleRequest("session/request_permission", s.onRequestPermission)
	peer.HandleRequest("fs/read_text_file", s.onReadTextFile)
	peer.HandleRequest("fs/write_text_file", s.onWriteTextFile)
	return s
}

// Initialize performs the ACP initialize handshake and records agent caps.
func (s *AgentSession) Initialize(ctx context.Context) error {
	res, err := s.peer.Call(ctx, "initialize", map[string]any{
		"protocolVersion": ACPProtocolVersion,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{"readTextFile": true, "writeTextFile": true},
		},
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.agentCaps = res
	s.mu.Unlock()
	return nil
}

// NewSession opens a session at cwd and records the session id, emitting a
// session_started event.
func (s *AgentSession) NewSession(ctx context.Context, cwd string, mcpServers []any) (string, error) {
	if mcpServers == nil {
		mcpServers = []any{}
	}
	res, err := s.peer.Call(ctx, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": mcpServers,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(res, &out)
	s.mu.Lock()
	s.sessionID = out.SessionID
	s.mu.Unlock()
	s.emit(map[string]any{"type": "session_started", "session_id": out.SessionID})
	return out.SessionID, nil
}

// SessionID returns the negotiated session id.
func (s *AgentSession) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// Prompt sends a prompt turn and returns the stop reason. session/update
// notifications stream in during the call and are published as they arrive.
func (s *AgentSession) Prompt(ctx context.Context, blocks []any) (string, error) {
	s.state.SetStatus(StatusPrompting)
	res, err := s.peer.Call(ctx, "session/prompt", map[string]any{
		"sessionId": s.SessionID(),
		"prompt":    blocks,
	})
	if err != nil {
		s.state.SetStatus(StatusError)
		s.emit(map[string]any{"type": "error", "message": err.Error()})
		return "", err
	}
	var out struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(res, &out)
	s.state.SetStatus(StatusConnected)
	s.emit(map[string]any{
		"type":        "turn_complete",
		"session_id":  s.SessionID(),
		"stop_reason": out.StopReason,
		"agent_type":  s.agentType,
	})
	return out.StopReason, nil
}

// SetMode selects the session's mode (ACP `session/set_mode`).
func (s *AgentSession) SetMode(ctx context.Context, modeID string) error {
	if _, err := s.peer.Call(ctx, "session/set_mode", map[string]any{
		"sessionId": s.SessionID(),
		"modeId":    modeID,
	}); err != nil {
		return err
	}
	// The agent also pushes the adopted mode as a session update; emit a local
	// copy so a caller that awaits this sees the change right away.
	s.emit(map[string]any{"type": "mode_changed", "modeId": modeID})
	return nil
}

// SetConfigOption sets one session config option (ACP
// `session/set_config_option`).
//
// The wire shape follows upstream's encode_config_option_value, which its
// boolean_values_carry_a_type_discriminator test pins: the value rides as an
// opaque string, except for a boolean option, where "true" becomes a real
// boolean AND the request gains `"type": "boolean"` — the discriminator is part
// of the payload, not just the value's JSON type.
func (s *AgentSession) SetConfigOption(ctx context.Context, configID, valueID string, boolean bool) error {
	params := map[string]any{
		"sessionId": s.SessionID(),
		"configId":  configID,
		"value":     valueID,
	}
	if boolean {
		params["type"] = "boolean"
		params["value"] = valueID == "true"
	}
	if _, err := s.peer.Call(ctx, "session/set_config_option", params); err != nil {
		return err
	}
	s.emit(map[string]any{
		"type": "config_option_changed", "configId": configID, "valueId": valueID,
	})
	return nil
}

// ResolvePermission answers a pending session/request_permission with the chosen
// option id (driven by acp_respond_permission).
func (s *AgentSession) ResolvePermission(requestID, optionID string) bool {
	s.mu.Lock()
	ch := s.pendingPerms[requestID]
	delete(s.pendingPerms, requestID)
	s.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- optionID
	return true
}

// ─── agent → client ─────────────────────────────────────────────────────────

func (s *AgentSession) onSessionUpdate(params json.RawMessage) {
	var p struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil || len(p.Update) == 0 {
		return
	}
	var disc struct {
		SessionUpdate string          `json:"sessionUpdate"`
		Content       json.RawMessage `json:"content"`
		ToolCallID    string          `json:"toolCallId"`
		Title         string          `json:"title"`
		Kind          string          `json:"kind"`
		Status        string          `json:"status"`
		Entries       json.RawMessage `json:"entries"`
	}
	_ = json.Unmarshal(p.Update, &disc)
	switch disc.SessionUpdate {
	case "agent_message_chunk":
		s.emit(map[string]any{"type": "content_delta", "text": contentText(disc.Content)})
	case "agent_thought_chunk":
		s.emit(map[string]any{"type": "thinking", "text": contentText(disc.Content)})
	case "tool_call":
		s.emit(map[string]any{
			"type": "tool_call", "tool_call_id": disc.ToolCallID,
			"title": disc.Title, "kind": disc.Kind, "status": disc.Status,
		})
	case "tool_call_update":
		ev := map[string]any{"type": "tool_call_update", "tool_call_id": disc.ToolCallID}
		if disc.Title != "" {
			ev["title"] = disc.Title
		}
		if disc.Status != "" {
			ev["status"] = disc.Status
		}
		s.emit(ev)
	case "plan":
		var entries any
		if len(disc.Entries) > 0 {
			_ = json.Unmarshal(disc.Entries, &entries)
		}
		s.emit(map[string]any{"type": "plan_update", "entries": entries})
	default:
		// Forward unknown updates verbatim under a passthrough event so no agent
		// signal is silently dropped while the translation table grows.
		var raw map[string]any
		_ = json.Unmarshal(p.Update, &raw)
		raw["type"] = "acp_update"
		s.emit(raw)
	}
}

func (s *AgentSession) onRequestPermission(params json.RawMessage) (any, error) {
	var p struct {
		ToolCall json.RawMessage `json:"toolCall"`
		Options  json.RawMessage `json:"options"`
	}
	_ = json.Unmarshal(params, &p)
	requestID := uuidV4()
	ch := make(chan string, 1)
	s.mu.Lock()
	s.pendingPerms[requestID] = ch
	s.mu.Unlock()

	s.emit(map[string]any{
		"type":       "permission_request",
		"request_id": requestID,
		"tool_call":  rawOrNull(p.ToolCall),
		"options":    rawOrNull(p.Options),
		"queued":     0,
	})

	optionID := <-ch
	return map[string]any{
		"outcome": map[string]any{"outcome": "selected", "optionId": optionID},
	}, nil
}

func (s *AgentSession) onReadTextFile(params json.RawMessage) (any, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: -32602, Message: "invalid params"}
	}
	data, err := os.ReadFile(p.Path)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	return map[string]any{"content": string(data)}, nil
}

func (s *AgentSession) onWriteTextFile(params json.RawMessage) (any, error) {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: -32602, Message: "invalid params"}
	}
	if err := os.WriteFile(p.Path, []byte(p.Content), 0o644); err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	return map[string]any{}, nil
}

// emit marshals an AcpEvent payload and publishes it on the connection stream.
func (s *AgentSession) emit(payload map[string]any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.state.Emit(raw)
}

// contentText pulls the text out of an ACP content block {type:"text", text}.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var c struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &c)
	return c.Text
}

func rawOrNull(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}
