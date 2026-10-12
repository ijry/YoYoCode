package handlers_test

import (
	"strings"
	"testing"
)

func TestChatCommandPrefix(t *testing.T) {
	srv := newTestServer(t)
	// default is "/"
	code, v := rawPostJSON(t, srv, "get_chat_command_prefix", map[string]any{})
	if code != 200 || strings.TrimSpace(v) != "\"/\"" {
		t.Fatalf("default prefix = %q (code %d)", v, code)
	}
	// set a valid prefix
	if c, _ := call(t, srv, "set_chat_command_prefix", map[string]any{"prefix": "!!"}); c != 200 {
		t.Fatalf("set valid prefix failed: %d", c)
	}
	_, v2 := rawPostJSON(t, srv, "get_chat_command_prefix", map[string]any{})
	if strings.TrimSpace(v2) != "\"!!\"" {
		t.Fatalf("prefix not persisted: %q", v2)
	}
	// reject alphanumeric / too long / empty
	for _, bad := range []string{"a", "/x", "////", ""} {
		if c, _ := call(t, srv, "set_chat_command_prefix", map[string]any{"prefix": bad}); c == 200 {
			t.Fatalf("prefix %q should be rejected", bad)
		}
	}
}

func TestChatMessageLanguage(t *testing.T) {
	srv := newTestServer(t)
	_, v := rawPostJSON(t, srv, "get_chat_message_language", map[string]any{})
	if strings.TrimSpace(v) != "\"en\"" {
		t.Fatalf("default language = %q", v)
	}
	if c, _ := call(t, srv, "set_chat_message_language", map[string]any{"language": "ZH-CN"}); c != 200 {
		t.Fatalf("set language failed")
	}
	_, v2 := rawPostJSON(t, srv, "get_chat_message_language", map[string]any{})
	if strings.TrimSpace(v2) != "\"zh-cn\"" {
		t.Fatalf("language should lowercase+persist, got %q", v2)
	}
	if c, _ := call(t, srv, "set_chat_message_language", map[string]any{"language": "klingon"}); c == 200 {
		t.Fatalf("unsupported language should be rejected")
	}
}

func TestChatEventFilter(t *testing.T) {
	srv := newTestServer(t)
	// default null
	code, out := rawPostJSON(t, srv, "get_chat_event_filter", map[string]any{})
	if code != 200 || strings.TrimSpace(out) != "null" {
		t.Fatalf("default filter should be null, got %q", out)
	}
	// set an array
	if c, _ := call(t, srv, "set_chat_event_filter", map[string]any{"filter": []any{"a", "b"}}); c != 200 {
		t.Fatalf("set filter failed")
	}
	_, arr := callArr(t, srv, "get_chat_event_filter", map[string]any{})
	if len(arr) != 2 || arr[0] != "a" {
		t.Fatalf("filter not persisted: %v", arr)
	}
	// set null again
	call(t, srv, "set_chat_event_filter", map[string]any{"filter": nil})
	_, out2 := rawPostJSON(t, srv, "get_chat_event_filter", map[string]any{})
	if strings.TrimSpace(out2) != "null" {
		t.Fatalf("filter should round-trip to null, got %q", out2)
	}
}

func TestChatEventWebhooks(t *testing.T) {
	srv := newTestServer(t)
	// default empty array
	_, arr := callArr(t, srv, "get_chat_event_webhooks", map[string]any{})
	if len(arr) != 0 {
		t.Fatalf("default webhooks should be empty, got %v", arr)
	}
	// set with trimming + dedup + scheme validation
	code, _ := call(t, srv, "set_chat_event_webhooks", map[string]any{
		"webhooks": []any{
			map[string]any{"url": "  https://example.com/hook  ", "enabled": true},
			map[string]any{"url": "https://example.com/hook", "enabled": false}, // dup → dropped
			map[string]any{"url": "", "enabled": true},                          // empty → dropped
		},
	})
	if code != 200 {
		t.Fatalf("set webhooks failed: %d", code)
	}
	_, arr2 := callArr(t, srv, "get_chat_event_webhooks", map[string]any{})
	if len(arr2) != 1 {
		t.Fatalf("expected 1 deduped webhook, got %v", arr2)
	}
	h := arr2[0].(map[string]any)
	if h["url"] != "https://example.com/hook" || h["enabled"] != true {
		t.Fatalf("webhook wrong: %v", h)
	}
	// reject non-http scheme
	if c, _ := call(t, srv, "set_chat_event_webhooks", map[string]any{
		"webhooks": []any{map[string]any{"url": "ftp://x/y", "enabled": true}},
	}); c == 200 {
		t.Fatalf("ftp webhook should be rejected")
	}
}
