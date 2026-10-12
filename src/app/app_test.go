package app

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDecodeHelpers(t *testing.T) {
	list := asList(json.RawMessage(`[{"name":"a"},{"name":"b"}]`))
	if len(list) != 2 || str(list[0], "name") != "a" {
		t.Fatalf("asList = %v", list)
	}
	if asList(nil) != nil {
		t.Fatal("asList(nil) should be nil")
	}
	obj := asObj(json.RawMessage(`{"language":"zh-CN"}`))
	if str(obj, "language") != "zh-CN" {
		t.Fatalf("asObj = %v", obj)
	}
	if str(nil, "x") != "" {
		t.Fatal("str(nil) should be empty")
	}
}

func TestNumOrString(t *testing.T) {
	if v, ok := numOrString("42").(int64); !ok || v != 42 {
		t.Fatalf("numOrString(42) = %v", numOrString("42"))
	}
	if v, ok := numOrString("chat-abc").(string); !ok || v != "chat-abc" {
		t.Fatalf("numOrString(chat-abc) = %v", numOrString("chat-abc"))
	}
}

func TestItoa(t *testing.T) {
	cases := map[int]string{0: "qm0", 1: "qm1", 10: "qm10", 123: "qm123"}
	for in, want := range cases {
		if got := itoa("qm", in); got != want {
			t.Fatalf("itoa(qm,%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTurnText(t *testing.T) {
	if turnText(map[string]any{"text": "hi"}) != "hi" {
		t.Fatal("text field")
	}
	if turnText(map[string]any{"content": "yo"}) != "yo" {
		t.Fatal("content field")
	}
	if turnText(nil) != "" {
		t.Fatal("nil -> empty")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if firstNonEmpty("", "", "x", "y") != "x" {
		t.Fatal("want first non-empty")
	}
	if firstNonEmpty("", "") != "" {
		t.Fatal("all empty -> empty")
	}
}

func TestToolStatusLabel(t *testing.T) {
	cases := map[string]string{
		"success":  "Completed",
		"failed":   "Failed",
		"rejected": "Rejected",
		"running":  "Running",
		"pending":  "Queued",
		"":         "Pending",
	}
	for in, want := range cases {
		if got := toolStatusLabel(in); got != want {
			t.Fatalf("toolStatusLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToolGlyph(t *testing.T) {
	cases := map[string]*ui.SVG{
		"success":  iconCheck,
		"failed":   iconCross,
		"rejected": iconCross,
		"running":  iconClock,
		"pending":  iconWrench,
		"":         iconWrench,
	}
	for in, want := range cases {
		if got := toolGlyph(in); got != want {
			t.Fatalf("toolGlyph(%q) = %p, want %p", in, got, want)
		}
	}
}

// TestBuildTranscript locks the fold from the flat ACP event stream into
// render rows: consecutive text deltas merge into one bubble, thinking gets its
// own row, a tool call is updated in place by its tool_call_update, and an
// error becomes a note.
func TestBuildTranscript(t *testing.T) {
	got := buildTranscript([]map[string]any{
		{"type": "session_started", "session_id": "s1"},
		{"type": "content_delta", "text": "Hel"},
		{"type": "content_delta", "text": "lo"},
		{"type": "thinking", "text": "hmm"},
		{"type": "tool_call", "tool_call_id": "t1", "title": "Read file", "kind": "read", "status": "running"},
		{"type": "tool_call_update", "tool_call_id": "t1", "status": "success"},
		{"type": "tool_call_update", "tool_call_id": "nope", "status": "failed"},
		{"type": "content_delta", "text": "done"},
		{"type": "turn_complete", "stop_reason": "end_turn"},
		{"type": "error", "message": "boom"},
	})
	if len(got) != 5 {
		t.Fatalf("rows = %d, want 5: %+v", len(got), got)
	}
	if got[0].kind != "message" || got[0].role != "assistant" || got[0].text != "Hello" {
		t.Fatalf("row0 = %+v", got[0])
	}
	if got[1].kind != "thinking" || got[1].text != "hmm" {
		t.Fatalf("row1 = %+v", got[1])
	}
	if got[2].kind != "tool" || got[2].tool.title != "Read file" || got[2].tool.status != "success" {
		t.Fatalf("row2 = %+v", got[2].tool)
	}
	if got[3].kind != "message" || got[3].text != "done" {
		t.Fatalf("row3 = %+v", got[3])
	}
	if got[4].kind != "note" || got[4].note != "boom" {
		t.Fatalf("row4 = %+v", got[4])
	}
}

func TestProseSpans(t *testing.T) {
	spans := proseSpans("run `go test` now", themeLight)
	if len(spans) != 3 {
		t.Fatalf("spans = %d, want 3: %+v", len(spans), spans)
	}
	if spans[0].Text != "run " || spans[2].Text != " now" {
		t.Fatalf("body spans = %+v", spans)
	}
	if spans[1].Text != "go test" || spans[1].Font == "" {
		t.Fatalf("code span = %+v", spans[1])
	}
}

func TestComposerStatus(t *testing.T) {
	if composerStrip("connected", "") != "" {
		t.Fatal("connected should have no strip")
	}
	if composerStrip("error", "") == "" || composerStrip("disconnected", "") == "" {
		t.Fatal("degraded states should warn")
	}
	if composerStrip("error", "custom") != "custom" {
		t.Fatal("explicit message should win")
	}
	if composerHint("prompting") == "" || composerHint("connected") != "" {
		t.Fatal("hint mapping")
	}
}
