package handlers

// Mirror upstream web/handlers/system_settings.rs — get/update language
// settings (public for the login page, like upstream).
import (
	"encoding/json"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const layoutKey = "system_language_settings"

// getLangSettings reads the persisted SystemLanguageSettings {mode, language}
// from app_metadata, defaulting the way upstream's `#[serde(default)]` does.
// LanguageMode is snake_case on the wire: "system" | "manual".
func getLangSettings(d *db.Database) map[string]any {
	value := ""
	if d != nil && d.Conn != nil {
		_ = d.Conn.QueryRow(
			`SELECT value FROM app_metadata WHERE key = ? AND deleted_at IS NULL`, layoutKey,
		).Scan(&value)
	}
	stored := map[string]any{}
	if value != "" {
		_ = json.Unmarshal([]byte(value), &stored)
	}
	mode, _ := stored["mode"].(string)
	if mode != "manual" {
		mode = "system"
	}
	lang, _ := stored["language"].(string)
	if lang == "" {
		lang = "zh-CN"
	}
	return map[string]any{"mode": mode, "language": lang}
}

func RegisterSystemSettings(reg *Registry) {
	// Public: upstream public_api exposes get_system_language_settings so the
	// login page can localize before auth.
	reg.web.RegisterPublic("get_system_language_settings", func(c *web.Context) (any, error) {
		return getLangSettings(stateDB(c)), nil
	})
	reg.web.Register("update_system_language_settings", func(c *web.Context) (any, error) {
		d := stateDB(c)
		// Upstream's shape is {settings: {mode, language}} and it answers with
		// the same object. The flat fields stay accepted for in-app callers.
		in, _ := c.Body["settings"].(map[string]any)
		mode := strVal(in["mode"], c.Body["mode"], "system")
		if mode != "manual" {
			mode = "system"
		}
		settings := map[string]any{
			"mode":     mode,
			"language": strVal(in["language"], c.Body["language"], "zh-CN"),
		}
		raw, _ := json.Marshal(settings)
		now := nowRFC3339()
		if _, err := d.Conn.Exec(
			`INSERT INTO app_metadata (key, value, created_at, updated_at) VALUES (?,?,?,?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
			layoutKey, string(raw), now, now,
		); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		Emit(c, "app://language-settings-updated", settings)
		return settings, nil
	})
}
