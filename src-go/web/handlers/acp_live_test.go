package handlers_test

import (
	"encoding/json"
	"io"
	"testing"
	"time"

	"yoyocode/src-go/acp"
)

// fakeAgentSpawner returns a SetSpawner function that wires an in-memory ACP
// agent (no subprocess): it answers initialize/session/new/session/prompt and
// streams one message chunk during the prompt.
func fakeAgentSpawner() func(acp.SpawnConfig) (*acp.AgentProcess, error) {
	return func(cfg acp.SpawnConfig) (*acp.AgentProcess, error) {
		cr, aw := io.Pipe() // agent -> client
		ar, cw := io.Pipe() // client -> agent
		clientPeer := acp.NewPeer(cr, cw)
		agentPeer := acp.NewPeer(ar, aw)

		agentPeer.HandleRequest("initialize", func(json.RawMessage) (any, error) {
			return map[string]any{"protocolVersion": acp.ACPProtocolVersion}, nil
		})
		agentPeer.HandleRequest("session/new", func(json.RawMessage) (any, error) {
			return map[string]any{"sessionId": "live-sess"}, nil
		})
		agentPeer.HandleRequest("session/prompt", func(json.RawMessage) (any, error) {
			_ = agentPeer.Notify("session/update", map[string]any{
				"sessionId": "live-sess",
				"update":    map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "live-reply"}},
			})
			return map[string]any{"stopReason": "end_turn"}, nil
		})

		go clientPeer.Run()
		go agentPeer.Run()
		return &acp.AgentProcess{Peer: clientPeer, CloseFn: func() { cw.Close(); aw.Close(); cr.Close(); ar.Close() }}, nil
	}
}

func TestAcpConnectPromptLive(t *testing.T) {
	srv, st := newTestServerState(t)
	st.ACP.SetSpawner(fakeAgentSpawner())

	// acp_connect returns ConnectionInfo {id, agent_type, status}.
	code, conn := call(t, srv, "acp_connect", map[string]any{"agentType": "claude_code", "folderPath": t.TempDir()})
	if code != 200 {
		t.Fatalf("acp_connect status %d: %v", code, conn)
	}
	connID, _ := conn["id"].(string)
	if connID == "" || conn["agent_type"] != "claude_code" || conn["status"] != "connected" {
		t.Fatalf("ConnectionInfo wrong: %v", conn)
	}

	// It appears in acp_list_connections.
	{
		_, list := callArr(t, srv, "acp_list_connections", map[string]any{})
		if len(list) != 1 {
			t.Fatalf("expected 1 connection, got %d", len(list))
		}
	}

	// acp_prompt drives a turn; the streamed content lands in the event buffer.
	if code, _ := call(t, srv, "acp_prompt", map[string]any{"connectionId": connID, "prompt": "hi"}); code != 200 {
		t.Fatalf("acp_prompt status %d", code)
	}

	state := st.Session.GetState(connID)
	if state == nil {
		t.Fatal("session not registered for attach")
	}
	deadline := time.Now().Add(3 * time.Second)
	var text, stop string
	for time.Now().Before(deadline) {
		envs, _ := state.RecentEventsAfter(0)
		for _, e := range envs {
			var m map[string]any
			_ = json.Unmarshal(e.Payload, &m)
			switch m["type"] {
			case "content_delta":
				if s, ok := m["text"].(string); ok {
					text = s
				}
			case "turn_complete":
				if s, ok := m["stop_reason"].(string); ok {
					stop = s
				}
			}
		}
		if text != "" && stop != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if text != "live-reply" {
		t.Fatalf("streamed content not published: %q", text)
	}
	if stop != "end_turn" {
		t.Fatalf("turn_complete not published: %q", stop)
	}

	// acp_disconnect tears it down.
	if code, _ := call(t, srv, "acp_disconnect", map[string]any{"connectionId": connID}); code != 200 {
		t.Fatalf("acp_disconnect status %d", code)
	}
	if st.Session.GetState(connID) != nil {
		t.Fatal("session should be removed after disconnect")
	}
}

// A binary-distribution agent cannot launch without the binary cache → the
// connect must surface dependency_missing (422), not a 500.
func TestAcpConnectBinaryUnavailable(t *testing.T) {
	srv, _ := newTestServerState(t)
	code, out := call(t, srv, "acp_connect", map[string]any{"agentType": "cursor", "folderPath": t.TempDir()})
	if code != 422 || out["code"] != "dependency_missing" {
		t.Fatalf("expected 422 dependency_missing, got %d %v", code, out)
	}
}
