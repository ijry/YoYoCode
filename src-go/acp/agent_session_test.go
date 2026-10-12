package acp

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// fakeAgent implements the agent (server) side of the ACP handshake on a Peer:
// it answers initialize/session/new/session/prompt and streams session/update
// notifications during a prompt turn.
func fakeAgent(agent *Peer) {
	agent.HandleRequest("initialize", func(params json.RawMessage) (any, error) {
		return map[string]any{"protocolVersion": ACPProtocolVersion, "agentCapabilities": map[string]any{}}, nil
	})
	agent.HandleRequest("session/new", func(params json.RawMessage) (any, error) {
		return map[string]any{"sessionId": "sess-1"}, nil
	})
	agent.HandleRequest("session/prompt", func(params json.RawMessage) (any, error) {
		// Stream a message chunk and a tool call, then end the turn.
		_ = agent.Notify("session/update", map[string]any{
			"sessionId": "sess-1",
			"update": map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"type": "text", "text": "Hello "},
			},
		})
		_ = agent.Notify("session/update", map[string]any{
			"sessionId": "sess-1",
			"update": map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"type": "text", "text": "world"},
			},
		})
		_ = agent.Notify("session/update", map[string]any{
			"sessionId": "sess-1",
			"update": map[string]any{
				"sessionUpdate": "tool_call",
				"toolCallId":    "tc-1",
				"title":         "read file",
				"kind":          "read",
				"status":        "pending",
			},
		})
		return map[string]any{"stopReason": "end_turn"}, nil
	})
}

func collectEvents(state *SessionState) []map[string]any {
	envs, _ := state.RecentEventsAfter(0)
	out := make([]map[string]any, 0, len(envs))
	for _, e := range envs {
		var m map[string]any
		_ = json.Unmarshal(e.Payload, &m)
		out = append(out, m)
	}
	return out
}

func TestAgentSessionHandshakeAndStream(t *testing.T) {
	client, agent, done := pipePair(t)
	defer done()
	fakeAgent(agent)

	state := NewSessionState("conn-1")
	sess := NewAgentSession(client, state, "claude_code")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := sess.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sid, err := sess.NewSession(ctx, "/tmp/x", nil)
	if err != nil || sid != "sess-1" {
		t.Fatalf("new session: %v %q", err, sid)
	}
	stop, err := sess.Prompt(ctx, []any{map[string]any{"type": "text", "text": "hi"}})
	if err != nil || stop != "end_turn" {
		t.Fatalf("prompt: %v %q", err, stop)
	}

	// Allow the trailing notifications (served in goroutines) to drain.
	deadline := time.Now().Add(2 * time.Second)
	var events []map[string]any
	for time.Now().Before(deadline) {
		events = collectEvents(state)
		if len(events) >= 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	types := []string{}
	var text string
	for _, e := range events {
		types = append(types, e["type"].(string))
		if e["type"] == "content_delta" {
			text += e["text"].(string)
		}
	}
	// session_started + 2 content_delta + tool_call + turn_complete (order of
	// the trailing notifications vs the prompt response is not guaranteed, but
	// all must be present).
	has := func(want string) bool {
		for _, ty := range types {
			if ty == want {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"session_started", "content_delta", "tool_call", "turn_complete"} {
		if !has(want) {
			t.Fatalf("missing %q event; got %v", want, types)
		}
	}
	if text != "Hello world" {
		t.Fatalf("assembled text = %q", text)
	}

	// turn_complete carries the agent type + stop reason.
	for _, e := range events {
		if e["type"] == "turn_complete" {
			if e["stop_reason"] != "end_turn" || e["agent_type"] != "claude_code" {
				t.Fatalf("turn_complete payload: %v", e)
			}
		}
	}
}

func TestAgentSessionPermissionResolve(t *testing.T) {
	client, agent, done := pipePair(t)
	defer done()
	agent.HandleRequest("initialize", func(json.RawMessage) (any, error) {
		return map[string]any{"protocolVersion": ACPProtocolVersion}, nil
	})
	agent.HandleRequest("session/new", func(json.RawMessage) (any, error) {
		return map[string]any{"sessionId": "s"}, nil
	})
	// The agent asks for permission mid-prompt and blocks on the answer.
	permOutcome := make(chan string, 1)
	agent.HandleRequest("session/prompt", func(json.RawMessage) (any, error) {
		res, err := agent.Call(context.Background(), "session/request_permission", map[string]any{
			"toolCall": map[string]any{"toolCallId": "tc"},
			"options":  []any{map[string]any{"optionId": "allow", "name": "Allow"}},
		})
		if err != nil {
			return nil, err
		}
		var out struct {
			Outcome struct {
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		_ = json.Unmarshal(res, &out)
		permOutcome <- out.Outcome.OptionID
		return map[string]any{"stopReason": "end_turn"}, nil
	})

	state := NewSessionState("c")
	sess := NewAgentSession(client, state, "codex")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = sess.Initialize(ctx)
	_, _ = sess.NewSession(ctx, "/tmp", nil)

	promptDone := make(chan error, 1)
	go func() {
		_, err := sess.Prompt(ctx, []any{})
		promptDone <- err
	}()

	// Wait for the permission_request event, then resolve it.
	var reqID string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && reqID == "" {
		for _, e := range collectEvents(state) {
			if e["type"] == "permission_request" {
				reqID = e["request_id"].(string)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reqID == "" {
		t.Fatal("no permission_request event emitted")
	}
	if !sess.ResolvePermission(reqID, "allow") {
		t.Fatal("ResolvePermission returned false")
	}
	select {
	case err := <-promptDone:
		if err != nil {
			t.Fatalf("prompt after permission: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not complete after permission resolve")
	}
	select {
	case id := <-permOutcome:
		if id != "allow" {
			t.Fatalf("agent received optionId %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("agent never got permission outcome")
	}
}
