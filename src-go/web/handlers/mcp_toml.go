package handlers

// Codex + Grok MCP agents (B11, mcp finisher 2), mirroring the codex and grok
// halves of commands/mcp.rs. Both store MCP servers in a TOML config
// (Codex: mcp_servers + legacy mcp.servers; Grok: mcp_servers) with transport
// inferred from command/url (no "type" key, except Grok writes type="sse").
// TOML parsing/serialization uses github.com/pelletier/go-toml/v2.

import (
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"yoyocode/src-go/web"
)

// ─── paths ──────────────────────────────────────────────────────────────────

func codexHomeDir() string {
	if v := strings.TrimSpace(os.Getenv("CODEX_HOME")); v != "" {
		return expandHomePrefix(v, mcpHome())
	}
	return filepath.Join(mcpHome(), ".codex")
}
func codexConfigPath() string { return filepath.Join(codexHomeDir(), "config.toml") }

func grokConfigPath() string {
	if v := strings.TrimSpace(os.Getenv("GROK_HOME")); v != "" {
		return filepath.Join(v, "config.toml")
	}
	return filepath.Join(mcpHome(), ".grok", "config.toml")
}

// ─── TOML file I/O ──────────────────────────────────────────────────────────

func readTomlRoot(path string) (map[string]any, error) {
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
	if err := toml.Unmarshal(raw, &v); err != nil {
		return nil, web.ErrConfigInvalid("invalid TOML at " + path + ": " + err.Error())
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, web.ErrConfigInvalid("invalid TOML root at " + path + ": expected table")
	}
	return m, nil
}

func writeTomlRoot(path string, root map[string]any) error {
	if parent := filepath.Dir(path); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return web.ErrIO(err.Error())
		}
	}
	b, err := toml.Marshal(root)
	if err != nil {
		return mcpConfigInvalid("failed to serialize TOML for " + path + ": " + err.Error())
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return web.ErrIO(err.Error())
	}
	return nil
}

// normalizeForTOML drops nulls and demotes whole floats to ints so passthrough
// keys round-trip as the integers/booleans TOML (and strict-config) expect.
func normalizeForTOML(v any) (any, bool) {
	switch t := v.(type) {
	case nil:
		return nil, false
	case float64:
		if t == float64(int64(t)) {
			return int64(t), true
		}
		return t, true
	case []any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			if nv, ok := normalizeForTOML(e); ok {
				out = append(out, nv)
			}
		}
		return out, true
	case map[string]any:
		out := map[string]any{}
		for k, e := range t {
			if nv, ok := normalizeForTOML(e); ok {
				out[k] = nv
			}
		}
		return out, true
	default:
		return v, true
	}
}

// stringMapFromTOML keeps only trimmed non-empty string values of a TOML table.
func stringMapFromTOML(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for k, item := range m {
		if s, ok := item.(string); ok {
			if ts := strings.TrimSpace(s); ts != "" {
				out[k] = ts
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stringArrayFromTOML keeps trimmed non-empty string elements.
func stringArrayFromTOML(v any) []any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := []any{}
	for _, e := range arr {
		if s, ok := e.(string); ok {
			if ts := strings.TrimSpace(s); ts != "" {
				out = append(out, ts)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ─── Codex entry conversion ─────────────────────────────────────────────────

func codexEntryToCanonical(id string, table map[string]any) (map[string]any, error) {
	_, hasCommand := table["command"]
	_, hasURL := table["url"]
	if hasCommand && hasURL {
		return nil, mcpInvalidInput("Codex MCP entry '" + id + "' sets both 'command' and 'url'; Codex accepts exactly one transport")
	}
	rawType := mstr(table, "type")
	hasKey := func(k string) bool { return mstr(table, k) != "" }
	var canonicalType string
	if rawType != "" {
		canonicalType = normalizeMCPType(rawType)
		if canonicalType == "" {
			return nil, mcpInvalidInput("Codex MCP entry '" + id + "' has unsupported type '" + rawType + "'")
		}
	} else if hasKey("url") && !hasKey("command") {
		canonicalType = "http"
	} else {
		canonicalType = "stdio"
	}
	spec := map[string]any{"type": canonicalType}
	switch canonicalType {
	case "stdio":
		if c := mstr(table, "command"); c != "" {
			spec["command"] = c
		}
		if args := stringArrayFromTOML(table["args"]); args != nil {
			spec["args"] = args
		}
		if env := stringMapFromTOML(table["env"]); env != nil {
			spec["env"] = env
		}
		if cwd := mstr(table, "cwd"); cwd != "" {
			spec["cwd"] = cwd
		}
	case "http", "sse":
		if u := mstr(table, "url"); u != "" {
			spec["url"] = u
		}
		headers := stringMapFromTOML(table["http_headers"])
		if headers == nil {
			headers = stringMapFromTOML(table["headers"])
		}
		if headers != nil {
			spec["headers"] = headers
		}
	default:
		return nil, mcpInvalidInput("Codex MCP entry '" + id + "' has unsupported type '" + rawType + "'")
	}
	for k, v := range table {
		switch k {
		case "type", "command", "args", "env", "cwd", "url", "headers", "http_headers":
			continue
		}
		spec[k] = v
	}
	return canonicalizeSpec(spec, "Codex config")
}

func canonicalToCodexEntry(spec map[string]any) (map[string]any, error) {
	c, err := canonicalizeSpec(spec, "Codex conversion")
	if err != nil {
		return nil, err
	}
	typ, _ := c["type"].(string)
	if typ == "" {
		typ = "stdio"
	}
	table := map[string]any{}
	switch typ {
	case "stdio":
		command, _ := c["command"].(string)
		if command == "" {
			return nil, mcpInvalidInput("Codex conversion: stdio MCP spec missing command")
		}
		table["command"] = command
		if args := stringArrayFromTOML(c["args"]); args != nil {
			table["args"] = args
		}
		if cwd := mstr(c, "cwd"); cwd != "" {
			table["cwd"] = cwd
		}
		if env := stringMapFromTOML(c["env"]); env != nil {
			table["env"] = env
		}
	case "http":
		url, _ := c["url"].(string)
		if url == "" {
			return nil, mcpInvalidInput("Codex conversion: remote MCP spec missing url")
		}
		table["url"] = url
		if headers := stringMapFromTOML(c["headers"]); headers != nil {
			table["http_headers"] = headers
		}
	case "sse":
		return nil, mcpInvalidInput("Codex conversion: SSE MCP servers are not supported by Codex; use streamable HTTP")
	default:
		return nil, mcpInvalidInput("Codex conversion: unsupported MCP type '" + typ + "'")
	}
	for k, v := range c {
		if k == "enabled" || k == "required" {
			if b, ok := v.(bool); ok {
				table[k] = b
			}
		}
	}
	return table, nil
}

// ─── Codex readers/writers ──────────────────────────────────────────────────

func readCodexServersImpl() (map[string]map[string]any, error) {
	root, err := readTomlRoot(codexConfigPath())
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	if servers, ok := root["mcp_servers"].(map[string]any); ok {
		for id, specv := range servers {
			if table, ok := specv.(map[string]any); ok {
				if norm, err := codexEntryToCanonical(id, table); err == nil {
					out[id] = norm
				}
			}
		}
	}
	if legacy, ok := root["mcp"].(map[string]any); ok {
		if servers, ok := legacy["servers"].(map[string]any); ok {
			for id, specv := range servers {
				if _, seen := out[id]; seen {
					continue
				}
				if table, ok := specv.(map[string]any); ok {
					if norm, err := codexEntryToCanonical(id, table); err == nil {
						out[id] = norm
					}
				}
			}
		}
	}
	return out, nil
}

func upsertCodexServer(id string, spec map[string]any) error {
	path := codexConfigPath()
	root, err := readTomlRoot(path)
	if err != nil {
		return err
	}
	entry, err := canonicalToCodexEntry(spec)
	if err != nil {
		return err
	}
	servers, ok := root["mcp_servers"].(map[string]any)
	if !ok {
		servers = map[string]any{}
		root["mcp_servers"] = servers
	}
	servers[id] = entry
	// Drop any stale legacy mcp.servers.<id>, cleaning empties.
	if legacy, ok := root["mcp"].(map[string]any); ok {
		if ls, ok := legacy["servers"].(map[string]any); ok {
			delete(ls, id)
			if len(ls) == 0 {
				delete(legacy, "servers")
			}
		}
		if len(legacy) == 0 {
			delete(root, "mcp")
		}
	}
	return writeTomlRoot(path, root)
}

func removeCodexServer(id string) (bool, error) {
	path := codexConfigPath()
	if !fileExists(path) {
		return false, nil
	}
	root, err := readTomlRoot(path)
	if err != nil {
		return false, err
	}
	removed := false
	if servers, ok := root["mcp_servers"].(map[string]any); ok {
		if _, had := servers[id]; had {
			delete(servers, id)
			removed = true
		}
		if len(servers) == 0 {
			delete(root, "mcp_servers")
		}
	}
	if legacy, ok := root["mcp"].(map[string]any); ok {
		if ls, ok := legacy["servers"].(map[string]any); ok {
			if _, had := ls[id]; had {
				delete(ls, id)
				removed = true
			}
			if len(ls) == 0 {
				delete(legacy, "servers")
			}
		}
		if len(legacy) == 0 {
			delete(root, "mcp")
		}
	}
	if removed {
		return true, writeTomlRoot(path, root)
	}
	return false, nil
}

// ─── Grok entry conversion ──────────────────────────────────────────────────

func grokEntryToCanonical(id string, table map[string]any) (map[string]any, error) {
	explicitType := mstr(table, "type")
	hasURL := mstr(table, "url") != ""
	isRemote := explicitType == "http" || explicitType == "sse" || (hasURL && explicitType != "stdio")

	spec := map[string]any{}
	if isRemote {
		canonicalType := "http"
		if explicitType == "sse" {
			canonicalType = "sse"
		}
		spec["type"] = canonicalType
		if u := mstr(table, "url"); u != "" {
			spec["url"] = u
		}
		if headers := stringMapFromTOML(table["headers"]); headers != nil {
			spec["headers"] = headers
		}
	} else {
		spec["type"] = "stdio"
		if c := mstr(table, "command"); c != "" {
			spec["command"] = c
		}
		if args := stringArrayFromTOML(table["args"]); args != nil {
			spec["args"] = args
		}
		if env := stringMapFromTOML(table["env"]); env != nil {
			spec["env"] = env
		}
		if cwd := mstr(table, "cwd"); cwd != "" {
			spec["cwd"] = cwd
		}
	}
	for k, v := range table {
		switch k {
		case "type", "command", "args", "env", "cwd", "url", "headers":
			continue
		}
		spec[k] = v
	}
	return canonicalizeSpec(spec, "Grok config")
}

func canonicalToGrokEntry(spec map[string]any) (map[string]any, error) {
	c, err := canonicalizeSpec(spec, "Grok conversion")
	if err != nil {
		return nil, err
	}
	typ, _ := c["type"].(string)
	if typ == "" {
		typ = "stdio"
	}
	table := map[string]any{}
	switch typ {
	case "stdio":
		command, _ := c["command"].(string)
		if command == "" {
			return nil, mcpInvalidInput("Grok conversion: stdio MCP spec missing command")
		}
		table["command"] = command
		if args := stringArrayFromTOML(c["args"]); args != nil {
			table["args"] = args
		}
		if env := stringMapFromTOML(c["env"]); env != nil {
			table["env"] = env
		}
		if cwd := mstr(c, "cwd"); cwd != "" {
			table["cwd"] = cwd
		}
	case "http", "sse":
		if typ == "sse" {
			table["type"] = "sse"
		}
		url, _ := c["url"].(string)
		if url == "" {
			return nil, mcpInvalidInput("Grok conversion: remote MCP spec missing url")
		}
		table["url"] = url
		if headers := stringMapFromTOML(c["headers"]); headers != nil {
			table["headers"] = headers
		}
	default:
		return nil, mcpInvalidInput("Grok conversion: unsupported MCP type '" + typ + "'")
	}
	for k, v := range c {
		switch k {
		case "type", "command", "args", "env", "cwd", "url", "headers":
			continue
		}
		if nv, ok := normalizeForTOML(v); ok {
			table[k] = nv
		}
	}
	return table, nil
}

// ─── Grok readers/writers ───────────────────────────────────────────────────

func readGrokServersImpl() (map[string]map[string]any, error) {
	root, err := readTomlRoot(grokConfigPath())
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	if servers, ok := root["mcp_servers"].(map[string]any); ok {
		for id, specv := range servers {
			if table, ok := specv.(map[string]any); ok {
				if norm, err := grokEntryToCanonical(id, table); err == nil {
					out[id] = norm
				}
			}
		}
	}
	return out, nil
}

func upsertGrokServer(id string, spec map[string]any) error {
	path := grokConfigPath()
	root, err := readTomlRoot(path)
	if err != nil {
		return err
	}
	entry, err := canonicalToGrokEntry(spec)
	if err != nil {
		return err
	}
	servers, ok := root["mcp_servers"].(map[string]any)
	if !ok {
		servers = map[string]any{}
		root["mcp_servers"] = servers
	}
	servers[id] = entry
	return writeTomlRoot(path, root)
}

func removeGrokServer(id string) (bool, error) {
	path := grokConfigPath()
	if !fileExists(path) {
		return false, nil
	}
	root, err := readTomlRoot(path)
	if err != nil {
		return false, err
	}
	servers, ok := root["mcp_servers"].(map[string]any)
	if !ok {
		return false, nil
	}
	if _, had := servers[id]; !had {
		return false, nil
	}
	delete(servers, id)
	if len(servers) == 0 {
		delete(root, "mcp_servers")
	}
	return true, writeTomlRoot(path, root)
}
