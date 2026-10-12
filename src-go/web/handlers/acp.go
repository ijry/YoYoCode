package handlers

import (
	"errors"
	"strconv"

	"yoyocode/src-go/acp"
	"yoyocode/src-go/app_state"
	"yoyocode/src-go/web"
)

// RegisterACP wires the acp_* command family.
func RegisterACP(reg *Registry) {
	reg.web.Register("acp_list_agents", func(c *web.Context) (any, error) {
		// Faithful AcpAgentInfo rows from the built-in catalog. availability /
		// installed version are stamped by preflight once the spawn layer lands.
		return acp.BuiltinAgentInfoList(), nil
	})
	reg.web.Register("acp_list_connections", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		conns := []map[string]any{}
		for _, conn := range st.ACP.ListConnections() {
			conns = append(conns, connectionInfo(conn))
		}
		return conns, nil
	})
	reg.web.Register("acp_connect", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		agentType := strVal(c.Body["agentType"])
		if agentType == "" {
			return nil, web.ErrInvalidInput("agentType required")
		}
		// The client sends `workingDir` (upstream's field name); folderPath/path
		// stay accepted for in-app callers.
		folderPath := strVal(c.Body["workingDir"], c.Body["folderPath"], c.Body["path"])
		convID := int64Num(c.Body["conversationId"])
		conn, err := st.ACP.ConnectLive(agentType, folderPath, convID, acp.ConnectOptions{
			SessionID:             strVal(c.Body["sessionId"], c.Body["session_id"]),
			PreferredModeID:       strVal(c.Body["preferredModeId"], c.Body["preferred_mode_id"]),
			PreferredConfigValues: stringMap(c.Body["preferredConfigValues"], c.Body["preferred_config_values"]),
		})
		if err != nil {
			return nil, acpError(err)
		}
		return connectionInfo(conn), nil
	})
	reg.web.Register("acp_disconnect", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		id := strVal(c.Body["connectionId"], c.Body["connection_id"])
		st.ACP.DisconnectLive(id)
		return nil, nil
	})
	reg.web.Register("acp_prompt", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		connID := strVal(c.Body["connectionId"], c.Body["connection_id"])
		if connID == "" {
			return nil, web.ErrInvalidInput("connectionId required")
		}
		blocks := acpPromptBlocks(c)
		if len(blocks) == 0 {
			return nil, web.ErrInvalidInput("prompt or blocks required")
		}
		if err := st.ACP.PromptLive(connID, blocks); err != nil {
			return nil, acpError(err)
		}
		return nil, nil
	})
	reg.web.Register("acp_cancel", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		connID := strVal(c.Body["connectionId"], c.Body["connection_id"])
		st.ACP.CancelLive(connID)
		return nil, nil
	})
	reg.web.Register("acp_goal_control", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		connID := strVal(c.Body["connectionId"], c.Body["connection_id"])
		if connID == "" {
			return nil, web.ErrInvalidInput("connectionId required")
		}
		action := strVal(c.Body["action"])
		if action != "pause" && action != "clear" {
			return nil, web.ErrInvalidInput("action must be 'pause' or 'clear'")
		}
		if !st.ACP.GoalControlLive(connID, action) {
			return nil, web.ErrNotFound("connection not found")
		}
		return nil, nil
	})
	reg.web.Register("acp_touch_connection", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		connID := strVal(c.Body["connectionId"], c.Body["connection_id"])
		return st.ACP.Touch(connID), nil
	})
	// acp_stop_async_task: the Go engine tracks no async (background) agent
	// tasks, so there is never one to stop → false ("adapter declined"), the
	// same 200-with-false the upstream returns when the adapter refuses.
	reg.web.Register("acp_stop_async_task", func(c *web.Context) (any, error) {
		return false, nil
	})
	// acp_answer_plan_approval: plan-mode approval is a per-agent protocol
	// feature (e.g. Grok plan mode) the Go engine does not yet surface, so no
	// approval is ever pending here. Accept the answer as a no-op rather than
	// error, so a client that sends one against a non-plan agent is not broken.
	reg.web.Register("acp_answer_plan_approval", func(c *web.Context) (any, error) {
		return nil, nil
	})
	reg.web.Register("acp_get_agent_status", func(c *web.Context) (any, error) {
		return map[string]any{"installed": false}, nil
	})
	reg.web.Register("acp_describe_agent_options", func(c *web.Context) (any, error) {
		return []map[string]any{}, nil
	})
	reg.web.Register("acp_preflight", func(c *web.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	reg.web.Register("acp_set_mode", func(c *web.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	reg.web.Register("acp_respond_permission", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		connID := strVal(c.Body["connectionId"], c.Body["connection_id"])
		requestID := strVal(c.Body["requestId"], c.Body["request_id"])
		optionID := strVal(c.Body["optionId"], c.Body["option_id"])
		st.ACP.RespondPermission(connID, requestID, optionID)
		return nil, nil
	})
	reg.web.Register("acp_get_session_snapshot", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		connID := strVal(c.Body["connectionId"], c.Body["connection_id"])
		state := st.Session.GetState(connID)
		if state == nil {
			return nil, nil
		}
		return state.Snapshot(), nil
	})
	reg.web.Register("acp_answer_question", func(c *web.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
}

// connectionInfo renders acp::types::ConnectionInfo { id, agent_type, status }.
func connectionInfo(conn *acp.Connection) map[string]any {
	return map[string]any{
		"id":         conn.ID,
		"agent_type": conn.AgentType,
		"status":     conn.Status,
	}
}

// acpPromptBlocks builds the ACP prompt content blocks from the request body:
// explicit "blocks" pass through; otherwise a "prompt" string is wrapped as a
// single text block.
func acpPromptBlocks(c *web.Context) []any {
	if raw, ok := c.Body["blocks"].([]any); ok && len(raw) > 0 {
		return raw
	}
	if p := strVal(c.Body["prompt"]); p != "" {
		return []any{map[string]any{"type": "text", "text": p}}
	}
	return nil
}

// acpError maps the engine's typed errors onto the HTTP error contract.
func acpError(err error) error {
	var unavailable *acp.AgentUnavailableError
	if errors.As(err, &unavailable) {
		return web.ErrDependencyMissing(err.Error())
	}
	var spawn *acp.SpawnError
	if errors.As(err, &spawn) {
		return web.ErrDependencyMissing(err.Error())
	}
	var unknown *acp.ConnectionUnknownError
	if errors.As(err, &unknown) {
		return web.ErrNotFound(err.Error())
	}
	return web.ErrTaskExecution(err.Error())
}

// stringMap reads a JSON object of string→string (upstream's
// BTreeMap<String, String> params, e.g. preferredConfigValues). Anything that is
// not an object of strings is reported as absent rather than coerced.
func stringMap(values ...any) map[string]string {
	for _, v := range values {
		raw, ok := v.(map[string]any)
		if !ok {
			continue
		}
		out := map[string]string{}
		for k, val := range raw {
			switch s := val.(type) {
			case string:
				out[k] = s
			case float64:
				out[k] = strconv.FormatFloat(s, 'f', -1, 64)
			case bool:
				out[k] = strconv.FormatBool(s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}
