package handlers

import "yoyocode/src-go/web"

// RegisterHealth wires `/api/health`. Upstream registers it on the protected
// router (src-tauri/src/web/router.rs) — it sits behind the token middleware,
// not the public one. The login page probes it to validate a token (200 →
// valid, 401 → invalid), so a public registration would make any token pass.
func RegisterHealth(reg *Registry) {
	reg.Register("health", func(c *web.Context) (any, error) {
		version := "0.1.0"
		return map[string]any{"status": "ok", "version": version}, nil
	})
}
