package handlers

// MCP (Model Context Protocol) local-server management, mirroring
// commands/mcp.rs. This file holds the shared canonical-spec model and the app
// (agent) type table; mcp.go holds the per-agent config readers, the scan
// aggregation and the command handlers.
//
// The canonical spec is upstream's normalized MCP server description — a JSON
// object with type stdio|http|sse plus the transport fields — into which every
// agent's native on-disk format is read, and out of which each agent's format
// is written. canonicalizeSpec / canonicalizeOpencodeSpec are a faithful port
// of the upstream functions of the same name; getting them byte-identical is
// what keeps mcode-app's MCP page interoperable with the desktop app.

import (
	"sort"
	"strings"
)

// McpAppType is an agent's wire name (serde snake_case with DeepSeek pinned to
// "deepseek"); it equals models::agent::AgentType::as_wire for the same agent,
// which the upstream wire-name test enforces.
type McpAppType = string

// allMCPApps lists every app the local-MCP paths walk, in the order the enum
// declares them. A server's apps list is sorted by this order (upstream's
// BTreeSet<McpAppType> iterates in discriminant order).
var allMCPApps = []McpAppType{
	"claude_code",
	"codex",
	"gemini",
	"open_claw",
	"open_code",
	"cline",
	"hermes",
	"code_buddy",
	"kimi_code",
	"grok",
	"cursor",
	"deepseek",
	"qoder",
	"antigravity",
	"pi",
}

var mcpAppOrder = func() map[string]int {
	m := make(map[string]int, len(allMCPApps))
	for i, a := range allMCPApps {
		m[a] = i
	}
	return m
}()

// sortApps orders a set of apps by the enum declaration order.
func sortApps(apps []McpAppType) []McpAppType {
	sort.SliceStable(apps, func(i, j int) bool {
		return mcpAppOrder[apps[i]] < mcpAppOrder[apps[j]]
	})
	return apps
}

// ─── canonical spec ─────────────────────────────────────────────────────────

func mcpTrim(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// normalizeMCPType maps a user transport string to a canonical value, or "".
// Stage 1: exact lowercase match (stdio/http/sse + opencode local/remote).
// Stage 2 (http only): strip to ascii-alnum and match streamablehttp -> http.
func normalizeMCPType(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	switch strings.ToLower(trimmed) {
	case "stdio":
		return "stdio"
	case "http":
		return "http"
	case "sse":
		return "sse"
	case "local":
		return "local"
	case "remote":
		return "remote"
	}
	var b strings.Builder
	for _, c := range strings.ToLower(trimmed) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		}
	}
	if b.String() == "streamablehttp" {
		return "http"
	}
	return ""
}

// objAsStringMap keeps only trimmed non-empty string values; nil if empty.
func objAsStringMap(v any) map[string]any {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for k, item := range obj {
		s, ok := item.(string)
		if !ok {
			continue
		}
		t := strings.TrimSpace(s)
		if t == "" {
			continue
		}
		out[k] = t
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

var canonReserved = map[string]bool{
	"type": true, "command": true, "args": true, "env": true,
	"cwd": true, "url": true, "headers": true,
}

// canonicalizeSpec normalizes an arbitrary MCP spec object into the canonical
// shape, or an error if it is not a usable spec. Port of mcp.rs::canonicalize_spec.
func canonicalizeSpec(spec map[string]any, source string) (map[string]any, error) {
	if spec == nil {
		return nil, mcpInvalidInput(source + ": MCP spec must be a JSON object")
	}
	rawType := mcpTrim(spec["type"])

	var resolved string
	if rawType == "" {
		if _, ok := spec["command"]; ok {
			resolved = "stdio"
		} else if _, ok := spec["url"]; ok {
			resolved = "http"
		} else {
			return nil, mcpInvalidInput(source + ": MCP spec missing 'type'; provide one of stdio, http (aliases: streamable-http, streamableHttp), sse")
		}
	} else {
		resolved = normalizeMCPType(rawType)
		if resolved == "" {
			return nil, mcpInvalidInput(source + ": unsupported MCP server type '" + rawType + "'; supported: stdio, http (aliases: streamable-http, streamableHttp), sse")
		}
	}

	if resolved == "local" || resolved == "remote" {
		return canonicalizeOpencodeSpec(spec, source)
	}

	normalized := map[string]any{}
	switch resolved {
	case "stdio":
		command := mcpTrim(spec["command"])
		if command == "" {
			return nil, mcpInvalidInput(source + ": stdio MCP spec requires a non-empty command")
		}
		normalized["type"] = "stdio"
		normalized["command"] = command
		if args, ok := spec["args"].([]any); ok {
			vals := []any{}
			for _, a := range args {
				if s, ok := a.(string); ok {
					if ts := strings.TrimSpace(s); ts != "" {
						vals = append(vals, ts)
					}
				}
			}
			if len(vals) > 0 {
				normalized["args"] = vals
			}
		}
		if env := objAsStringMap(spec["env"]); env != nil {
			normalized["env"] = env
		}
		if cwd := mcpTrim(spec["cwd"]); cwd != "" {
			normalized["cwd"] = cwd
		}
	case "http", "sse":
		url := mcpTrim(spec["url"])
		if url == "" {
			return nil, mcpInvalidInput(source + ": remote MCP spec requires a non-empty url")
		}
		normalized["type"] = resolved
		normalized["url"] = url
		if headers := objAsStringMap(spec["headers"]); headers != nil {
			normalized["headers"] = headers
		}
	}

	for k, v := range spec {
		if _, taken := normalized[k]; taken {
			continue
		}
		if canonReserved[k] {
			continue
		}
		if v != nil {
			normalized[k] = v
		}
	}
	return normalized, nil
}

// canonicalizeOpencodeSpec translates an OpenCode-format spec (type local|remote,
// command-as-array, environment) into canonical shape. Port of
// mcp.rs::canonicalize_opencode_spec.
func canonicalizeOpencodeSpec(spec map[string]any, source string) (map[string]any, error) {
	if spec == nil {
		return nil, mcpInvalidInput(source + ": OpenCode MCP spec must be a JSON object")
	}
	typ := "local"
	if s := mcpTrim(spec["type"]); s != "" {
		typ = s
	}
	switch typ {
	case "local":
		converted := map[string]any{"type": "stdio"}
		if command, ok := spec["command"]; ok {
			if arr, ok := command.([]any); ok {
				first := ""
				if len(arr) > 0 {
					first = mcpTrim(arr[0])
				}
				if first == "" {
					return nil, mcpInvalidInput(source + ": local MCP command array must include executable")
				}
				converted["command"] = first
				if len(arr) > 1 {
					args := []any{}
					for _, a := range arr[1:] {
						if s, ok := a.(string); ok {
							if ts := strings.TrimSpace(s); ts != "" {
								args = append(args, ts)
							}
						}
					}
					if len(args) > 0 {
						converted["args"] = args
					}
				}
			} else if raw, ok := command.(string); ok {
				t := strings.TrimSpace(raw)
				if t == "" {
					return nil, mcpInvalidInput(source + ": local MCP command must be non-empty")
				}
				converted["command"] = t
			}
		}
		if env := objAsStringMap(spec["environment"]); env != nil {
			converted["env"] = env
		}
		if cwd := mcpTrim(spec["cwd"]); cwd != "" {
			converted["cwd"] = cwd
		}
		return canonicalizeSpec(converted, source)
	case "remote":
		converted := map[string]any{}
		remoteType := "http"
		if mcpTrim(spec["transport"]) == "sse" {
			remoteType = "sse"
		}
		converted["type"] = remoteType
		url := mcpTrim(spec["url"])
		if url == "" {
			url = mcpTrim(spec["deploymentUrl"])
		}
		if url != "" {
			converted["url"] = url
		}
		if headers := objAsStringMap(spec["headers"]); headers != nil {
			converted["headers"] = headers
		}
		return canonicalizeSpec(converted, source)
	default:
		return canonicalizeSpec(spec, source)
	}
}
