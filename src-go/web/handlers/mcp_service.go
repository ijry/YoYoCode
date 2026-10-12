package handlers

// yoyocode-mcp companion service health + tool-group toggles (B11, mcp_service
// module), mirroring commands/mcp_service.rs: the status-bar indicator's one
// status report and the one toggle that fixes a disabled tool group.
//
// Fidelity note: the "service" upstream reports on is three stacked pieces — an
// in-process delegation broker socket, the on-disk yoyocode-mcp companion binary,
// and the enabled tool groups. The Go server ships neither the broker nor the
// companion binary, so listening=false and binary_path=null → the state
// computes to "stopped" with can_start=false (a runtime that never bound a
// socket; the UI hides the start button). The tool-group switches ARE real:
// they read/write the same app_metadata keys the settings page uses, so the
// popover and settings window agree, and set_upstream_mcp_tool_group flips the
// exact per-feature key its slug owns.

import (
	"os"
	"strconv"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

func RegisterMcpService(reg *Registry) {
	reg.Register("get_codeg_mcp_service_status", func(c *web.Context) (any, error) {
		return yoyocodeMcpServiceStatus(stateDB(c)), nil
	})
	// No broker socket to bind in this build; return the (stopped) status so the
	// popover re-syncs rather than erroring on a missing route.
	reg.Register("start_codeg_mcp_service", func(c *web.Context) (any, error) {
		return yoyocodeMcpServiceStatus(stateDB(c)), nil
	})
	reg.Register("set_codeg_mcp_tool_group", setYoyocodeMcpToolGroup)
}

func toolGroup(key string, enabled bool) map[string]any {
	return map[string]any{"key": key, "enabled": enabled}
}
func toolGroupWithin(key, parent string, enabled bool) map[string]any {
	return map[string]any{"key": key, "enabled": enabled, "requires": parent}
}

func yoyocodeMcpServiceStatus(d *db.Database) map[string]any {
	delegationEnabled := boolMeta(d, keyDelegationEnabled, false)
	depthLimit := int64(delegationDepthMin)
	if raw := valueOrDefault(d, keyDelegationDepth); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			depthLimit = v
		}
	}
	browser := metadataBool(d, keyBrowserToolsEnabled, false)
	browserEval := metadataBool(d, keyBrowserToolsEval, false)

	toolGroups := []map[string]any{
		toolGroup("delegation", delegationEnabled),
		toolGroup("feedback", boolMeta(d, keyFeedbackEnabled, false)),
		toolGroup("ask", boolMeta(d, keyQuestionEnabled, true)),
		toolGroup("sessions", boolMeta(d, keySessionInfoEnabled, false)),
		toolGroup("automations", boolMeta(d, keyChatAuthoringAutomations, false)),
		toolGroup("taskboard", boolMeta(d, keyChatAuthoringWorkTasks, false)),
		toolGroup("browser", browser),
		toolGroupWithin("browser_eval", "browser", browser && browserEval),
		toolGroup("computer", metadataBool(d, keyCTEnabled, false)),
	}

	// Resolve the companion binary only from an explicit override; this build
	// ships none, so otherwise it is null (→ Unavailable, were the socket up).
	var binaryPath any
	if p := os.Getenv("YOYOCODE_MCP_BIN"); p != "" {
		binaryPath = p
	}

	// No in-process broker socket here, so the socket never answers a liveness
	// ping; per upstream's ordering that makes the headline "stopped".
	listening := false
	state := "stopped"

	return map[string]any{
		"state":              state,
		"listening":          listening,
		"socket_path":        "",
		"binary_path":        binaryPath,
		"tool_groups":        toolGroups,
		"companion_count":    0,
		"session_count":      0,
		"active_delegations": 0,
		"depth_limit":        depthLimit,
		"started_at":         nil,
		"last_error":         nil,
		"can_start":          false,
	}
}

// setYoyocodeMcpToolGroup flips one tool group by the same slug the status reports,
// writing the per-feature app_metadata key and broadcasting that feature's own
// change event so the settings window converges.
func setYoyocodeMcpToolGroup(c *web.Context) (any, error) {
	d, err := requireDB(c)
	if err != nil {
		return nil, err
	}
	key, err := GetStringOrErr(c, "key")
	if err != nil {
		return nil, err
	}
	enabled := GetBool(c, "enabled")
	now := nowRFC3339()
	b := strconv.FormatBool(enabled)
	switch key {
	case "delegation":
		upsertValue(d, keyDelegationEnabled, b, now)
		Emit(c, evtDelegation, loadDelegationSettings(d))
	case "feedback":
		upsertValue(d, keyFeedbackEnabled, b, now)
		Emit(c, evtFeedback, map[string]any{"enabled": enabled})
	case "ask":
		upsertValue(d, keyQuestionEnabled, b, now)
		Emit(c, evtQuestion, map[string]any{"enabled": enabled})
	case "sessions":
		upsertValue(d, keySessionInfoEnabled, b, now)
		Emit(c, evtSessionInfo, map[string]any{"enabled": enabled})
	case "automations":
		upsertValue(d, keyChatAuthoringAutomations, b, now)
		Emit(c, evtChatAuthoring, map[string]any{
			"automations_enabled": enabled,
			"work_tasks_enabled":  boolMeta(d, keyChatAuthoringWorkTasks, false),
		})
	case "taskboard":
		upsertValue(d, keyChatAuthoringWorkTasks, b, now)
		Emit(c, evtChatAuthoring, map[string]any{
			"automations_enabled": boolMeta(d, keyChatAuthoringAutomations, false),
			"work_tasks_enabled":  enabled,
		})
	case "browser":
		upsertValue(d, keyBrowserToolsEnabled, b, now)
		Emit(c, "browser-tools-settings://changed", map[string]any{})
	case "browser_eval":
		upsertValue(d, keyBrowserToolsEval, b, now)
		Emit(c, "browser-tools-settings://changed", map[string]any{})
	case "computer":
		upsertValue(d, keyCTEnabled, b, now)
		Emit(c, "computer-tools-settings://changed", map[string]any{})
	default:
		return nil, web.ErrInvalidInput("unknown tool group: " + key)
	}
	return nil, nil
}
