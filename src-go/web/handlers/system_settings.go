package handlers

// Mirror upstream web/handlers/system_settings.rs — get/update language
// settings (public for the login page, like upstream).
import (
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const layoutKey = "system_language_settings"

// getLangSettings reads persisted language settings from app_metadata; falls
// back to a sane default (zh-CN) when unset.
func getLangSettings(d *db.Database) map[string]any {
	value := ""
	if d != nil && d.Conn != nil {
		_ = d.Conn.QueryRow(
			`SELECT value FROM app_metadata WHERE key = ? AND deleted_at IS NULL`, layoutKey,
		).Scan(&value)
	}
	settings := map[string]any{}
	if value != "" {
		indent := strings.Index(value, "{")
		if indent >= 0 {
			value = value[indent:]
		}
		_ = JSONUnmarshalFirst(value, settings)
	}
	if _, ok := settings["language"]; !ok {
		settings["language"] = "zh-CN"
	}
	lang := settings["language"]
	delete(settings, "language")
	// serialize back with snake_case rename: upstream returns { language } etc.
	out := map[string]any{"language": lang}
	for k, v := range settings {
		out[k] = v
	}
	return out
}

func RegisterSystemSettings(reg *Registry) {
	// Public: upstream public_api exposes get_system_language_settings so the
	// login page can localize before auth.
	reg.web.RegisterPublic("get_system_language_settings", func(c *web.Context) (any, error) {
		return getLangSettings(stateDB(c)), nil
	})
	reg.web.Register("update_system_language_settings", func(c *web.Context) (any, error) {
		d := stateDB(c)
		lang, _ := c.Body["language"].(string)
		if lang == "" {
			lang = "zh-CN"
		}
		settings := `{"language":"` + lang + `"}`
		now := nowRFC3339()
		_, err := d.Conn.Exec(
			`INSERT INTO app_metadata (key, value, created_at, updated_at) VALUES (?,?,?,?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
			layoutKey, settings, now, now,
		)
		if err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return map[string]any{"ok": true}, nil
	})
}
