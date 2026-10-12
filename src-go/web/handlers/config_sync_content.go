package handlers

// config_sync content export / import / rollback (upstream
// commands/config_sync/{local_io,snapshot,domains}.rs). A portable settings
// snapshot: a JSON envelope { upstreamConfigExport, manifest, config } where
// config.domains maps a domain id to its payload. Rows are matched on their
// natural key and upserted (never wiped), and an import first writes a
// pre-apply rollback snapshot so it can be undone. The modelProviders domain is
// intentionally omitted (model-provider config is out of scope); unknown
// domains in an imported file are ignored, exactly as upstream degrades.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const configSnapshotSchemaVersion = 1
const configRollbackKeep = 10
const configMaxImportBytes = 8 * 1024 * 1024

// portablePreferenceKeys mirrors PORTABLE_PREFERENCE_KEYS: the app_metadata
// keys safe to carry between machines.
var portablePreferenceKeys = []string{
	"system_language_settings", "appearance_mode", "appearance_zoom_level",
	"system_close_behavior_settings", "logging.level", "delegation.enabled",
	"delegation.depth_limit", "delegation.agent_defaults", "delegation.completed_cache_max_mb",
	"feedback.enabled", "question.enabled", "session_info.enabled",
	"chat_authoring.automations_enabled", "chat_authoring.work_tasks_enabled",
	"chat_command_prefix", "chat_message_language", "chat_event_filter",
	"system_proxy_settings", "system_terminal_settings", "web_service_port",
	"web_service_token", "web_service_auto_start", "github_accounts",
	"chat_event_webhooks", "git_settings", "pet.config", "forge_workbench_settings",
	"canvas_revision", "opened_tabs_version", "token_usage_fact_schema_version",
}

func isPortablePrefKey(key string) bool {
	for _, k := range portablePreferenceKeys {
		if k == key {
			return true
		}
	}
	return false
}

// ── snapshot model ──

type configSnapshot struct {
	SchemaVersion int            `json:"schemaVersion"`
	Domains       map[string]any `json:"domains"`
}

type configFileMeta struct {
	Size   int64  `json:"size"`
	Sha256 string `json:"sha256"`
}

type configManifest struct {
	SchemaVersion int            `json:"schemaVersion"`
	Encryption    string         `json:"encryption"`
	CreatedAt     string         `json:"createdAt"`
	AppVersion    string         `json:"appVersion"`
	SourceDevice  string         `json:"sourceDevice"`
	Config        configFileMeta `json:"config"`
	Counts        map[string]int `json:"counts"`
}

type configExportFile struct {
	YoyocodeConfigExport int            `json:"codegConfigExport"`
	Manifest             configManifest `json:"manifest"`
	Config               configSnapshot `json:"config"`
}

// ── collect (DB → domain payloads) ──

func collectConfigSnapshot(d *db.Database) (configSnapshot, error) {
	domains := map[string]any{}
	as, err := collectAgentSettingsDomain(d)
	if err != nil {
		return configSnapshot{}, err
	}
	domains["agentSettings"] = as
	ca, err := collectCustomAgentsDomain(d)
	if err != nil {
		return configSnapshot{}, err
	}
	domains["customAgents"] = ca
	qm, err := collectQuickMessagesDomain(d)
	if err != nil {
		return configSnapshot{}, err
	}
	domains["quickMessages"] = qm
	tt, err := collectTaskTemplatesDomain(d)
	if err != nil {
		return configSnapshot{}, err
	}
	domains["taskTemplates"] = tt
	domains["preferences"] = collectPreferencesDomain(d)
	return configSnapshot{SchemaVersion: configSnapshotSchemaVersion, Domains: domains}, nil
}

type providerRef struct {
	AgentType string `json:"agentType"`
	Name      string `json:"name"`
}

type agentSettingDTO struct {
	AgentType  string       `json:"agentType"`
	RegistryID string       `json:"registryId"`
	Enabled    bool         `json:"enabled"`
	SortOrder  int          `json:"sortOrder"`
	EnvJSON    *string      `json:"envJson"`
	Provider   *providerRef `json:"provider"`
}

func collectAgentSettingsDomain(d *db.Database) ([]agentSettingDTO, error) {
	rows, err := d.Conn.Query(`SELECT agent_type, registry_id, enabled, sort_order, env_json, model_provider_id FROM agent_setting ORDER BY id ASC`)
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	defer rows.Close()
	out := []agentSettingDTO{}
	for rows.Next() {
		var at, rid string
		var enabled int
		var sortOrder int
		var envJSON sql.NullString
		var provID sql.NullInt64
		if rows.Scan(&at, &rid, &enabled, &sortOrder, &envJSON, &provID) != nil {
			continue
		}
		dto := agentSettingDTO{AgentType: at, RegistryID: rid, Enabled: enabled != 0, SortOrder: sortOrder}
		if envJSON.Valid {
			v := envJSON.String
			dto.EnvJSON = &v
		}
		if provID.Valid {
			var pat, pname string
			if d.Conn.QueryRow(`SELECT agent_type, name FROM model_provider WHERE id = ?`, provID.Int64).Scan(&pat, &pname) == nil {
				dto.Provider = &providerRef{AgentType: pat, Name: pname}
			}
		}
		out = append(out, dto)
	}
	return out, nil
}

type customAgentDTO struct {
	RegistryID        string  `json:"registryId"`
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	Version           string  `json:"version"`
	DistributionKind  string  `json:"distributionKind"`
	SpecJSON          string  `json:"specJson"`
	IconURL           *string `json:"iconUrl"`
	SkillsSharedStore bool    `json:"skillsSharedStore"`
	Source            string  `json:"source"`
	VersionProbe      *string `json:"versionProbe"`
	SupportsMcp       bool    `json:"supportsMcp"`
}

func collectCustomAgentsDomain(d *db.Database) ([]customAgentDTO, error) {
	rows, err := d.Conn.Query(`SELECT registry_id, name, description, version, distribution_kind, spec_json, icon_url, skills_shared_store, source, version_probe, supports_mcp FROM custom_agent ORDER BY id ASC`)
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	defer rows.Close()
	out := []customAgentDTO{}
	for rows.Next() {
		var rid, name, desc, ver, dk, spec, source string
		var icon, probe sql.NullString
		var shared, mcp int
		if rows.Scan(&rid, &name, &desc, &ver, &dk, &spec, &icon, &shared, &source, &probe, &mcp) != nil {
			continue
		}
		dto := customAgentDTO{RegistryID: rid, Name: name, Description: desc, Version: ver, DistributionKind: dk, SpecJSON: spec, SkillsSharedStore: shared != 0, Source: source, SupportsMcp: mcp != 0}
		if icon.Valid {
			v := icon.String
			dto.IconURL = &v
		}
		if probe.Valid {
			v := probe.String
			dto.VersionProbe = &v
		}
		out = append(out, dto)
	}
	return out, nil
}

type quickMessageDTO struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	SortOrder int    `json:"sortOrder"`
}

func collectQuickMessagesDomain(d *db.Database) ([]quickMessageDTO, error) {
	rows, err := d.Conn.Query(`SELECT title, content, sort_order FROM quick_message ORDER BY id ASC`)
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	defer rows.Close()
	out := []quickMessageDTO{}
	for rows.Next() {
		var t, c string
		var so int
		if rows.Scan(&t, &c, &so) != nil {
			continue
		}
		out = append(out, quickMessageDTO{Title: t, Content: c, SortOrder: so})
	}
	return out, nil
}

type taskTemplateDTO struct {
	Name   string `json:"name"`
	Title  string `json:"title"`
	Config string `json:"config"`
}

func collectTaskTemplatesDomain(d *db.Database) ([]taskTemplateDTO, error) {
	rows, err := d.Conn.Query(`SELECT name, title, config FROM work_task_template ORDER BY id ASC`)
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	defer rows.Close()
	out := []taskTemplateDTO{}
	for rows.Next() {
		var n, t, cfg string
		if rows.Scan(&n, &t, &cfg) != nil {
			continue
		}
		out = append(out, taskTemplateDTO{Name: n, Title: t, Config: cfg})
	}
	return out, nil
}

func collectPreferencesDomain(d *db.Database) map[string]string {
	out := map[string]string{}
	rows, err := d.Conn.Query(`SELECT key, value FROM app_metadata WHERE deleted_at IS NULL`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) != nil {
			continue
		}
		if isPortablePrefKey(k) {
			out[k] = v
		}
	}
	return out
}

// ── counts ──

func snapshotCounts(s configSnapshot) map[string]int {
	counts := map[string]int{}
	for id, v := range s.Domains {
		counts[id] = countDomainEntries(id, v)
	}
	return counts
}

func countDomainEntries(id string, v any) int {
	if id == "preferences" {
		n := 0
		switch m := v.(type) {
		case map[string]string:
			for k, val := range m {
				if isPortablePrefKey(k) && val != "" {
					n++
				}
			}
		case map[string]any:
			for k, val := range m {
				if _, ok := val.(string); ok && isPortablePrefKey(k) {
					n++
				}
			}
		}
		return n
	}
	// array domains → element count
	switch arr := v.(type) {
	case []any:
		return len(arr)
	case []agentSettingDTO:
		return len(arr)
	case []customAgentDTO:
		return len(arr)
	case []quickMessageDTO:
		return len(arr)
	case []taskTemplateDTO:
		return len(arr)
	}
	return 0
}

// ── serialize + manifest ──

func serializeSnapshot(s configSnapshot) ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, web.ErrTaskExecution("Serialize config snapshot: " + err.Error())
	}
	return b, nil
}

func buildManifest(configBytes []byte, counts map[string]int) configManifest {
	sum := sha256.Sum256(configBytes)
	host, _ := os.Hostname()
	return configManifest{
		SchemaVersion: configSnapshotSchemaVersion,
		Encryption:    "none",
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		AppVersion:    appVersionString(),
		SourceDevice:  host,
		Config:        configFileMeta{Size: int64(len(configBytes)), Sha256: hex.EncodeToString(sum[:])},
		Counts:        counts,
	}
}

func buildExport(d *db.Database) (configExportFile, error) {
	snapshot, err := collectConfigSnapshot(d)
	if err != nil {
		return configExportFile{}, err
	}
	bytes, err := serializeSnapshot(snapshot)
	if err != nil {
		return configExportFile{}, err
	}
	return configExportFile{
		YoyocodeConfigExport: 1,
		Manifest:             buildManifest(bytes, snapshotCounts(snapshot)),
		Config:               snapshot,
	}, nil
}

// ── parse ──

func parseConfigSnapshot(bytes []byte) (configSnapshot, error) {
	var s configSnapshot
	if err := json.Unmarshal(bytes, &s); err != nil {
		return configSnapshot{}, web.ErrInvalidInput("Not a YoYoCode config snapshot").WithDetail(err.Error())
	}
	if s.SchemaVersion > configSnapshotSchemaVersion {
		return configSnapshot{}, web.ErrInvalidInput("Snapshot was written by a newer version of YoYoCode")
	}
	return s, nil
}

func parseExportBytes(bytes []byte) (configExportFile, error) {
	if len(bytes) > configMaxImportBytes {
		return configExportFile{}, web.ErrInvalidInput("Config file is too large")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(bytes, &probe); err != nil {
		return configExportFile{}, web.ErrInvalidInput("Not a YoYoCode config file").WithDetail(err.Error())
	}
	if _, ok := probe["codegConfigExport"]; ok {
		var export configExportFile
		if err := json.Unmarshal(bytes, &export); err != nil {
			return configExportFile{}, web.ErrInvalidInput("Malformed YoYoCode config export").WithDetail(err.Error())
		}
		if export.Config.SchemaVersion > configSnapshotSchemaVersion {
			return configExportFile{}, web.ErrInvalidInput("Snapshot was written by a newer version of YoYoCode")
		}
		return export, nil
	}
	// Bare config.json → synthesize an envelope.
	snapshot, err := parseConfigSnapshot(bytes)
	if err != nil {
		return configExportFile{}, err
	}
	canonical, _ := serializeSnapshot(snapshot)
	m := buildManifest(canonical, snapshotCounts(snapshot))
	m.AppVersion = "unknown"
	return configExportFile{YoyocodeConfigExport: 1, Manifest: m, Config: snapshot}, nil
}

// ── apply ──

func applySnapshot(d *db.Database, s configSnapshot) (map[string]int, int, error) {
	tx, err := d.Conn.Begin()
	if err != nil {
		return nil, 0, web.ErrDatabase(err.Error())
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	report := map[string]int{}
	total := 0
	// Order: agentSettings, customAgents, quickMessages, taskTemplates, preferences.
	order := []string{"agentSettings", "customAgents", "quickMessages", "taskTemplates", "preferences"}
	for _, id := range order {
		raw, ok := s.Domains[id]
		if !ok || raw == nil {
			continue
		}
		rawJSON, _ := json.Marshal(raw)
		n, aerr := applyDomain(tx, id, rawJSON)
		if aerr != nil {
			return nil, 0, aerr
		}
		report[id] = n
		total += n
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, web.ErrDatabase("Commit config apply: " + err.Error())
	}
	committed = true
	return report, total, nil
}

func applyDomain(tx *sql.Tx, id string, rawJSON []byte) (int, error) {
	now := nowRFC3339()
	switch id {
	case "agentSettings":
		var rows []agentSettingDTO
		if json.Unmarshal(rawJSON, &rows) != nil {
			return 0, web.ErrInvalidInput("Snapshot domain 'agentSettings' is malformed")
		}
		for _, dto := range rows {
			var provID any
			if dto.Provider != nil {
				var pid int64
				if tx.QueryRow(`SELECT id FROM model_provider WHERE agent_type = ? AND name = ?`, dto.Provider.AgentType, dto.Provider.Name).Scan(&pid) == nil {
					provID = pid
				}
			}
			var envJSON any
			if dto.EnvJSON != nil {
				envJSON = *dto.EnvJSON
			}
			var existing int64
			if tx.QueryRow(`SELECT id FROM agent_setting WHERE agent_type = ?`, dto.AgentType).Scan(&existing) == nil {
				if dto.RegistryID != "" {
					_, _ = tx.Exec(`UPDATE agent_setting SET registry_id = ?, enabled = ?, sort_order = ?, env_json = ?, model_provider_id = ?, updated_at = ? WHERE id = ?`,
						dto.RegistryID, boolToInt(dto.Enabled), dto.SortOrder, envJSON, provID, now, existing)
				} else {
					_, _ = tx.Exec(`UPDATE agent_setting SET enabled = ?, sort_order = ?, env_json = ?, model_provider_id = ?, updated_at = ? WHERE id = ?`,
						boolToInt(dto.Enabled), dto.SortOrder, envJSON, provID, now, existing)
				}
			} else {
				regID := dto.RegistryID
				if regID == "" {
					regID = dto.AgentType
				}
				_, _ = tx.Exec(`INSERT INTO agent_setting (agent_type, registry_id, enabled, sort_order, installed_version, env_json, model_provider_id, created_at, updated_at) VALUES (?,?,?,?,NULL,?,?,?,?)`,
					dto.AgentType, regID, boolToInt(dto.Enabled), dto.SortOrder, envJSON, provID, now, now)
			}
		}
		return len(rows), nil
	case "customAgents":
		var rows []customAgentDTO
		if json.Unmarshal(rawJSON, &rows) != nil {
			return 0, web.ErrInvalidInput("Snapshot domain 'customAgents' is malformed")
		}
		for _, dto := range rows {
			var icon, probe any
			if dto.IconURL != nil {
				icon = *dto.IconURL
			}
			if dto.VersionProbe != nil {
				probe = *dto.VersionProbe
			}
			var existing int64
			if tx.QueryRow(`SELECT id FROM custom_agent WHERE registry_id = ?`, dto.RegistryID).Scan(&existing) == nil {
				_, _ = tx.Exec(`UPDATE custom_agent SET name = ?, description = ?, version = ?, distribution_kind = ?, spec_json = ?, icon_url = ?, skills_shared_store = ?, source = ?, version_probe = ?, supports_mcp = ?, updated_at = ? WHERE id = ?`,
					dto.Name, dto.Description, dto.Version, dto.DistributionKind, dto.SpecJSON, icon, boolToInt(dto.SkillsSharedStore), dto.Source, probe, boolToInt(dto.SupportsMcp), now, existing)
			} else {
				_, _ = tx.Exec(`INSERT INTO custom_agent (registry_id, name, description, version, distribution_kind, spec_json, icon_url, skills_shared_store, skills_dir, source, version_probe, supports_mcp, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,NULL,?,?,?,?,?)`,
					dto.RegistryID, dto.Name, dto.Description, dto.Version, dto.DistributionKind, dto.SpecJSON, icon, boolToInt(dto.SkillsSharedStore), dto.Source, probe, boolToInt(dto.SupportsMcp), now, now)
			}
		}
		return len(rows), nil
	case "quickMessages":
		var rows []quickMessageDTO
		if json.Unmarshal(rawJSON, &rows) != nil {
			return 0, web.ErrInvalidInput("Snapshot domain 'quickMessages' is malformed")
		}
		for _, dto := range rows {
			var existing int64
			if tx.QueryRow(`SELECT id FROM quick_message WHERE title = ? ORDER BY id ASC LIMIT 1`, dto.Title).Scan(&existing) == nil {
				_, _ = tx.Exec(`UPDATE quick_message SET content = ?, sort_order = ?, updated_at = ? WHERE id = ?`, dto.Content, dto.SortOrder, now, existing)
			} else {
				_, _ = tx.Exec(`INSERT INTO quick_message (title, content, sort_order, created_at, updated_at) VALUES (?,?,?,?,?)`, dto.Title, dto.Content, dto.SortOrder, now, now)
			}
		}
		return len(rows), nil
	case "taskTemplates":
		var rows []taskTemplateDTO
		if json.Unmarshal(rawJSON, &rows) != nil {
			return 0, web.ErrInvalidInput("Snapshot domain 'taskTemplates' is malformed")
		}
		for _, dto := range rows {
			var existing int64
			if tx.QueryRow(`SELECT id FROM work_task_template WHERE name = ?`, dto.Name).Scan(&existing) == nil {
				_, _ = tx.Exec(`UPDATE work_task_template SET title = ?, config = ?, updated_at = ? WHERE id = ?`, dto.Title, dto.Config, now, existing)
			} else {
				_, _ = tx.Exec(`INSERT INTO work_task_template (name, title, config, created_at, updated_at) VALUES (?,?,?,?,?)`, dto.Name, dto.Title, dto.Config, now, now)
			}
		}
		return len(rows), nil
	case "preferences":
		var m map[string]any
		if json.Unmarshal(rawJSON, &m) != nil {
			return 0, nil
		}
		applied := 0
		for k, v := range m {
			if !isPortablePrefKey(k) {
				continue
			}
			s, ok := v.(string)
			if !ok {
				continue
			}
			_, _ = tx.Exec(`INSERT INTO app_metadata (key, value, created_at, updated_at) VALUES (?,?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, k, s, now, now)
			applied++
		}
		return applied, nil
	}
	return 0, nil
}

// ── rollback files ──

func configSnapshotDir() string {
	root := strings.TrimSpace(os.Getenv("YOYOCODE_HOME"))
	if root == "" {
		root = filepath.Join(skillHomeDir(), ".yoyocode")
	}
	return filepath.Join(root, "config-snapshots")
}

func isRollbackID(id string) bool {
	stamp, ok := strings.CutPrefix(id, "config-")
	if !ok || stamp == "" {
		return false
	}
	for _, b := range stamp {
		if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')) {
			return false
		}
	}
	return true
}

func createdAtFromID(id string) any {
	stamp, ok := strings.CutPrefix(id, "config-")
	if !ok || len(stamp) < 15 {
		return nil
	}
	t, err := time.Parse("20060102T150405", stamp[:15])
	if err != nil {
		return nil
	}
	ms := 0
	if len(stamp) >= 18 {
		for _, c := range stamp[15:18] {
			if c < '0' || c > '9' {
				ms = 0
				break
			}
			ms = ms*10 + int(c-'0')
		}
	}
	t = t.Add(time.Duration(ms) * time.Millisecond).UTC()
	return t.Format(time.RFC3339)
}

func writeRollbackSnapshot(dir string, s configSnapshot) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", web.ErrIO(err.Error())
	}
	bytes, err := serializeSnapshot(s)
	if err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	ms := time.Now().UTC().Nanosecond() / 1e6
	name := "config-" + stamp + padMs(ms) + ".json"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, bytes, 0o644); err != nil {
		return "", web.ErrIO(err.Error())
	}
	pruneRollbacks(dir, configRollbackKeep)
	return path, nil
}

func padMs(ms int) string {
	s := itoaInt(ms)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

func listRollbackFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, "config-") && strings.HasSuffix(n, ".json") {
			names = append(names, n)
		}
	}
	// Names are timestamp-ordered → lexicographic desc is newest-first.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names
}

func pruneRollbacks(dir string, keep int) {
	names := listRollbackFiles(dir)
	for i := keep; i < len(names); i++ {
		_ = os.Remove(filepath.Join(dir, names[i]))
	}
}

func resolveRollback(dir, id string) (string, error) {
	if !isRollbackID(id) {
		return "", web.ErrNotFound("That rollback snapshot is no longer on this machine")
	}
	path := filepath.Join(dir, id+".json")
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return "", web.ErrNotFound("That rollback snapshot is no longer on this machine")
	}
	return path, nil
}

func listRollbacksInfos(dir string) []map[string]any {
	out := []map[string]any{}
	for _, name := range listRollbackFiles(dir) {
		id := strings.TrimSuffix(name, ".json")
		bytes, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		snapshot, perr := parseConfigSnapshot(bytes)
		if perr != nil {
			continue
		}
		out = append(out, map[string]any{
			"id":        id,
			"createdAt": createdAtFromID(id),
			"size":      int64(len(bytes)),
			"counts":    snapshotCounts(snapshot),
		})
	}
	return out
}

// saveRollback captures current state; best-effort (nil path on failure).
func saveRollback(d *db.Database, dir string) any {
	snapshot, err := collectConfigSnapshot(d)
	if err != nil {
		return nil
	}
	path, werr := writeRollbackSnapshot(dir, snapshot)
	if werr != nil {
		return nil
	}
	return path
}

func applyImport(d *db.Database, s configSnapshot, dir string) (map[string]any, error) {
	rollbackPath := saveRollback(d, dir)
	report, total, err := applySnapshot(d, s)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"applied":      map[string]any{"domains": report, "total": total},
		"rollbackPath": rollbackPath,
	}, nil
}

// ── handlers ──

// RegisterConfigSyncContent wires export/peek/import + rollback endpoints.
func RegisterConfigSyncContent(reg *Registry) {
	reg.Register("config_sync_export_content", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		export, berr := buildExport(d)
		if berr != nil {
			return nil, berr
		}
		content, merr := json.MarshalIndent(export, "", "  ")
		if merr != nil {
			return nil, web.ErrTaskExecution("Serialize config export: " + merr.Error())
		}
		return map[string]any{"content": string(content), "counts": export.Manifest.Counts}, nil
	})

	reg.Register("config_sync_peek_content", func(c *web.Context) (any, error) {
		content := GetString(c, "content")
		export, perr := parseExportBytes([]byte(content))
		if perr != nil {
			return nil, perr
		}
		return map[string]any{"manifest": export.Manifest, "counts": snapshotCounts(export.Config)}, nil
	})

	reg.Register("config_sync_import_content", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		content := GetString(c, "content")
		export, perr := parseExportBytes([]byte(content))
		if perr != nil {
			return nil, perr
		}
		return applyImport(d, export.Config, configSnapshotDir())
	})

	reg.Register("config_sync_list_rollbacks", func(c *web.Context) (any, error) {
		return listRollbacksInfos(configSnapshotDir()), nil
	})

	reg.Register("config_sync_apply_rollback", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id := strings.TrimSpace(GetString(c, "id"))
		dir := configSnapshotDir()
		path, rerr := resolveRollback(dir, id)
		if rerr != nil {
			return nil, rerr
		}
		bytes, ierr := os.ReadFile(path)
		if ierr != nil {
			return nil, web.ErrIO(ierr.Error())
		}
		snapshot, serr := parseConfigSnapshot(bytes)
		if serr != nil {
			return nil, serr
		}
		return applyImport(d, snapshot, dir)
	})
}
