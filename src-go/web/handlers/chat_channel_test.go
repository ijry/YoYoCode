package handlers_test

import "testing"

func TestChatChannelCRUD(t *testing.T) {
	srv := newTestServer(t)

	// empty list
	_, empty := callArr(t, srv, "list_chat_channels", map[string]any{})
	if len(empty) != 0 {
		t.Fatalf("expected no channels, got %d", len(empty))
	}

	// reject bad type
	if code, _ := call(t, srv, "create_chat_channel", map[string]any{
		"name": "x", "channelType": "slack", "configJson": "{}", "enabled": true,
		"dailyReportEnabled": false,
	}); code == 200 {
		t.Fatalf("invalid channel type should be rejected")
	}

	// create
	_, created := call(t, srv, "create_chat_channel", map[string]any{
		"name": "Team Lark", "channelType": "lark", "configJson": "{\"app_id\":\"a\"}",
		"enabled": false, "dailyReportEnabled": true, "dailyReportTime": "09:00",
	})
	if created["channel_type"] != "lark" || created["name"] != "Team Lark" {
		t.Fatalf("create wrong: %v", created)
	}
	if created["enabled"] != false || created["daily_report_enabled"] != true || created["daily_report_time"] != "09:00" {
		t.Fatalf("create fields wrong: %v", created)
	}
	id := created["id"]

	// list has 1
	_, list := callArr(t, srv, "list_chat_channels", map[string]any{})
	if len(list) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(list))
	}

	// update: enable + change name + clear daily time (null)
	_, upd := call(t, srv, "update_chat_channel", map[string]any{
		"id": id, "name": "Lark 2", "enabled": true, "dailyReportTime": nil,
	})
	if upd["name"] != "Lark 2" || upd["enabled"] != true {
		t.Fatalf("update wrong: %v", upd)
	}
	if upd["daily_report_time"] != nil {
		t.Fatalf("daily_report_time should be cleared, got %v", upd["daily_report_time"])
	}

	// token: none → false, save → true, delete → false
	_, has0 := callBool(t, srv, "get_chat_channel_has_token", map[string]any{"channelId": id})
	if has0 {
		t.Fatalf("should have no token yet")
	}
	call(t, srv, "save_chat_channel_token", map[string]any{"channelId": id, "token": "secret-xyz"})
	_, has1 := callBool(t, srv, "get_chat_channel_has_token", map[string]any{"channelId": id})
	if !has1 {
		t.Fatalf("token should be present after save")
	}
	call(t, srv, "delete_chat_channel_token", map[string]any{"channelId": id})
	_, has2 := callBool(t, srv, "get_chat_channel_has_token", map[string]any{"channelId": id})
	if has2 {
		t.Fatalf("token should be gone after delete")
	}

	// delete channel
	if code, _ := call(t, srv, "delete_chat_channel", map[string]any{"id": id}); code != 200 {
		t.Fatalf("delete failed: %d", code)
	}
	_, after := callArr(t, srv, "list_chat_channels", map[string]any{})
	if len(after) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(after))
	}
}

func TestChatChannelStatusAndMessages(t *testing.T) {
	srv := newTestServer(t)
	// status: no transport → empty list
	_, st := callArr(t, srv, "get_chat_channel_status", map[string]any{})
	if len(st) != 0 {
		t.Fatalf("status should be empty, got %v", st)
	}
	// create a channel, then its message log is empty
	_, ch := call(t, srv, "create_chat_channel", map[string]any{
		"name": "C", "channelType": "telegram", "configJson": "{}", "enabled": false, "dailyReportEnabled": false,
	})
	id := ch["id"]
	_, msgs := callArr(t, srv, "list_chat_channel_messages", map[string]any{"channelId": id})
	if len(msgs) != 0 {
		t.Fatalf("messages should be empty, got %v", msgs)
	}
	// connect/test refused; disconnect is a no-op success
	if code, _ := call(t, srv, "connect_chat_channel", map[string]any{"id": id}); code == 200 {
		t.Fatalf("connect should be refused without transport")
	}
	if code, _ := call(t, srv, "disconnect_chat_channel", map[string]any{"id": id}); code != 200 {
		t.Fatalf("disconnect should succeed as a no-op, got %d", code)
	}
}
