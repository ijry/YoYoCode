package app

// render_test.go renders the conversation detail headlessly (mygo's Tester
// draws frames in memory, no window) and asserts the visual structure upstream's
// transcript is made of: the assistant prose bubble, the tool-call card with
// its status badge, the conversation header and the composer dock.
//
// Set MYGO_UI_PNG to a directory to also write the rendered frames there, for
// looking at by eye.

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// conversationShellForTest builds a Shell with the conversation detail already
// selected and a live connection carrying a short turn, with every backend
// response pre-seeded so the view renders without touching the network.
func conversationShellForTest(t *testing.T) *Shell {
	t.Helper()
	b := newBackendForTest(t)
	s := New(b)
	// A pre-set stream keeps ensureStream from dialing, and lets the test push
	// the ACP events the transcript is built from.
	s.stream = newStreamForTest()
	s.selectedFolder = "1"
	s.selectedConv = "1"
	s.connByConv["1"] = "c1"

	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"light"}`)
	s.cache["folders"] = json.RawMessage(`[{"id":1,"name":"demo","path":"/tmp/demo"}]`)
	s.cache["convs:1"] = json.RawMessage(`[{"id":1,"title":"Fix the parser","agent_type":"claude_code","status":"active"}]`)
	s.cache["turns:1"] = json.RawMessage(`{"turns":[{"role":"user","blocks":[{"type":"text","text":"Fix the parser in ` + "`main.go`" + `."}]}]}`)
	s.cache["agents"] = json.RawMessage(`[{"agent_type":"claude_code"}]`)
	s.cache["snapshot:c1"] = json.RawMessage(`{"connection_id":"c1","status":"prompting","active_tool_calls":[]}`)

	push := func(ev map[string]any) {
		ev["connection_id"] = "c1"
		s.stream.handle(&wsFrame{Type: "event", Envelope: ev})
	}
	push(map[string]any{"seq": float64(1), "type": "content_delta", "text": "Let me look at the parser first."})
	push(map[string]any{"seq": float64(2), "type": "tool_call", "tool_call_id": "t1", "title": "Read main.go", "kind": "read", "status": "running"})
	push(map[string]any{"seq": float64(3), "type": "tool_call_update", "tool_call_id": "t1", "status": "success"})
	return s
}

// dumpFrame writes a rendered frame when MYGO_UI_PNG names a directory.
func dumpFrame(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("MYGO_UI_PNG")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestConversationDetailRenders(t *testing.T) {
	s := conversationShellForTest(t)
	// NewTester runs the view in strict mode: a duplicate element key panics,
	// so this also guards key uniqueness across the detail surface.
	tt := ui.NewTester(s.View, 1240, 820)

	for _, want := range []string{
		"Fix the parser",            // conversation header
		"Let me look at the parser", // assistant bubble
		"Read main.go",              // tool card title
		"Completed",                 // tool status badge
		"Send",                      // composer action
		"demo",                      // folder row
	} {
		if !tt.HasText(want) {
			t.Errorf("frame is missing %q; texts = %v", want, tt.Texts())
		}
	}
	dumpFrame(t, tt, "conversation-light")
	lightBg := tt.Image().RGBAAt(700, 420)

	// Dark is driven by the backend's appearance mode, which the view re-applies
	// every frame, so flip the seeded response rather than the tester's theme.
	s.cache["appearance-mode"] = json.RawMessage(`{"mode":"dark"}`)
	tt.Frame()
	if !tt.HasText("Let me look at the parser") || !tt.HasText("Completed") {
		t.Errorf("dark frame lost content; texts = %v", tt.Texts())
	}
	darkBg := tt.Image().RGBAAt(700, 420)
	t.Logf("mode=%q bg light=%v dark=%v", s.appearanceMode, lightBg, darkBg)
	if lightBg == darkBg {
		t.Errorf("dark mode did not repaint the canvas: %v", lightBg)
	}
	dumpFrame(t, tt, "conversation-dark")
}

// TestToolCardExpands drives the tool card's header click and asserts the body
// (the tool's detail line) appears — the collapsible behaviour upstream's Tool
// shell has.
func TestToolCardExpands(t *testing.T) {
	s := conversationShellForTest(t)
	// Give the call an output so the expanded body has something to show.
	s.cache["snapshot:c1"] = json.RawMessage(`{"connection_id":"c1","status":"prompting","active_tool_calls":[]}`)
	tt := ui.NewTester(s.View, 1240, 820)

	// A settled success starts collapsed, so the body is not in the frame yet.
	if tt.HasText("read") {
		t.Fatalf("collapsed card should hide its detail line; texts = %v", tt.Texts())
	}
	if err := tt.Click("Read main.go"); err != nil {
		t.Fatalf("click tool header: %v", err)
	}
	tt.Frame()
	if !tt.HasText("read") {
		t.Fatalf("expanded card should show its detail line; texts = %v", tt.Texts())
	}
	dumpFrame(t, tt, "conversation-tool-open")
}
