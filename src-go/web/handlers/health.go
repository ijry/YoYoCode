package handlers

import "yoyocode/src-go/web"

// RegisterHealth wires `/api/health`.
func RegisterHealth(reg *Registry) {
	reg.web.RegisterPublic("health", func(c *web.Context) (any, error) {
		version := "0.1.0"
		return map[string]any{"status": "ok", "version": version}, nil
	})
}
