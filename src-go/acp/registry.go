package acp

// Built-in agent registry — the compile-time agent catalog, mirroring upstream
// acp::registry (agent wire type, display name, registry id, distribution).
// Availability/installed-version are resolved later by preflight; until the
// spawn+preflight layer lands they report available=false / version=null.

// BuiltinAgent is one row of the compile-time catalog.
type BuiltinAgent struct {
	// AgentType is the on-wire agent id (models::agent::AgentType to_wire).
	AgentType   string
	Name        string
	Description string
	// RegistryID is the ACP registry id (acp::registry::registry_id_for).
	RegistryID string
	// Distribution: "npx" | "binary" | "uvx".
	Distribution string
	// IsACPAdapter: a third-party ACP adapter wrapping a differently-named CLI.
	IsACPAdapter bool
	// SkillsCapable: has a yoyocode-known skill store (every built-in).
	SkillsCapable bool
	// SupportsCustomVersion: an explicit "install version X" can fetch it
	// (true for npx; false for uvx; binary depends on platform URL).
	SupportsCustomVersion bool
}

// BuiltinAgents is the fifteen compile-time agents, in upstream declaration
// order (models::agent::BUILTIN_AGENT_TYPES).
var BuiltinAgents = []BuiltinAgent{
	{"claude_code", "Claude Code", "ACP wrapper for Anthropic's Claude", "claude-acp", "npx", true, true, true},
	{"codex", "Codex", "ACP wrapper for OpenAI's Codex CLI", "codex-acp", "npx", true, true, true},
	{"open_code", "OpenCode", "The OpenCode agent", "opencode", "npx", false, true, true},
	{"gemini", "Gemini", "Google's Gemini CLI agent", "gemini", "npx", false, true, true},
	{"open_claw", "OpenClaw", "ACP wrapper for OpenClaw", "openclaw-acp", "npx", false, true, true},
	{"cline", "Cline", "The Cline coding agent", "cline", "npx", false, true, true},
	{"hermes", "Hermes", "The Hermes agent (npm bridge)", "hermes", "npx", false, true, true},
	{"code_buddy", "CodeBuddy", "Tencent CodeBuddy Code", "codebuddy-code", "npx", false, true, true},
	{"kimi_code", "Kimi Code", "Moonshot Kimi Code agent", "kimi-code", "npx", false, true, true},
	{"pi", "Pi", "The Pi ACP agent", "pi-acp", "uvx", false, true, false},
	{"grok", "Grok", "xAI Grok Build agent", "grok-build", "npx", false, true, true},
	{"cursor", "Cursor", "Cursor CLI agent", "cursor", "binary", false, true, false},
	{"deepseek", "DeepSeek", "ACP wrapper for DeepSeek", "deepseek-acp", "npx", false, true, true},
	{"qoder", "Qoder", "Qoder CLI agent", "qoder-cli", "binary", false, true, false},
	{"antigravity", "Antigravity", "ACP wrapper for Antigravity", "antigravity-acp", "binary", false, true, false},
}

// AgentWireForRegistryID maps an ACP registry id back to its agent wire type.
func AgentWireForRegistryID(id string) (string, bool) {
	for _, a := range BuiltinAgents {
		if a.RegistryID == id {
			return a.AgentType, true
		}
	}
	return "", false
}

// FindBuiltinAgent returns the catalog row for an agent wire type.
func FindBuiltinAgent(agentType string) (BuiltinAgent, bool) {
	for _, a := range BuiltinAgents {
		if a.AgentType == agentType {
			return a, true
		}
	}
	return BuiltinAgent{}, false
}

// AgentInfoWire renders one BuiltinAgent as the AcpAgentInfo wire shape
// (acp::types::AcpAgentInfo). available/registry_version come from preflight,
// which has not run yet, so they are false/null here.
func (a BuiltinAgent) AgentInfoWire() map[string]any {
	return map[string]any{
		"agent_type":              a.AgentType,
		"skills_capable":          a.SkillsCapable,
		"registry_id":             a.RegistryID,
		"registry_version":        nil,
		"supports_custom_version": a.SupportsCustomVersion,
		"name":                    a.Name,
		"description":             a.Description,
		"available":               false,
		"distribution_type":       a.Distribution,
		"is_acp_adapter":          a.IsACPAdapter,
		"custom_source":           nil,
	}
}

// BuiltinAgentInfoList renders the whole catalog as AcpAgentInfo wire rows.
func BuiltinAgentInfoList() []map[string]any {
	out := make([]map[string]any, 0, len(BuiltinAgents))
	for _, a := range BuiltinAgents {
		out = append(out, a.AgentInfoWire())
	}
	return out
}

// ─── launch specs ────────────────────────────────────────────────────────────
//
// How each built-in agent is actually started (acp::registry distribution).
// npx agents run via the Node package runner; uvx via the Python tool runner;
// binary agents need a downloaded binary (resolved by the binary-cache layer,
// not here) so they report dependency_missing until that lands.

// LaunchSpec is the resolved-command recipe for one agent.
type LaunchSpec struct {
	Kind    string // "npx" | "uvx" | "binary"
	Package string // npm/pypi package@version (npx/uvx)
	Cmd     string // bin name the package exposes
	Args    []string
}

var launchSpecs = map[string]LaunchSpec{
	"claude_code": {"npx", "@agentclientprotocol/claude-agent-acp@0.86.0", "claude-agent-acp", nil},
	"codex":       {"npx", "@agentclientprotocol/codex-acp@2.1.1", "codex-acp", nil},
	"gemini":      {"npx", "@google/gemini-cli@0.60.0", "gemini", []string{"--acp", "--skip-trust"}},
	"open_claw":   {"npx", "openclaw@2026.9.8", "openclaw", []string{"acp"}},
	"cline":       {"npx", "cline@3.0.68", "cline", []string{"--acp"}},
	"open_code":   {"npx", "opencode@1.18.35", "opencode", []string{"acp"}},
	"hermes":      {"npx", "hermes-agent@0.21.5", "hermes", []string{"acp"}},
	"code_buddy":  {"npx", "@tencent-ai/codebuddy-code@2.161.4", "codebuddy", []string{"--acp"}},
	"kimi_code":   {"npx", "@moonshot-ai/kimi-code@2.1.1", "kimi", []string{"acp"}},
	"pi":          {"uvx", "pi-acp@0.0.34", "pi-acp", nil},
	"grok":        {"npx", "@xai-official/grok@1.0.46", "grok", []string{"agent", "stdio"}},
	"cursor":      {"binary", "", "cursor-agent", []string{"acp"}},
	"deepseek":    {"npx", "deepseek-acp@0.9.0", "deepseek-acp", nil},
	"qoder":       {"binary", "", "qoder", []string{"--acp"}},
	"antigravity": {"binary", "", "agy_acp_server", nil},
}

// LaunchSpecFor returns the launch recipe for an agent wire type.
func LaunchSpecFor(agentType string) (LaunchSpec, bool) {
	spec, ok := launchSpecs[agentType]
	return spec, ok
}

// ResolveSpawn turns an agent wire type into a concrete SpawnConfig.
//   - npx:    npx -y -p <package> <cmd> <args...>
//   - uvx:    uvx --from <package> <cmd> <args...>
//   - binary: not resolvable here (needs the binary cache) → ok=false
func ResolveSpawn(agentType, cwd string, env map[string]string) (SpawnConfig, bool) {
	spec, ok := launchSpecs[agentType]
	if !ok {
		return SpawnConfig{}, false
	}
	switch spec.Kind {
	case "npx":
		args := []string{"-y", "-p", spec.Package, spec.Cmd}
		args = append(args, spec.Args...)
		return SpawnConfig{Command: "npx", Args: args, Env: env, Cwd: cwd}, true
	case "uvx":
		args := []string{"--from", spec.Package, spec.Cmd}
		args = append(args, spec.Args...)
		return SpawnConfig{Command: "uvx", Args: args, Env: env, Cwd: cwd}, true
	default: // binary — needs the download/cache layer
		return SpawnConfig{}, false
	}
}
