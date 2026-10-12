package handlers

// Browser-tools + computer-use settings (B11): the web-mode mirror of
// commands/browser_tools.rs + computer_tools.rs. One setting in one database,
// so a user administering their desktop instance from elsewhere sees the same
// switches. Values persist per-key in app_metadata; the full settings object is
// the wire contract. Screen/window sharing itself is desktop-only (no HTTP face).

import (
	"encoding/json"
	"runtime"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// ── browser_tools ──

const (
	keyBrowserToolsEnabled = "browser_tools.enabled"
	keyBrowserToolsEval    = "browser_tools.eval_enabled"
)

func loadBrowserToolsSettings(d *db.Database) map[string]any {
	return map[string]any{
		"enabled": metadataBool(d, keyBrowserToolsEnabled, false),
		"eval":    metadataBool(d, keyBrowserToolsEval, false),
	}
}

// ── computer_tools ──

const (
	keyCTEnabled         = "computer_tools.enabled"
	keyCTGrantTTL        = "computer_tools.grant_ttl_minutes"
	keyCTBlocklist       = "computer_tools.blocklist"
	keyCTBlocklistRemove = "computer_tools.blocklist_removed"
	keyCTStopShortcut    = "computer_tools.stop_shortcut"
	keyCTShowIndicator   = "computer_tools.show_indicator"
	keyCTAllowForeground = "computer_tools.allow_foreground"
	keyCTDefaultDelivery = "computer_tools.default_delivery"
	keyCTLaunchEnabled   = "computer_tools.launch_enabled"
	keyCTClipboard       = "computer_tools.clipboard_enabled"
	keyCTScreenEnabled   = "computer_tools.screen_enabled"
	ctDefaultGrantTTL    = 30
)

func ctDefaultStopShortcut() string {
	if runtime.GOOS == "darwin" {
		return "Control+Command+Escape"
	}
	return "Control+Alt+Escape"
}

func loadComputerToolsSettings(d *db.Database) map[string]any {
	return map[string]any{
		"enabled":            metadataBool(d, keyCTEnabled, false),
		"grant_ttl_minutes":  metadataInt(d, keyCTGrantTTL, ctDefaultGrantTTL),
		"blocklist":          metadataStrSlice(d, keyCTBlocklist),
		"blocklist_removed":  metadataStrSlice(d, keyCTBlocklistRemove),
		"blocklist_defaults": []any{},
		"stop_shortcut":      metadataStr(d, keyCTStopShortcut, ctDefaultStopShortcut()),
		"show_indicator":     metadataBool(d, keyCTShowIndicator, true),
		"allow_foreground":   metadataBool(d, keyCTAllowForeground, true),
		"default_delivery":   metadataDelivery(d, keyCTDefaultDelivery),
		"launch_enabled":     metadataBool(d, keyCTLaunchEnabled, false),
		"clipboard_enabled":  metadataBool(d, keyCTClipboard, false),
		"screen_enabled":     metadataBool(d, keyCTScreenEnabled, false),
	}
}

// ── metadata typed readers ──

func metadataBool(d *db.Database, key string, def bool) bool {
	raw := metadataValue(d, key)
	if raw == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1":
		return true
	case "false", "0":
		return false
	}
	return def
}

func metadataInt(d *db.Database, key string, def int64) int64 {
	raw := metadataValue(d, key)
	if n, ok := parseInt64(raw); ok && raw != "" {
		return n
	}
	return def
}

func metadataStr(d *db.Database, key, def string) string {
	raw := metadataValue(d, key)
	if raw == "" {
		return def
	}
	return raw
}

func metadataStrSlice(d *db.Database, key string) []any {
	raw := metadataValue(d, key)
	out := []any{}
	if raw == "" {
		return out
	}
	var arr []string
	if json.Unmarshal([]byte(raw), &arr) != nil {
		return out
	}
	for _, s := range arr {
		out = append(out, s)
	}
	return out
}

func metadataDelivery(d *db.Database, key string) string {
	switch metadataValue(d, key) {
	case "foreground":
		return "foreground"
	default:
		return "background"
	}
}

func RegisterToolsSettings(reg *Registry) {
	reg.Register("get_browser_tools_settings", func(c *web.Context) (any, error) {
		return loadBrowserToolsSettings(stateDB(c)), nil
	})
	reg.Register("set_browser_tools_settings", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		in, _ := c.Body["settings"].(map[string]any)
		if in == nil {
			in = map[string]any{}
		}
		now := nowRFC3339()
		upsertValue(d, keyBrowserToolsEnabled, boolStr(asBool(pick(in, "enabled"))), now)
		upsertValue(d, keyBrowserToolsEval, boolStr(asBool(pick(in, "eval"))), now)
		Emit(c, "browser-tools-settings://changed", map[string]any{})
		return loadBrowserToolsSettings(d), nil
	})

	reg.Register("get_computer_tools_settings", func(c *web.Context) (any, error) {
		return loadComputerToolsSettings(stateDB(c)), nil
	})
	reg.Register("set_computer_tools_settings", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		in, _ := c.Body["settings"].(map[string]any)
		if in == nil {
			in = map[string]any{}
		}
		now := nowRFC3339()
		upsertValue(d, keyCTEnabled, boolStr(asBool(pick(in, "enabled"))), now)
		writeComputerToolsPrefs(d, in, now)
		Emit(c, "computer-tools-settings://changed", map[string]any{})
		return loadComputerToolsSettings(d), nil
	})
	reg.Register("set_computer_tools_enabled", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		upsertValue(d, keyCTEnabled, boolStr(GetBool(c, "enabled")), nowRFC3339())
		Emit(c, "computer-tools-settings://changed", map[string]any{})
		return loadComputerToolsSettings(d), nil
	})
	reg.Register("set_computer_tools_preferences", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		writeComputerToolsPrefs(d, c.Body, nowRFC3339())
		Emit(c, "computer-tools-settings://changed", map[string]any{})
		return loadComputerToolsSettings(d), nil
	})
}

// writeComputerToolsPrefs writes only the preference fields present in `in`
// (never `enabled`), matching the partial-patch semantics of the editor.
func writeComputerToolsPrefs(d *db.Database, in map[string]any, now string) {
	if _, ok := pickKey(in, "grant_ttl_minutes", "grantTtlMinutes"); ok {
		upsertValue(d, keyCTGrantTTL, int64ToStr(asInt64(pick(in, "grant_ttl_minutes", "grantTtlMinutes"))), now)
	}
	if v, ok := pickKey(in, "blocklist"); ok {
		upsertValue(d, keyCTBlocklist, jsonStrSlice(v), now)
	}
	if v, ok := pickKey(in, "blocklist_removed", "blocklistRemoved"); ok {
		upsertValue(d, keyCTBlocklistRemove, jsonStrSlice(v), now)
	}
	if _, ok := pickKey(in, "stop_shortcut", "stopShortcut"); ok {
		upsertValue(d, keyCTStopShortcut, asString(pick(in, "stop_shortcut", "stopShortcut")), now)
	}
	if _, ok := pickKey(in, "show_indicator", "showIndicator"); ok {
		upsertValue(d, keyCTShowIndicator, boolStr(asBool(pick(in, "show_indicator", "showIndicator"))), now)
	}
	if _, ok := pickKey(in, "allow_foreground", "allowForeground"); ok {
		upsertValue(d, keyCTAllowForeground, boolStr(asBool(pick(in, "allow_foreground", "allowForeground"))), now)
	}
	if _, ok := pickKey(in, "default_delivery", "defaultDelivery"); ok {
		dv := asString(pick(in, "default_delivery", "defaultDelivery"))
		if dv != "foreground" {
			dv = "background"
		}
		upsertValue(d, keyCTDefaultDelivery, dv, now)
	}
	if _, ok := pickKey(in, "launch_enabled", "launchEnabled"); ok {
		upsertValue(d, keyCTLaunchEnabled, boolStr(asBool(pick(in, "launch_enabled", "launchEnabled"))), now)
	}
	if _, ok := pickKey(in, "clipboard_enabled", "clipboardEnabled"); ok {
		upsertValue(d, keyCTClipboard, boolStr(asBool(pick(in, "clipboard_enabled", "clipboardEnabled"))), now)
	}
	if _, ok := pickKey(in, "screen_enabled", "screenEnabled"); ok {
		upsertValue(d, keyCTScreenEnabled, boolStr(asBool(pick(in, "screen_enabled", "screenEnabled"))), now)
	}
}

func pickKey(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v, true
		}
	}
	return nil, false
}

func jsonStrSlice(v any) string {
	arr, ok := v.([]any)
	if !ok {
		return "[]"
	}
	out := []string{}
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			continue
		}
		t := strings.TrimSpace(s)
		if t == "" {
			continue
		}
		// Case-insensitive dedup, preserving first spelling (normalize_blocklist).
		dup := false
		for _, existing := range out {
			if strings.EqualFold(existing, t) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, t)
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
