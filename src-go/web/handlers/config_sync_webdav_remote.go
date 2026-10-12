package handlers

// config_sync remote WebDAV transport: upload_now / peek_remote / download_apply
// (upstream commands/config_sync/webdav_sync.rs). Moves the two files the
// snapshot format defines — config.json + manifest.json — under
// <remoteDir>/v1/<profile>/ on the user's WebDAV share. Reuses the local
// snapshot machinery (collect/serialize/manifest/apply) from
// config_sync_content.go and the probe URL logic from config_sync_webdav.go.
//
// Encryption: this build writes/reads the plaintext ("none") protection only.
// A settings row with encrypt=true is refused with a clear error rather than
// silently downgrading, so an encrypted remote is never half-written.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"yoyocode/src-go/db"

	"yoyocode/src-go/web"
)

const (
	configFileName    = "config.json"
	manifestFileName  = "manifest.json"
	webdavIOTimeout   = 30 * time.Second
	webdavMaxDownload = 8 * 1024 * 1024
)

// RegisterConfigSyncRemote wires the remote WebDAV sync endpoints.
func RegisterConfigSyncRemote(reg *Registry) {
	reg.Register("config_sync_upload_now", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		s := loadConfigSyncSettings(d)
		if strings.TrimSpace(s.ServerURL) == "" {
			return nil, web.ErrInvalidInput("Server URL is required")
		}
		snapshot, cerr := collectConfigSnapshot(d)
		if cerr != nil {
			return nil, cerr
		}
		payload, serr := serializeSnapshot(snapshot)
		if serr != nil {
			return nil, serr
		}
		sum := sha256.Sum256(payload)
		hash := hex.EncodeToString(sum[:])
		counts := snapshotCounts(snapshot)
		target := configSyncRemoteTarget(s)

		state := configSyncLoadState(d)
		force := GetBool(c, "force")
		if !force && asString(state["lastUploadedSha256"]) == hash && asString(state["lastUploadedTarget"]) == target {
			return map[string]any{"uploaded": false, "sha256": hash, "counts": counts, "syncedAt": asString(state["lastSyncAt"])}, nil
		}
		if s.Encrypt {
			return nil, web.ErrConfigInvalid("encrypted config sync is not supported in this build")
		}

		manifest := buildManifest(payload, counts)
		manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
		rel := configSyncRemoteDir(s)
		if e := webdavEnsureDir(s, rel); e != nil {
			configSyncSaveState(d, nil, nil, nil, e.Error())
			return nil, e
		}
		if e := webdavPut(s, rel+"/"+configFileName, payload); e != nil {
			configSyncSaveState(d, nil, nil, nil, e.Error())
			return nil, e
		}
		if e := webdavPut(s, rel+"/"+manifestFileName, manifestBytes); e != nil {
			configSyncSaveState(d, nil, nil, nil, e.Error())
			return nil, e
		}
		now := nowRFC3339()
		configSyncSaveState(d, strPtr(hash), strPtr(target), strPtr(now), "")
		return map[string]any{"uploaded": true, "sha256": hash, "counts": counts, "syncedAt": now}, nil
	})

	reg.Register("config_sync_peek_remote", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		s := loadConfigSyncSettings(d)
		if strings.TrimSpace(s.ServerURL) == "" {
			return nil, web.ErrInvalidInput("Server URL is required")
		}
		b, found, gerr := webdavGet(s, configSyncRemoteDir(s)+"/"+manifestFileName)
		if gerr != nil {
			return nil, gerr
		}
		if !found {
			return nil, nil
		}
		var m configManifest
		if json.Unmarshal(b, &m) != nil {
			return nil, web.ErrConfigInvalid("remote manifest is malformed")
		}
		return m, nil
	})

	reg.Register("config_sync_download_apply", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		s := loadConfigSyncSettings(d)
		if strings.TrimSpace(s.ServerURL) == "" {
			return nil, web.ErrInvalidInput("Server URL is required")
		}
		dir := configSyncRemoteDir(s)
		manifestBytes, mfound, merr := webdavGet(s, dir+"/"+manifestFileName)
		if merr != nil {
			return nil, merr
		}
		configBytes, cfound, cerr := webdavGet(s, dir+"/"+configFileName)
		if cerr != nil {
			return nil, cerr
		}
		if !mfound || !cfound {
			return nil, web.ErrInvalidInput("No config snapshot on the remote yet")
		}
		var m configManifest
		if json.Unmarshal(manifestBytes, &m) != nil {
			return nil, web.ErrConfigInvalid("remote manifest is malformed")
		}
		if e := validateConfigManifest(&m, configBytes); e != nil {
			return nil, e
		}
		if m.Encryption != "none" {
			return nil, web.ErrConfigInvalid("encrypted config sync is not supported in this build")
		}
		snapshot, perr := parseConfigSnapshot(configBytes)
		if perr != nil {
			return nil, perr
		}
		res, aerr := applyImport(d, snapshot, configSnapshotDir())
		if aerr != nil {
			return nil, aerr
		}
		out := map[string]any{"manifest": m}
		if r, ok := res["applied"]; ok {
			out["applied"] = r
		}
		out["rollbackPath"] = res["rollbackPath"]
		return out, nil
	})
}

func strPtr(s string) *string { return &s }

func configSyncRemoteDir(s configSyncSettings) string {
	dir := s.RemoteDir
	if strings.TrimSpace(dir) == "" {
		dir = configSyncDefaultRemoteDir
	}
	profile := s.Profile
	if strings.TrimSpace(profile) == "" {
		profile = configSyncDefaultProfile
	}
	return dir + "/v" + itoaInt(configSyncProtocolVersion) + "/" + profile
}

func configSyncRemoteTarget(s configSyncSettings) string {
	protection := "none"
	if s.Encrypt {
		protection = "aes-256-gcm"
	}
	return s.ServerURL + "\x00" + s.RemoteDir + "\x00" + s.Profile + "\x00" + protection
}

func configSyncSaveState(d *db.Database, sha, target, syncedAt *string, lastError string) {
	st := map[string]any{
		"lastUploadedSha256": nullableStrPtr(sha),
		"lastUploadedTarget": nullableStrPtr(target),
		"lastSyncAt":         nullableStrPtr(syncedAt),
	}
	// Preserve prior upload baseline on an error-only save.
	if sha == nil && target == nil && syncedAt == nil {
		prev := configSyncLoadState(d)
		st["lastUploadedSha256"] = prev["lastUploadedSha256"]
		st["lastUploadedTarget"] = prev["lastUploadedTarget"]
		st["lastSyncAt"] = prev["lastSyncAt"]
	}
	if lastError != "" {
		st["lastError"] = lastError
	} else {
		st["lastError"] = nil
	}
	b, _ := json.Marshal(st)
	_ = upsertMetadataValue(d, configSyncStateKey, string(b))
}

func nullableStrPtr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func validateConfigManifest(m *configManifest, configBytes []byte) error {
	if m.SchemaVersion > configSnapshotSchemaVersion {
		return web.ErrInvalidInput("Snapshot was written by a newer version of YoYoCode")
	}
	if m.Encryption != "none" && m.Encryption != "aes-256-gcm" {
		return web.ErrConfigInvalid("Unsupported snapshot encryption '" + m.Encryption + "'")
	}
	if m.Config.Size != int64(len(configBytes)) {
		return web.ErrConfigInvalid("remote config checksum mismatch (size)")
	}
	sum := sha256.Sum256(configBytes)
	if hex.EncodeToString(sum[:]) != m.Config.Sha256 {
		return web.ErrConfigInvalid("remote config checksum mismatch")
	}
	return nil
}

// ── WebDAV client (read/write) ──

func webdavURL(s configSyncSettings, rel string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(s.ServerURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") {
		return "", web.ErrInvalidInput("Invalid WebDAV server URL")
	}
	base.Path = strings.TrimRight(base.Path, "/")
	for _, seg := range strings.Split(rel, "/") {
		if seg != "" {
			base.Path += "/" + seg
		}
	}
	return base.String(), nil
}

func webdavDo(s configSyncSettings, method, rel string, body []byte, headers map[string]string) (*http.Response, error) {
	u, err := webdavURL(s, rel)
	if err != nil {
		return nil, err
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, nerr := http.NewRequest(method, u, rdr)
	if nerr != nil {
		return nil, web.ErrInvalidInput("Invalid WebDAV server URL")
	}
	if s.Username != "" || s.password != "" {
		req.SetBasicAuth(s.Username, s.password)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: webdavIOTimeout}
	resp, derr := client.Do(req)
	if derr != nil {
		return nil, web.ErrNetwork("Could not reach the WebDAV server")
	}
	return resp, nil
}

func webdavClassify(status int) error {
	switch {
	case status == http.StatusUnauthorized:
		return web.ErrAuthFailed("WebDAV authentication failed")
	case status == http.StatusForbidden:
		return web.ErrAuthFailed("WebDAV access forbidden")
	case status == http.StatusNotFound, status == http.StatusConflict:
		return web.ErrConfigInvalid("WebDAV remote path is missing")
	default:
		return web.ErrNetwork("WebDAV server returned status " + itoaInt(status))
	}
}

func webdavGet(s configSyncSettings, rel string) ([]byte, bool, error) {
	resp, err := webdavDo(s, http.MethodGet, rel, nil, nil)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, webdavClassify(resp.StatusCode)
	}
	b, rerr := io.ReadAll(io.LimitReader(resp.Body, webdavMaxDownload+1))
	if rerr != nil {
		return nil, false, web.ErrNetwork("Failed to read WebDAV response")
	}
	if len(b) > webdavMaxDownload {
		return nil, false, web.ErrConfigInvalid("remote config file is too large")
	}
	return b, true, nil
}

func webdavPut(s configSyncSettings, rel string, body []byte) error {
	resp, err := webdavDo(s, http.MethodPut, rel, body, map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return webdavClassify(resp.StatusCode)
}

// webdavEnsureDir MKCOLs each level of rel, tolerating levels that already exist.
func webdavEnsureDir(s configSyncSettings, rel string) error {
	prefix := ""
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" {
			continue
		}
		if prefix == "" {
			prefix = seg
		} else {
			prefix = prefix + "/" + seg
		}
		resp, err := webdavDo(s, "MKCOL", prefix, nil, nil)
		if err != nil {
			return err
		}
		status := resp.StatusCode
		resp.Body.Close()
		// 201 created; 405/301/302 commonly mean "already exists"; 200 ok.
		switch {
		case status >= 200 && status < 300:
		case status == http.StatusMethodNotAllowed, status == http.StatusMovedPermanently, status == http.StatusFound:
		case status == http.StatusUnauthorized, status == http.StatusForbidden:
			return webdavClassify(status)
		default:
			// Keep going — a strict server may 409 an intermediate that the
			// final PUT still succeeds against; surface a real failure on PUT.
		}
	}
	return nil
}
