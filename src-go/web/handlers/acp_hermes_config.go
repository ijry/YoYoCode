package handlers

// acp_hermes_config.go ports acp_update_hermes_config: Hermes keeps its model
// selection in `$HERMES_HOME/config.yaml` (default ~/.hermes) and its API keys in
// `$HERMES_HOME/.env`. This route writes the `model:` section (structured mode)
// or a verbatim document (raw mode), and mirrors the active provider's API key /
// endpoint into `.env` — which Hermes reads with override semantics, so the two
// stores must agree.
//
// Everything is validated BEFORE any write, so an invalid document or a crafted
// key never half-applies; config.yaml is written first so a `.env` failure can
// never leave the credential changed against a stale config.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"yoyocode/src-go/web"
)

// hermesProviderMeta is a managed Hermes provider: the config.yaml
// `model.provider` value (its id) and the `.env` variable carrying its API key.
// `keyEnvVar` is empty for OAuth providers and AWS Bedrock. `needsBaseURL` marks
// providers whose endpoint is user-supplied. `baseURLEnvVar` is the `.env`
// variable Hermes reads for that endpoint (mirrored so both of Hermes'
// resolution paths agree).
type hermesProviderMeta struct {
	id            string
	keyEnvVar     string
	needsBaseURL  bool
	baseURLEnvVar string
}

// hermesProviders is the curated subset of Hermes providers the structured
// fields edit, keyed by the canonical `model.provider` id and `.env` key var
// from Hermes' own PROVIDER_REGISTRY. The long tail goes through the raw
// config.yaml escape hatch.
var hermesProviders = []hermesProviderMeta{
	{"openrouter", "OPENROUTER_API_KEY", false, "OPENROUTER_BASE_URL"},
	{"openai-api", "OPENAI_API_KEY", true, "OPENAI_BASE_URL"},
	// User-supplied OpenAI-compatible endpoint: carries BOTH its key and
	// endpoint INLINE in config.yaml (`model.api_key` / `model.base_url`) and
	// reads no `.env` var.
	{"custom", "", true, ""},
	{"anthropic", "ANTHROPIC_API_KEY", false, "ANTHROPIC_BASE_URL"},
	{"gemini", "GOOGLE_API_KEY", false, "GEMINI_BASE_URL"},
	{"deepseek", "DEEPSEEK_API_KEY", false, "DEEPSEEK_BASE_URL"},
	{"xai", "XAI_API_KEY", false, "XAI_BASE_URL"},
	{"zai", "GLM_API_KEY", false, "GLM_BASE_URL"},
	{"minimax", "MINIMAX_API_KEY", false, "MINIMAX_BASE_URL"},
	{"minimax-cn", "MINIMAX_CN_API_KEY", false, "MINIMAX_CN_BASE_URL"},
	{"kimi-coding", "KIMI_API_KEY", false, "KIMI_BASE_URL"},
	{"kimi-coding-cn", "KIMI_CN_API_KEY", false, ""},
	{"nvidia", "NVIDIA_API_KEY", false, "NVIDIA_BASE_URL"},
	{"alibaba", "DASHSCOPE_API_KEY", false, "DASHSCOPE_BASE_URL"},
	{"alibaba-coding-plan", "ALIBABA_CODING_PLAN_API_KEY", false, "ALIBABA_CODING_PLAN_BASE_URL"},
	{"copilot", "COPILOT_GITHUB_TOKEN", false, "COPILOT_API_BASE_URL"},
	{"lmstudio", "LM_API_KEY", true, "LM_BASE_URL"},
	{"azure-foundry", "AZURE_FOUNDRY_API_KEY", true, "AZURE_FOUNDRY_BASE_URL"},
	{"stepfun", "STEPFUN_API_KEY", false, "STEPFUN_BASE_URL"},
	{"arcee", "ARCEEAI_API_KEY", false, "ARCEE_BASE_URL"},
	{"gmi", "GMI_API_KEY", false, "GMI_BASE_URL"},
	{"huggingface", "HF_TOKEN", false, "HF_BASE_URL"},
	{"kilocode", "KILOCODE_API_KEY", false, "KILOCODE_BASE_URL"},
	{"opencode-zen", "OPENCODE_ZEN_API_KEY", false, "OPENCODE_ZEN_BASE_URL"},
	{"opencode-go", "OPENCODE_GO_API_KEY", false, "OPENCODE_GO_BASE_URL"},
	{"xiaomi", "XIAOMI_API_KEY", false, "XIAOMI_BASE_URL"},
	{"tencent-tokenhub", "TOKENHUB_API_KEY", false, "TOKENHUB_BASE_URL"},
	{"ollama-cloud", "OLLAMA_API_KEY", false, "OLLAMA_BASE_URL"},
	{"novita", "NOVITA_API_KEY", false, "NOVITA_BASE_URL"},
	{"ai-gateway", "AI_GATEWAY_API_KEY", false, "AI_GATEWAY_BASE_URL"},
	// OAuth / external-process providers — credentials via the terminal
	// `--setup` flow; no `.env` key var.
	{"nous", "", false, ""},
	{"openai-codex", "", false, ""},
	{"minimax-oauth", "", false, ""},
	{"xai-oauth", "", false, ""},
	{"qwen-oauth", "", false, ""},
	{"google-gemini-cli", "", false, ""},
	{"copilot-acp", "", false, ""},
	// AWS Bedrock — credentials from the AWS SDK chain.
	{"bedrock", "", false, ""},
	// Google Vertex AI — OAuth2, endpoint computed per request.
	{"vertex", "", false, ""},
}

func hermesProvider(id string) (hermesProviderMeta, bool) {
	for _, p := range hermesProviders {
		if p.id == id {
			return p, true
		}
	}
	return hermesProviderMeta{}, false
}

// hermesInlinesAPIKey reports whether a provider stores its API key INLINE in
// config.yaml `model.api_key` rather than `.env`. Only `custom` does.
func hermesInlinesAPIKey(provider string) bool { return provider == "custom" }

// RegisterACPHermes wires the Hermes config command.
func RegisterACPHermes(reg *Registry) {
	reg.Register("acp_update_hermes_config", func(c *web.Context) (any, error) {
		rawYAML, rawPresent := stringPresent(c, "rawConfigYaml", "raw_config_yaml")
		if err := applyHermesConfigUpdate(
			strVal(c.Body["provider"]),
			strVal(c.Body["apiKey"], c.Body["api_key"]),
			strVal(c.Body["model"]),
			strVal(c.Body["baseUrl"], c.Body["base_url"]),
			rawYAML,
			rawPresent,
		); err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		Emit(c, "agents://changed", map[string]any{})
		return nil, nil
	})
}

// stringPresent reads a string body field, distinguishing absent/null from
// present-but-empty (Hermes' raw mode hinges on that distinction).
func stringPresent(c *web.Context, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := c.Body[k]; ok && v != nil {
			if s, ok := v.(string); ok {
				return s, true
			}
			return "", true
		}
	}
	return "", false
}

// hermesEnvUpdate is an ordered `.env` `(var name, value)` update.
type hermesEnvUpdate struct {
	key   string
	value string
}

// hermesBaseURLWrite / hermesInlineKeyWrite mirror the upstream enums.
type hermesBaseURLWrite struct {
	preserve bool
	value    string
}

type hermesInlineKeyWrite struct {
	clear     bool
	key       string
	scrubMode bool
}

func applyHermesConfigUpdate(provider, apiKey, model, baseURL, rawYAML string, rawPresent bool) error {
	home := hermesHomeDir()
	if err := ensureHermesHomeSecure(home); err != nil {
		return err
	}

	configPath := hermesConfigYamlPath()
	var existing string
	if !rawPresent {
		if b, err := os.ReadFile(configPath); err == nil {
			existing = string(b)
		}
	}
	configYAML, envUpdates, err := planHermesWrite(provider, apiKey, strings.TrimSpace(model), baseURL, rawYAML, rawPresent, existing)
	if err != nil {
		return err
	}

	if err := writeHermesSecretFile(configPath, configYAML, "config.yaml"); err != nil {
		return err
	}
	if len(envUpdates) > 0 {
		envPath := hermesEnvPath()
		existingEnv, _ := os.ReadFile(envPath)
		patched := patchEnvText(string(existingEnv), envUpdates)
		if err := writeHermesSecretFile(envPath, patched, ".env"); err != nil {
			return err
		}
	}
	return nil
}

// planHermesWrite is the pure decision logic for a Hermes config save: it
// computes the config.yaml content and the `.env` updates. Validation happens
// here (no I/O) so a bad request fails before anything is written. Raw mode is
// enforced server-side to never touch `.env`.
func planHermesWrite(provider, apiKey, model, baseURL, rawYAML string, rawPresent bool, existing string) (string, []hermesEnvUpdate, error) {
	previousProvider := existingHermesModelProvider(existing)

	var configYAML string
	if rawPresent {
		var doc any
		if err := yaml.Unmarshal([]byte(rawYAML), &doc); err != nil {
			return "", nil, fmt.Errorf("invalid hermes config.yaml: %w", err)
		}
		configYAML = rawYAML
	} else {
		meta, ok := hermesProvider(provider)
		if !ok {
			return "", nil, fmt.Errorf("unknown hermes provider '%s'; edit ~/.hermes/config.yaml directly", provider)
		}
		// Decide what happens to `model.base_url`.
		var base hermesBaseURLWrite
		switch {
		case meta.needsBaseURL:
			base = hermesBaseURLWrite{value: baseURL}
		case previousProvider == provider:
			base = hermesBaseURLWrite{preserve: true}
		default:
			base = hermesBaseURLWrite{value: ""}
		}
		// Inline key — `custom` only.
		var inline hermesInlineKeyWrite
		if hermesInlinesAPIKey(provider) {
			key := strings.TrimSpace(apiKey)
			if strings.ContainsAny(key, "\n\r") {
				return "", nil, fmt.Errorf("hermes api key must not contain newlines")
			}
			inline = hermesInlineKeyWrite{key: key, scrubMode: previousProvider != provider}
		} else {
			inline = hermesInlineKeyWrite{clear: true}
		}
		out, err := mergeHermesModelConfig(existing, provider, model, base, inline)
		if err != nil {
			return "", nil, err
		}
		configYAML = out
	}

	var envUpdates []hermesEnvUpdate
	if !rawPresent {
		meta, _ := hermesProvider(provider)
		// API key — keyed providers only. A blank key leaves the stored secret
		// untouched (so switching providers can't wipe it).
		if meta.keyEnvVar != "" {
			if key := strings.TrimSpace(apiKey); key != "" {
				if strings.ContainsAny(key, "\n\r") {
					return "", nil, fmt.Errorf("hermes api key must not contain newlines")
				}
				envUpdates = append(envUpdates, hermesEnvUpdate{meta.keyEnvVar, key})
			}
		}
		// Endpoint URL — mirror the structured base URL into the provider's
		// base-URL env var. An empty value clears a stale override.
		if meta.needsBaseURL && meta.baseURLEnvVar != "" {
			base := strings.TrimSpace(baseURL)
			if strings.ContainsAny(base, "\n\r") {
				return "", nil, fmt.Errorf("hermes base url must not contain newlines")
			}
			envUpdates = append(envUpdates, hermesEnvUpdate{meta.baseURLEnvVar, base})
		}
		// Neutralize the one documented cross-provider fallback: openrouter
		// (OpenAI-API compatible) falls back to OPENAI_API_KEY / OPENAI_BASE_URL,
		// so write explicit empties that mask a stale inherited value.
		if provider == "openrouter" {
			for _, v := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL"} {
				if !hasEnvUpdate(envUpdates, v) {
					envUpdates = append(envUpdates, hermesEnvUpdate{v, ""})
				}
			}
		}
	}
	return configYAML, envUpdates, nil
}

func hasEnvUpdate(updates []hermesEnvUpdate, key string) bool {
	for _, u := range updates {
		if u.key == key {
			return true
		}
	}
	return false
}

// patchEnvText updates `KEY=value` entries in a dotenv file while preserving
// comments, blank lines, ordering and unrelated keys. The first occurrence of an
// updated key is replaced in place; later duplicates are dropped. Missing keys
// are appended — including with an empty value (`KEY=`), which both clears a
// stored credential and masks an inherited process-env value.
func patchEnvText(existing string, updates []hermesEnvUpdate) string {
	applied := make([]bool, len(updates))
	var out []string
	for _, line := range splitLines(existing) {
		if key, ok := envLineKey(line); ok {
			if i := envUpdateIndex(updates, key); i >= 0 {
				if applied[i] {
					continue // drop later duplicates of a key we already rewrote
				}
				out = append(out, updates[i].key+"="+updates[i].value)
				applied[i] = true
				continue
			}
		}
		out = append(out, line)
	}
	for i, u := range updates {
		if !applied[i] {
			out = append(out, u.key+"="+u.value)
		}
	}
	result := strings.Join(out, "\n")
	if result != "" {
		result += "\n"
	}
	return result
}

// splitLines splits on \n and drops a single trailing empty element, mirroring
// Rust's `str::lines()`.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// envLineKey extracts the `KEY` of a `KEY=value` dotenv line, or ok=false for a
// blank line, a comment, or a line with no `=`.
func envLineKey(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	body := strings.TrimPrefix(trimmed, "export ")
	i := strings.Index(body, "=")
	if i < 0 {
		return "", false
	}
	return strings.TrimSpace(body[:i]), true
}

func envUpdateIndex(updates []hermesEnvUpdate, key string) int {
	for i, u := range updates {
		if u.key == key {
			return i
		}
	}
	return -1
}

// existingHermesModelProvider reads `model.provider` from an existing
// config.yaml, if present.
func existingHermesModelProvider(existing string) string {
	if strings.TrimSpace(existing) == "" {
		return ""
	}
	var root any
	if err := yaml.Unmarshal([]byte(existing), &root); err != nil {
		return ""
	}
	m, ok := asStringMap(root)
	if !ok {
		return ""
	}
	model, ok := asStringMap(m["model"])
	if !ok {
		return ""
	}
	return yamlStr(model, "provider")
}

// mergeHermesModelConfig sets `model.{provider,default,base_url,api_key,api_mode}`
// in a Hermes config.yaml document, preserving every other top-level key.
func mergeHermesModelConfig(existing, provider, model string, base hermesBaseURLWrite, inline hermesInlineKeyWrite) (string, error) {
	root := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		var doc any
		if err := yaml.Unmarshal([]byte(existing), &doc); err != nil {
			return "", fmt.Errorf("invalid hermes config.yaml: %w", err)
		}
		if m, ok := asStringMap(doc); ok {
			root = m
		}
	}

	modelMap, ok := asStringMap(root["model"])
	if !ok {
		modelMap = map[string]any{}
		root["model"] = modelMap
	}
	modelMap["provider"] = provider
	if model != "" {
		modelMap["default"] = model
	}
	if base.preserve {
		// leave whatever `model.base_url` is already there
	} else if strings.TrimSpace(base.value) != "" {
		modelMap["base_url"] = strings.TrimSpace(base.value)
	} else {
		delete(modelMap, "base_url")
	}
	if inline.clear {
		delete(modelMap, "api_key")
		delete(modelMap, "api_mode")
	} else {
		if strings.TrimSpace(inline.key) == "" {
			delete(modelMap, "api_key") // keyless local server
		} else {
			modelMap["api_key"] = strings.TrimSpace(inline.key)
		}
		if inline.scrubMode {
			delete(modelMap, "api_mode")
		}
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("serialize hermes config.yaml failed: %w", err)
	}
	return string(out), nil
}

// yamlStr returns a non-empty trimmed YAML string value at key, or "".
func yamlStr(m map[string]any, key string) string {
	s, ok := m[key].(string)
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return s
}

// asStringMap normalizes a decoded YAML value to map[string]any, accepting both
// the string-keyed maps yaml.v3 produces and generic-keyed maps.
func asStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			ks, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[ks] = val
		}
		return out, true
	}
	return nil, false
}

// ─── Paths + permissions ─────────────────────────────────────────────────────

// hermesConfigYamlPath is the native config Hermes reads (hermesHomeDir is
// shared with the skills/MCP helpers).
func hermesConfigYamlPath() string { return filepath.Join(hermesHomeDir(), "config.yaml") }

// hermesEnvPath is the dotenv file Hermes reads with override semantics.
func hermesEnvPath() string { return filepath.Join(hermesHomeDir(), ".env") }

// hermesSkipChmod mirrors the opt-outs Hermes itself honors for containerized /
// managed deployments where forcing 0700/0600 breaks multi-process access.
func hermesSkipChmod() bool {
	if runtime.GOOS == "windows" {
		return true
	}
	truthy := func(key string) bool {
		v, ok := os.LookupEnv(key)
		return ok && v != ""
	}
	if truthy("HERMES_CONTAINER") || truthy("HERMES_SKIP_CHMOD") {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	if b, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(b)
		return strings.Contains(s, "docker") || strings.Contains(s, "lxc") || strings.Contains(s, "kubepods")
	}
	return false
}

// parseHermesHomeMode parses a `HERMES_HOME_MODE` value (octal, optional `0o`),
// falling back to owner-only 0700.
func parseHermesHomeMode(raw string) int {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0o700
	}
	s = strings.TrimPrefix(s, "0o")
	m, err := strconv.ParseUint(s, 8, 32)
	if err != nil || m == 0 {
		return 0o700
	}
	return int(m)
}

// ensureHermesHomeSecure creates the Hermes home directory if needed. On Unix it
// tightens a freshly-created directory to HERMES_HOME_MODE (or 0700) when Hermes
// itself would chmod; an existing home is left untouched.
func ensureHermesHomeSecure(home string) error {
	_, statErr := os.Stat(home)
	preexisting := statErr == nil
	if err := os.MkdirAll(home, 0o755); err != nil {
		return fmt.Errorf("create hermes directory failed: %w", err)
	}
	if runtime.GOOS != "windows" && !preexisting && !hermesSkipChmod() {
		mode := parseHermesHomeMode(os.Getenv("HERMES_HOME_MODE"))
		_ = os.Chmod(home, os.FileMode(mode)) // best-effort
	}
	return nil
}

// writeHermesSecretFile writes a Hermes secret file (.env / config.yaml). A
// brand-new target is created owner-only (0600 on Unix); an existing target is
// written through in place, preserving its inode, mode, owner and any symlink.
// An accidentally world-accessible existing secret is repaired back to 0600.
func writeHermesSecretFile(path, contents, label string) error {
	if runtime.GOOS != "windows" {
		// `os.Stat` FOLLOWS symlinks, so this is true when the resolved target
		// does not exist yet (a fresh path or a dangling symlink).
		if _, err := os.Stat(path); err != nil {
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				return fmt.Errorf("create hermes %s failed: %w", label, err)
			}
			if _, err := f.WriteString(contents); err != nil {
				_ = f.Close()
				return fmt.Errorf("write hermes %s failed: %w", label, err)
			}
			return f.Close()
		}
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("write hermes %s failed: %w", label, err)
	}
	if runtime.GOOS != "windows" && !hermesSkipChmod() {
		if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o007 != 0 {
			_ = os.Chmod(path, 0o600)
		}
	}
	return nil
}
