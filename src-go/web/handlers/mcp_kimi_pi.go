package handlers

// pi + Kimi Code MCP agents (B11, mcp finisher increment), mirroring the pi and
// kimi_code halves of commands/mcp.rs. Both store a top-level mcpServers object
// in a JSON file resolved through their own env/home rules. pi's schema is
// Claude-shaped (canonical) with no translation; Kimi keys the transport off a
// "transport" discriminant and models extension fields no other agent does.

import (
	"math"
	"os"
	"path/filepath"
	"strings"
)

// ─── paths ──────────────────────────────────────────────────────────────────

// piMcpPath: PI_CODING_AGENT_DIR (with ~ expansion) else ~/.pi/agent, + mcp.json.
func piMcpPath() string {
	home := mcpHome()
	if v := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); v != "" {
		return filepath.Join(expandHomePrefix(v, home), "mcp.json")
	}
	return filepath.Join(home, ".pi", "agent", "mcp.json")
}

// kimiMcpPath: KIMI_CODE_HOME (used verbatim) else ~/.kimi-code, + mcp.json.
func kimiMcpPath() string {
	if v := strings.TrimSpace(os.Getenv("KIMI_CODE_HOME")); v != "" {
		return filepath.Join(v, "mcp.json")
	}
	return filepath.Join(mcpHome(), ".kimi-code", "mcp.json")
}

// ─── pi (Claude-shaped, no translation) ─────────────────────────────────────

func readPiServersImpl() (map[string]map[string]any, error) {
	return readSimpleMcpServers(piMcpPath(), "pi config", nil)
}
func upsertPiServer(id string, spec map[string]any) error {
	c, err := canonicalizeSpec(spec, "pi write")
	if err != nil {
		return err
	}
	return upsertRootMcpServers(piMcpPath(), id, c, writeJSONFile)
}
func removePiServer(id string) (bool, error) {
	return removeRootMcpServers(piMcpPath(), id, writeJSONFile)
}

// ─── Kimi Code (transport discriminant + extension fields) ──────────────────

var kimiOwnedKeys = []string{"runtime_id", "executor", "bearerTokenEnvVar", "startupTimeoutMs", "toolTimeoutMs", "enabledTools", "disabledTools"}
var kimiSharedKeys = []string{"auth"}

func isNonEmptyStr(v any) bool { s, ok := v.(string); return ok && s != "" }

func isStringArray(v any) bool {
	a, ok := v.([]any)
	if !ok {
		return false
	}
	for _, x := range a {
		if _, ok := x.(string); !ok {
			return false
		}
	}
	return true
}

func isKimiTimeoutMs(v any) bool {
	f, ok := v.(float64)
	if !ok {
		return false
	}
	return f == math.Trunc(f) && f >= 1 && f <= float64(math.MaxInt32)
}

// isKimiExtensionField reports whether key carries a value valid under Kimi's
// schema for the given transport (stdio vs remote).
func isKimiExtensionField(key string, value any, stdio bool) bool {
	switch key {
	case "runtime_id":
		return stdio && isNonEmptyStr(value)
	case "executor":
		s, _ := value.(string)
		return stdio && (s == "local" || s == "kaos")
	case "auth":
		s, _ := value.(string)
		return !stdio && s == "oauth"
	case "bearerTokenEnvVar":
		return !stdio && isNonEmptyStr(value)
	case "startupTimeoutMs", "toolTimeoutMs":
		return isKimiTimeoutMs(value)
	case "enabledTools", "disabledTools":
		return isStringArray(value)
	default:
		return false
	}
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// kimiEntryToCanonical maps a Kimi mcpServers entry to canonical: drop any
// on-disk type, map an explicit transport discriminant to the canonical type
// (reject unknown), then canonicalize.
func kimiEntryToCanonical(spec map[string]any, id string) (map[string]any, error) {
	obj := cloneMap(spec)
	delete(obj, "type")
	if tv, has := obj["transport"]; has {
		ts, _ := tv.(string)
		var ct string
		switch ts {
		case "stdio":
			ct = "stdio"
		case "http":
			ct = "http"
		case "sse":
			ct = "sse"
		default:
			shown := ts
			if shown == "" {
				shown = "<non-string>"
			}
			return nil, mcpInvalidInput("Kimi Code config '" + id + "': unsupported transport '" + shown + "' (Kimi accepts only \"stdio\", \"http\", or \"sse\")")
		}
		obj["type"] = ct
	}
	delete(obj, "transport")
	return canonicalizeSpec(obj, "Kimi Code config '"+id+"'")
}

func readKimiCodeServersImpl() (map[string]map[string]any, error) {
	root, err := readJSONValue(kimiMcpPath())
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
	out := map[string]map[string]any{}
	for id, specv := range servers {
		spec, ok := specv.(map[string]any)
		if !ok {
			continue
		}
		norm, err := kimiEntryToCanonical(spec, id)
		if err != nil {
			continue
		}
		out[id] = norm
	}
	return out, nil
}

// canonicalToKimiEntry emits only the fields Kimi models (plus validated
// extension fields), with a transport discriminant for remote entries.
func canonicalToKimiEntry(spec map[string]any) (map[string]any, error) {
	c, err := canonicalizeSpec(spec, "Kimi Code write")
	if err != nil {
		return nil, err
	}
	transport := ""
	switch c["type"] {
	case "http":
		transport = "http"
	case "sse":
		transport = "sse"
	}
	out := map[string]any{}
	for key, value := range c {
		keep := false
		switch key {
		case "type", "command", "args", "env", "cwd", "url", "headers":
			keep = true
		case "enabled":
			_, keep = value.(bool)
		default:
			keep = isKimiExtensionField(key, value, transport == "")
		}
		if keep {
			out[key] = value
		}
	}
	if transport != "" {
		out["transport"] = transport
	}
	return out, nil
}

func upsertKimiCodeServer(id string, spec map[string]any) error {
	c, err := canonicalToKimiEntry(spec)
	if err != nil {
		return err
	}
	return upsertRootMcpServers(kimiMcpPath(), id, c, writeJSONFile)
}
func removeKimiCodeServer(id string) (bool, error) {
	return removeRootMcpServers(kimiMcpPath(), id, writeJSONFile)
}

// mergeKimiExtensionFields folds Kimi's extension fields back onto the canonical
// spec a shared server resolved to, so they survive another agent winning the
// scan merge and are visible for editing/deletion. KIMI_OWNED keys take Kimi's
// copy (absence included); KIMI_SHARED keys are refilled from the owning agent's
// declaration, falling back to Kimi's.
func mergeKimiExtensionFields(canonical, kimiSpec, ownerValues map[string]any) {
	stdio := canonical["type"] == "stdio"
	for _, key := range append(append([]string{}, kimiOwnedKeys...), kimiSharedKeys...) {
		delete(canonical, key)
		var value any
		if v, ok := ownerValues[key]; ok {
			value = v
		} else if v, ok := kimiSpec[key]; ok {
			value = v
		} else {
			continue
		}
		if isKimiExtensionField(key, value, stdio) {
			canonical[key] = value
		}
	}
}
