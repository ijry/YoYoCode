package handlers

// Mirror upstream settings handlers: question / delegation / feedback /
// session_info / chat_authoring. Each is a persisted toggle (or small struct)
// in app_metadata plus a firehose broadcast so other windows converge. Keys and
// defaults match commands/{question,delegation,feedback,session_info,chat_authoring}.rs.

import (
	"encoding/json"
	"strconv"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const (
	keyQuestionEnabled          = "question.enabled"
	keyFeedbackEnabled          = "feedback.enabled"
	keySessionInfoEnabled       = "session_info.enabled"
	keyChatAuthoringAutomations = "chat_authoring.automations_enabled"
	keyChatAuthoringWorkTasks   = "chat_authoring.work_tasks_enabled"
	keyDelegationEnabled        = "delegation.enabled"
	keyDelegationDepth          = "delegation.depth_limit"
	keyDelegationAgentDefaults  = "delegation.agent_defaults"
	keyDelegationCompletedCache = "delegation.completed_cache_max_mb"

	delegationDepthMin       = 1
	delegationDepthMax       = 8
	delegationDefaultCacheMB = 512

	evtQuestion      = "question-settings://changed"
	evtFeedback      = "feedback-settings://changed"
	evtSessionInfo   = "session-info-settings://changed"
	evtChatAuthoring = "chat-authoring-settings://changed"
	evtDelegation    = "delegation-settings://changed"
)

// boolMeta reads a persisted bool key with a default.
func boolMeta(d *db.Database, key string, def bool) bool {
	raw := valueOrDefault(d, key)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}

// RegisterSettingsMisc wires the small persisted settings toggles.
func RegisterSettingsMisc(reg *Registry) {
	// ── question (default ON) ──
	reg.Register("get_question_settings", func(c *web.Context) (any, error) {
		return map[string]any{"enabled": boolMeta(stateDB(c), keyQuestionEnabled, true)}, nil
	})
	reg.Register("set_question_settings", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		enabled := settingsBool(c, true)
		upsertValue(d, keyQuestionEnabled, strconv.FormatBool(enabled), nowRFC3339())
		out := map[string]any{"enabled": enabled}
		Emit(c, evtQuestion, out)
		return out, nil
	})

	// ── feedback (default OFF) ──
	reg.Register("get_feedback_settings", func(c *web.Context) (any, error) {
		return map[string]any{"enabled": boolMeta(stateDB(c), keyFeedbackEnabled, false)}, nil
	})
	reg.Register("set_feedback_settings", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		enabled := settingsBool(c, false)
		upsertValue(d, keyFeedbackEnabled, strconv.FormatBool(enabled), nowRFC3339())
		out := map[string]any{"enabled": enabled}
		Emit(c, evtFeedback, out)
		return out, nil
	})
	// submit_session_feedback delivers live steering to an in-flight agent turn
	// through the ACP feedback tool. That tool is not wired in the Go server, so
	// (like upstream's NoActiveTurn / FeedbackDisabled path) this returns a
	// recoverable invalid_input — the signal the frontend uses to fall back to
	// sending the note as an ordinary prompt. connectionId + text are still
	// validated so a malformed call is rejected distinctly.
	reg.Register("submit_session_feedback", func(c *web.Context) (any, error) {
		if _, err := GetStringOrErr(c, "connectionId", "connection_id"); err != nil {
			return nil, err
		}
		if _, err := GetStringOrErr(c, "text"); err != nil {
			return nil, err
		}
		return nil, web.ErrInvalidInput("no active turn is accepting feedback; send it as a prompt instead")
	})

	// ── session_info (default OFF) ──
	reg.Register("get_session_info_settings", func(c *web.Context) (any, error) {
		return map[string]any{"enabled": boolMeta(stateDB(c), keySessionInfoEnabled, false)}, nil
	})
	reg.Register("set_session_info_settings", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		enabled := settingsBool(c, false)
		upsertValue(d, keySessionInfoEnabled, strconv.FormatBool(enabled), nowRFC3339())
		out := map[string]any{"enabled": enabled}
		Emit(c, evtSessionInfo, out)
		return out, nil
	})

	// ── chat_authoring (two toggles, both default OFF) ──
	reg.Register("get_chat_authoring_settings", func(c *web.Context) (any, error) {
		d := stateDB(c)
		return map[string]any{
			"automations_enabled": boolMeta(d, keyChatAuthoringAutomations, false),
			"work_tasks_enabled":  boolMeta(d, keyChatAuthoringWorkTasks, false),
		}, nil
	})
	reg.Register("set_chat_authoring_settings", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		settings := settingsObject(c)
		automations := asBool(settings["automations_enabled"])
		workTasks := asBool(settings["work_tasks_enabled"])
		now := nowRFC3339()
		upsertValue(d, keyChatAuthoringAutomations, strconv.FormatBool(automations), now)
		upsertValue(d, keyChatAuthoringWorkTasks, strconv.FormatBool(workTasks), now)
		out := map[string]any{"automations_enabled": automations, "work_tasks_enabled": workTasks}
		Emit(c, evtChatAuthoring, out)
		return out, nil
	})

	// ── delegation (default OFF, depth 1..8, cache 512MB) ──
	reg.Register("get_delegation_settings", func(c *web.Context) (any, error) {
		return loadDelegationSettings(stateDB(c)), nil
	})
	reg.Register("set_delegation_settings", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		settings := settingsObject(c)
		enabled := asBool(settings["enabled"])
		depth := asInt64(settings["depth_limit"])
		if depth < delegationDepthMin {
			depth = delegationDepthMin
		}
		if depth > delegationDepthMax {
			depth = delegationDepthMax
		}
		cache := delegationDefaultCacheMB
		if v, ok := settings["completed_cache_max_mb"]; ok {
			cache = int(asInt64(v))
		}
		agentDefaults := map[string]any{}
		if ad, ok := settings["agent_defaults"].(map[string]any); ok {
			// Drop empty per-agent entries (upstream filters !v.is_empty()).
			for k, v := range ad {
				if m, ok := v.(map[string]any); ok && len(m) == 0 {
					continue
				}
				agentDefaults[k] = v
			}
		}
		now := nowRFC3339()
		upsertValue(d, keyDelegationEnabled, strconv.FormatBool(enabled), now)
		upsertValue(d, keyDelegationDepth, strconv.FormatInt(depth, 10), now)
		upsertValue(d, keyDelegationCompletedCache, strconv.Itoa(cache), now)
		adJSON, _ := json.Marshal(agentDefaults)
		upsertValue(d, keyDelegationAgentDefaults, string(adJSON), now)
		out := map[string]any{
			"enabled": enabled, "depth_limit": depth, "completed_cache_max_mb": cache,
		}
		if len(agentDefaults) > 0 {
			out["agent_defaults"] = agentDefaults
		}
		Emit(c, evtDelegation, out)
		return out, nil
	})
}

func loadDelegationSettings(d *db.Database) map[string]any {
	enabled := boolMeta(d, keyDelegationEnabled, false)
	depth := int64(delegationDepthMin)
	if raw := valueOrDefault(d, keyDelegationDepth); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			depth = v
		}
	}
	cache := delegationDefaultCacheMB
	if raw := valueOrDefault(d, keyDelegationCompletedCache); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			cache = v
		}
	}
	out := map[string]any{"enabled": enabled, "depth_limit": depth, "completed_cache_max_mb": cache}
	if raw := valueOrDefault(d, keyDelegationAgentDefaults); raw != "" && raw != "{}" {
		var ad map[string]any
		if json.Unmarshal([]byte(raw), &ad) == nil && len(ad) > 0 {
			out["agent_defaults"] = ad
		}
	}
	return out
}

// settingsBool reads params.settings.enabled, tolerating a flat {enabled} body.
func settingsBool(c *web.Context, def bool) bool {
	s := settingsObject(c)
	if v, ok := s["enabled"]; ok {
		return asBool(v)
	}
	return def
}

// settingsObject returns the "settings" sub-object, or the whole body if the
// client sent the fields flat (be lenient across client versions).
func settingsObject(c *web.Context) map[string]any {
	if raw, ok := c.Body["settings"].(map[string]any); ok {
		return raw
	}
	return c.Body
}
