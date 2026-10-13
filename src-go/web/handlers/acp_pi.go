package handlers

// acp_pi.go ports the pi-agent config/probe family: BYO-pi binary validation
// (acp_validate_pi_command), the native-config projection the settings panel
// reads (acp_load_pi_config) and the merge-write that edits pi's own files
// (acp_update_pi_config). Mirrors commands/acp.rs's pi section.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"yoyocode/src-go/web"
)

// piThinkingLevels is pi's fixed thinking-level vocabulary
// (EXTENDED_THINKING_LEVELS in pi-ai); a thinkingLevelMap key outside it is dead
// weight no picker offers.
var piThinkingLevels = map[string]bool{
	"off": true, "minimal": true, "low": true, "medium": true,
	"high": true, "xhigh": true, "max": true,
}

// RegisterACPPi wires the pi-agent config/probe commands.
func RegisterACPPi(reg *Registry) {
	// acp_validate_pi_command: `found=false` with resolvedPath=null is a normal
	// result (the panel shows "not found"), not an error.
	reg.Register("acp_validate_pi_command", func(c *web.Context) (any, error) {
		resolved, ok := resolvePiCommandPath(strVal(c.Body["command"]))
		if !ok {
			return map[string]any{"found": false, "resolvedPath": nil, "version": nil}, nil
		}
		var version any
		if v := probePiVersion(resolved); v != "" {
			version = v
		}
		return map[string]any{"found": true, "resolvedPath": resolved, "version": version}, nil
	})

	// acp_load_pi_config: read pi's native files into a projection. Never errors
	// on absent/malformed files (all-None / empty lists).
	reg.Register("acp_load_pi_config", func(c *web.Context) (any, error) {
		dir, err := piSettingsDir()
		if err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		return loadPiConfigAt(dir), nil
	})

	// acp_update_pi_config: merge-write settings.json, then auth.json (only with
	// an API key) and models.json (only with a custom base URL).
	reg.Register("acp_update_pi_config", func(c *web.Context) (any, error) {
		provider := strings.TrimSpace(strVal(c.Body["provider"]))
		if provider == "" {
			return nil, web.ErrTaskExecution("pi provider is required")
		}
		model := strings.TrimSpace(strVal(c.Body["model"]))
		if model == "" {
			return nil, web.ErrTaskExecution("pi model is required")
		}
		thinking := strings.TrimSpace(strVal(c.Body["thinkingLevel"], c.Body["thinking_level"]))
		apiKey := strings.TrimSpace(strVal(c.Body["apiKey"], c.Body["api_key"]))
		if strings.ContainsAny(apiKey, "\n\r") {
			return nil, web.ErrTaskExecution("pi API key must not contain line breaks")
		}
		customBase := strings.TrimSpace(strVal(c.Body["customBaseUrl"], c.Body["custom_base_url"]))
		customAPI := strings.TrimSpace(strVal(c.Body["customApi"], c.Body["custom_api"]))

		dir, err := piSettingsDir()
		if err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}

		// settings.json: merge-write the model keys.
		settings := readJSONObjectOrEmpty(filepath.Join(dir, "settings.json"))
		settings["defaultProvider"] = provider
		settings["defaultModel"] = model
		if thinking != "" {
			settings["defaultThinkingLevel"] = thinking
		}
		if err := writeJSONObjectPretty(filepath.Join(dir, "settings.json"), settings); err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}

		// auth.json: merge-write the provider credential (only when given).
		if apiKey != "" {
			auth := readJSONObjectOrEmpty(filepath.Join(dir, "auth.json"))
			auth[provider] = map[string]any{"type": "api_key", "key": apiKey}
			if err := writeJSONObjectPretty(filepath.Join(dir, "auth.json"), auth); err != nil {
				return nil, web.ErrTaskExecution(err.Error())
			}
		}

		// models.json: define the custom provider (only when a base URL is given).
		if customBase != "" {
			if customAPI == "" {
				customAPI = "openai-completions"
			}
			doc := readJSONObjectOrEmpty(filepath.Join(dir, "models.json"))
			providers, _ := doc["providers"].(map[string]any)
			if providers == nil {
				providers = map[string]any{}
			}
			entry, _ := providers[provider].(map[string]any)
			if entry == nil {
				entry = map[string]any{}
			}
			entry["baseUrl"] = customBase
			entry["api"] = customAPI
			applyPiCustomModel(entry, model, c.Body["modelReasoning"])
			providers[provider] = entry
			doc["providers"] = providers
			if err := writeJSONObjectPretty(filepath.Join(dir, "models.json"), doc); err != nil {
				return nil, web.ErrTaskExecution(err.Error())
			}
		}
		Emit(c, "agents://changed", map[string]any{})
		return nil, nil
	})
}

// piSettingsDir resolves pi's agent directory (PI_CODING_AGENT_DIR with ~
// expansion, else ~/.pi/agent) and refuses a relative one: a relative dir names
// a different directory in every workspace, so there is no single profile the
// settings page could read or write.
func piSettingsDir() (string, error) {
	home := mcpHome()
	dir := filepath.Join(home, ".pi", "agent")
	if v := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); v != "" {
		dir = expandHomePrefix(v, home)
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("pi agent directory must be absolute or start with ~/ before editing native settings (this agent's pi uses %q, relative to each workspace)", dir)
	}
	return dir, nil
}

// loadPiConfigAt reads pi's native files into the settings projection.
func loadPiConfigAt(dir string) map[string]any {
	settings := readJSONObjectOrEmpty(filepath.Join(dir, "settings.json"))
	strKey := func(k string) any {
		if v, ok := settings[k].(string); ok {
			return v
		}
		return nil
	}
	auth := readJSONObjectOrEmpty(filepath.Join(dir, "auth.json"))
	authProviders := make([]string, 0, len(auth))
	for k := range auth {
		authProviders = append(authProviders, k)
	}
	sort.Strings(authProviders)

	customProviders := []map[string]any{}
	if providers, ok := readJSONObjectOrEmpty(filepath.Join(dir, "models.json"))["providers"].(map[string]any); ok {
		for id, raw := range providers {
			entry, _ := raw.(map[string]any)
			if entry == nil {
				continue
			}
			baseURL, _ := entry["baseUrl"].(string)
			api, _ := entry["api"].(string)
			if api == "" {
				api = "openai-completions"
			}
			customProviders = append(customProviders, map[string]any{
				"id": id, "baseUrl": baseURL, "api": api,
				"models": piCustomModelsFromEntry(entry),
			})
		}
		sort.Slice(customProviders, func(i, j int) bool {
			return mapStr(customProviders[i], "id") < mapStr(customProviders[j], "id")
		})
	}
	return map[string]any{
		"defaultProvider":      strKey("defaultProvider"),
		"defaultModel":         strKey("defaultModel"),
		"defaultThinkingLevel": strKey("defaultThinkingLevel"),
		"authProviders":        authProviders,
		"customProviders":      customProviders,
	}
}

// piCustomModelsFromEntry projects a custom provider's models array. Entries
// without a string id are skipped; a thinkingLevelMap value that is neither a
// string nor null is dropped rather than guessed at.
func piCustomModelsFromEntry(entry map[string]any) []map[string]any {
	out := []map[string]any{}
	arr, _ := entry["models"].([]any)
	for _, raw := range arr {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		id, _ := item["id"].(string)
		if id == "" {
			continue
		}
		m := map[string]any{"id": id, "thinkingLevelMap": map[string]any{}}
		if r, ok := item["reasoning"].(bool); ok {
			m["reasoning"] = r
		}
		if tlm, ok := item["thinkingLevelMap"].(map[string]any); ok {
			clean := map[string]any{}
			for level, v := range tlm {
				switch x := v.(type) {
				case nil:
					clean[level] = nil
				case string:
					clean[level] = x
				}
			}
			m["thinkingLevelMap"] = clean
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return mapStr(out[i], "id") < mapStr(out[j], "id") })
	return out
}

// mapStr reads a string field out of a decoded JSON object.
func mapStr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// applyPiCustomModel upserts modelID into a custom provider entry's models array
// (creating it if absent) and, when a reasoning spec is given, writes its
// reasoning flag + the thinkingLevelMap restricted to pi's known levels (an empty
// map drops the key rather than writing `{}`).
func applyPiCustomModel(entry map[string]any, modelID string, reasoning any) {
	models, _ := entry["models"].([]any)
	var modelObj map[string]any
	for _, raw := range models {
		if m, ok := raw.(map[string]any); ok {
			if id, _ := m["id"].(string); id == modelID {
				modelObj = m
				break
			}
		}
	}
	if modelObj == nil {
		modelObj = map[string]any{"id": modelID, "name": modelID}
		models = append(models, modelObj)
	}
	if spec, ok := reasoning.(map[string]any); ok && spec != nil {
		if r, ok := spec["reasoning"].(bool); ok {
			modelObj["reasoning"] = r
		}
		if tlm, ok := spec["thinkingLevelMap"].(map[string]any); ok {
			clean := map[string]any{}
			for level, v := range tlm {
				if !piThinkingLevels[level] {
					continue
				}
				switch x := v.(type) {
				case nil:
					clean[level] = nil
				case string:
					clean[level] = x
				}
			}
			if len(clean) == 0 {
				delete(modelObj, "thinkingLevelMap")
			} else {
				modelObj["thinkingLevelMap"] = clean
			}
		}
	}
	entry["models"] = models
}

// resolvePiCommandPath mirrors upstream resolve_pi_command_path: a command that
// looks like a path is checked directly (and made absolute); otherwise it is
// looked up on PATH.
func resolvePiCommandPath(command string) (string, bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "", false
	}
	looksLikePath := strings.ContainsRune(trimmed, os.PathSeparator) ||
		strings.ContainsRune(trimmed, '/') ||
		filepath.IsAbs(trimmed)
	if looksLikePath {
		if !piPathIsExecutable(trimmed) {
			return "", false
		}
		if abs, err := filepath.Abs(trimmed); err == nil {
			return abs, true
		}
		return trimmed, true
	}
	p, err := exec.LookPath(trimmed)
	if err != nil {
		return "", false
	}
	return p, true
}

// piPathIsExecutable reports whether path exists as a file and, on unix, carries
// any execute bit; on Windows existence is enough.
func piPathIsExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

// probePiVersion runs `<resolved> --version` and returns the trimmed first
// non-empty stdout line, or "" on any failure.
func probePiVersion(resolved string) string {
	out, err := exec.Command(resolved, "--version").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// readJSONObjectOrEmpty reads a JSON object file, returning an empty map when it
// is absent, unreadable, or does not parse to an object.
func readJSONObjectOrEmpty(path string) map[string]any {
	b, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// writeJSONObjectPretty writes obj as indented JSON, creating parent dirs.
func writeJSONObjectPretty(path string, obj map[string]any) error {
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
