package handlers

// Remaining acp_* P0: agent status/detection, per-agent settings storage,
// agent ordering, config option forwarding, and conversation→connection
// lookups. Settings persist in the agent_setting table (upstream entity).

import (
	"database/sql"
	"encoding/json"
	"os/exec"

	"yoyocode/src-go/acp"
	"yoyocode/src-go/app_state"
	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// RegisterACPAgents wires the agent-settings + lookup endpoints (registered
// after RegisterACP so it can extend/override the skeletons).
func RegisterACPAgents(reg *Registry) {
	reg.Register("acp_get_agent_status", func(c *web.Context) (any, error) {
		d := stateDB(c)
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		if agentType == "" {
			return nil, web.ErrInvalidInput("agentType required")
		}
		enabled, installedVersion := agentSettingStatus(d, agentType)
		isAdapter := false
		if meta, ok := acp.FindBuiltinAgent(agentType); ok {
			isAdapter = meta.IsACPAdapter
		}
		return map[string]any{
			"agent_type":        agentType,
			"available":         agentLauncherAvailable(agentType),
			"enabled":           enabled,
			"installed_version": installedVersion,
			"is_acp_adapter":    isAdapter,
		}, nil
	})

	reg.Register("acp_update_agent_env", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		if agentType == "" {
			return nil, web.ErrInvalidInput("agentType required")
		}
		enabled := GetBool(c, "enabled")
		envJSON := GetRaw(c, "env")
		var mpid any = nil
		if v := GetInt(c, "modelProviderId", "model_provider_id"); v > 0 {
			mpid = v
		}
		upsertAgentSetting(d, agentType, map[string]any{
			"enabled":           boolToInt(enabled),
			"env_json":          rawStringOrNil(envJSON),
			"model_provider_id": mpid,
		})
		Emit(c, "agents://changed", map[string]any{})
		return 1, nil
	})

	reg.Register("acp_update_agent_config", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		if agentType == "" {
			return nil, web.ErrInvalidInput("agentType required")
		}
		// Config blobs (config_json / codex_* / grok_*) live outside the
		// agent_setting row; persist them under a per-agent app_metadata key so
		// they survive restart and re-apply on connect.
		storeAgentConfigBlobs(d, agentType, c.Body)
		Emit(c, "agents://changed", map[string]any{})
		return 1, nil
	})

	reg.Register("acp_update_agent_preferences", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		if agentType == "" {
			return nil, web.ErrInvalidInput("agentType required")
		}
		upsertAgentSetting(d, agentType, map[string]any{
			"enabled":  boolToInt(GetBool(c, "enabled")),
			"env_json": rawStringOrNil(GetRaw(c, "env")),
		})
		storeAgentConfigBlobs(d, agentType, c.Body)
		Emit(c, "agents://changed", map[string]any{})
		return 1, nil
	})

	reg.Register("acp_reorder_agents", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		order := GetStringSlice(c, "agentTypes", "agent_types")
		for pos, at := range order {
			upsertAgentSetting(d, at, map[string]any{"sort_order": pos})
		}
		Emit(c, "agents://changed", map[string]any{})
		return nil, nil
	})

	reg.Register("acp_set_config_option", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		connID := strVal(c.Body["connectionId"], c.Body["connection_id"])
		configID := strVal(c.Body["configId"], c.Body["config_id"])
		valueID := strVal(c.Body["valueId"], c.Body["value_id"])
		if connID == "" {
			return nil, web.ErrInvalidInput("connectionId required")
		}
		if configID == "" {
			return nil, web.ErrInvalidInput("configId required")
		}
		// Upstream decides the encoding from the option's advertised kind; this
		// build has no option registry yet, so the boolean discriminator is
		// inferred from the value — the selectors only ever emit "true"/"false"
		// for a boolean.
		if err := st.ACP.SetConfigOption(connID, configID, valueID); err != nil {
			return nil, acpError(err)
		}
		return nil, nil
	})

	reg.Register("acp_find_connection_for_conversation", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		convID := int64Num(c.Body["conversationId"])
		if convID == 0 {
			convID = int64Num(c.Body["conversation_id"])
		}
		sessionID := strVal(c.Body["sessionId"], c.Body["session_id"])
		connID, seq, ok := st.ACP.FindConnectionForConversation(convID, sessionID)
		if !ok {
			return nil, nil
		}
		return map[string]any{"connection_id": connID, "event_seq": seq}, nil
	})

	reg.Register("acp_get_session_snapshot_by_conversation", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		convID := int64Num(c.Body["conversationId"])
		if convID == 0 {
			convID = int64Num(c.Body["conversation_id"])
		}
		snap := st.ACP.SnapshotByConversation(convID)
		if snap == nil {
			return nil, nil
		}
		return snap, nil
	})
}

// agentLauncherAvailable reports whether the agent's launcher is on PATH.
func agentLauncherAvailable(agentType string) bool {
	spec, ok := acp.LaunchSpecFor(agentType)
	if !ok {
		return false
	}
	switch spec.Kind {
	case "npx":
		return lookPathOK("npx")
	case "uvx":
		return lookPathOK("uvx")
	default: // binary: the agent's own command must be resolvable
		return lookPathOK(spec.Cmd)
	}
}

func lookPathOK(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func agentSettingStatus(d *db.Database, agentType string) (enabled bool, installedVersion any) {
	enabled = true
	installedVersion = nil
	if d == nil || d.Conn == nil {
		return
	}
	var en sql.NullInt64
	var ver sql.NullString
	err := d.Conn.QueryRow(`SELECT enabled, installed_version FROM agent_setting WHERE agent_type = ?`, agentType).Scan(&en, &ver)
	if err == nil {
		if en.Valid {
			enabled = en.Int64 != 0
		}
		if ver.Valid && ver.String != "" {
			installedVersion = ver.String
		}
	}
	return
}

// upsertAgentSetting creates/updates an agent_setting row with the given
// columns (only the provided keys are written).
func upsertAgentSetting(d *db.Database, agentType string, cols map[string]any) {
	if d == nil || d.Conn == nil {
		return
	}
	now := nowRFC3339()
	registryID := ""
	if meta, ok := acp.FindBuiltinAgent(agentType); ok {
		registryID = meta.RegistryID
	}
	// Ensure the row exists.
	_, _ = d.Conn.Exec(
		`INSERT INTO agent_setting (agent_type, registry_id, created_at, updated_at) VALUES (?,?,?,?) ON CONFLICT(agent_type) DO NOTHING`,
		agentType, registryID, now, now)
	for col, val := range cols {
		_, _ = d.Conn.Exec(`UPDATE agent_setting SET `+col+` = ?, updated_at = ? WHERE agent_type = ?`, val, now, agentType)
	}
}

// storeAgentConfigBlobs persists the per-agent config blobs under app_metadata.
func storeAgentConfigBlobs(d *db.Database, agentType string, body map[string]any) {
	blobKeys := []string{
		"config_json", "configJson", "opencode_auth_json", "opencodeAuthJson",
		"codex_auth_json", "codexAuthJson", "codex_config_toml", "codexConfigToml",
		"codex_model_catalog", "codexModelCatalog", "grok_config_toml", "grokConfigToml",
	}
	collected := map[string]any{}
	for _, k := range blobKeys {
		if v, ok := body[k]; ok && v != nil {
			collected[k] = v
		}
	}
	if len(collected) == 0 {
		return
	}
	raw, _ := json.Marshal(collected)
	upsertValue(d, "acp_agent_config:"+agentType, string(raw), nowRFC3339())
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func rawStringOrNil(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}
