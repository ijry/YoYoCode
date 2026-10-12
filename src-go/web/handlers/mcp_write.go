package handlers

// MCP write path (B11, mcp increment 2), mirroring the mutation half of
// commands/mcp.rs: mcp_upsert_local_server / mcp_set_server_apps /
// mcp_remove_server plus the per-agent canonical->native writers.
//
// Covers the ten JSON-config agents whose readers increment 1 already ported
// (claude_code, code_buddy, gemini, open_claw, cline, open_code, cursor,
// deepseek, qoder, antigravity). The five still-staged agents (codex/grok TOML,
// hermes YAML, pi/kimi external path resolvers) refuse an explicit upsert with
// a clear error and treat removal as a no-op, so a multi-agent save that does
// not target them still succeeds.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"yoyocode/src-go/web"
)

// ─── JSON writing ───────────────────────────────────────────────────────────

func writeJSONFile(path string, value any) error {
	if parent := filepath.Dir(path); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return web.ErrIO(err.Error())
		}
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return mcpConfigInvalid("failed to serialize JSON for " + path + ": " + err.Error())
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return web.ErrIO(err.Error())
	}
	return nil
}

// writeDeepseekJSONFile creates fresh files owner-only (0600) under a 0700
// parent, mirroring the harness; the env-map of a stdio entry routinely holds a
// token. On Windows the mode bits are advisory but the API is identical.
func writeDeepseekJSONFile(path string, value any) error {
	if parent := filepath.Dir(path); parent != "" {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return web.ErrIO(err.Error())
		}
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return mcpConfigInvalid("failed to serialize JSON for " + path + ": " + err.Error())
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return web.ErrIO(err.Error())
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readRootObjectOrNew reads a JSON config as an object, replacing a non-object
// root with a fresh empty object (upstream: root = json!({}) when !is_object).
func readRootObjectOrNew(path string) (map[string]any, error) {
	root, err := readJSONValue(path)
	if err != nil {
		return nil, err
	}
	if obj, ok := root.(map[string]any); ok {
		return obj, nil
	}
	return map[string]any{}, nil
}

// ensureObj returns obj[key] as a map, creating an empty one if missing or not
// an object (upstream only replaces when the existing value is not an object).
func ensureObj(obj map[string]any, key string) map[string]any {
	if m, ok := obj[key].(map[string]any); ok {
		return m
	}
	m := map[string]any{}
	obj[key] = m
	return m
}

// upsertRootMcpServers inserts entry under root.mcpServers at a config path.
func upsertRootMcpServers(path, id string, entry map[string]any, write func(string, any) error) error {
	root, err := readRootObjectOrNew(path)
	if err != nil {
		return err
	}
	ensureObj(root, "mcpServers")[id] = entry
	return write(path, root)
}

// removeRootMcpServers deletes id from root.mcpServers; a missing file / key is
// a no-op success.
func removeRootMcpServers(path, id string, write func(string, any) error) (bool, error) {
	if !fileExists(path) {
		return false, nil
	}
	root, err := readJSONValue(path)
	if err != nil {
		return false, err
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return false, nil
	}
	servers, ok := obj["mcpServers"].(map[string]any)
	if !ok {
		return false, nil
	}
	if _, had := servers[id]; !had {
		return false, nil
	}
	delete(servers, id)
	return true, write(path, obj)
}

// ─── canonical -> native converters ─────────────────────────────────────────

// canonicalToClineEntry remaps http -> streamableHttp (Cline's literal); the
// reader collapses it straight back via normalizeMCPType.
func canonicalToClineEntry(spec map[string]any) (map[string]any, error) {
	c, err := canonicalizeSpec(spec, "Cline write")
	if err != nil {
		return nil, err
	}
	if c["type"] == "http" {
		c["type"] = "streamableHttp"
	}
	return c, nil
}

// canonicalToCursorEntry keeps only the fields Cursor models, shape-discriminated
// (no type key).
func canonicalToCursorEntry(spec map[string]any) (map[string]any, error) {
	c, err := canonicalizeSpec(spec, "Cursor write")
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, k := range []string{"command", "args", "env", "cwd", "url", "headers"} {
		if v, ok := c[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

// canonicalToOpencodeSpec converts canonical -> OpenCode native (type local|remote,
// command-as-array, environment).
func canonicalToOpencodeSpec(spec map[string]any) (map[string]any, error) {
	c, err := canonicalizeSpec(spec, "OpenCode conversion")
	if err != nil {
		return nil, err
	}
	typ, _ := c["type"].(string)
	out := map[string]any{}
	switch typ {
	case "stdio", "":
		cmd, _ := c["command"].(string)
		if cmd == "" {
			return nil, mcpInvalidInput("OpenCode conversion: stdio MCP spec missing command")
		}
		out["type"] = "local"
		command := []any{cmd}
		if args, ok := c["args"].([]any); ok {
			for _, a := range args {
				if s, ok := a.(string); ok {
					if ts := strings.TrimSpace(s); ts != "" {
						command = append(command, ts)
					}
				}
			}
		}
		out["command"] = command
		if env := objAsStringMap(c["env"]); env != nil {
			out["environment"] = env
		}
		if cwd, ok := c["cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
			out["cwd"] = strings.TrimSpace(cwd)
		}
	case "http", "sse":
		url, _ := c["url"].(string)
		if url == "" {
			return nil, mcpInvalidInput("OpenCode conversion: remote MCP spec missing url")
		}
		out["type"] = "remote"
		out["url"] = url
		if typ == "sse" {
			out["transport"] = "sse"
		}
		if headers := objAsStringMap(c["headers"]); headers != nil {
			out["headers"] = headers
		}
	default:
		return nil, mcpInvalidInput("OpenCode conversion: unsupported MCP type '" + typ + "'")
	}
	return out, nil
}

// ─── Claude / CodeBuddy enabledPlugins gate ─────────────────────────────────

func claudeSettingsPath() string    { return filepath.Join(mcpHome(), ".claude", "settings.json") }
func codebuddySettingsPath() string { return filepath.Join(mcpHome(), ".codebuddy", "settings.json") }

func claudeLocalPluginKey(id string) string { return id + "@local" }

func enableLocalPlugin(settingsPath, id string) error {
	root, err := readRootObjectOrNew(settingsPath)
	if err != nil {
		return err
	}
	plugins := ensureObj(root, "enabledPlugins")
	key := claudeLocalPluginKey(id)
	if v, ok := plugins[key].(bool); ok && v {
		return nil // avoid a needless write that trips the settings watcher
	}
	plugins[key] = true
	return writeJSONFile(settingsPath, root)
}

func disableLocalPlugin(settingsPath, id string) error {
	if !fileExists(settingsPath) {
		return nil
	}
	root, err := readJSONValue(settingsPath)
	if err != nil {
		return err
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	plugins, ok := obj["enabledPlugins"].(map[string]any)
	if !ok {
		return nil
	}
	key := claudeLocalPluginKey(id)
	if _, had := plugins[key]; !had {
		return nil
	}
	delete(plugins, key)
	return writeJSONFile(settingsPath, obj)
}

// ─── per-agent writers ──────────────────────────────────────────────────────

func upsertClaudeServer(id string, spec map[string]any) error {
	c, err := canonicalizeSpec(spec, "Claude write")
	if err != nil {
		return err
	}
	if err := upsertRootMcpServers(claudeConfigPath(), id, c, writeJSONFile); err != nil {
		return err
	}
	return enableLocalPlugin(claudeSettingsPath(), id)
}

func removeClaudeServer(id string) (bool, error) {
	removed, err := removeRootMcpServers(claudeConfigPath(), id, writeJSONFile)
	if err != nil {
		return false, err
	}
	if derr := disableLocalPlugin(claudeSettingsPath(), id); derr != nil {
		return removed, derr
	}
	return removed, nil
}

func upsertCodebuddyServer(id string, spec map[string]any) error {
	c, err := canonicalizeSpec(spec, "CodeBuddy write")
	if err != nil {
		return err
	}
	if err := upsertRootMcpServers(codebuddyConfigPath(), id, c, writeJSONFile); err != nil {
		return err
	}
	return enableLocalPlugin(codebuddySettingsPath(), id)
}

func removeCodebuddyServer(id string) (bool, error) {
	removed, err := removeRootMcpServers(codebuddyConfigPath(), id, writeJSONFile)
	if err != nil {
		return false, err
	}
	if derr := disableLocalPlugin(codebuddySettingsPath(), id); derr != nil {
		return removed, derr
	}
	return removed, nil
}

func upsertGeminiServer(id string, spec map[string]any) error {
	c, err := canonicalizeSpec(spec, "Gemini write")
	if err != nil {
		return err
	}
	return upsertRootMcpServers(geminiConfigPath(), id, c, writeJSONFile)
}
func removeGeminiServer(id string) (bool, error) {
	return removeRootMcpServers(geminiConfigPath(), id, writeJSONFile)
}

func upsertClineServer(id string, spec map[string]any) error {
	c, err := canonicalToClineEntry(spec)
	if err != nil {
		return err
	}
	return upsertRootMcpServers(clineConfigPath(), id, c, writeJSONFile)
}
func removeClineServer(id string) (bool, error) {
	return removeRootMcpServers(clineConfigPath(), id, writeJSONFile)
}

func upsertCursorServer(id string, spec map[string]any) error {
	c, err := canonicalToCursorEntry(spec)
	if err != nil {
		return err
	}
	return upsertRootMcpServers(cursorConfigPath(), id, c, writeJSONFile)
}
func removeCursorServer(id string) (bool, error) {
	return removeRootMcpServers(cursorConfigPath(), id, writeJSONFile)
}

func upsertDeepseekServer(id string, spec map[string]any) error {
	c, err := canonicalizeSpec(spec, "DeepSeek write")
	if err != nil {
		return err
	}
	return upsertRootMcpServers(deepseekConfigPath(), id, c, writeDeepseekJSONFile)
}
func removeDeepseekServer(id string) (bool, error) {
	return removeRootMcpServers(deepseekConfigPath(), id, writeDeepseekJSONFile)
}

func upsertQoderServer(id string, spec map[string]any) error {
	c, err := canonicalizeSpec(spec, "Qoder write")
	if err != nil {
		return err
	}
	return upsertRootMcpServers(qoderConfigPath(), id, c, writeJSONFile)
}
func removeQoderServer(id string) (bool, error) {
	return removeRootMcpServers(qoderConfigPath(), id, writeJSONFile)
}

// OpenClaw stores servers under mcp.servers.
func upsertOpenclawServer(id string, spec map[string]any) error {
	c, err := canonicalizeSpec(spec, "OpenClaw write")
	if err != nil {
		return err
	}
	root, err := readRootObjectOrNew(openclawConfigPath())
	if err != nil {
		return err
	}
	mcp := ensureObj(root, "mcp")
	ensureObj(mcp, "servers")[id] = c
	return writeJSONFile(openclawConfigPath(), root)
}

func removeOpenclawServer(id string) (bool, error) {
	path := openclawConfigPath()
	if !fileExists(path) {
		return false, nil
	}
	root, err := readJSONValue(path)
	if err != nil {
		return false, err
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return false, nil
	}
	mcp, ok := obj["mcp"].(map[string]any)
	if !ok {
		return false, nil
	}
	servers, ok := mcp["servers"].(map[string]any)
	if !ok {
		return false, nil
	}
	if _, had := servers[id]; !had {
		return false, nil
	}
	delete(servers, id)
	if len(servers) == 0 {
		delete(mcp, "servers")
	}
	if len(mcp) == 0 {
		delete(obj, "mcp")
	}
	return true, writeJSONFile(path, obj)
}

// OpenCode writes canonical under mcpServers when that key exists, else creates
// the OpenCode-native mcp section.
func upsertOpencodeServer(id string, spec map[string]any) error {
	path := opencodeConfigPath()
	root, err := readRootObjectOrNew(path)
	if err != nil {
		return err
	}
	if _, ok := root["mcpServers"].(map[string]any); ok {
		c, err := canonicalizeSpec(spec, "OpenCode write mcpServers")
		if err != nil {
			return err
		}
		root["mcpServers"].(map[string]any)[id] = c
	} else {
		conv, err := canonicalToOpencodeSpec(spec)
		if err != nil {
			return err
		}
		ensureObj(root, "mcp")[id] = conv
	}
	return writeJSONFile(path, root)
}

func removeOpencodeServer(id string) (bool, error) {
	path := opencodeConfigPath()
	if !fileExists(path) {
		return false, nil
	}
	root, err := readJSONValue(path)
	if err != nil {
		return false, err
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return false, nil
	}
	removed := false
	if servers, ok := obj["mcpServers"].(map[string]any); ok {
		if _, had := servers[id]; had {
			delete(servers, id)
			removed = true
		}
	}
	if servers, ok := obj["mcp"].(map[string]any); ok {
		if _, had := servers[id]; had {
			delete(servers, id)
			removed = true
		}
	}
	if removed {
		return true, writeJSONFile(path, obj)
	}
	return false, nil
}

// ─── Antigravity ────────────────────────────────────────────────────────────

func antigravityConfigBlocksEdit(root map[string]any) bool {
	v, ok := root["mcpServers"]
	if !ok {
		return false
	}
	_, isObj := v.(map[string]any)
	return !isObj
}

// antigravityServersObject returns the editable mcpServers map, migrating a bare
// top-level document into the explicit wrapper; error when blocked / non-object.
func antigravityServersObject(root map[string]any) (map[string]any, error) {
	if antigravityConfigBlocksEdit(root) {
		return nil, mcpConfigInvalid("mcpServers is not an object")
	}
	switch v := root["mcpServers"].(type) {
	case map[string]any:
		return v, nil
	case nil:
		// Bare document: everything at the root moves into the wrapper.
		migrated := map[string]any{}
		for k, val := range root {
			migrated[k] = val
			delete(root, k)
		}
		root["mcpServers"] = migrated
		return migrated, nil
	default:
		return nil, mcpConfigInvalid("mcpServers is not an object")
	}
}

func upsertAntigravityServer(id string, spec map[string]any) error {
	path := antigravityConfigPath()
	c, err := canonicalizeSpec(spec, "Antigravity write")
	if err != nil {
		return err
	}
	root, err := readRootObjectOrNew(path)
	if err != nil {
		return err
	}
	servers, err := antigravityServersObject(root)
	if err != nil {
		return mcpConfigInvalid("invalid JSON root in " + path)
	}
	servers[id] = c
	return writeJSONFile(path, root)
}

func removeAntigravityServer(id string) (bool, error) {
	path := antigravityConfigPath()
	if !fileExists(path) {
		return false, nil
	}
	root, err := readRootObjectOrNew(path)
	if err != nil {
		return false, err
	}
	servers, err := antigravityServersObject(root)
	if err != nil {
		return false, nil
	}
	if _, had := servers[id]; !had {
		return false, nil
	}
	delete(servers, id)
	return true, writeJSONFile(path, root)
}

// ─── dispatch ───────────────────────────────────────────────────────────────

// errMCPWriteStaged is returned when a user explicitly targets an agent whose
// native write path is not yet ported (Codex/Grok TOML, Hermes YAML, pi/kimi).
func errMCPWriteStaged(app McpAppType) error {
	return web.ErrDependencyMissing("writing MCP servers for '" + app + "' is not yet supported in this build")
}

func upsertServerForApp(app McpAppType, id string, spec map[string]any) error {
	switch app {
	case "claude_code":
		return upsertClaudeServer(id, spec)
	case "code_buddy":
		return upsertCodebuddyServer(id, spec)
	case "gemini":
		return upsertGeminiServer(id, spec)
	case "open_claw":
		return upsertOpenclawServer(id, spec)
	case "cline":
		return upsertClineServer(id, spec)
	case "open_code":
		return upsertOpencodeServer(id, spec)
	case "cursor":
		return upsertCursorServer(id, spec)
	case "deepseek":
		return upsertDeepseekServer(id, spec)
	case "qoder":
		return upsertQoderServer(id, spec)
	case "antigravity":
		return upsertAntigravityServer(id, spec)
	case "pi":
		return upsertPiServer(id, spec)
	case "kimi_code":
		return upsertKimiCodeServer(id, spec)
	case "codex":
		return upsertCodexServer(id, spec)
	case "grok":
		return upsertGrokServer(id, spec)
	case "hermes":
		return upsertHermesServer(id, spec)
	default:
		return errMCPWriteStaged(app)
	}
}

func removeServerForApp(app McpAppType, id string) (bool, error) {
	switch app {
	case "claude_code":
		return removeClaudeServer(id)
	case "code_buddy":
		return removeCodebuddyServer(id)
	case "gemini":
		return removeGeminiServer(id)
	case "open_claw":
		return removeOpenclawServer(id)
	case "cline":
		return removeClineServer(id)
	case "open_code":
		return removeOpencodeServer(id)
	case "cursor":
		return removeCursorServer(id)
	case "deepseek":
		return removeDeepseekServer(id)
	case "qoder":
		return removeQoderServer(id)
	case "antigravity":
		return removeAntigravityServer(id)
	case "pi":
		return removePiServer(id)
	case "kimi_code":
		return removeKimiCodeServer(id)
	case "codex":
		return removeCodexServer(id)
	case "grok":
		return removeGrokServer(id)
	case "hermes":
		return removeHermesServer(id)
	default:
		return false, nil
	}
}

// ─── shared mutation helpers ────────────────────────────────────────────────

// appCanHostSpec: Codex/DeepSeek config.toml/json cannot host SSE (would persist
// a url-only entry reread as http), so write paths exclude such pairs.
func appCanHostSpec(app McpAppType, spec map[string]any) bool {
	isSSE := spec["type"] == "sse"
	return !((app == "codex" || app == "deepseek") && isSSE)
}

func normalizeApps(apps []McpAppType) []McpAppType {
	seen := map[McpAppType]bool{}
	out := []McpAppType{}
	for _, a := range apps {
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return sortApps(out)
}

func requireCompleteScan() error {
	scan := mcpScanLocal()
	warnings := scan["warnings"].([]map[string]any)
	if len(warnings) == 0 {
		return nil
	}
	w := warnings[0]
	return mcpConfigInvalid("cannot change MCP server assignments while " + toStr(w["app"]) +
		"'s configuration cannot be read (" + toStr(w["message"]) + "). Fix or remove that file, then try again.")
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func findLocalServer(id string) map[string]any {
	scan := mcpScanLocal()
	for _, s := range scan["servers"].([]map[string]any) {
		if s["id"] == id {
			return s
		}
	}
	return nil
}

func preflightAntigravityUpsert(targets map[McpAppType]bool) error {
	if !targets["antigravity"] {
		return nil
	}
	root, err := readRootObjectOrNew(antigravityConfigPath())
	if err != nil {
		return err
	}
	if antigravityConfigBlocksEdit(root) {
		return mcpConfigInvalid("cannot edit " + antigravityConfigPath() +
			": mcpServers is not an object. Fix or remove that key before assigning MCP servers to Google Antigravity.")
	}
	return nil
}

// ─── handlers ───────────────────────────────────────────────────────────────

func mcpUpsertLocalServer(c *web.Context) (any, error) {
	serverID, err := GetStringOrErr(c, "serverId", "server_id")
	if err != nil {
		return nil, err
	}
	specObj, _ := c.Body["spec"].(map[string]any)
	canonical, err := canonicalizeSpec(specObj, "local MCP save")
	if err != nil {
		return nil, err
	}
	target := normalizeApps(GetStringSlice(c, "apps"))
	if len(target) == 0 {
		return nil, mcpInvalidInput("at least one target app is required")
	}
	targetSet := map[McpAppType]bool{}
	for _, a := range target {
		if appCanHostSpec(a, canonical) {
			targetSet[a] = true
		}
	}
	if len(targetSet) == 0 {
		return nil, mcpInvalidInput("none of the selected agents can host this MCP server's transport (e.g. Codex does not support SSE)")
	}
	if err := requireCompleteScan(); err != nil {
		return nil, err
	}
	if err := preflightAntigravityUpsert(targetSet); err != nil {
		return nil, err
	}
	for _, app := range allMCPApps {
		if targetSet[app] {
			if err := upsertServerForApp(app, serverID, canonical); err != nil {
				return nil, err
			}
		} else {
			if _, err := removeServerForApp(app, serverID); err != nil {
				return nil, err
			}
		}
	}
	saved := findLocalServer(serverID)
	if saved == nil {
		return nil, mcpConfigInvalid("saved local MCP server '" + serverID + "', but failed to reload it")
	}
	return saved, nil
}

func mcpSetServerApps(c *web.Context) (any, error) {
	serverID, err := GetStringOrErr(c, "serverId", "server_id")
	if err != nil {
		return nil, err
	}
	target := normalizeApps(GetStringSlice(c, "apps"))
	if err := requireCompleteScan(); err != nil {
		return nil, err
	}
	current := findLocalServer(serverID)
	if current == nil {
		return nil, web.ErrNotFound("local MCP server not found: " + serverID)
	}
	currentSpec, _ := current["spec"].(map[string]any)
	targetSet := map[McpAppType]bool{}
	for _, a := range target {
		if appCanHostSpec(a, currentSpec) {
			targetSet[a] = true
		}
	}
	if len(target) > 0 && len(targetSet) == 0 {
		return nil, mcpInvalidInput("none of the selected agents can host this MCP server's transport (e.g. Codex does not support SSE)")
	}
	currentSet := map[McpAppType]bool{}
	for _, a := range current["apps"].([]McpAppType) {
		currentSet[a] = true
	}
	added := map[McpAppType]bool{}
	for a := range targetSet {
		if !currentSet[a] {
			added[a] = true
		}
	}
	if err := preflightAntigravityUpsert(added); err != nil {
		return nil, err
	}
	for _, app := range allMCPApps {
		if currentSet[app] && !targetSet[app] {
			if _, err := removeServerForApp(app, serverID); err != nil {
				return nil, err
			}
		}
	}
	for _, app := range allMCPApps {
		if added[app] {
			if err := upsertServerForApp(app, serverID, currentSpec); err != nil {
				return nil, err
			}
		}
	}
	return findLocalServer(serverID), nil
}

func mcpRemoveServer(c *web.Context) (any, error) {
	serverID, err := GetStringOrErr(c, "serverId", "server_id")
	if err != nil {
		return nil, err
	}
	var target []McpAppType
	if _, present := c.Body["apps"]; present {
		target = normalizeApps(GetStringSlice(c, "apps"))
	} else {
		target = append([]McpAppType{}, allMCPApps...)
	}
	if len(target) == 0 {
		return false, nil
	}
	removed := false
	for _, app := range target {
		r, err := removeServerForApp(app, serverID)
		if err != nil {
			return nil, err
		}
		removed = removed || r
	}
	return removed, nil
}
