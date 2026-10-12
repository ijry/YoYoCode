package handlers

// config_sync_test_connection (upstream commands/config_sync/webdav_sync.rs
// test_connection_core + network/webdav.rs probe). Credentials + reachability
// check against the WebDAV remote, writing nothing: PROPFIND (Depth 0) the
// remote config dir and treat success / 207 Multi-Status / 404 (dir not yet
// created) as OK; map 401/403 to auth errors and anything else to a server/
// network error. Never logs the URL or credentials.

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"yoyocode/src-go/web"
)

const configSyncProtocolVersion = 1
const webdavProbeTimeout = 15 * time.Second

// RegisterConfigSyncWebdav wires config_sync_test_connection.
func RegisterConfigSyncWebdav(reg *Registry) {
	reg.Register("config_sync_test_connection", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		existing := loadConfigSyncSettings(d)
		in, _ := c.Body["settings"].(map[string]any)

		serverURL := existing.ServerURL
		username := existing.Username
		if in != nil {
			if v, ok := optStringField(in, "serverUrl"); ok {
				serverURL = strings.TrimSpace(v)
			}
			if v, ok := optStringField(in, "username"); ok {
				username = strings.TrimSpace(v)
			}
		}
		// password: request value wins; else keep stored when the account is
		// unchanged; else none (mirrors update_settings' sameAccount rule).
		password := ""
		sameAccount := serverURL == existing.ServerURL && username == existing.Username
		if sameAccount {
			password = existing.password
		}
		if in != nil {
			if v, ok := optStringField(in, "password"); ok && v != "" {
				password = v
			}
		}
		var remoteDirIn, profileIn *string
		if in != nil {
			remoteDirIn = optStringPtr(in, "remoteDir")
			profileIn = optStringPtr(in, "profile")
		}
		remoteDir, ok1 := normalizeSegment(remoteDirIn, existing.RemoteDir, configSyncDefaultRemoteDir)
		if !ok1 {
			return nil, web.ErrInvalidInput("Invalid remote path segment")
		}
		profile, ok2 := normalizeSegment(profileIn, existing.Profile, configSyncDefaultProfile)
		if !ok2 {
			return nil, web.ErrInvalidInput("Invalid remote path segment")
		}

		if strings.TrimSpace(serverURL) == "" {
			return nil, web.ErrInvalidInput("Server URL is required")
		}
		rel := remoteDir + "/v" + itoaInt(configSyncProtocolVersion) + "/" + profile
		return nil, webdavProbe(serverURL, username, password, rel)
	})
}

// webdavProbe PROPFINDs the remote dir and classifies the result.
func webdavProbe(serverURL, username, password, rel string) error {
	base, perr := url.Parse(strings.TrimSpace(serverURL))
	if perr != nil || (base.Scheme != "http" && base.Scheme != "https") {
		return web.ErrInvalidInput("Invalid WebDAV server URL")
	}
	base.Path = strings.TrimRight(base.Path, "/")
	for _, seg := range strings.Split(rel, "/") {
		if seg != "" {
			base.Path += "/" + seg
		}
	}
	req, nerr := http.NewRequest("PROPFIND", base.String(), nil)
	if nerr != nil {
		return web.ErrInvalidInput("Invalid WebDAV server URL")
	}
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	req.Header.Set("Depth", "0")
	client := &http.Client{Timeout: webdavProbeTimeout}
	resp, derr := client.Do(req)
	if derr != nil {
		// The transport error carries the full URL (and may embed the
		// username) — never surface it; a bare network message is enough.
		return web.ErrNetwork("Could not reach the WebDAV server")
	}
	defer resp.Body.Close()
	st := resp.StatusCode
	switch {
	case st >= 200 && st < 300, st == http.StatusMultiStatus, st == http.StatusNotFound:
		return nil
	case st == http.StatusUnauthorized:
		return web.ErrAuthFailed("WebDAV authentication failed")
	case st == http.StatusForbidden:
		return web.ErrAuthFailed("WebDAV access forbidden")
	case st == http.StatusConflict:
		return web.ErrConfigInvalid("WebDAV remote path is missing")
	default:
		return web.ErrNetwork("WebDAV server returned status " + itoaInt(st))
	}
}
