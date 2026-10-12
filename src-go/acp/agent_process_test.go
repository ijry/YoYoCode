package acp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// fakeAgentMainSrc is a self-contained ACP agent (stdlib only, no struct tags so
// it nests inside this raw string): reads newline JSON-RPC from stdin, answers
// initialize/session/new/session/prompt, streams one message chunk.
const fakeAgentMainSrc = `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

func send(v map[string]any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(os.Stdout, "%s\n", b)
}

func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		method, _ := m["method"].(string)
		id := m["id"]
		switch method {
		case "initialize":
			send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"protocolVersion": 1}})
		case "session/new":
			send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"sessionId": "sp-1"}})
		case "session/prompt":
			send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
				"sessionId": "sp-1",
				"update":    map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "spawned-ok"}},
			}})
			send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn"}})
		}
	}
}
`

func TestSpawnAgentEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go-run spawn smoke test in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainPath, []byte(fakeAgentMainSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ap, err := SpawnAgent(SpawnConfig{Command: "go", Args: []string{"run", mainPath}})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer ap.Close()

	state := NewSessionState("spawned")
	sess := NewAgentSession(ap.Peer, state, "claude_code")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := sess.Initialize(ctx); err != nil {
		t.Fatalf("initialize over subprocess: %v (stderr: %v)", err, ap.StderrTail())
	}
	if _, err := sess.NewSession(ctx, dir, nil); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := sess.Prompt(ctx, []any{map[string]any{"type": "text", "text": "go"}}); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var text string
	for time.Now().Before(deadline) {
		for _, e := range collectEvents(state) {
			if e["type"] == "content_delta" {
				if s, ok := e["text"].(string); ok {
					text = s
				}
			}
		}
		if text != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if text != "spawned-ok" {
		t.Fatalf("expected streamed content from subprocess, got %q (stderr: %v)", text, ap.StderrTail())
	}
}
