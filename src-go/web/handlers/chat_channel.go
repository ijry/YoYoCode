package handlers

// Chat-channel CRUD + token storage (upstream commands/chat_channel.rs +
// db/service/chat_channel_service.rs). Original Go reimplementation of the wire
// contract. The live transport (connect/disconnect/test/status/messages) binds
// Lark/Telegram/WeChat backends and is a separate, network-bound subsystem left
// for later; these are the standalone, DB-backed management routes.
//
// SECURITY NOTE: upstream keeps the per-channel token in the OS keyring. The Go
// build has no keyring, so the token is persisted under a dedicated
// app_metadata key (chat_channel_token:<id>) — never in the chat_channel row.
// The plaintext-in-local-DB trade-off is documented here deliberately.

import (
	"database/sql"
	"strconv"
	"strings"

	"yoyocode/src-go/web"
)

var chatChannelTypes = map[string]bool{"lark": true, "telegram": true, "weixin": true}

func chatChannelTokenKey(id int64) string { return "chat_channel_token:" + strconv.FormatInt(id, 10) }

func chatChannelRowToInfo(id int64, name, channelType string, enabled int, configJSON string,
	eventFilter sql.NullString, dailyEnabled int, dailyTime sql.NullString, createdAt, updatedAt string) map[string]any {
	return map[string]any{
		"id":                   id,
		"name":                 name,
		"channel_type":         channelType,
		"enabled":              enabled != 0,
		"config_json":          configJSON,
		"event_filter_json":    nsAnyLocal(eventFilter),
		"daily_report_enabled": dailyEnabled != 0,
		"daily_report_time":    nsAnyLocal(dailyTime),
		"created_at":           createdAt,
		"updated_at":           updatedAt,
	}
}

func nsAnyLocal(n sql.NullString) any {
	if !n.Valid {
		return nil
	}
	return n.String
}

func chatChannelGetInfo(c *web.Context, id int64) (map[string]any, error) {
	d, err := requireDB(c)
	if err != nil {
		return nil, err
	}
	var name, channelType, configJSON, createdAt, updatedAt string
	var eventFilter, dailyTime sql.NullString
	var enabled, dailyEnabled int
	row := d.Conn.QueryRow(`SELECT name, channel_type, enabled, config_json, event_filter_json,
		daily_report_enabled, daily_report_time, created_at, updated_at FROM chat_channel WHERE id = ?`, id)
	if err := row.Scan(&name, &channelType, &enabled, &configJSON, &eventFilter, &dailyEnabled, &dailyTime, &createdAt, &updatedAt); err != nil {
		return nil, web.ErrNotFound("Chat channel " + strconv.FormatInt(id, 10) + " not found")
	}
	return chatChannelRowToInfo(id, name, channelType, enabled, configJSON, eventFilter, dailyEnabled, dailyTime, createdAt, updatedAt), nil
}

// RegisterChatChannel wires the chat-channel management + token endpoints.
func RegisterChatChannel(reg *Registry) {
	reg.Register("list_chat_channels", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		rows, qerr := d.Conn.Query(`SELECT id, name, channel_type, enabled, config_json, event_filter_json,
			daily_report_enabled, daily_report_time, created_at, updated_at FROM chat_channel ORDER BY id ASC`)
		if qerr != nil {
			return out, nil
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var name, channelType, configJSON, createdAt, updatedAt string
			var eventFilter, dailyTime sql.NullString
			var enabled, dailyEnabled int
			if rows.Scan(&id, &name, &channelType, &enabled, &configJSON, &eventFilter, &dailyEnabled, &dailyTime, &createdAt, &updatedAt) == nil {
				out = append(out, chatChannelRowToInfo(id, name, channelType, enabled, configJSON, eventFilter, dailyEnabled, dailyTime, createdAt, updatedAt))
			}
		}
		return out, nil
	})

	reg.Register("create_chat_channel", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		name := GetString(c, "name")
		channelType := GetString(c, "channelType", "channel_type")
		if !chatChannelTypes[channelType] {
			return nil, web.ErrInvalidInput("Invalid channel type: " + channelType)
		}
		configJSON := GetString(c, "configJson", "config_json")
		if configJSON == "" {
			configJSON = "{}"
		}
		enabled := GetBool(c, "enabled")
		dailyEnabled := GetBool(c, "dailyReportEnabled", "daily_report_enabled")
		dailyTime := optStringField2(c, "dailyReportTime", "daily_report_time")
		now := nowRFC3339()
		res, ierr := d.Conn.Exec(`INSERT INTO chat_channel
			(name, channel_type, enabled, config_json, event_filter_json, daily_report_enabled, daily_report_time, created_at, updated_at)
			VALUES (?,?,?,?,NULL,?,?,?,?)`,
			name, channelType, boolToInt(enabled), configJSON, boolToInt(dailyEnabled), nullableStrAny(dailyTime), now, now)
		if ierr != nil {
			return nil, web.ErrDatabase(ierr.Error())
		}
		id, _ := res.LastInsertId()
		return chatChannelGetInfo(c, id)
	})

	reg.Register("update_chat_channel", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id := GetInt(c, "id")
		if id <= 0 {
			return nil, web.ErrInvalidInput("id required")
		}
		if _, gerr := chatChannelGetInfo(c, id); gerr != nil {
			return nil, gerr
		}
		sets := []string{}
		args := []any{}
		if v, ok := optStringPresent(c, "name"); ok {
			sets = append(sets, "name = ?")
			args = append(args, v)
		}
		if b, ok := optBoolPresent(c, "enabled"); ok {
			sets = append(sets, "enabled = ?")
			args = append(args, boolToInt(b))
		}
		if v, ok := optStringPresent(c, "configJson", "config_json"); ok {
			sets = append(sets, "config_json = ?")
			args = append(args, v)
		}
		// event_filter_json / daily_report_time are Option<Option<String>>:
		// present → set (null clears); absent → keep.
		if present, val := optNullableField(c, "eventFilterJson", "event_filter_json"); present {
			sets = append(sets, "event_filter_json = ?")
			args = append(args, val)
		}
		if b, ok := optBoolPresent(c, "dailyReportEnabled", "daily_report_enabled"); ok {
			sets = append(sets, "daily_report_enabled = ?")
			args = append(args, boolToInt(b))
		}
		if present, val := optNullableField(c, "dailyReportTime", "daily_report_time"); present {
			sets = append(sets, "daily_report_time = ?")
			args = append(args, val)
		}
		if len(sets) > 0 {
			sets = append(sets, "updated_at = ?")
			args = append(args, nowRFC3339())
			args = append(args, id)
			if _, uerr := d.Conn.Exec(`UPDATE chat_channel SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); uerr != nil {
				return nil, web.ErrDatabase(uerr.Error())
			}
		}
		return chatChannelGetInfo(c, id)
	})

	reg.Register("delete_chat_channel", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id := GetInt(c, "id")
		if id <= 0 {
			return nil, web.ErrInvalidInput("id required")
		}
		if _, derr := d.Conn.Exec(`DELETE FROM chat_channel WHERE id = ?`, id); derr != nil {
			return nil, web.ErrDatabase(derr.Error())
		}
		// Drop the stored token too (keyring equivalent).
		_ = upsertMetadataValue(d, chatChannelTokenKey(id), "")
		return nil, nil
	})

	reg.Register("save_chat_channel_token", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id := GetInt(c, "channelId", "channel_id")
		if id <= 0 {
			return nil, web.ErrInvalidInput("channelId required")
		}
		token := GetString(c, "token")
		if token == "" {
			return nil, web.ErrInvalidInput("token required")
		}
		if err := upsertMetadataValue(d, chatChannelTokenKey(id), token); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return nil, nil
	})

	reg.Register("get_chat_channel_has_token", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id := GetInt(c, "channelId", "channel_id")
		if id <= 0 {
			return nil, web.ErrInvalidInput("channelId required")
		}
		return metadataValue(d, chatChannelTokenKey(id)) != "", nil
	})

	reg.Register("delete_chat_channel_token", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id := GetInt(c, "channelId", "channel_id")
		if id <= 0 {
			return nil, web.ErrInvalidInput("channelId required")
		}
		if err := upsertMetadataValue(d, chatChannelTokenKey(id), ""); err != nil {
			return nil, web.ErrIO(err.Error())
		}
		return nil, nil
	})

	// get_chat_channel_status reports the live backends. This build runs no
	// chat-channel transport, so nothing is connected → empty list (faithful:
	// the manager would report the same before any connect).
	reg.Register("get_chat_channel_status", func(c *web.Context) (any, error) {
		return []map[string]any{}, nil
	})

	// list_chat_channel_messages reads the per-channel message log (DB-backed,
	// populated by the transport once that lands; empty until then).
	reg.Register("list_chat_channel_messages", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		channelID := GetInt(c, "channelId", "channel_id")
		if channelID <= 0 {
			return nil, web.ErrInvalidInput("channelId required")
		}
		limit := GetInt(c, "limit")
		if limit <= 0 {
			limit = 50
		}
		offset := GetInt(c, "offset")
		if offset < 0 {
			offset = 0
		}
		out := []map[string]any{}
		rows, qerr := d.Conn.Query(`SELECT id, channel_id, direction, message_type, content_preview, status, error_detail, created_at
			FROM chat_channel_message_log WHERE channel_id = ? ORDER BY id DESC LIMIT ? OFFSET ?`, channelID, limit, offset)
		if qerr != nil {
			return out, nil
		}
		defer rows.Close()
		for rows.Next() {
			var id, chID int64
			var direction, messageType, preview, status, createdAt string
			var errDetail sql.NullString
			if rows.Scan(&id, &chID, &direction, &messageType, &preview, &status, &errDetail, &createdAt) == nil {
				out = append(out, map[string]any{
					"id": id, "channel_id": chID, "direction": direction,
					"message_type": messageType, "content_preview": preview, "status": status,
					"error_detail": nsAnyLocal(errDetail), "created_at": createdAt,
				})
			}
		}
		return out, nil
	})

	// Live transport (connect/test) binds a Lark/Telegram/WeChat backend over
	// the network — not wired in this build. disconnect is a no-op success
	// (nothing is connected to tear down).
	reg.Register("connect_chat_channel", func(c *web.Context) (any, error) {
		return nil, web.ErrConfigInvalid("live chat-channel transport is not available in this build")
	})
	reg.Register("test_chat_channel", func(c *web.Context) (any, error) {
		return nil, web.ErrConfigInvalid("live chat-channel transport is not available in this build")
	})
	reg.Register("disconnect_chat_channel", func(c *web.Context) (any, error) {
		return nil, nil
	})

	// WeChat QR login talks to the WeChat bot server over the network — the
	// live backend is not wired in this build, so both routes refuse legibly
	// rather than 404 (mirrors the chat-transport refusal above).
	reg.Register("weixin_get_qrcode", func(c *web.Context) (any, error) {
		return nil, web.ErrConfigInvalid("WeChat QR login is not available in this build")
	})
	reg.Register("weixin_check_qrcode", func(c *web.Context) (any, error) {
		return nil, web.ErrConfigInvalid("WeChat QR login is not available in this build")
	})
}

func nullableStrAny(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// optStringField2 reads a string param by aliases, nil when absent.
func optStringField2(c *web.Context, keys ...string) *string {
	for _, k := range keys {
		if v, ok := c.Body[k]; ok {
			if s, ok := v.(string); ok {
				return &s
			}
		}
	}
	return nil
}

// optStringPresent returns (value, true) when the key is present as a string.
func optStringPresent(c *web.Context, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := c.Body[k]; ok {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

func optBoolPresent(c *web.Context, keys ...string) (bool, bool) {
	for _, k := range keys {
		if v, ok := c.Body[k]; ok {
			if b, ok := v.(bool); ok {
				return b, true
			}
		}
	}
	return false, false
}

// optNullableField models Option<Option<String>>: returns (present, value)
// where present is true when the key exists at all; value is nil for an
// explicit JSON null, else the string.
func optNullableField(c *web.Context, keys ...string) (bool, any) {
	for _, k := range keys {
		if v, ok := c.Body[k]; ok {
			if v == nil {
				return true, nil
			}
			if s, ok := v.(string); ok {
				return true, s
			}
			return true, nil
		}
	}
	return false, nil
}
