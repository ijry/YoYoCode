package handlers

// Custom (user-registered) ACP agents (upstream commands/custom_agents.rs +
// db/service/custom_agent_service.rs). list / save / delete back the Settings
// "add your own agent" surface. Rows are the persistent form of a
// CustomAgentDef; this is an original Go reimplementation of the wire contract.
//
// The registry-catalog fetch (acp_fetch_registry_catalog) and one-click add
// (acp_add_registry_agent) need the remote ACP registry + npm metadata and are
// left for a later pass; everything here is offline + DB-backed.
//
// Icon handling: upstream downloads+inlines the registry SVG at save time. We
// pass icon_url through as-is (registry rows arrive already inlined; a plain
// data:/https: URL is stored verbatim) rather than making the save path hit
// the network — the field is display-only and never used to launch anything.

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"yoyocode/src-go/acp"
	"yoyocode/src-go/web"
)

// ─── value helpers (mirror acp::custom_registry) ───────────────────────────

const customFallbackVersion = "custom"

var builtinWireNames = map[string]bool{
	"claude_code": true, "codex": true, "open_code": true, "gemini": true,
	"open_claw": true, "cline": true, "hermes": true, "code_buddy": true,
	"kimi_code": true, "pi": true, "grok": true, "cursor": true,
	"deepseek": true, "qoder": true, "antigravity": true,
}

func isValidCustomAgentID(id string) bool {
	if id == "" || len(id) > 64 || strings.HasPrefix(id, ".") {
		return false
	}
	for _, c := range id {
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return !builtinWireNames[id]
}

func isSafePathComponent(s string) bool {
	if s == "" || len(s) > 128 || strings.HasPrefix(s, ".") {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		ok := (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_' || b == '.'
		if !ok {
			return false
		}
	}
	return true
}

// splitArchiveEntry folds backslashes to forward slashes and drops an optional
// trailing "::stream"-style suffix is not modeled; returns the normalized path.
func splitArchiveEntry(raw string) string {
	return strings.ReplaceAll(raw, "\\", "/")
}

func isSafeArchiveEntry(raw string) bool {
	path := splitArchiveEntry(raw)
	if path == "" {
		return true
	}
	if strings.HasPrefix(path, "/") || strings.Contains(path, ":") {
		return false
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func parseDistributionKind(s string) (string, bool) {
	switch s {
	case "npx", "uvx", "binary":
		return s, true
	}
	return "", false
}

func parseCustomSource(s string) string {
	if s == "manual" {
		return "manual"
	}
	return "registry"
}

// customAgentDef is the Go form of CustomAgentDef (post-parse).
type customAgentDef struct {
	registryID        string
	name              string
	description       string
	version           string
	distributionKind  string
	spec              map[string]any // {npx?, uvx?, binary?}
	iconURL           *string
	skillsSharedStore bool
	skillsDir         *string
	source            string
	versionProbe      *string
	supportsMcp       bool
}

func (d *customAgentDef) effectiveVersion() string {
	v := strings.TrimSpace(d.version)
	if v == "" {
		return customFallbackVersion
	}
	return v
}

// validateCustomDef mirrors custom_registry::validate — returns "" when the
// definition could launch, else a human-readable problem.
func validateCustomDef(d *customAgentDef) string {
	// Builtin registry-id collision first (the actionable diagnosis).
	if _, ok := acp.AgentWireForRegistryID(d.registryID); ok {
		return "'" + d.registryID + "' is a built-in agent; delete this entry"
	}
	if !isValidCustomAgentID(d.registryID) {
		return "invalid custom agent id: " + d.registryID
	}
	if strings.TrimSpace(d.name) == "" {
		return "agent name cannot be empty"
	}
	if !isSafePathComponent(d.effectiveVersion()) {
		return "unsafe version: " + d.effectiveVersion()
	}
	switch d.distributionKind {
	case "npx":
		npx := childObj(d.spec, "npx")
		if npx == nil {
			return "missing npx channel"
		}
		if strings.TrimSpace(caStr(npx, "package")) == "" {
			return "missing npx package"
		}
	case "uvx":
		uvx := childObj(d.spec, "uvx")
		if uvx == nil {
			return "missing uvx channel"
		}
		if strings.TrimSpace(caStr(uvx, "package")) == "" {
			return "missing uvx package"
		}
	case "binary":
		bin := childObj(d.spec, "binary")
		if len(bin) == 0 {
			return "missing binary channel"
		}
		cur := acpCurrentPlatform()
		here := childObj(bin, cur)
		if here == nil {
			return "no binary for this platform: " + cur
		}
		archive := firstNonEmptyStr(caStr(here, "archive"), caStr(here, "url"))
		if strings.TrimSpace(archive) == "" {
			return "missing archive url for " + cur
		}
		for platform, v := range bin {
			if m, ok := v.(map[string]any); ok {
				if !isSafeArchiveEntry(caStr(m, "cmd")) {
					return "unsafe archive entry for " + platform
				}
			}
		}
	default:
		return "unknown distribution kind: " + d.distributionKind
	}
	return ""
}

func childObj(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func customExpandHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	if rest, ok := strings.CutPrefix(path, "~\\"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

func normalizeSkillsDir(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, nil
	}
	expanded := customExpandHome(trimmed)
	if !filepath.IsAbs(expanded) {
		return nil, web.ErrTaskExecution("skills directory must be an absolute path (got " + trimmed + ")")
	}
	return &expanded, nil
}

// ─── wire info ─────────────────────────────────────────────────────────────

func customAgentInfo(d *customAgentDef) map[string]any {
	problem := validateCustomDef(d)
	var problemVal any
	launchable := true
	if problem != "" {
		problemVal = problem
		launchable = false
	}
	var iconVal, skillsDirVal, versionProbeVal any
	if d.iconURL != nil {
		iconVal = *d.iconURL
	}
	if d.skillsDir != nil {
		skillsDirVal = *d.skillsDir
	}
	if d.versionProbe != nil {
		versionProbeVal = *d.versionProbe
	}
	spec := d.spec
	if spec == nil {
		spec = map[string]any{}
	}
	return map[string]any{
		"registryId":        d.registryID,
		"agentType":         "custom:" + d.registryID,
		"name":              d.name,
		"description":       d.description,
		"version":           d.version,
		"distributionKind":  d.distributionKind,
		"spec":              spec,
		"iconUrl":           iconVal,
		"skillsSharedStore": d.skillsSharedStore,
		"skillsDir":         skillsDirVal,
		"source":            d.source,
		"versionProbe":      versionProbeVal,
		"supportsMcp":       d.supportsMcp,
		"launchable":        launchable,
		"problem":           problemVal,
	}
}

// ─── DB layer ──────────────────────────────────────────────────────────────

func defFromRow(registryID, name, description, version, distKind, specJSON string,
	iconURL *string, skillsShared bool, skillsDir *string, source string,
	versionProbe *string, supportsMcp bool) (*customAgentDef, bool) {
	var spec map[string]any
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		return nil, false
	}
	kind, ok := parseDistributionKind(distKind)
	if !ok {
		return nil, false
	}
	vp := versionProbe
	if vp != nil {
		t := strings.TrimSpace(*vp)
		if t == "" {
			vp = nil
		} else {
			vp = &t
		}
	}
	return &customAgentDef{
		registryID: registryID, name: name, description: description, version: version,
		distributionKind: kind, spec: spec, iconURL: iconURL,
		skillsSharedStore: skillsShared, skillsDir: skillsDir,
		source: parseCustomSource(source), versionProbe: vp, supportsMcp: supportsMcp,
	}, true
}

func listCustomAgentDefs(conn *sql.DB) ([]*customAgentDef, error) {
	rows, err := conn.Query(`SELECT registry_id, name, description, version, distribution_kind,
		spec_json, icon_url, skills_shared_store, skills_dir, source, version_probe, supports_mcp
		FROM custom_agent ORDER BY registry_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*customAgentDef
	for rows.Next() {
		var registryID, name, description, version, distKind, specJSON, source string
		var iconURL, skillsDir, versionProbe sql.NullString
		var skillsShared, supportsMcp int
		if err := rows.Scan(&registryID, &name, &description, &version, &distKind, &specJSON,
			&iconURL, &skillsShared, &skillsDir, &source, &versionProbe, &supportsMcp); err != nil {
			return nil, err
		}
		d, ok := defFromRow(registryID, name, description, version, distKind, specJSON,
			caNullStr(iconURL), skillsShared != 0, caNullStr(skillsDir), source, caNullStr(versionProbe), supportsMcp != 0)
		if ok {
			out = append(out, d)
		}
	}
	return out, rows.Err()
}

func caNullStr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func getCustomAgentRow(conn *sql.DB, registryID string) (source string, supportsMcp bool, found bool) {
	var sm int
	err := conn.QueryRow(`SELECT source, supports_mcp FROM custom_agent WHERE registry_id = ?`, registryID).Scan(&source, &sm)
	if err != nil {
		return "", false, false
	}
	return source, sm != 0, true
}

// ─── handlers ──────────────────────────────────────────────────────────────

// RegisterACPCustomAgents wires acp_list/save/delete_custom_agent.
func RegisterACPCustomAgents(reg *Registry) {
	reg.Register("acp_list_custom_agents", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		defs, lerr := listCustomAgentDefs(d.Conn)
		if lerr != nil {
			return nil, web.ErrTaskExecution(lerr.Error())
		}
		out := make([]map[string]any, 0, len(defs))
		for _, def := range defs {
			out = append(out, customAgentInfo(def))
		}
		return out, nil
	})

	reg.Register("acp_save_custom_agent", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		// Body is {params: {...}}; tolerate a bare body too.
		params, _ := c.Body["params"].(map[string]any)
		if params == nil {
			params = c.Body
		}
		registryID := strings.TrimSpace(caStr(params, "registry_id", "registryId"))
		distKindRaw := caStr(params, "distribution_kind", "distributionKind")
		distKind, ok := parseDistributionKind(distKindRaw)
		if !ok {
			return nil, web.ErrTaskExecution("unknown distribution kind: " + distKindRaw)
		}
		spec, _ := params["spec"].(map[string]any)
		if spec == nil {
			spec = map[string]any{}
		}
		def := &customAgentDef{
			registryID:        registryID,
			name:              caStr(params, "name"),
			description:       caStr(params, "description"),
			version:           caStr(params, "version"),
			distributionKind:  distKind,
			spec:              spec,
			iconURL:           caOptStr(params, "icon_url", "iconUrl"),
			skillsSharedStore: caBoolVal(params, "skills_shared_store", "skillsSharedStore"),
			skillsDir:         caOptStr(params, "skills_dir", "skillsDir"),
		}
		if vp := caOptStr(params, "version_probe", "versionProbe"); vp != nil {
			t := strings.TrimSpace(*vp)
			if t != "" {
				def.versionProbe = &t
			}
		}
		// Resolve source + supports_mcp against the stored row when the caller
		// did not state them (mirrors acp_save_custom_agent_params_core).
		storedSource, storedMcp, had := getCustomAgentRow(d.Conn, registryID)
		if explicit := caOptStr(params, "source"); explicit != nil && strings.TrimSpace(*explicit) != "" {
			def.source = parseCustomSource(strings.TrimSpace(*explicit))
		} else if had {
			def.source = parseCustomSource(storedSource)
		} else {
			def.source = "manual"
		}
		if mcp, ok := caOptBool(params, "supports_mcp", "supportsMcp"); ok {
			def.supportsMcp = mcp
		} else if had {
			def.supportsMcp = storedMcp
		} else {
			def.supportsMcp = true
		}
		// Normalize + validate before persisting.
		nd, nerr := normalizeSkillsDir(def.skillsDir)
		if nerr != nil {
			return nil, nerr
		}
		def.skillsDir = nd
		if problem := validateCustomDef(def); problem != "" {
			return nil, web.ErrTaskExecution(problem)
		}
		if err := upsertCustomAgent(d.Conn, def); err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		return nil, nil
	})

	reg.Register("acp_delete_custom_agent", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		registryID := strings.TrimSpace(caStr(c.Body, "registry_id", "registryId"))
		if registryID == "" {
			return nil, web.ErrInvalidInput("registryId required")
		}
		res, derr := d.Conn.Exec(`DELETE FROM custom_agent WHERE registry_id = ?`, registryID)
		if derr != nil {
			return nil, web.ErrTaskExecution(derr.Error())
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil, web.ErrTaskExecution("custom agent not found: " + registryID)
		}
		return nil, nil
	})
}

func upsertCustomAgent(conn *sql.DB, def *customAgentDef) error {
	specBytes, err := json.Marshal(def.spec)
	if err != nil {
		return err
	}
	version := strings.TrimSpace(def.version)
	if version == "" {
		version = customFallbackVersion
	}
	now := nowRFC3339()
	_, err = conn.Exec(`INSERT INTO custom_agent
		(registry_id, name, description, version, distribution_kind, spec_json, icon_url,
		 skills_shared_store, skills_dir, source, version_probe, supports_mcp, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(registry_id) DO UPDATE SET
			name=excluded.name, description=excluded.description, version=excluded.version,
			distribution_kind=excluded.distribution_kind, spec_json=excluded.spec_json,
			icon_url=excluded.icon_url, skills_shared_store=excluded.skills_shared_store,
			skills_dir=excluded.skills_dir, source=excluded.source,
			version_probe=excluded.version_probe, supports_mcp=excluded.supports_mcp,
			updated_at=excluded.updated_at`,
		def.registryID, strings.TrimSpace(def.name), strings.TrimSpace(def.description), version,
		def.distributionKind, string(specBytes), nullableStr(def.iconURL),
		boolToInt(def.skillsSharedStore), nullableStr(def.skillsDir),
		def.source, nullableStr(def.versionProbe), boolToInt(def.supportsMcp), now, now)
	return err
}

func nullableStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// cstr reads a string param by aliases, returning the first non-empty one.
func caStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if sv, ok := v.(string); ok && sv != "" {
				return sv
			}
		}
	}
	return ""
}

// optStr reads a string param (aliases) and returns a pointer, or nil when
// absent. An explicit empty string is returned as a pointer to "".
func caOptStr(m map[string]any, keys ...string) *string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return &s
			}
		}
	}
	return nil
}

func caOptBool(m map[string]any, keys ...string) (bool, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if b, ok := v.(bool); ok {
				return b, true
			}
		}
	}
	return false, false
}

func caBoolVal(m map[string]any, keys ...string) bool {
	b, _ := caOptBool(m, keys...)
	return b
}
