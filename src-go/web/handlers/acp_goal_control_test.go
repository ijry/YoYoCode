package handlers_test

import (
	"encoding/json"
	"io"
	"testing"
	"time"

	"yoyocode/src-go/acp"
)

func TestAcpGoalControlLive(t *testing.T) {
	srv, st := newTestServerState(t)

	got := make(chan string, 4)
	st.ACP.SetSpawner(func(cfg acp.SpawnConfig) (*acp.AgentProcess, error) {
		cr, aw := io.Pipe()
		ar, cw := io.Pipe()
		clientPeer := acp.NewPeer(cr, cw)
		agentPeer := acp.NewPeer(ar, aw)
		agentPeer.HandleRequest("initialize", func(json.RawMessage) (any, error) {
			return map[string]any{"protocolVersion": acp.ACPProtocolVersion}, nil
		})
		agentPeer.HandleRequest("session/new", func(json.RawMessage) (any, error) {
			return map[string]any{"sessionId": "live-sess"}, nil
		})
		agentPeer.HandleNotification("_codex/session/goal_control", func(params json.RawMessage) {
			var p struct {
				Action string `json:"action"`
			}
			_ = json.Unmarshal(params, &p)
			got <- p.Action
		})
		go clientPeer.Run()
		go agentPeer.Run()
		return &acp.AgentProcess{Peer: clientPeer, CloseFn: func() { cw.Close(); aw.Close(); cr.Close(); ar.Close() }}, nil
	})

	code, conn := call(t, srv, "acp_connect", map[string]any{"agentType": "codex", "folderPath": t.TempDir()})
	if code != 200 {
		t.Fatalf("acp_connect status %d: %v", code, conn)
	}
	connID, _ := conn["id"].(string)
	if connID == "" {
		t.Fatalf("no connection id: %v", conn)
	}

	// invalid action rejected
	if c, _ := call(t, srv, "acp_goal_control", map[string]any{"connectionId": connID, "action": "nope"}); c == 200 {
		t.Fatalf("invalid action should be refused")
	}
	// unknown connection rejected
	if c, _ := call(t, srv, "acp_goal_control", map[string]any{"connectionId": "nope", "action": "pause"}); c == 200 {
		t.Fatalf("unknown connection should be refused")
	}

	// pause forwards to the adapter
	if c, _ := call(t, srv, "acp_goal_control", map[string]any{"connectionId": connID, "action": "pause"}); c != 200 {
		t.Fatalf("pause status = %d", c)
	}
	select {
	case a := <-got:
		if a != "pause" {
			t.Fatalf("adapter got action %q, want pause", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("adapter never received goal_control")
	}

	// clear forwards too
	if c, _ := call(t, srv, "acp_goal_control", map[string]any{"connectionId": connID, "action": "clear"}); c != 200 {
		t.Fatalf("clear status = %d", c)
	}
	select {
	case a := <-got:
		if a != "clear" {
			t.Fatalf("adapter got action %q, want clear", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("adapter never received clear")
	}
}
