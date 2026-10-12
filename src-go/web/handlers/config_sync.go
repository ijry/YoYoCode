package handlers

// Config-sync local settings/state (upstream commands/config_sync/webdav_sync.rs:
// config_sync_get_settings / config_sync_update_settings / config_sync_get_state).
// Original Go reimplementation of the wire contract. The actual WebDAV transport
// (test_connection / upload_now / peek_remote / download_apply) and the local
// import/export + rollback machinery are a larger subsystem handled later; these
// three are the standalone, DB-backed settings/state routes.
//
// SECURITY NOTE: upstream keeps the WebDAV password + snapshot passphrase in the
// OS keyring, never in the settings row. The Go build has no keyring, so secrets
// are persisted under their OWN app_metadata keys (config_sync_password /
// config_sync_passphrase) — the settings row itself still carries no secret
// (matching the upstream invariant), and the view only ever reports whether a
// secret is on file. The trade-off (plaintext secret in the local app DB) is
// documented here deliberately.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const (
	configSyncSettingsKey   = "config_sync_settings"
	configSyncStateKey      = "config_sync_state"
	configSyncPasswordKey   = "config_sync_password"
	configSyncPassphraseKey = "config_sync_passphrase"

	configSyncDefaultRemoteDir = "yoyocode"
	configSyncDefaultProfile   = "default"
	configSyncDefaultInterval  = 5
	configSyncMaxInterval      = 1440
)

// configSyncSettings is the stored form (secrets live in separate keys, never
// in this JSON — mirrors the upstream row invariant).
type configSyncSettings struct {
	Enabled         bool   `json:"enabled"`
	ServerURL       string `json:"serverUrl"`
	Username        string `json:"username"`
	Encrypt         bool   `json:"encrypt"`
	PassphraseID    string `json:"passphraseId"`
	RemoteDir       string `json:"remoteDir"`
	Profile         string `json:"profile"`
	AutoSync        bool   `json:"autoSync"`
	IntervalMinutes int    `json:"intervalMinutes"`
	// password/passphrase are filled in by loadConfigSyncSettings from the
	// dedicated keys and never serialized back into this row.
	password   string
	passphrase string
}

func defaultConfigSyncSettings() configSyncSettings {
	return configSyncSettings{
		Enabled:         false,
		RemoteDir:       configSyncDefaultRemoteDir,
		Profile:         configSyncDefaultProfile,
		AutoSync:        true,
		IntervalMinutes: configSyncDefaultInterval,
	}
}

func loadConfigSyncSettings(d *db.Database) configSyncSettings {
	s := defaultConfigSyncSettings()
	if raw := metadataValue(d, configSyncSettingsKey); raw != "" {
		var parsed configSyncSettings
		if json.Unmarshal([]byte(raw), &parsed) == nil {
			parsed.password = ""
			parsed.passphrase = ""
			s = parsed
			// Heal a legacy row that still carried an inline password.
			var legacy struct {
				Password   string `json:"password"`
				Passphrase string `json:"passphrase"`
			}
			_ = json.Unmarshal([]byte(raw), &legacy)
			if legacy.Password != "" {
				s.password = legacy.Password
			}
			if legacy.Passphrase != "" {
				s.passphrase = legacy.Passphrase
			}
		}
	}
	if pw := metadataValue(d, configSyncPasswordKey); pw != "" {
		s.password = pw
	}
	if pp := metadataValue(d, configSyncPassphraseKey); pp != "" {
		s.passphrase = pp
	}
	return s
}

func configSyncView(s configSyncSettings) map[string]any {
	return map[string]any{
		"enabled":         s.Enabled,
		"serverUrl":       s.ServerURL,
		"username":        s.Username,
		"hasPassword":     s.password != "",
		"encrypt":         s.Encrypt,
		"hasPassphrase":   s.passphrase != "",
		"remoteDir":       s.RemoteDir,
		"profile":         s.Profile,
		"autoSync":        s.AutoSync,
		"intervalMinutes": s.IntervalMinutes,
	}
}

func configSyncLoadState(d *db.Database) map[string]any {
	out := map[string]any{
		"lastUploadedSha256": nil,
		"lastUploadedTarget": nil,
		"lastSyncAt":         nil,
		"lastError":          nil,
	}
	raw := metadataValue(d, configSyncStateKey)
	if raw == "" {
		return out
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return out
	}
	for _, k := range []string{"lastUploadedSha256", "lastUploadedTarget", "lastSyncAt", "lastError"} {
		if v, ok := parsed[k]; ok {
			out[k] = v
		}
	}
	return out
}

func sanitizePathSegment(value string) (string, bool) {
	t := strings.TrimSpace(value)
	if t == "" || t == "." || t == ".." ||
		strings.Contains(t, "/") || strings.Contains(t, "\\") || strings.ContainsRune(t, 0) {
		return "", false
	}
	return t, true
}

func normalizeSegment(incoming *string, existing, fallback string) (string, bool) {
	candidate := existing
	if incoming != nil {
		candidate = *incoming
	}
	if strings.TrimSpace(candidate) == "" {
		candidate = fallback
	}
	return sanitizePathSegment(candidate)
}

func newPassphraseID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RegisterConfigSync wires the config-sync settings/state read-write endpoints.
func RegisterConfigSync(reg *Registry) {
	reg.Register("config_sync_get_settings", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		return configSyncView(loadConfigSyncSettings(d)), nil
	})

	reg.Register("config_sync_get_state", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		return configSyncLoadState(d), nil
	})

	reg.Register("config_sync_update_settings", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		in, _ := c.Body["settings"].(map[string]any)
		if in == nil {
			return nil, web.ErrInvalidInput("settings required")
		}
		existing := loadConfigSyncSettings(d)

		serverURL := strings.TrimSpace(asStringVal(in["serverUrl"]))
		username := strings.TrimSpace(asStringVal(in["username"]))
		sameAccount := serverURL == existing.ServerURL && username == existing.Username

		// password: present+non-empty → use; else keep stored when same account,
		// else drop.
		inPassword, hasPasswordField := optStringField(in, "password")
		newPassword := ""
		hasNewPassword := false
		if hasPasswordField && inPassword != "" {
			newPassword = inPassword
			hasNewPassword = true
		}
		password := ""
		switch {
		case hasNewPassword:
			password = newPassword
		case sameAccount:
			password = existing.password
		}

		inPassphrase, hasPassphraseField := optStringField(in, "passphrase")
		newPassphrase := ""
		hasNewPassphrase := false
		if hasPassphraseField && inPassphrase != "" {
			newPassphrase = inPassphrase
			hasNewPassphrase = true
		}
		passphrase := existing.passphrase
		if hasNewPassphrase {
			passphrase = newPassphrase
		}

		encrypt := asBoolVal(in["encrypt"])
		if encrypt && passphrase == "" {
			return nil, web.ErrInvalidInput("A snapshot passphrase is required when encryption is enabled")
		}

		passphraseID := newPassphraseID()
		if passphrase == existing.passphrase && existing.PassphraseID != "" {
			passphraseID = existing.PassphraseID
		}

		remoteDirIn := optStringPtr(in, "remoteDir")
		profileIn := optStringPtr(in, "profile")
		remoteDir, ok1 := normalizeSegment(remoteDirIn, existing.RemoteDir, configSyncDefaultRemoteDir)
		if !ok1 {
			return nil, web.ErrInvalidInput("Invalid remote path segment")
		}
		profile, ok2 := normalizeSegment(profileIn, existing.Profile, configSyncDefaultProfile)
		if !ok2 {
			return nil, web.ErrInvalidInput("Invalid remote path segment")
		}

		interval := int(asFloatVal(in["intervalMinutes"]))
		if interval < 1 {
			interval = 1
		}
		if interval > configSyncMaxInterval {
			interval = configSyncMaxInterval
		}

		merged := configSyncSettings{
			Enabled:         asBoolVal(in["enabled"]),
			ServerURL:       serverURL,
			Username:        username,
			Encrypt:         encrypt,
			PassphraseID:    passphraseID,
			RemoteDir:       remoteDir,
			Profile:         profile,
			AutoSync:        asBoolVal(in["autoSync"]),
			IntervalMinutes: interval,
			password:        password,
			passphrase:      passphrase,
		}

		// Secrets first (separate keys; row stays secret-free).
		if hasNewPassword {
			if err := upsertMetadataValue(d, configSyncPasswordKey, password); err != nil {
				return nil, web.ErrIO(err.Error())
			}
		} else if !sameAccount {
			if err := upsertMetadataValue(d, configSyncPasswordKey, ""); err != nil {
				return nil, web.ErrIO(err.Error())
			}
		}
		if hasNewPassphrase {
			if err := upsertMetadataValue(d, configSyncPassphraseKey, passphrase); err != nil {
				return nil, web.ErrIO(err.Error())
			}
		}

		b, _ := json.Marshal(merged)
		if err := upsertMetadataValue(d, configSyncSettingsKey, string(b)); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return configSyncView(merged), nil
	})
}

// ─── small param coercers ──────────────────────────────────────────────────

func asStringVal(v any) string {
	s, _ := v.(string)
	return s
}

func asBoolVal(v any) bool {
	b, _ := v.(bool)
	return b
}

func asFloatVal(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

// optStringField returns (value, present): present is true when the key exists
// and is a string (including ""), matching Option<String> semantics.
func optStringField(m map[string]any, key string) (string, bool) {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return "", false
}

func optStringPtr(m map[string]any, key string) *string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return &s
		}
	}
	return nil
}
