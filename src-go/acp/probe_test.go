package acp

import (
	"encoding/json"
	"testing"
	"time"
)

// probeAgent is a fake agent (server side) that publishes a full selector
// catalog during the handshake and then signals selectors_ready, mirroring what
// a real agent does on session/new. The probe should snapshot exactly this
// catalog.
func probeAgent(agent *Peer, modes, config, commands, prompts bool) {
	agent.HandleRequest("initialize", func(json.RawMessage) (any, error) {
		return map[string]any{"protocolVersion": ACPProtocolVersion}, nil
	})
	agent.HandleRequest("session/new", func(json.RawMessage) (any, error) {
		sid := "probe-sess"
		if modes {
			_ = agent.Notify("session/update", map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "session_modes",
					"modes": map[string]any{
						"currentModeId": "default",
						"availableModes": []any{
							map[string]any{"id": "default", "name": "Default"},
							map[string]any{"id": "plan", "name": "Plan", "description": "planning mode"},
						},
					},
				},
			})
		}
		if config {
			_ = agent.Notify("session/update", map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "session_config_options",
					"configOptions": []any{
						map[string]any{
							"id":   "model",
							"name": "Model",
							"kind": map[string]any{
								"type":         "select",
								"currentValue": "anthropic/sonnet",
								"options": []any{
									map[string]any{"value": "anthropic/sonnet", "name": "Sonnet"},
									map[string]any{"value": "anthropic/opus", "name": "Opus", "description": "strong"},
								},
								"groups": []any{
									map[string]any{"group": "anthropic", "name": "Anthropic", "options": []any{}},
								},
							},
						},
						map[string]any{
							"id":   "auto_approve",
							"name": "Auto-approve",
							"kind": map[string]any{"type": "boolean", "currentValue": true},
						},
					},
				},
			})
		}
		if commands {
			_ = agent.Notify("session/update", map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "available_commands",
					"commands": []any{
						map[string]any{"name": "/review", "description": "review the diff", "inputHint": "optional path"},
					},
				},
			})
		}
		if prompts {
			_ = agent.Notify("session/update", map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate":      "prompt_capabilities",
					"promptCapabilities": map[string]any{"image": true, "audio": false, "embeddedContext": true},
				},
			})
		}
		// Signal the catalog is complete.
		_ = agent.Notify("session/update", map[string]any{
			"sessionId": sid,
			"update":    map[string]any{"sessionUpdate": "selectors_ready"},
		})
		return map[string]any{"sessionId": sid}, nil
	})
}

// TestProbeAgentOptionsWireShape pins the exact JSON shape acp_describe_agent_options
// returns: camelCase fields, the select/boolean tagged kind union, modes /
// promptCapabilities omitted when absent, and configOptions / availableCommands
// always arrays. A green "no error" test would not catch a regressed wire shape.
func TestProbeAgentOptionsWireShape(t *testing.T) {
	mgr, agent, done := probeManager(t)
	defer done()
	probeAgent(agent, true, true, true, true)

	snap, err := mgr.ProbeAgentOptions("claude_code", t.TempDir())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	// modes present (Option -> object, not null)
	modes, ok := got["modes"].(map[string]any)
	if !ok {
		t.Fatalf("modes missing/non-object: %v", got["modes"])
	}
	if modes["currentModeId"] != "default" {
		t.Fatalf("currentModeId = %v", modes["currentModeId"])
	}
	avail, _ := modes["availableModes"].([]any)
	if len(avail) != 2 {
		t.Fatalf("availableModes len = %d", len(avail))
	}

	// configOptions always an array; the tagged kind union renders inline.
	co, ok := got["configOptions"].([]any)
	if !ok {
		t.Fatalf("configOptions not array: %v", got["configOptions"])
	}
	if len(co) != 2 {
		t.Fatalf("configOptions len = %d", len(co))
	}
	model := co[0].(map[string]any)
	kind := model["kind"].(map[string]any)
	if kind["type"] != "select" {
		t.Fatalf("model kind.type = %v (want select)", kind["type"])
	}
	if kind["currentValue"] != "anthropic/sonnet" {
		t.Fatalf("model currentValue = %v", kind["currentValue"])
	}
	// options deserialized back to the Go struct: confirm round-trip via the
	// typed snapshot, not just the raw map.
	if snap.ConfigOptions[0].Kind.Type != "select" ||
		snap.ConfigOptions[0].Kind.Select.CurrentValue != "anthropic/sonnet" {
		t.Fatalf("select kind not round-tripped: %+v", snap.ConfigOptions[0].Kind)
	}
	boolOpt := co[1].(map[string]any)
	boolKind := boolOpt["kind"].(map[string]any)
	if boolKind["type"] != "boolean" || boolKind["currentValue"] != true {
		t.Fatalf("boolean kind = %v", boolKind)
	}
	if snap.ConfigOptions[1].Kind.Type != "boolean" || !snap.ConfigOptions[1].Kind.Boolean.CurrentValue {
		t.Fatalf("boolean kind not round-tripped: %+v", snap.ConfigOptions[1].Kind)
	}

	// availableCommands always array.
	cmds, ok := got["availableCommands"].([]any)
	if !ok || len(cmds) != 1 {
		t.Fatalf("availableCommands = %v", got["availableCommands"])
	}

	// promptCapabilities present.
	pc, ok := got["promptCapabilities"].(map[string]any)
	if !ok {
		t.Fatalf("promptCapabilities missing: %v", got["promptCapabilities"])
	}
	if pc["image"] != true || pc["embeddedContext"] != true || pc["audio"] != false {
		t.Fatalf("promptCapabilities = %v", pc)
	}
}

// TestProbeAgentOptionsEmptyWhenNoOptions pins that an agent which only emits
// selectors_ready (no modes, no config, no commands) yields a clean snapshot:
// modes / promptCapabilities omitted, configOptions / availableCommands empty
// arrays — NOT null / missing.
func TestProbeAgentOptionsEmptyWhenNoOptions(t *testing.T) {
	mgr, agent, done := probeManager(t)
	defer done()
	probeAgent(agent, false, false, false, false)

	snap, err := mgr.ProbeAgentOptions("codex", t.TempDir())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if snap.Modes != nil {
		t.Fatalf("modes should be nil, got %v", snap.Modes)
	}
	if snap.PromptCapabilities != nil {
		t.Fatalf("promptCapabilities should be nil, got %v", snap.PromptCapabilities)
	}
	if snap.ConfigOptions == nil {
		t.Fatal("configOptions must be non-nil empty slice")
	}
	if len(snap.ConfigOptions) != 0 {
		t.Fatalf("configOptions len = %d", len(snap.ConfigOptions))
	}
	if snap.AvailableCommands == nil || len(snap.AvailableCommands) != 0 {
		t.Fatalf("availableCommands must be non-nil empty slice")
	}
	raw, _ := json.Marshal(snap)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if _, present := got["modes"]; present {
		t.Fatal("modes must be omitted when absent")
	}
	if co, ok := got["configOptions"].([]any); !ok || co != nil && len(co) != 0 {
		t.Fatalf("configOptions wire must be empty array, got %v", got["configOptions"])
	}
}

// TestProbeAgentOptionsTimesOut pins the never-ready → ProbeTimedOut contract:
// an agent that emits nothing must fail the probe (not return an empty
// snapshot), so the UI can tell the user to retry.
func TestProbeAgentOptionsTimesOut(t *testing.T) {
	mgr, agent, done := probeManager(t)
	defer done()
	agent.HandleRequest("initialize", func(json.RawMessage) (any, error) {
		return map[string]any{"protocolVersion": ACPProtocolVersion}, nil
	})
	agent.HandleRequest("session/new", func(json.RawMessage) (any, error) {
		// Never emit selectors_ready.
		return map[string]any{"sessionId": "s"}, nil
	})

	// Speed up the test: shrink probeTimeout so we don't wait 60s.
	orig := probeTimeout
	probeTimeout = 400 * time.Millisecond
	defer func() { probeTimeout = orig }()

	_, err := mgr.ProbeAgentOptions("claude_code", t.TempDir())
	var to *ProbeTimedOutError
	if !asProbeTimeout(err, &to) {
		t.Fatalf("want ProbeTimedOut, got %v", err)
	}
}

// TestProbeAgentOptionsCapturesLastError pins the agent's own handshake error
// is surfaced (via ProbeAgentError) instead of the generic ProbeTimedOut.
func TestProbeAgentOptionsCapturesLastError(t *testing.T) {
	mgr, agent, done := probeManager(t)
	defer done()
	agent.HandleRequest("initialize", func(json.RawMessage) (any, error) {
		return map[string]any{"protocolVersion": ACPProtocolVersion}, nil
	})
	agent.HandleRequest("session/new", func(json.RawMessage) (any, error) {
		// Agent explains why it failed, then never readies.
		_ = agent.Notify("session/update", map[string]any{
			"sessionId": "s",
			"update": map[string]any{
				"sessionUpdate": "error",
				"message":       "invalid API key",
				"code":          "auth_failed",
			},
		})
		return map[string]any{"sessionId": "s"}, nil
	})

	orig := probeTimeout
	probeTimeout = 400 * time.Millisecond
	defer func() { probeTimeout = orig }()

	_, err := mgr.ProbeAgentOptions("claude_code", t.TempDir())
	var pa *ProbeAgentError
	if !asProbeAgentError(err, &pa) || pa.Message != "invalid API key" {
		t.Fatalf("want ProbeAgentError 'invalid API key', got %v", err)
	}
}

// ─── test harness ─────────────────────────────────────────────────────────────

// probeManager builds a Manager wired to a fake agent via the spawner/resolver
// injection, returning the manager plus the agent peer (client is captured by
// the injected spawner).
func probeManager(t *testing.T) (*Manager, *Peer, func()) {
	t.Helper()
	client, agent, done := pipePair(t)
	mgr := NewManager(nil, nil)
	mgr.SetSpawner(func(SpawnConfig) (*AgentProcess, error) {
		return &AgentProcess{Peer: client, CloseFn: func() {}}, nil
	})
	mgr.SetResolver(func(agentType, cwd string, env map[string]string) (SpawnConfig, bool) {
		return SpawnConfig{Command: "fake", Cwd: cwd}, true
	})
	return mgr, agent, done
}

func asProbeTimeout(err error, target **ProbeTimedOutError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*ProbeTimedOutError); ok {
		*target = e
		return true
	}
	return false
}

func asProbeAgentError(err error, target **ProbeAgentError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*ProbeAgentError); ok {
		*target = e
		return true
	}
	return false
}
