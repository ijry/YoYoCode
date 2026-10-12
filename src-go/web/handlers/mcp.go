package handlers

// MCP command handlers (B11), mirroring commands/mcp.rs + web/handlers/mcp.rs.
//
// This increment covers the read/scan path and the marketplace provider list:
//   - mcp_scan_local       — merge every agent's on-disk MCP config into one list
//   - mcp_list_marketplaces — the two provider descriptors
//
// The write path (mcp_upsert_local_server / mcp_set_server_apps /
// mcp_remove_server), the marketplace search/detail/install trio, and the five
// agents whose config lives behind another subsystem's path resolver or a
// non-JSON format (Codex/Grok TOML, Hermes YAML, Pi/Kimi home resolvers) are
// staged for the next MCP increment; their readers return empty here, so an
// as-yet-unported agent contributes nothing to the scan — the same outcome as
// an agent the user has not configured — rather than failing it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	apperror "yoyocode/src-go/app_error"
	"yoyocode/src-go/web"
)

const (
	marketplaceOfficial = "official_registry"
	marketplaceSmithery = "smithery"
)

func mcpInvalidInput(msg string) error  { return web.ErrInvalidInput(msg) }
func mcpConfigInvalid(msg string) error { return web.ErrConfigInvalid(msg) }

// RegisterMCP wires the implemented MCP commands.
func RegisterMCP(reg *Registry) {
	reg.Register("mcp_scan_local", func(c *web.Context) (any, error) {
		return mcpScanLocal(), nil
	})
	reg.Register("mcp_list_marketplaces", func(c *web.Context) (any, error) {
		return mcpListMarketplaces(), nil
	})
	reg.Register("mcp_upsert_local_server", mcpUpsertLocalServer)
	reg.Register("mcp_set_server_apps", mcpSetServerApps)
	reg.Register("mcp_remove_server", mcpRemoveServer)
	reg.Register("mcp_search_marketplace", mcpSearchMarketplace)
	reg.Register("mcp_get_marketplace_server_detail", mcpGetMarketplaceServerDetail)
	reg.Register("mcp_install_from_marketplace", mcpInstallFromMarketplace)
}

func mcpListMarketplaces() any {
	return []map[string]any{
		{
			"id":          marketplaceOfficial,
			"name":        "Official MCP Registry",
			"description": "registry.modelcontextprotocol.io official MCP server registry",
		},
		{
			"id":          marketplaceSmithery,
			"name":        "Smithery",
			"description": "smithery.ai MCP server marketplace",
		},
	}
}

// ─── home + per-agent config paths ──────────────────────────────────────────

// mcpHomeOverride lets tests point the home-relative config paths at a temp dir.
var mcpHomeOverride string

func mcpHome() string {
	if mcpHomeOverride != "" {
		return mcpHomeOverride
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

func expandHomePrefix(value, home string) string {
	v := strings.TrimSpace(value)
	if v == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(v, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return v
}

func claudeConfigPath() string    { return filepath.Join(mcpHome(), ".claude.json") }
func codebuddyConfigPath() string { return filepath.Join(mcpHome(), ".codebuddy.json") }
func geminiConfigPath() string    { return filepath.Join(mcpHome(), ".gemini", "settings.json") }
func openclawConfigPath() string  { return filepath.Join(mcpHome(), ".openclaw", "openclaw.json") }
func clineConfigPath() string {
	return filepath.Join(mcpHome(), ".cline", "data", "settings", "cline_mcp_settings.json")
}
func opencodeConfigPath() string {
	return filepath.Join(mcpHome(), ".config", "opencode", "opencode.json")
}
func cursorConfigPath() string { return filepath.Join(mcpHome(), ".cursor", "mcp.json") }

// deepseek: DSH_HOME (with ~ expansion) else ~/.dsh, then mcp.json.
func deepseekConfigPath() string {
	home := mcpHome()
	root := filepath.Join(home, ".dsh")
	if v := strings.TrimSpace(os.Getenv("DSH_HOME")); v != "" {
		root = expandHomePrefix(v, home)
	}
	return filepath.Join(root, "mcp.json")
}

// qoder: QODER_CONFIG_DIR else (QODER_CLI_HOME|GEMINI_CLI_HOME|home)/(QODER_CONFIG_DIR_NAME|.qoder).
func qoderConfigPath() string {
	home := mcpHome()
	if v := strings.TrimSpace(os.Getenv("QODER_CONFIG_DIR")); v != "" {
		return filepath.Join(v, "settings.json")
	}
	root := home
	if v := strings.TrimSpace(os.Getenv("QODER_CLI_HOME")); v != "" {
		root = v
	} else if v := strings.TrimSpace(os.Getenv("GEMINI_CLI_HOME")); v != "" {
		root = v
	}
	name := ".qoder"
	if v := strings.TrimSpace(os.Getenv("QODER_CONFIG_DIR_NAME")); v != "" {
		name = v
	}
	return filepath.Join(root, name, "settings.json")
}

// antigravity: GEMINI_HOME (with ~ expansion) else ~/.gemini, then config/mcp_config.json.
func antigravityConfigPath() string {
	home := mcpHome()
	root := filepath.Join(home, ".gemini")
	if v := strings.TrimSpace(os.Getenv("GEMINI_HOME")); v != "" {
		root = expandHomePrefix(v, home)
	}
	return filepath.Join(root, "config", "mcp_config.json")
}

// ─── JSON config reading ────────────────────────────────────────────────────

// readJSONValue reads a JSON config, mapping absence/emptiness to an empty
// object (several agents touch the file into existence before writing a server
// into it) and a malformed body to a configuration_invalid error. A permission
// / I/O failure is surfaced with the path in the detail so the scan warning is
// actionable.
func readJSONValue(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, web.ErrIO(path + ": " + err.Error())
	}
	if strings.TrimSpace(string(raw)) == "" {
		return map[string]any{}, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, web.ErrConfigInvalid("invalid JSON at " + path + ": " + err.Error())
	}
	return v, nil
}

func asObject(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// readMcpServersFromObject reads an {id: spec} servers object, canonicalizing
// each entry (skipping — not failing on — individual invalid entries, as
// upstream does with a warning log).
func readMcpServersFromObject(servers map[string]any, source string, pre func(map[string]any) map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for id, specv := range servers {
		spec, ok := specv.(map[string]any)
		if !ok {
			continue
		}
		if pre != nil {
			spec = pre(spec)
		}
		norm, err := canonicalizeSpec(spec, source)
		if err != nil {
			continue
		}
		out[id] = norm
	}
	return out
}

// readSimpleMcpServers reads a config whose servers live under root.mcpServers.
func readSimpleMcpServers(path, source string, pre func(map[string]any) map[string]any) (map[string]map[string]any, error) {
	root, err := readJSONValue(path)
	if err != nil {
		return nil, err
	}
	obj, ok := asObject(root)
	if !ok {
		return map[string]map[string]any{}, nil
	}
	servers, ok := asObject(obj["mcpServers"])
	if !ok {
		return map[string]map[string]any{}, nil
	}
	return readMcpServersFromObject(servers, source, pre), nil
}

// ─── per-agent readers ──────────────────────────────────────────────────────

func readClaudeServers() (map[string]map[string]any, error) {
	return readSimpleMcpServers(claudeConfigPath(), "Claude config", nil)
}
func readCodebuddyServers() (map[string]map[string]any, error) {
	return readSimpleMcpServers(codebuddyConfigPath(), "CodeBuddy config", nil)
}
func readGeminiServers() (map[string]map[string]any, error) {
	return readSimpleMcpServers(geminiConfigPath(), "Gemini config", nil)
}
func readClineServers() (map[string]map[string]any, error) {
	return readSimpleMcpServers(clineConfigPath(), "Cline config", nil)
}
func readDeepseekServers() (map[string]map[string]any, error) {
	return readSimpleMcpServers(deepseekConfigPath(), "DeepSeek config", nil)
}
func readQoderServers() (map[string]map[string]any, error) {
	return readSimpleMcpServers(qoderConfigPath(), "Qoder config", nil)
}

// Cursor discriminates on shape alone; strip any foreign type key so
// canonicalize re-infers it (command => stdio, url => http).
func readCursorServers() (map[string]map[string]any, error) {
	return readSimpleMcpServers(cursorConfigPath(), "Cursor config", func(spec map[string]any) map[string]any {
		clone := map[string]any{}
		for k, v := range spec {
			if k == "type" {
				continue
			}
			clone[k] = v
		}
		return clone
	})
}

// OpenClaw stores servers under mcp.servers.
func readOpenclawServers() (map[string]map[string]any, error) {
	root, err := readJSONValue(openclawConfigPath())
	if err != nil {
		return nil, err
	}
	obj, ok := asObject(root)
	if !ok {
		return map[string]map[string]any{}, nil
	}
	mcp, ok := asObject(obj["mcp"])
	if !ok {
		return map[string]map[string]any{}, nil
	}
	servers, ok := asObject(mcp["servers"])
	if !ok {
		return map[string]map[string]any{}, nil
	}
	return readMcpServersFromObject(servers, "OpenClaw config", nil), nil
}

// Antigravity reads root.mcpServers, falling back to a bare top-level map (the
// loader's data.get("mcpServers", data) with the key absent).
func readAntigravityServers() (map[string]map[string]any, error) {
	root, err := readJSONValue(antigravityConfigPath())
	if err != nil {
		return nil, err
	}
	obj, ok := asObject(root)
	if !ok {
		return map[string]map[string]any{}, nil
	}
	var servers map[string]any
	switch v := obj["mcpServers"].(type) {
	case map[string]any:
		servers = v
	case nil:
		servers = obj
	default:
		return map[string]map[string]any{}, nil
	}
	return readMcpServersFromObject(servers, "Antigravity config", nil), nil
}

// OpenCode: canonical servers under mcpServers, plus OpenCode-format entries
// under mcp (not overriding an id already seen in mcpServers).
func readOpencodeServers() (map[string]map[string]any, error) {
	root, err := readJSONValue(opencodeConfigPath())
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	obj, ok := asObject(root)
	if !ok {
		return out, nil
	}
	if servers, ok := asObject(obj["mcpServers"]); ok {
		for id, spec := range readMcpServersFromObject(servers, "OpenCode mcpServers", nil) {
			out[id] = spec
		}
	}
	if servers, ok := asObject(obj["mcp"]); ok {
		for id, specv := range servers {
			if _, seen := out[id]; seen {
				continue
			}
			spec, ok := specv.(map[string]any)
			if !ok {
				continue
			}
			norm, err := canonicalizeOpencodeSpec(spec, "OpenCode mcp")
			if err != nil {
				continue
			}
			out[id] = norm
		}
	}
	return out, nil
}

// Staged for the next MCP increment (non-JSON format or external path resolver).

// ─── scan aggregation ───────────────────────────────────────────────────────

type localMcpReader struct {
	source string
	app    McpAppType
	read   func() (map[string]map[string]any, error)
}

// localMcpReaders lists the agents in the exact order upstream walks them, so
// the first agent to declare an id wins its spec (first-writer-wins merge).
func localMcpReaders() []localMcpReader {
	return []localMcpReader{
		{"Claude Code", "claude_code", readClaudeServers},
		{"Codex", "codex", readCodexServersImpl},
		{"OpenCode", "open_code", readOpencodeServers},
		{"Gemini", "gemini", readGeminiServers},
		{"OpenClaw", "open_claw", readOpenclawServers},
		{"Cline", "cline", readClineServers},
		{"Hermes", "hermes", readHermesServersImpl},
		{"CodeBuddy", "code_buddy", readCodebuddyServers},
		{"Kimi Code", "kimi_code", readKimiCodeServersImpl},
		{"Grok", "grok", readGrokServersImpl},
		{"Cursor", "cursor", readCursorServers},
		{"DeepSeek", "deepseek", readDeepseekServers},
		{"Antigravity", "antigravity", readAntigravityServers},
		{"Qoder", "qoder", readQoderServers},
		{"pi", "pi", readPiServersImpl},
	}
}

// mcpScanLocal merges every agent's MCP config into one list, degrading a source
// that cannot be read into a warning instead of failing the whole scan.
func mcpScanLocal() map[string]any {
	type merged struct {
		spec map[string]any
		apps map[McpAppType]bool
	}
	byID := map[string]*merged{}
	warnings := []map[string]any{}
	// OpenClaw is the one agent that shares the "auth" key with Kimi; keep what
	// its own config declares so Kimi's pass can tell a genuine OpenClaw setting
	// from an echo upstream once wrote into some other agent's file.
	openclawDeclares := map[string]map[string]any{}

	for _, reader := range localMcpReaders() {
		servers, err := reader.read()
		if err != nil {
			msg := err.Error()
			if ae, ok := err.(*apperror.AppCommandError); ok && ae.Detail != "" {
				msg = ae.Message + ": " + ae.Detail
			}
			warnings = append(warnings, map[string]any{"app": reader.app, "message": msg})
			continue
		}
		for id, spec := range servers {
			if reader.app == "open_claw" {
				declared := map[string]any{}
				for _, k := range kimiSharedKeys {
					if v, ok := spec[k]; ok {
						declared[k] = v
					}
				}
				if len(declared) > 0 {
					openclawDeclares[id] = declared
				}
			}
			entry := byID[id]
			if entry == nil {
				entry = &merged{spec: spec, apps: map[McpAppType]bool{}}
				byID[id] = entry
			}
			// Kimi models fields no other agent does; fold them back onto the
			// winning (first) spec so they reach the editor and survive a save.
			if reader.app == "kimi_code" {
				owner := openclawDeclares[id]
				if owner == nil {
					owner = map[string]any{}
				}
				mergeKimiExtensionFields(entry.spec, spec, owner)
			}
			entry.apps[reader.app] = true
		}
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	servers := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		entry := byID[id]
		apps := make([]McpAppType, 0, len(entry.apps))
		for a := range entry.apps {
			apps = append(apps, a)
		}
		servers = append(servers, map[string]any{
			"id":   id,
			"spec": entry.spec,
			"apps": sortApps(apps),
		})
	}
	return map[string]any{"servers": servers, "warnings": warnings}
}
