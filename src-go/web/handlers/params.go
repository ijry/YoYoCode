package handlers

import (
	"encoding/json"

	"yoyocode/src-go/app_state"
	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// ─── Parameter access ──────────────────────────────────────────────────────
//
// Upstream is inconsistent about casing: most handlers use
// `#[serde(rename_all = "camelCase")]` but plenty use snake_case, and a few
// accept both across client versions. Every getter here therefore accepts an
// alias list and returns the first present, non-zero value. Being lenient on
// input is what keeps mcode-app (and older desktop builds) working against the
// same endpoint.

// GetInt reads an integer param, checking aliases in order.
func GetInt(c *web.Context, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := c.Body[k]; ok {
			if n, ok := toInt64(v); ok {
				return n
			}
		}
	}
	return 0
}

// GetIntOrErr reads a required integer param.
func GetIntOrErr(c *web.Context, keys ...string) (int64, error) {
	if n := GetInt(c, keys...); n > 0 {
		return n, nil
	}
	return 0, web.ErrInvalidInput(joinKeys(keys) + " required")
}

// GetString reads a string param, checking aliases in order.
func GetString(c *web.Context, keys ...string) string {
	for _, k := range keys {
		if v, ok := c.Body[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// GetStringOrErr reads a required string param.
func GetStringOrErr(c *web.Context, keys ...string) (string, error) {
	if s := GetString(c, keys...); s != "" {
		return s, nil
	}
	return "", web.ErrInvalidInput(joinKeys(keys) + " required")
}

// GetBool reads a boolean param.
func GetBool(c *web.Context, keys ...string) bool {
	for _, k := range keys {
		if v, ok := c.Body[k]; ok {
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return false
}

// GetIntSlice reads an array-of-numbers param.
func GetIntSlice(c *web.Context, keys ...string) []int64 {
	for _, k := range keys {
		if v, ok := c.Body[k]; ok {
			if arr, ok := v.([]any); ok {
				out := make([]int64, 0, len(arr))
				for _, item := range arr {
					if n, ok := toInt64(item); ok {
						out = append(out, n)
					}
				}
				return out
			}
		}
	}
	return nil
}

// GetStringSlice reads an array-of-strings param.
func GetStringSlice(c *web.Context, keys ...string) []string {
	for _, k := range keys {
		if v, ok := c.Body[k]; ok {
			if arr, ok := v.([]any); ok {
				out := make([]string, 0, len(arr))
				for _, item := range arr {
					if s, ok := item.(string); ok {
						out = append(out, s)
					}
				}
				return out
			}
		}
	}
	return nil
}

// GetRaw returns a nested object as raw JSON (for pass-through payloads).
func GetRaw(c *web.Context, keys ...string) json.RawMessage {
	for _, k := range keys {
		if v, ok := c.Body[k]; ok && v != nil {
			if b, err := json.Marshal(v); err == nil {
				return b
			}
		}
	}
	return nil
}

// Bind unmarshals the whole request body into out.
func Bind(c *web.Context, out any) error {
	if len(c.RawBody) == 0 {
		return web.ErrInvalidInput("empty request body")
	}
	if err := json.Unmarshal(c.RawBody, out); err != nil {
		return web.ErrInvalidInput("malformed request body: " + err.Error())
	}
	return nil
}

func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int:
		return int64(t), true
	case int64:
		return t, true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		// Clients occasionally stringify numeric ids; parse defensively.
		var n int64
		var neg bool
		for i, r := range t {
			if i == 0 && r == '-' {
				neg = true
				continue
			}
			if r < '0' || r > '9' {
				return 0, false
			}
			n = n*10 + int64(r-'0')
		}
		if neg {
			n = -n
		}
		return n, true
	}
	return 0, false
}

func joinKeys(keys []string) string {
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += " or "
		}
		out += k
	}
	return out
}

// ─── State access ──────────────────────────────────────────────────────────

func requireDB(c *web.Context) (*db.Database, error) {
	d := stateDB(c)
	if d == nil || d.Conn == nil {
		return nil, web.ErrDatabase("database unavailable")
	}
	return d, nil
}

func appState(c *web.Context) (*appstate.AppState, bool) {
	st, ok := c.State.(*appstate.AppState)
	return st, ok
}

// ─── Firehose emission ─────────────────────────────────────────────────────
//
// Upstream emits a `conversation://changed` firehose event after every mutation
// so other connected clients (desktop + mobile) re-fetch. mcode-app has an
// explicit invalidation path for this channel — see
// `services/conversation/conversationListRefresh.ts`.

// EmitConversationChanged broadcasts a conversation mutation on the legacy
// firehose. Best-effort: a missing broadcaster must never fail a mutation that
// already succeeded.
func EmitConversationChanged(c *web.Context, conversationID int64) {
	if st, ok := appState(c); ok && st != nil {
		st.Broadcaster().Send("conversation://changed", map[string]any{
			"conversationId": conversationID,
		})
	}
}

// Emit broadcasts an arbitrary channel/payload on the legacy firehose.
func Emit(c *web.Context, channel string, payload any) {
	if st, ok := appState(c); ok && st != nil {
		st.Broadcaster().Send(channel, payload)
	}
}

// ─── Response helpers ──────────────────────────────────────────────────────

// EmptyResult is what upstream returns for handlers declared `Result<Json<()>>`:
// the body is literally `null`, NOT `{}`. Serde writes the unit type as null,
// and the router already maps a nil result onto that — so this must stay nil
// rather than an empty map, or every such handler ships the wrong JSON.
func EmptyResult() any { return nil }
