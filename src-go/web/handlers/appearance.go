package handlers

// update_appearance_mode / get_appearance_mode (upstream commands/windows.rs).
// The appearance mode ("system" | "light" | "dark") is a portable preference
// stored in app_metadata under "appearance_mode"; the native UI reads it on
// startup and applies the matching light/dark theme.

import "yoyocode/src-go/web"

const appearanceModeKey = "appearance_mode"

func normalizeAppearanceMode(s string) string {
	switch s {
	case "light", "dark", "system":
		return s
	default:
		return "system"
	}
}

// RegisterAppearance wires the appearance-mode get/set endpoints.
func RegisterAppearance(reg *Registry) {
	reg.Register("get_appearance_mode", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		mode := normalizeAppearanceMode(metadataValue(d, appearanceModeKey))
		return map[string]any{"mode": mode}, nil
	})
	reg.Register("update_appearance_mode", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		mode := normalizeAppearanceMode(GetString(c, "mode"))
		if e := upsertMetadataValue(d, appearanceModeKey, mode); e != nil {
			return nil, web.ErrDatabase(e.Error())
		}
		return EmptyResult(), nil
	})
}
