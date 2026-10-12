package handlers

import (
	"strconv"

	"yoyocode/src-go/app_state"
	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// RegisterWebServer wires web-service config handlers.
func RegisterWebServer(reg *Registry) {
	reg.web.Register("get_web_server_status", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		if !st.Server.IsRunning() {
			return nil, nil
		}
		return webServerInfo(st.Server.Port(), st.Server.Token()), nil
	})
	reg.web.Register("get_web_service_config", func(c *web.Context) (any, error) {
		return loadWebServiceConfig(stateDB(c)), nil
	})
	reg.web.Register("update_web_service_config", func(c *web.Context) (any, error) {
		d := stateDB(c)
		cfg, _ := c.Body["config"].(map[string]any)
		if cfg == nil {
			cfg = c.Body
		}
		updateWebServiceConfig(d, cfg)
		return loadWebServiceConfig(d), nil
	})
	reg.web.Register("start_web_server", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		if st.Server.IsRunning() {
			return webServerInfo(st.Server.Port(), st.Server.Token()), nil
		}
		return nil, web.ErrInvalidInput("Cannot start web server from within web mode")
	})
	reg.web.Register("stop_web_server", func(c *web.Context) (any, error) {
		st := c.State.(*appstate.AppState)
		if st.Server.IsRunning() {
			return nil, web.ErrInvalidInput("Cannot stop web server from within web mode")
		}
		return map[string]any{}, nil
	})
	reg.web.Register("probe_web_service_port", func(c *web.Context) (any, error) {
		port := uint16Val(c.Body["port"], web.DEFAULT_WEB_SERVICE_PORT)
		return probePort(port), nil
	})
}

func webServerInfo(port uint16, token string) map[string]any {
	return map[string]any{
		"port":      port,
		"token":     token,
		"addresses": []string{"http://127.0.0.1:" + strconv.Itoa(int(port))},
	}
}

func loadWebServiceConfig(d *db.Database) map[string]any {
	token := valueOrDefault(d, web.WebServiceTokenKey)
	portRaw := valueOrDefault(d, web.WebServicePortKey)
	port := uint16(web.DEFAULT_WEB_SERVICE_PORT)
	if p, err := strconv.ParseUint(portRaw, 10, 16); err == nil && p > 0 {
		port = uint16(p)
	}
	return map[string]any{
		"token":     token,
		"port":      port,
		"autoStart": valueOrDefault(d, web.WebServiceAutoStartKey) == "true",
	}
}

func updateWebServiceConfig(d *db.Database, cfg map[string]any) {
	token, _ := cfg["token"].(string)
	port := uint16Val(cfg["port"], web.DEFAULT_WEB_SERVICE_PORT)
	flag := "false"
	if boolVal(cfg["autoStart"], false) {
		flag = "true"
	}
	now := nowRFC3339()
	upsertValue(d, web.WebServiceTokenKey, token, now)
	upsertValue(d, web.WebServicePortKey, strconv.Itoa(int(port)), now)
	upsertValue(d, web.WebServiceAutoStartKey, flag, now)
}

func valueOrDefault(d *db.Database, key string) string {
	if d == nil || d.Conn == nil {
		return ""
	}
	var v string
	_ = d.Conn.QueryRow(`SELECT value FROM app_metadata WHERE key = ? AND deleted_at IS NULL`, key).Scan(&v)
	return v
}

func boolVal(v any, def bool) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		if t == "true" {
			return true
		}
		if t == "false" {
			return false
		}
	}
	return def
}

func upsertValue(d *db.Database, key, value, now string) {
	if d == nil || d.Conn == nil {
		return
	}
	_, _ = d.Conn.Exec(
		`INSERT INTO app_metadata (key, value, created_at, updated_at) VALUES (?,?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, now, now)
}

func probePort(port uint16) map[string]any {
	return map[string]any{"state": "available", "port": port, "used": false}
}
