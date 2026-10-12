package handlers

// Hermes MCP agent (B11, mcp finisher 3 — final agent), mirroring the hermes
// half of commands/mcp.rs. Hermes stores servers under mcp_servers in a YAML
// config (~/.hermes/config.yaml), encoding SSE via transport: sse (a bare url
// is StreamableHTTP) and reading command/args/env for stdio. With this the mcp
// module covers all 15 agents' local read/write. YAML uses gopkg.in/yaml.v3.
//
// Fidelity note: upstream writes config.yaml through a symlink-preserving,
// owner-only "secret" writer; here we write owner-only (0600) under a 0700
// parent, which matches the security intent without the symlink preservation.

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"yoyocode/src-go/web"
)

func hermesConfigPath() string {
	if v := strings.TrimSpace(os.Getenv("HERMES_HOME")); v != "" {
		return filepath.Join(expandHomePrefix(v, mcpHome()), "config.yaml")
	}
	return filepath.Join(mcpHome(), ".hermes", "config.yaml")
}

// yamlToStringMaps recursively coerces map[any]any (yaml.v3 for non-string keys)
// into map[string]any so the shared canonical helpers can read it.
func yamlNormalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = yamlNormalize(e)
		}
		return t
	case map[any]any:
		out := map[string]any{}
		for k, e := range t {
			if ks, ok := k.(string); ok {
				out[ks] = yamlNormalize(e)
			}
		}
		return out
	case []any:
		for i, e := range t {
			t[i] = yamlNormalize(e)
		}
		return t
	default:
		return v
	}
}

func hermesEntryToCanonical(entry map[string]any, id string) (map[string]any, error) {
	obj := cloneMap(entry)
	_, hasURL := obj["url"]
	if mstr(obj, "type") == "" && hasURL {
		if strings.EqualFold(mstr(obj, "transport"), "sse") {
			obj["type"] = "sse"
		} else {
			obj["type"] = "http"
		}
	}
	delete(obj, "transport")
	return canonicalizeSpec(obj, "Hermes mcp_servers '"+id+"'")
}

func canonicalToHermesEntry(spec map[string]any) (map[string]any, error) {
	c, err := canonicalizeSpec(spec, "Hermes conversion")
	if err != nil {
		return nil, err
	}
	typ, _ := c["type"].(string)
	if typ == "" {
		typ = "stdio"
	}
	out := map[string]any{}
	switch typ {
	case "stdio":
		for _, key := range []string{"command", "args", "env"} {
			if v, ok := c[key]; ok {
				out[key] = v
			}
		}
	case "http", "sse":
		if v, ok := c["url"]; ok {
			out["url"] = v
		}
		if typ == "sse" {
			out["transport"] = "sse"
		}
		if v, ok := c["headers"]; ok {
			out["headers"] = v
		}
	default:
		return nil, mcpInvalidInput("Hermes conversion: unsupported MCP type '" + typ + "'")
	}
	for k, v := range c {
		switch k {
		case "type", "command", "args", "env", "cwd", "url", "headers", "transport":
			continue
		}
		if v != nil {
			out[k] = v
		}
	}
	return out, nil
}

func readHermesServersImpl() (map[string]map[string]any, error) {
	raw, err := os.ReadFile(hermesConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]map[string]any{}, nil
		}
		return nil, web.ErrIO(hermesConfigPath() + ": " + err.Error())
	}
	if strings.TrimSpace(string(raw)) == "" {
		return map[string]map[string]any{}, nil
	}
	var root any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, web.ErrConfigInvalid("invalid YAML at " + hermesConfigPath() + ": " + err.Error())
	}
	obj, ok := yamlNormalize(root).(map[string]any)
	if !ok {
		return map[string]map[string]any{}, nil
	}
	servers, ok := obj["mcp_servers"].(map[string]any)
	if !ok {
		return map[string]map[string]any{}, nil
	}
	out := map[string]map[string]any{}
	for id, entryv := range servers {
		entry, ok := entryv.(map[string]any)
		if !ok {
			continue
		}
		if norm, err := hermesEntryToCanonical(entry, id); err == nil {
			out[id] = norm
		}
	}
	return out, nil
}

// writeHermesConfig serializes root to YAML owner-only (0600) under a 0700 parent.
func writeHermesConfig(path string, root map[string]any) error {
	if parent := filepath.Dir(path); parent != "" {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return web.ErrIO(err.Error())
		}
	}
	b, err := yaml.Marshal(root)
	if err != nil {
		return mcpConfigInvalid("serialize hermes config.yaml failed: " + err.Error())
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return web.ErrIO(err.Error())
	}
	return nil
}

func readHermesRootForWrite(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, mcpConfigInvalid("read hermes config.yaml failed: " + err.Error())
	}
	if strings.TrimSpace(string(raw)) == "" {
		return map[string]any{}, nil
	}
	var root any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, mcpConfigInvalid("invalid hermes config.yaml: " + err.Error())
	}
	if obj, ok := yamlNormalize(root).(map[string]any); ok {
		return obj, nil
	}
	return map[string]any{}, nil
}

func upsertHermesServer(id string, spec map[string]any) error {
	entry, err := canonicalToHermesEntry(spec)
	if err != nil {
		return err
	}
	path := hermesConfigPath()
	root, err := readHermesRootForWrite(path)
	if err != nil {
		return err
	}
	servers, ok := root["mcp_servers"].(map[string]any)
	if !ok {
		servers = map[string]any{}
		root["mcp_servers"] = servers
	}
	servers[id] = entry
	return writeHermesConfig(path, root)
}

func removeHermesServer(id string) (bool, error) {
	path := hermesConfigPath()
	raw, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return false, nil
	}
	var r any
	if yaml.Unmarshal(raw, &r) != nil {
		return false, nil
	}
	root, ok := yamlNormalize(r).(map[string]any)
	if !ok {
		return false, nil
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
	return true, writeHermesConfig(path, root)
}
