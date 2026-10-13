package acp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

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
	{"open_code", "OpenCode", "The OpenCode agent", "opencode", "binary", false, true, false},
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
	{"qoder", "Qoder", "Qoder CLI agent", "qoder-cli", "npx", false, true, true},
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
	"open_code":   {"binary", "", "opencode", []string{"acp"}},
	"hermes":      {"npx", "hermes-agent@0.21.5", "hermes", []string{"acp"}},
	"code_buddy":  {"npx", "@tencent-ai/codebuddy-code@2.161.4", "codebuddy", []string{"--acp"}},
	"kimi_code":   {"npx", "@moonshot-ai/kimi-code@2.1.1", "kimi", []string{"acp"}},
	"pi":          {"uvx", "pi-acp@0.0.34", "pi-acp", nil},
	"grok":        {"npx", "@xai-official/grok@1.0.46", "grok", []string{"agent", "stdio"}},
	"cursor":      {"binary", "", "cursor-agent", []string{"acp"}},
	"deepseek":    {"npx", "deepseek-acp@0.9.0", "deepseek-acp", nil},
	"qoder":       {"npx", "@qoder-ai/qodercli@1.1.65", "qoder", []string{"--acp"}},
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
	default: // binary — resolve the cached download
		if binaryCacheRoot == "" {
			return SpawnConfig{}, false
		}
		bin := CachedBinaryPath(binaryCacheRoot, agentType, "")
		if bin == "" {
			return SpawnConfig{}, false
		}
		if _, err := os.Stat(bin); err != nil {
			return SpawnConfig{}, false
		}
		return SpawnConfig{Command: bin, Args: spec.Args, Env: env, Cwd: cwd}, true
	}
}

// ─── binary distributions ────────────────────────────────────────────────────

// platformBinary is one platform's archive for a binary agent.
type platformBinary struct {
	url string
	// entry is the launch path inside the extracted tree (relative, forward
	// slashes). Empty means the archive is a single file named cmd.
	entry string
}

// binaryDist is the download recipe for a binary agent (acp::registry
// AgentDistribution::Binary). Only the current platform's entry is ever used.
type binaryDist struct {
	version string
	cmd     string
	args    []string
	// dirTree: keep the whole archive tree (a bundled runtime / sibling
	// executables); otherwise only the single cmd file is copied out.
	dirTree bool
	// siblings are extra executables inside a dir-tree archive that must be
	// present and executable (acp::registry BinaryDirEntry::required_siblings),
	// by platform family.
	siblingsUnix    []string
	siblingsWindows []string
	platforms       map[string]platformBinary
}

// binaryDists mirrors the upstream registry's binary distributions for the
// built-in agents (URLs are repository constants, sha256 None for built-ins).
var binaryDists = map[string]binaryDist{
	"cursor": {
		version: "2026.10.01-14929f9",
		cmd:     "cursor-agent",
		args:    []string{"acp"},
		dirTree: true,
		platforms: map[string]platformBinary{
			"darwin-aarch64":  {"https://downloads.cursor.com/lab/2026.10.01-14929f9/darwin/arm64/agent-cli-package.tar.gz", "dist-package/cursor-agent"},
			"darwin-x86_64":   {"https://downloads.cursor.com/lab/2026.10.01-14929f9/darwin/x64/agent-cli-package.tar.gz", "dist-package/cursor-agent"},
			"linux-aarch64":   {"https://downloads.cursor.com/lab/2026.10.01-14929f9/linux/arm64/agent-cli-package.tar.gz", "dist-package/cursor-agent"},
			"linux-x86_64":    {"https://downloads.cursor.com/lab/2026.10.01-14929f9/linux/x64/agent-cli-package.tar.gz", "dist-package/cursor-agent"},
			"windows-aarch64": {"https://downloads.cursor.com/lab/2026.10.01-14929f9/windows/arm64/agent-cli-package.zip", "dist-package/cursor-agent.cmd"},
			"windows-x86_64":  {"https://downloads.cursor.com/lab/2026.10.01-14929f9/windows/x64/agent-cli-package.zip", "dist-package/cursor-agent.cmd"},
		},
	},
	"open_code": {
		version: "1.18.35",
		cmd:     "opencode",
		args:    []string{"acp"},
		dirTree: false,
		platforms: map[string]platformBinary{
			"darwin-aarch64":  {"https://github.com/anomalyco/opencode/releases/download/v1.18.35/opencode-darwin-arm64.zip", ""},
			"darwin-x86_64":   {"https://github.com/anomalyco/opencode/releases/download/v1.18.35/opencode-darwin-x64.zip", ""},
			"linux-aarch64":   {"https://github.com/anomalyco/opencode/releases/download/v1.18.35/opencode-linux-arm64.tar.gz", ""},
			"linux-x86_64":    {"https://github.com/anomalyco/opencode/releases/download/v1.18.35/opencode-linux-x64.tar.gz", ""},
			"windows-aarch64": {"https://github.com/anomalyco/opencode/releases/download/v1.18.35/opencode-windows-arm64.zip", ""},
			"windows-x86_64":  {"https://github.com/anomalyco/opencode/releases/download/v1.18.35/opencode-windows-x64.zip", ""},
		},
	},
	"antigravity": {
		version:         "1.3.0",
		cmd:             "agy_acp_server",
		dirTree:         true,
		siblingsUnix:    []string{"localharness_external"},
		siblingsWindows: []string{"localharness_external.exe"},
		platforms: map[string]platformBinary{
			"darwin-aarch64":  {"https://dl.google.com/agy-extensions/releases/macos/agy-acp-server-1.3.0-darwin-arm64.zip", "agy_acp_server.par"},
			"darwin-x86_64":   {"https://dl.google.com/agy-extensions/releases/macos/agy-acp-server-1.3.0-darwin-x86_64.zip", "agy_acp_server.par"},
			"linux-aarch64":   {"https://dl.google.com/agy-extensions/releases/linux/agy-acp-server-1.3.0-linux-arm64.zip", "agy_acp_server.par"},
			"linux-x86_64":    {"https://dl.google.com/agy-extensions/releases/linux/agy-acp-server-1.3.0-linux-x86_64.zip", "agy_acp_server.par"},
			"windows-aarch64": {"https://dl.google.com/agy-extensions/releases/windows/agy-acp-server-1.3.0-windows-arm64.zip", "agy_acp_server.exe"},
			"windows-x86_64":  {"https://dl.google.com/agy-extensions/releases/windows/agy-acp-server-1.3.0-windows-x86_64.zip", "agy_acp_server.exe"},
		},
	},
}

// AgentBinary is the resolved download recipe for a binary agent on the current
// platform.
type AgentBinary struct {
	Version string
	Cmd     string
	Args    []string
	URL     string
	// Entry is the launch path inside the extracted cache dir (relative,
	// forward slashes); "" when the archive is a single file named Cmd.
	Entry   string
	DirTree bool
	// Siblings are extra executables that must be present in a dir-tree archive
	// (acp::registry required_siblings); empty for single-file agents.
	Siblings []string
}

// CurrentPlatform maps the running OS/arch to the registry's platform key
// (acp::registry::current_platform).
func CurrentPlatform() string {
	arch := "x86_64"
	switch runtime.GOARCH {
	case "arm64":
		arch = "aarch64"
	case "amd64":
		arch = "x86_64"
	default:
		arch = runtime.GOARCH
	}
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		return runtime.GOOS + "-" + arch
	}
	return runtime.GOOS + "-" + arch
}

// BinaryDistFor resolves a binary agent's download recipe for the current
// platform.
func BinaryDistFor(agentType string) (AgentBinary, bool) {
	d, ok := binaryDists[agentType]
	if !ok {
		return AgentBinary{}, false
	}
	p, ok := d.platforms[CurrentPlatform()]
	if !ok {
		return AgentBinary{}, false
	}
	// Antigravity passes `--uid=` on Linux only (an absl InitGoogle flag; the
	// Windows build need not link it and an unknown flag is a hard error).
	args := d.args
	if agentType == "antigravity" && strings.HasPrefix(CurrentPlatform(), "linux-") {
		args = []string{"--uid="}
	}
	siblings := d.siblingsUnix
	if runtime.GOOS == "windows" {
		siblings = d.siblingsWindows
	}
	return AgentBinary{Version: d.version, Cmd: d.cmd, Args: args, URL: p.url, Entry: p.entry, DirTree: d.dirTree, Siblings: siblings}, true
}

// CachedBinaryPath is where a binary agent's launch entry lives in the cache
// (cacheRoot/agent-binaries/<agent>/<version>/<entry>); "" if the agent has no
// binary recipe on this platform.
func CachedBinaryPath(cacheRoot, agentType, version string) string {
	d, ok := binaryDists[agentType]
	if !ok {
		return ""
	}
	p, ok := d.platforms[CurrentPlatform()]
	if !ok {
		return ""
	}
	if strings.TrimSpace(version) == "" {
		version = d.version
	}
	entry := p.entry
	if entry == "" {
		// Single-file archive: the file is named after cmd (+ .exe on Windows).
		entry = d.cmd
		if runtime.GOOS == "windows" {
			entry += ".exe"
		}
	}
	return filepath.Join(cacheRoot, "agent-binaries", agentType, version, filepath.FromSlash(entry))
}
