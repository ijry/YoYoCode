package handlers

// Web-mode port bridge (B11), mirroring web/handlers/browser_bridge.rs. The
// bridge lets the workbench iframe a localhost dev server through upstream’s auth.
// This Go port does not run the bridge listener subsystem, so the bridge reports
// disabled (its Idle/unconfigured state): browser_bridge_status → {enabled:false},
// open → configuration_missing (BridgeError::Disabled), close → {ok:true}. The
// wire shapes match so the UI degrades the same way the desktop build does.

import (
	"errors"
	"net/url"
	"strings"

	"yoyocode/src-go/web"
)

var errBadURL = errors.New("missing scheme or host")

func RegisterBrowserBridge(reg *Registry) {
	reg.Register("browser_bridge_status", func(c *web.Context) (any, error) {
		return map[string]any{
			"enabled":      false,
			"ports":        []any{},
			"public_host":  nil,
			"host_pattern": nil,
		}, nil
	})
	reg.Register("browser_bridge_open", func(c *web.Context) (any, error) {
		urlArg := strings.TrimSpace(GetString(c, "url"))
		// Validate the target shape first (same order as upstream): a non-http or
		// non-loopback target is a client bug regardless of the bridge being off.
		if _, _, e := bridgeableTarget(urlArg); e != nil {
			return nil, e
		}
		return nil, web.ErrConfigMissing("the port bridge is not enabled in this build")
	})
	reg.Register("browser_bridge_close", func(c *web.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
}

// bridgeableTarget mirrors the upstream check: only plain http:// to the
// server’s own loopback may be bridged.
func bridgeableTarget(raw string) (int64, string, error) {
	u, err := parseAbsURL(raw)
	if err != nil {
		return 0, "", web.ErrInvalidInput("not a URL: " + err.Error())
	}
	if u.Scheme != "http" {
		return 0, "", web.ErrInvalidInput("only http:// addresses on the server's loopback can be bridged")
	}
	host := u.Hostname()
	if !isLoopbackHost(host) {
		return 0, "", web.ErrInvalidInput(host + " is not the server's loopback; only localhost ports can be bridged")
	}
	port := int64(80)
	if p := u.Port(); p != "" {
		if n, ok := parseInt64(p); ok {
			port = n
		}
	}
	pathAndQuery := u.Path
	if pathAndQuery == "" {
		pathAndQuery = "/"
	}
	if u.RawQuery != "" {
		pathAndQuery += "?" + u.RawQuery
	}
	return port, pathAndQuery, nil
}

func parseAbsURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, errBadURL
	}
	return u, nil
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(host)
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "[::1]" || strings.HasPrefix(h, "127.")
}
