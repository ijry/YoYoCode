package acp

import "testing"

func TestBuiltinAgentRegistry(t *testing.T) {
	if len(BuiltinAgents) != 15 {
		t.Fatalf("expected 15 built-in agents, got %d", len(BuiltinAgents))
	}
	// First two are the ACP adapters (claude/codex).
	if BuiltinAgents[0].AgentType != "claude_code" || !BuiltinAgents[0].IsACPAdapter {
		t.Fatalf("claude_code adapter row wrong: %+v", BuiltinAgents[0])
	}
	if BuiltinAgents[1].AgentType != "codex" || !BuiltinAgents[1].IsACPAdapter {
		t.Fatalf("codex adapter row wrong: %+v", BuiltinAgents[1])
	}
	// registry id round-trips.
	if w, ok := AgentWireForRegistryID("claude-acp"); !ok || w != "claude_code" {
		t.Fatalf("registry id lookup: %v %v", w, ok)
	}
	// Wire rows carry the AcpAgentInfo keys.
	row := BuiltinAgents[0].AgentInfoWire()
	for _, k := range []string{"agent_type", "name", "description", "registry_id", "available", "distribution_type", "is_acp_adapter", "skills_capable", "registry_version", "supports_custom_version", "custom_source"} {
		if _, ok := row[k]; !ok {
			t.Fatalf("wire row missing %q: %v", k, row)
		}
	}
	if row["available"] != false || row["registry_version"] != nil {
		t.Fatalf("uninstalled agent should be available=false version=nil: %v", row)
	}
}
