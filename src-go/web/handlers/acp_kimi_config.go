package handlers

// acp_kimi_config.go ports acp_update_kimi_code_config: Kimi Code reads its
// model selection from `$KIMI_CODE_HOME/config.toml` (default ~/.kimi-code) and
// gates `kimi acp` sessions on a credential file under `credentials/`. This
// route writes that native config (three modes: apikey / login / raw) and keeps
// the gate credential in sync, so a BYO API key actually authenticates the CLI.
//
// The managed provider/model block is written under OUR brand
// ("yoyocode" / "yoyocode-managed") — the upstream wrote its own name there.
// The block is recognizable and removable without disturbing any provider or
// model the user authored by hand.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const (
	// kimiManagedProvider / kimiManagedModelAlias are the synthetic provider and
	// model alias our managed block is written under.
	kimiManagedProvider   = "yoyocode"
	kimiManagedModelAlias = "yoyocode-managed"
	// kimiSyntheticToken / kimiSyntheticMarker identify OUR gate token so a real
	// `kimi login` OAuth token is never clobbered.
	kimiSyntheticToken  = "yoyocode-local-gate"
	kimiSyntheticMarker = "_yoyocode_synthetic"
	// kimiDefaultMaxContextSize is the fallback context window for the managed
	// model. Kimi's schema REQUIRES a positive `max_context_size` — omitting it
	// makes kimi discard the whole model block, leaving `default_model` dangling
	// and every prompt ending with no reply. So one is always written.
	kimiDefaultMaxContextSize = 262144
)

// KIMI_MODEL_* env keys `kimi acp` reads BEFORE config.toml; a stale entry would
// silently win over the managed provider, so every save clears them.
const (
	kimiModelAPIKeyEnv  = "KIMI_MODEL_API_KEY"
	kimiModelBaseURLEnv = "KIMI_MODEL_BASE_URL"
	kimiModelNameEnv    = "KIMI_MODEL_NAME"
)

// kimiInterfaceTypes is the set of native provider `type` values Kimi accepts in
// `[providers.<name>]`.
var kimiInterfaceTypes = []string{
	"kimi", "openai", "openai_responses", "anthropic", "google-genai", "vertexai",
}

// kimiBaseCapabilities are the input modalities always declared alongside a
// thinking capability. Kimi reads capabilities permissively (an ABSENT key
// allows everything, a PRESENT array allows only what it lists), so declaring
// `thinking` has to re-declare them or enabling reasoning would silently revoke
// image/video input.
var kimiBaseCapabilities = []string{"image_in", "video_in", "tool_use"}

// kimiConfigUpdate mirrors KimiCodeConfigUpdate (all fields optional but mode).
type kimiConfigUpdate struct {
	mode             string
	interfaceType    string
	authType         string
	baseURL          string
	apiKey           string
	model            string
	maxContextSize   int64
	vertexProject    string
	vertexLocation   string
	rawConfigToml    string
	reasoningEnabled bool
	alwaysThinking   bool
	supportEfforts   []string
	defaultEffort    string
}

// kimiManagedSpec is the resolved managed provider/model block to write.
type kimiManagedSpec struct {
	interfaceType  string
	baseURL        string
	apiKey         string
	env            map[string]string
	model          string
	maxContextSize int64
	capabilities   []string
	supportEfforts []string
	defaultEffort  string
}

func registerKimiConfig(reg *Registry) {
	reg.Register("acp_update_kimi_code_config", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		u := kimiConfigUpdate{
			mode:             strings.TrimSpace(strVal(c.Body["mode"])),
			interfaceType:    strVal(c.Body["interfaceType"], c.Body["interface_type"]),
			authType:         strVal(c.Body["authType"], c.Body["auth_type"]),
			baseURL:          strVal(c.Body["baseUrl"], c.Body["base_url"]),
			apiKey:           strVal(c.Body["apiKey"], c.Body["api_key"]),
			model:            strVal(c.Body["model"]),
			maxContextSize:   GetInt(c, "maxContextSize", "max_context_size"),
			vertexProject:    strVal(c.Body["vertexProject"], c.Body["vertex_project"]),
			vertexLocation:   strVal(c.Body["vertexLocation"], c.Body["vertex_location"]),
			rawConfigToml:    strVal(c.Body["rawConfigToml"], c.Body["raw_config_toml"]),
			reasoningEnabled: GetBool(c, "reasoningEnabled", "reasoning_enabled"),
			alwaysThinking:   GetBool(c, "alwaysThinking", "always_thinking"),
			supportEfforts:   GetStringSlice(c, "supportEfforts", "support_efforts"),
			defaultEffort:    strVal(c.Body["defaultEffort"], c.Body["default_effort"]),
		}
		if err := applyKimiConfigUpdate(u); err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		clearKimiModelEnv(d)
		Emit(c, "agents://changed", map[string]any{})
		// Upstream returns the count of running Kimi sessions left on stale
		// (launch-time) config; the port does not track launch-time staleness,
		// so there is nothing to report.
		return 0, nil
	})
}

// applyKimiConfigUpdate plans the whole request before any write, then writes
// config.toml first so an env-write failure can never leave the file pointing at
// credentials that were rolled back.
func applyKimiConfigUpdate(u kimiConfigUpdate) error {
	const (
		fileManaged = iota
		fileClear
		fileRaw
	)
	fileKind := fileManaged
	var spec *kimiManagedSpec
	raw := ""
	seedCredential := true

	switch u.mode {
	case "apikey":
		s, err := buildKimiManagedSpec(u)
		if err != nil {
			return err
		}
		spec = s
	case "login":
		fileKind = fileClear
		seedCredential = false
	case "raw":
		raw = u.rawConfigToml
		var tbl map[string]any
		if err := toml.Unmarshal([]byte(raw), &tbl); err != nil {
			return fmt.Errorf("invalid kimi config.toml: %w", err)
		}
		fileKind = fileRaw
	default:
		return fmt.Errorf("unknown kimi config mode: '%s'", u.mode)
	}

	switch fileKind {
	case fileManaged:
		if err := mutateKimiConfigToml(spec); err != nil {
			return err
		}
	case fileClear:
		if err := mutateKimiConfigToml(nil); err != nil {
			return err
		}
	case fileRaw:
		if err := writeKimiConfigRaw(raw); err != nil {
			return err
		}
	}

	if seedCredential {
		return seedKimiSyntheticCredential()
	}
	return removeKimiSyntheticCredentialIfOurs()
}

// buildKimiManagedSpec validates + resolves an `apikey`-mode update into the
// managed block to write.
func buildKimiManagedSpec(u kimiConfigUpdate) (*kimiManagedSpec, error) {
	iface := strings.TrimSpace(u.interfaceType)
	if !containsStr(kimiInterfaceTypes, iface) {
		return nil, fmt.Errorf("unknown kimi interface type: '%s'", iface)
	}
	model := strings.TrimSpace(u.model)
	if model == "" {
		return nil, fmt.Errorf("kimi native config requires a model id")
	}
	baseURL := strings.TrimSpace(u.baseURL)
	if strings.ContainsAny(baseURL, "\n\r") {
		return nil, fmt.Errorf("kimi base url must not contain newlines")
	}
	spec := &kimiManagedSpec{
		interfaceType: iface,
		baseURL:       baseURL,
		model:         model,
		env:           map[string]string{},
	}
	if u.maxContextSize > 0 {
		spec.maxContextSize = u.maxContextSize
	}

	key := strings.TrimSpace(u.apiKey)
	if iface == "vertexai" {
		// Vertex AI: no API key (GCP Application Default Credentials); persist
		// the project/location into the provider env sub-table.
		if p := strings.TrimSpace(u.vertexProject); p != "" {
			spec.env["GOOGLE_CLOUD_PROJECT"] = p
		}
		if l := strings.TrimSpace(u.vertexLocation); l != "" {
			spec.env["GOOGLE_CLOUD_LOCATION"] = l
		}
	} else if key != "" {
		if strings.ContainsAny(key, "\n\r") {
			return nil, fmt.Errorf("kimi api key must not contain newlines")
		}
		// "env" auth writes the key into the provider env sub-table under the
		// interface's canonical key var; otherwise it goes in the inline api_key.
		if strings.TrimSpace(u.authType) == "env" {
			if v, ok := kimiProviderKeyEnvVar(iface); ok {
				spec.env[v] = key
			} else {
				spec.apiKey = key
			}
		} else {
			spec.apiKey = key
		}
	}

	// Reasoning metadata: only applies when the user turned reasoning on;
	// otherwise every key is left out and the block keeps its previous shape.
	if u.reasoningEnabled {
		if u.alwaysThinking {
			spec.capabilities = append(spec.capabilities, "always_thinking")
		} else {
			spec.capabilities = append(spec.capabilities, "thinking")
		}
		spec.capabilities = append(spec.capabilities, kimiBaseCapabilities...)

		for _, raw := range u.supportEfforts {
			e := strings.TrimSpace(raw)
			if e == "" {
				continue
			}
			if strings.ContainsAny(e, "\n\r") {
				return nil, fmt.Errorf("kimi thinking effort must not contain newlines")
			}
			if !containsStr(spec.supportEfforts, e) {
				spec.supportEfforts = append(spec.supportEfforts, e)
			}
		}
		// Kimi clamps an out-of-range default back to the middle entry, so an
		// unlisted value is dropped rather than rejected.
		if d := strings.TrimSpace(u.defaultEffort); d != "" && containsStr(spec.supportEfforts, d) {
			spec.defaultEffort = d
		}
	}
	return spec, nil
}

// kimiProviderKeyEnvVar is the `[providers.<name>].env` variable Kimi reads each
// interface type's API key from when the user picks "env sub-table" auth. No
// entry for vertexai, whose credentials come from GCP ADC (no inline key).
func kimiProviderKeyEnvVar(interfaceType string) (string, bool) {
	switch interfaceType {
	case "kimi":
		return "KIMI_API_KEY", true
	case "openai", "openai_responses":
		return "OPENAI_API_KEY", true
	case "anthropic":
		return "ANTHROPIC_API_KEY", true
	case "google-genai":
		return "GOOGLE_API_KEY", true
	}
	return "", false
}

// mutateKimiConfigToml read-modify-writes config.toml, upserting (spec != nil)
// or clearing (spec == nil) the managed block. A clear on a non-existent file is
// a no-op (never creates an empty file). Data in other sections is preserved;
// comments/formatting are not (the raw editor covers that), matching upstream.
func mutateKimiConfigToml(spec *kimiManagedSpec) error {
	path := kimiConfigTomlPath()
	if spec == nil {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
	}
	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(b, &root); err != nil {
			root = map[string]any{}
		}
	}
	applyKimiManagedBlock(root, spec)
	body, err := toml.Marshal(root)
	if err != nil {
		return fmt.Errorf("serialize kimi config.toml failed: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create kimi config directory failed: %w", err)
	}
	return writeTextFile(path, string(body))
}

// writeKimiConfigRaw writes a verbatim config.toml (already validated as TOML).
func writeKimiConfigRaw(raw string) error {
	path := kimiConfigTomlPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create kimi config directory failed: %w", err)
	}
	return os.WriteFile(path, []byte(raw), 0o644)
}

// applyKimiManagedBlock upserts or removes the `[providers.yoyocode]` +
// `[models.yoyocode-managed]` block in a parsed config.toml document, preserving
// every other section. Removal also resets `default_model` only when it points
// at our managed alias.
func applyKimiManagedBlock(root map[string]any, spec *kimiManagedSpec) {
	if spec != nil {
		providers := ensureSubTable(root, "providers")
		pt := map[string]any{"type": spec.interfaceType}
		if url := strings.TrimSpace(spec.baseURL); url != "" {
			pt["base_url"] = url
		}
		if key := strings.TrimSpace(spec.apiKey); key != "" {
			pt["api_key"] = key
		}
		if len(spec.env) > 0 {
			env := map[string]any{}
			for k, v := range spec.env {
				if trimmed := strings.TrimSpace(v); trimmed != "" {
					env[k] = trimmed
				}
			}
			if len(env) > 0 {
				pt["env"] = env
			}
		}
		providers[kimiManagedProvider] = pt

		models := ensureSubTable(root, "models")
		mt := map[string]any{
			"provider": kimiManagedProvider,
			"model":    spec.model,
		}
		ctx := spec.maxContextSize
		if ctx <= 0 {
			ctx = kimiDefaultMaxContextSize
		}
		mt["max_context_size"] = ctx
		// Each reasoning key is omitted when empty so the block stays
		// byte-identical to the pre-reasoning shape when the feature is off.
		if len(spec.capabilities) > 0 {
			mt["capabilities"] = toAnySlice(spec.capabilities)
		}
		if len(spec.supportEfforts) > 0 {
			mt["support_efforts"] = toAnySlice(spec.supportEfforts)
		}
		if spec.defaultEffort != "" {
			mt["default_effort"] = spec.defaultEffort
		}
		models[kimiManagedModelAlias] = mt

		root["default_model"] = kimiManagedModelAlias
		return
	}

	if providers, ok := root["providers"].(map[string]any); ok {
		delete(providers, kimiManagedProvider)
		if len(providers) == 0 {
			delete(root, "providers")
		}
	}
	if models, ok := root["models"].(map[string]any); ok {
		delete(models, kimiManagedModelAlias)
		if len(models) == 0 {
			delete(root, "models")
		}
	}
	if s, ok := root["default_model"].(string); ok && s == kimiManagedModelAlias {
		delete(root, "default_model")
	}
}

// ensureSubTable returns root[key] as a table, replacing a non-table value (or a
// missing one) with a fresh empty table.
func ensureSubTable(root map[string]any, key string) map[string]any {
	if t, ok := root[key].(map[string]any); ok {
		return t
	}
	t := map[string]any{}
	root[key] = t
	return t
}

// ─── Gate credential ─────────────────────────────────────────────────────────

// kimiConfigTomlPath is the native config `kimi acp` reads.
func kimiConfigTomlPath() string {
	return filepath.Join(kimiHomeDir(), "config.toml")
}

// kimiCredentialTokenPath is the synthetic-gate-token file `kimi acp` checks to
// decide a session is authenticated.
func kimiCredentialTokenPath() string {
	return filepath.Join(kimiHomeDir(), "credentials", "kimi-code.json")
}

// kimiSyntheticTokenDoc is the gate token document we seed. Field order matches
// the upstream document.
type kimiSyntheticTokenDoc struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	Marker       bool   `json:"_yoyocode_synthetic"`
}

func readKimiTokenAt(path string) (map[string]any, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false
	}
	return m, true
}

// kimiTokenIsSynthetic reports whether a token document is OUR gate token (vs a
// real OAuth login the user performed via `kimi login`).
func kimiTokenIsSynthetic(t map[string]any) bool {
	if v, ok := t[kimiSyntheticMarker].(bool); ok && v {
		return true
	}
	if s, ok := t["access_token"].(string); ok && s == kimiSyntheticToken {
		return true
	}
	return false
}

// kimiTokenHasAccess reports whether a token carries a non-empty access_token —
// i.e. would pass `kimi acp`'s session gate.
func kimiTokenHasAccess(t map[string]any) bool {
	s, ok := t["access_token"].(string)
	return ok && strings.TrimSpace(s) != ""
}

// seedKimiSyntheticCredential seeds OUR gate token so `kimi acp` treats the
// session as authenticated. No-op (preserves) when a real OAuth login token is
// already present — that already satisfies the gate and must never be clobbered.
func seedKimiSyntheticCredential() error {
	path := kimiCredentialTokenPath()
	if t, ok := readKimiTokenAt(path); ok {
		if kimiTokenHasAccess(t) && !kimiTokenIsSynthetic(t) {
			return nil
		}
	}
	body, err := json.MarshalIndent(kimiSyntheticTokenDoc{
		AccessToken: kimiSyntheticToken,
		ExpiresAt:   9999999999,
		ExpiresIn:   9999999,
		TokenType:   "Bearer",
		Marker:      true,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize kimi credential failed: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create kimi credentials directory failed: %w", err)
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

// removeKimiSyntheticCredentialIfOurs removes the gate token ONLY when it is
// ours — leaving any real OAuth login untouched.
func removeKimiSyntheticCredentialIfOurs() error {
	path := kimiCredentialTokenPath()
	if t, ok := readKimiTokenAt(path); ok && kimiTokenIsSynthetic(t) {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove kimi credential failed: %w", err)
		}
	}
	return nil
}

// ─── Env override ────────────────────────────────────────────────────────────

// clearKimiModelEnv removes any `KIMI_MODEL_*` override from the agent_setting
// env_json, preserving every other key. `kimi acp` reads that family BEFORE
// config.toml, so a stale entry would silently override the managed provider;
// every save clears it to keep config.toml authoritative.
func clearKimiModelEnv(d *db.Database) {
	if d == nil || d.Conn == nil {
		return
	}
	var raw sql.NullString
	err := d.Conn.QueryRow(`SELECT env_json FROM agent_setting WHERE agent_type = ?`, "kimi_code").Scan(&raw)
	if err != nil || !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(raw.String), &env); err != nil {
		return
	}
	had := false
	for _, k := range []string{kimiModelBaseURLEnv, kimiModelAPIKeyEnv, kimiModelNameEnv} {
		if _, ok := env[k]; ok {
			delete(env, k)
			had = true
		}
	}
	if !had {
		return
	}
	out, err := json.Marshal(env)
	if err != nil {
		return
	}
	upsertAgentSetting(d, "kimi_code", map[string]any{"env_json": string(out)})
}

// writeTextFile writes a serialized document with exactly one trailing newline.
func writeTextFile(path, body string) error {
	return os.WriteFile(path, []byte(strings.TrimRight(body, "\n")+"\n"), 0o644)
}
