package handlers

// acp_agent_probes.go ports the cursor / qoder auth probes:
//
//   - acp_qoder_auth_status — runs `qoder status -o json` and parses the flat
//     object into the Qoder settings panel's auth card.
//   - acp_cursor_auth_status — runs `cursor-agent status --format json`.
//   - acp_cursor_list_models — runs `cursor-agent models` for the model picker.
//
// All three are read-only probes: a failure is reported in-band as `error` (with
// `installed`/`logged_in` still honest), never as a request failure. Wire fields
// are snake_case (no camelCase rename), matching the client types.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"yoyocode/src-go/acp"
	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// ─── wire types (snake_case) ─────────────────────────────────────────────────

type qoderAuthStatus struct {
	Installed  bool    `json:"installed"`
	LoggedIn   bool    `json:"logged_in"`
	Username   *string `json:"username"`
	Email      *string `json:"email"`
	UserType   *string `json:"user_type"`
	Version    *string `json:"version"`
	AllowBYOK  *bool   `json:"allow_byok"`
	Error      *string `json:"error"`
	BinaryPath *string `json:"binary_path"`
}

type cursorAuthStatus struct {
	Installed          bool    `json:"installed"`
	IsAuthenticated    bool    `json:"is_authenticated"`
	RawStatus          *string `json:"raw_status"`
	Email              *string `json:"email"`
	Membership         *string `json:"membership"`
	Error              *string `json:"error"`
	BinaryPath         *string `json:"binary_path"`
	CredentialVerified *bool   `json:"credential_verified"`
}

type cursorModelInfo struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	IsDefault bool   `json:"is_default"`
}

type cursorModelsResult struct {
	Models       []cursorModelInfo `json:"models"`
	DefaultModel *string           `json:"default_model"`
	Error        *string           `json:"error"`
}

func registerACPAgentProbes(reg *Registry) {
	reg.Register("acp_qoder_auth_status", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		pat, present := stringPresent(c, "personalAccessToken", "personal_access_token")
		return qoderAuthStatusFor(d, catalogDataDir(c), pat, present), nil
	})

	reg.Register("acp_cursor_auth_status", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		key, present := stringPresent(c, "apiKey", "api_key")
		return cursorAuthStatusFor(d, catalogDataDir(c), key, present), nil
	})

	reg.Register("acp_cursor_list_models", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		key, present := stringPresent(c, "apiKey", "api_key")
		return cursorListModelsFor(d, catalogDataDir(c), key, present), nil
	})
}

// ─── qoder ───────────────────────────────────────────────────────────────────

// resolveQoderBinary is the `qoder` binary we would launch: managed cache first,
// then the user's own install.
func resolveQoderBinary(dataDir string) string {
	if p, _, ok := acp.FindBestCachedBinary(dataDir, "qoder"); ok {
		return p
	}
	return acp.ResolveSystemAgentBinary("qoder")
}

// qoderProbeEnv is the Qoder agent's effective probe env: the saved env with the
// settings form's live personal access token applied on top. The token is always
// materialized (empty when unset) so the probe makes an explicit set-or-remove
// decision — an inherited token from the user's dev shell must not make the card
// claim an account a launch would not use.
func qoderProbeEnv(d *db.Database, token string, present bool) map[string]string {
	env := agentEnvJSON(d, "qoder")
	if present {
		env["QODER_PERSONAL_ACCESS_TOKEN"] = strings.TrimSpace(token)
	}
	if _, ok := env["QODER_PERSONAL_ACCESS_TOKEN"]; !ok {
		env["QODER_PERSONAL_ACCESS_TOKEN"] = ""
	}
	return env
}

func qoderAuthStatusFor(d *db.Database, dataDir, token string, present bool) qoderAuthStatus {
	bin := resolveQoderBinary(dataDir)
	if bin == "" {
		return qoderAuthStatus{}
	}
	binPath := bin
	failed := func(msg string) qoderAuthStatus {
		e := msg
		return qoderAuthStatus{Installed: true, Error: &e, BinaryPath: &binPath}
	}
	stdout, errMsg := runAgentProbe("qoder", bin, []string{"status", "-o", "json"}, 30, qoderProbeEnv(d, token, present))
	if errMsg != "" {
		return failed(errMsg)
	}
	// Scan to the first `{` so a leading log/update-notice line can't break
	// parsing.
	idx := strings.IndexByte(stdout, '{')
	if idx < 0 {
		idx = 0
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout[idx:])), &v); err != nil {
		return failed(fmt.Sprintf("unexpected status output: %v: %s", err, truncateProbeOutput(stdout)))
	}
	getStr := func(key string) *string {
		if s, ok := v[key].(string); ok && s != "" {
			return &s
		}
		return nil
	}
	loggedIn, _ := v["logged_in"].(bool)
	return qoderAuthStatus{
		Installed:  true,
		LoggedIn:   loggedIn,
		Username:   getStr("username"),
		Email:      getStr("email"),
		UserType:   getStr("user_type"),
		Version:    getStr("version"),
		AllowBYOK:  boolOrInt(v["allow_byok"]),
		BinaryPath: &binPath,
	}
}

// boolOrInt reads a JSON boolean, or the CLI's 0/1 spelling of one.
func boolOrInt(v any) *bool {
	switch t := v.(type) {
	case bool:
		return &t
	case float64:
		b := t != 0
		return &b
	case int64:
		b := t != 0
		return &b
	}
	return nil
}

// ─── cursor ──────────────────────────────────────────────────────────────────

// resolveCursorBinary is the cursor-agent binary we would launch: managed cache
// first, then the user's own install.
func resolveCursorBinary(dataDir string) string {
	if p, _, ok := acp.FindBestCachedBinary(dataDir, "cursor"); ok {
		return p
	}
	return acp.ResolveSystemAgentBinary("cursor-agent")
}

// cursorProbeEnv is the Cursor agent's effective probe env: the saved env with
// the settings form's live API key applied on top, so `status`/`models` test
// exactly the credential on screen.
//
// `present` distinguishes API-key mode (a real key) from subscription mode
// (`apiKey: ""`), which forces and verifies the browser-login credential.
// `CURSOR_API_KEY` is always materialized (empty when unset) so the probe makes
// an explicit set-or-remove decision. `CURSOR_API_BASE_URL` is always cleared —
// the CLI has no custom-endpoint support.
func cursorProbeEnv(d *db.Database, apiKey string, present bool) map[string]string {
	env := agentEnvJSON(d, "cursor")
	if present {
		env["CURSOR_API_KEY"] = strings.TrimSpace(apiKey)
	}
	if _, ok := env["CURSOR_API_KEY"]; !ok {
		env["CURSOR_API_KEY"] = ""
	}
	env["CURSOR_API_BASE_URL"] = ""
	return env
}

// cursorStatusUnverifiedMarker is the exact phrase `cursor-agent status` prints
// when it HAS a stored login but could not use it. Matched as a literal because
// it is one: the CLI emits these strings in English regardless of locale.
const cursorStatusUnverifiedMarker = "unable to fetch user details"

// cursorCredentialVerified reports whether the login `cursor-agent status`
// reports actually worked against Cursor's backend. Deliberately conservative:
// only the CLI's own "I could not reach the backend with this token" branch
// counts as unverified.
func cursorCredentialVerified(v map[string]any) *bool {
	authenticated, _ := v["isAuthenticated"].(bool)
	if !authenticated {
		// Nothing to verify: the panel already renders this as "not signed in".
		return nil
	}
	msg, _ := v["message"].(string)
	verified := !strings.Contains(strings.ToLower(msg), cursorStatusUnverifiedMarker)
	return &verified
}

func cursorAuthStatusFor(d *db.Database, dataDir, apiKey string, present bool) cursorAuthStatus {
	bin := resolveCursorBinary(dataDir)
	if bin == "" {
		return cursorAuthStatus{}
	}
	binPath := bin
	stdout, errMsg := runAgentProbe("cursor-agent", bin, []string{"status", "--format", "json"}, 20, cursorProbeEnv(d, apiKey, present))
	if errMsg != "" {
		e := errMsg
		return cursorAuthStatus{Installed: true, Error: &e, BinaryPath: &binPath}
	}
	// The CLI prints one JSON object; scan to the first `{` so a leading log
	// line can't break parsing.
	idx := strings.IndexByte(stdout, '{')
	if idx < 0 {
		idx = 0
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout[idx:])), &v); err != nil {
		raw := truncateProbeOutput(stdout)
		e := fmt.Sprintf("unexpected status output: %v", err)
		return cursorAuthStatus{Installed: true, RawStatus: &raw, Error: &e, BinaryPath: &binPath}
	}
	getStr := func(keys ...string) *string {
		for _, k := range keys {
			if s, ok := v[k].(string); ok && s != "" {
				return &s
			}
		}
		return nil
	}
	// Email is nested under `userInfo` in current CLI output; fall back to a
	// top-level field for forward-compatibility.
	var email *string
	if ui, ok := v["userInfo"].(map[string]any); ok {
		if s, ok := ui["email"].(string); ok && s != "" {
			email = &s
		}
	}
	if email == nil {
		email = getStr("email", "userEmail", "user_email")
	}
	authenticated, _ := v["isAuthenticated"].(bool)
	return cursorAuthStatus{
		Installed:          true,
		IsAuthenticated:    authenticated,
		RawStatus:          getStr("status", "message"),
		Email:              email,
		Membership:         getStr("membershipType", "membership", "plan"),
		BinaryPath:         &binPath,
		CredentialVerified: cursorCredentialVerified(v),
	}
}

func cursorListModelsFor(d *db.Database, dataDir, apiKey string, present bool) cursorModelsResult {
	stdout, errMsg := runAgentProbe("cursor-agent", resolveCursorBinary(dataDir), []string{"models"}, 30, cursorProbeEnv(d, apiKey, present))
	if errMsg != "" {
		e := errMsg
		return cursorModelsResult{Models: []cursorModelInfo{}, Error: &e}
	}
	models, defaultModel := parseCursorModels(stdout)
	return cursorModelsResult{Models: models, DefaultModel: defaultModel}
}

// parseCursorModels parses `cursor-agent models` output. Each model line is
// `<id> - <label> [(default)]`; a leading `Available models` header and blank
// lines are skipped. Returns the entries in CLI order plus the default id.
func parseCursorModels(stdout string) ([]cursorModelInfo, *string) {
	models := []cursorModelInfo{}
	var defaultModel *string
	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimSpace(stripANSI(line))
		if trimmed == "" {
			continue
		}
		// Split id from label on the first " - ". A line with no " - " is only a
		// model if it's a bare single-token id — otherwise it's prose (the
		// "Available models" header has a space, so it's skipped).
		idRaw, labelRaw := trimmed, ""
		if i := strings.Index(trimmed, " - "); i >= 0 {
			idRaw, labelRaw = trimmed[:i], trimmed[i+len(" - "):]
		}
		id := strings.TrimSpace(strings.TrimLeft(idRaw, "-*\u2022"))
		if id == "" || strings.ContainsAny(id, " \t") {
			continue
		}
		isDefault := strings.Contains(labelRaw, "(default)") || strings.Contains(labelRaw, "(current)")
		label := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(labelRaw, "(default)", ""), "(current)", ""))
		if isDefault {
			d := id
			defaultModel = &d
		}
		duplicate := false
		for _, m := range models {
			if m.ID == id {
				duplicate = true
				break
			}
		}
		if !duplicate {
			models = append(models, cursorModelInfo{ID: id, Label: label, IsDefault: isDefault})
		}
	}
	return models, defaultModel
}

// stripANSI removes ANSI SGR escape sequences from CLI output.
func stripANSI(input string) string {
	var out strings.Builder
	out.Grow(len(input))
	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\x1b' {
			if i+1 < len(runes) && runes[i+1] == '[' {
				i += 2
				for i < len(runes) {
					r := runes[i]
					if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
						break
					}
					i++
				}
			}
			continue
		}
		out.WriteRune(runes[i])
	}
	return out.String()
}

// truncateProbeOutput trims and caps probe output for an error message.
func truncateProbeOutput(s string) string {
	t := strings.TrimSpace(s)
	runes := []rune(t)
	if len(runes) > 400 {
		return string(runes[:400]) + "…"
	}
	return t
}

// ─── shared plumbing ─────────────────────────────────────────────────────────

// runAgentProbe runs a vendor subcommand with a timeout, capturing stdout. The
// extra env is applied on top of the inherited environment: an empty value means
// "ensure absent", so a stale inherited credential can't leak into the probe.
func runAgentProbe(name, bin string, args []string, timeoutSecs int, extraEnv map[string]string) (string, string) {
	if bin == "" {
		return "", name + " is not installed"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSecs)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = probeEnv(extraEnv)
	out, err := cmd.Output()
	stdout := string(out)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Sprintf("%s %s timed out", name, strings.Join(args, " "))
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if strings.TrimSpace(stdout) == "" {
				return "", fmt.Sprintf("%s %s failed: %s", name, strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
			}
			// A non-zero exit with usable stdout is kept (upstream behavior).
			return stdout, ""
		}
		return "", fmt.Sprintf("failed to run %s: %v", name, err)
	}
	return stdout, ""
}

// probeEnv builds the child environment: the inherited env with the probe
// overrides applied, where an empty override removes the variable.
func probeEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		if strings.TrimSpace(v) == "" {
			env = removeEnvKey(env, k)
			continue
		}
		env = setEnvKey(env, k, v)
	}
	return env
}

// envNameEq compares env var names, case-insensitively on Windows.
func envNameEq(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func removeEnvKey(env []string, key string) []string {
	out := env[:0]
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if envNameEq(name, key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func setEnvKey(env []string, key, value string) []string {
	env = removeEnvKey(env, key)
	return append(env, key+"="+value)
}

// agentEnvJSON reads an agent's saved env_json as a string map (empty when the
// row, the column or the JSON is absent).
func agentEnvJSON(d *db.Database, agentType string) map[string]string {
	out := map[string]string{}
	if d == nil || d.Conn == nil {
		return out
	}
	var raw *string
	if err := d.Conn.QueryRow(`SELECT env_json FROM agent_setting WHERE agent_type = ?`, agentType).Scan(&raw); err != nil {
		return out
	}
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return out
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(*raw), &m); err != nil {
		return out
	}
	for k, v := range m {
		out[k] = v
	}
	return out
}
