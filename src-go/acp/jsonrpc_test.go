package acp

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"
)

// pipePair wires two Peers together over in-memory pipes so a test can act as
// both the client (our engine) and the agent.
func pipePair(t *testing.T) (client, agent *Peer, closeFn func()) {
	t.Helper()
	cr, aw := io.Pipe() // agent writes -> client reads
	ar, cw := io.Pipe() // client writes -> agent reads
	client = NewPeer(cr, cw)
	agent = NewPeer(ar, aw)
	go client.Run()
	go agent.Run()
	closeFn = func() {
		cw.Close()
		aw.Close()
		cr.Close()
		ar.Close()
	}
	return
}

func TestPeerCallResponse(t *testing.T) {
	client, agent, done := pipePair(t)
	defer done()

	agent.HandleRequest("initialize", func(params json.RawMessage) (any, error) {
		var p struct {
			ProtocolVersion int `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		return map[string]any{"protocolVersion": p.ProtocolVersion, "ok": true}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := client.Call(ctx, "initialize", map[string]any{"protocolVersion": 1})
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}
	var out map[string]any
	if json.Unmarshal(res, &out) != nil || out["ok"] != true || out["protocolVersion"].(float64) != 1 {
		t.Fatalf("unexpected result: %s", res)
	}
}

func TestPeerCallError(t *testing.T) {
	client, agent, done := pipePair(t)
	defer done()
	agent.HandleRequest("boom", func(params json.RawMessage) (any, error) {
		return nil, &RPCError{Code: -32000, Message: "nope"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Call(ctx, "boom", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Message != "nope" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPeerNotification(t *testing.T) {
	client, agent, done := pipePair(t)
	defer done()
	var mu sync.Mutex
	got := []string{}
	ch := make(chan struct{}, 2)
	client.HandleNotification("session/update", func(params json.RawMessage) {
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(params, &p)
		mu.Lock()
		got = append(got, p.Text)
		mu.Unlock()
		ch <- struct{}{}
	})
	if err := agent.Notify("session/update", map[string]any{"text": "hello"}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if err := agent.Notify("session/update", map[string]any{"text": "world"}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for notification")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

// The agent may call back into the client (permission / fs requests). Verify
// bidirectional request handling.
func TestPeerReverseRequest(t *testing.T) {
	client, agent, done := pipePair(t)
	defer done()
	client.HandleRequest("fs/read_text_file", func(params json.RawMessage) (any, error) {
		return map[string]any{"content": "file-body"}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := agent.Call(ctx, "fs/read_text_file", map[string]any{"path": "/x"})
	if err != nil {
		t.Fatalf("reverse call: %v", err)
	}
	var out map[string]any
	if json.Unmarshal(res, &out) != nil || out["content"] != "file-body" {
		t.Fatalf("unexpected: %s", res)
	}
}

func TestPeerMethodNotFound(t *testing.T) {
	client, agent, done := pipePair(t)
	defer done()
	_ = agent
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Call(ctx, "no_such_method", nil)
	if err == nil {
		t.Fatal("expected method-not-found error")
	}
	if rpcErr, ok := err.(*RPCError); !ok || rpcErr.Code != -32601 {
		t.Fatalf("expected -32601, got %v", err)
	}
}
