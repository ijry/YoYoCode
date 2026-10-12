package handlers

// Chat-channel settings get/set (upstream commands/chat_channel.rs): command
// prefix, event filter, event webhooks, message language. All persist as
// app_metadata key/value and mirror the upstream wire shapes exactly. The
// chat-channel transport itself (connect/test/messages) is a separate, larger
// subsystem; these are the standalone, DB-backed settings routes.

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const (
	chatCommandPrefixKey = "chat_command_prefix"
	chatDefaultPrefix    = "/"
	chatMessageLangKey   = "chat_message_language"
	chatEventFilterKey   = "chat_event_filter"
	chatEventWebhooksKey = "chat_event_webhooks"
)

var chatValidLanguages = []string{"en", "zh-cn", "zh-tw", "ja", "ko", "es", "de", "fr", "pt", "ar"}

func upsertMetadataValue(d *db.Database, key, value string) error {
	now := nowRFC3339()
	_, err := d.Conn.Exec(
		`INSERT INTO app_metadata (key, value, created_at, updated_at) VALUES (?,?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, now, now,
	)
	return err
}

// RegisterChatSettings wires the chat-channel settings get/set endpoints.
func RegisterChatSettings(reg *Registry) {
	reg.Register("get_chat_command_prefix", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		v := metadataValue(d, chatCommandPrefixKey)
		if v == "" {
			v = chatDefaultPrefix
		}
		return v, nil
	})

	reg.Register("set_chat_command_prefix", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		prefix := strings.TrimSpace(GetString(c, "prefix"))
		if prefix == "" || len([]rune(prefix)) > 3 || hasAlphanumeric(prefix) {
			return nil, web.ErrInvalidInput("Prefix must be 1-3 non-alphanumeric characters")
		}
		if err := upsertMetadataValue(d, chatCommandPrefixKey, prefix); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return nil, nil
	})

	reg.Register("get_chat_message_language", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		v := metadataValue(d, chatMessageLangKey)
		if v == "" {
			v = "en"
		}
		return v, nil
	})

	reg.Register("set_chat_message_language", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		lang := strings.ToLower(GetString(c, "language"))
		if !containsString(chatValidLanguages, lang) {
			return nil, web.ErrInvalidInput("Unsupported language: " + GetString(c, "language") + ". Supported: " + strings.Join(chatValidLanguages, ", "))
		}
		if err := upsertMetadataValue(d, chatMessageLangKey, lang); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return nil, nil
	})

	reg.Register("get_chat_event_filter", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		raw := metadataValue(d, chatEventFilterKey)
		if raw == "" {
			return nil, nil
		}
		// Stored "null" sentinel → null (the default event set).
		var filter []string
		if err := json.Unmarshal([]byte(raw), &filter); err != nil {
			return nil, web.ErrInvalidInput(err.Error())
		}
		return filter, nil
	})

	reg.Register("set_chat_event_filter", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		// filter is Option<[]string>: present array → store JSON; absent/null →
		// persist the "null" sentinel (default event set).
		raw := GetRaw(c, "filter")
		value := "null"
		if len(raw) > 0 && string(raw) != "null" {
			var arr []string
			if err := json.Unmarshal(raw, &arr); err != nil {
				return nil, web.ErrInvalidInput(err.Error())
			}
			b, _ := json.Marshal(arr)
			value = string(b)
		}
		if err := upsertMetadataValue(d, chatEventFilterKey, value); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return nil, nil
	})

	reg.Register("get_chat_event_webhooks", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		raw := metadataValue(d, chatEventWebhooksKey)
		if raw == "" {
			return []any{}, nil
		}
		var hooks []map[string]any
		if err := json.Unmarshal([]byte(raw), &hooks); err != nil {
			return nil, web.ErrInvalidInput(err.Error())
		}
		if hooks == nil {
			hooks = []map[string]any{}
		}
		return hooks, nil
	})

	reg.Register("set_chat_event_webhooks", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		raw := GetRaw(c, "webhooks")
		var hooks []struct {
			URL     string `json:"url"`
			Enabled bool   `json:"enabled"`
		}
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &hooks); err != nil {
				return nil, web.ErrInvalidInput(err.Error())
			}
		}
		// Trim, drop empty-URL rows, require http(s), dedup by URL (first wins).
		cleaned := []map[string]any{}
		seen := map[string]bool{}
		for _, w := range hooks {
			u := strings.TrimSpace(w.URL)
			if u == "" {
				continue
			}
			parsed, perr := url.Parse(u)
			if perr != nil {
				return nil, web.ErrInvalidInput("Invalid webhook URL: " + u)
			}
			if parsed.Scheme != "http" && parsed.Scheme != "https" {
				return nil, web.ErrInvalidInput("Webhook URL must use http or https: " + u)
			}
			if !seen[u] {
				seen[u] = true
				cleaned = append(cleaned, map[string]any{"url": u, "enabled": w.Enabled})
			}
		}
		b, merr := json.Marshal(cleaned)
		if merr != nil {
			return nil, web.ErrInvalidInput(merr.Error())
		}
		if err := upsertMetadataValue(d, chatEventWebhooksKey, string(b)); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return nil, nil
	})
}

func hasAlphanumeric(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
