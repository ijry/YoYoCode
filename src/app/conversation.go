package app

// conversation.go is the upstream-style visual layer of the conversation detail
// surface: message bubbles, tool-call cards and the composer dock. It mirrors
// upstream components/message (Message + MessageContent), ai-elements/tool
// (Tool + ToolHeader + ToolContent) and chat/message-input (the composer
// chrome), translated into mygo's widget tree.
//
// The reference look, taken from those files:
//
//   - transcript column: p-4, gap-8 between rows;
//   - user message: right-aligned (ml-auto) capsule, w-fit max-w-[88%],
//     rounded-lg bg-secondary px-4 py-3 text-sm;
//   - assistant message: plain full-width prose (no capsule);
//   - tool call: full-width rounded-md card, border-border/60, header with a
//     tool glyph + title + rounded-full status badge + chevron; body p-4;
//   - composer: rounded-xl chrome with a hairline border-foreground/20, the
//     draft input, and a footer row with the actions on the right.

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// ─── transcript view model ─────────────────────────────────────────────────

// transcriptItem is one row of the rendered transcript. A live ACP turn arrives
// as a flat stream of content/thinking deltas and tool-call events; buildTranscript
// folds that into role-grouped rows so each renders as a upstream bubble or card
// instead of a raw envelope dump.
type transcriptItem struct {
	kind string // message | thinking | tool | note
	role string // user | assistant | system (message rows only)
	text string
	tool *toolCard
	note string
}

// toolCard is one tool call's live rendered state, updated in place as
// tool_call_update events arrive.
type toolCard struct {
	id     string
	title  string
	kind   string
	status string // pending | running | success | failed | rejected
	output string
}

// buildTranscript folds a connection's flat ACP event stream into render rows.
func buildTranscript(events []map[string]any) []transcriptItem {
	var items []transcriptItem
	byTool := map[string]int{}
	var open *transcriptItem
	flush := func() {
		if open != nil {
			items = append(items, *open)
			open = nil
		}
	}
	for _, ev := range events {
		typ := str(ev, "type")
		switch typ {
		case "content_delta", "thinking":
			text := str(ev, "text")
			if text == "" {
				continue
			}
			want := "message"
			if typ == "thinking" {
				want = "thinking"
			}
			if open == nil || open.kind != want {
				flush()
				open = &transcriptItem{kind: want, role: "assistant"}
			}
			open.text += text
		case "tool_call":
			flush()
			tc := &toolCard{
				id:     str(ev, "tool_call_id"),
				title:  firstNonEmpty(str(ev, "title"), str(ev, "kind"), "Tool"),
				kind:   str(ev, "kind"),
				status: str(ev, "status"),
			}
			items = append(items, transcriptItem{kind: "tool", tool: tc})
			if tc.id != "" {
				byTool[tc.id] = len(items) - 1
			}
		case "tool_call_update":
			if i, ok := byTool[str(ev, "tool_call_id")]; ok {
				if v := str(ev, "title"); v != "" {
					items[i].tool.title = v
				}
				if v := str(ev, "status"); v != "" {
					items[i].tool.status = v
				}
			}
		case "turn_complete":
			flush()
		case "error":
			flush()
			items = append(items, transcriptItem{kind: "note", note: str(ev, "message")})
		}
	}
	flush()
	return items
}

// ─── transcript rendering ──────────────────────────────────────────────────

// viewTranscript renders the whole transcript column: parsed historical turns
// (when a parser is available) followed by the live rows, in upstream's bubble and
// card language. An empty transcript shows a centred hint rather than a blank
// scroll area.
func (s *Shell) viewTranscript(c *ui.Context, turns []any, live []transcriptItem) {
	t := c.Theme()
	if len(turns) == 0 && len(live) == 0 {
		ui.Column(c.Key("tr-empty")).Fill().Center().Gap(6).Children(func() {
			ui.Text(c, "No messages yet").FontSize(15).Bold().TextColor(t.TextMuted)
			ui.Text(c, "Send a prompt below to start the agent.").FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	ui.Scroll(c.Key("transcript")).Grow(1).Children(func() {
		ui.Column(c).FillWidth().Gap(18).Padding(2).Children(func() {
			for i, raw := range turns {
				m, _ := raw.(map[string]any)
				s.viewHistoricalTurn(c, itoa("turn", i), m)
			}
			for i, it := range live {
				key := itoa("tr", i)
				switch it.kind {
				case "message":
					s.viewMessageBubble(c, key, it.role, it.text)
				case "thinking":
					s.viewThinkingRow(c, key, it.text)
				case "tool":
					s.viewToolCard(c, key, it.tool)
				case "note":
					s.viewNoteRow(c, key, it.note)
				}
			}
		})
	})
}

// viewMessageBubble renders one role-grouped message in upstream's language: the
// user's is a right-aligned gray capsule hugging its content (max 88% wide),
// the assistant's is plain full-width prose, and a system note is a muted
// bordered card. Mirrors upstream Message + MessageContent.
func (s *Shell) viewMessageBubble(c *ui.Context, key, role, text string) {
	t := c.Theme()
	switch role {
	case "user":
		ui.Row(c.Key(key)).FillWidth().Justify(ui.End).Children(func() {
			ui.Column(c).MaxWidthPercent(88).Radius(8).Background(t.SurfaceHover).
				Padding(12, 16, 12, 16).Children(func() {
				viewProse(c, text, t)
			})
		})
	case "system":
		ui.Column(c.Key(key)).FillWidth().Radius(6).Border(1, t.Border).
			Background(t.Surface).Padding(12, 12, 12, 12).Children(func() {
			ui.Text(c, text).FontSize(13).TextColor(t.TextMuted).Wrap()
		})
	default:
		ui.Column(c.Key(key)).FillWidth().Children(func() {
			viewProse(c, text, t)
		})
	}
}

// viewThinkingRow renders the agent's reasoning the way upstream does: italic,
// muted, and visibly subordinate to the reply that follows.
func (s *Shell) viewThinkingRow(c *ui.Context, key, text string) {
	t := c.Theme()
	ui.Column(c.Key(key)).FillWidth().Padding(0, 2, 0, 2).Children(func() {
		ui.RichText(c, ui.Span{Text: text, Italic: true, Color: t.TextMuted}).
			FontSize(13).LineHeight(1.5).Wrap()
	})
}

// viewNoteRow renders a non-message transcript row (an agent error) in upstream's
// alert tone.
func (s *Shell) viewNoteRow(c *ui.Context, key, note string) {
	t := c.Theme()
	ui.Row(c.Key(key)).FillWidth().Radius(6).Border(1, t.Danger.Alpha(0.3)).
		Background(t.Danger.Alpha(0.06)).Padding(8, 12, 8, 12).Gap(8).
		AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconAlert).Width(14).Height(14).TextColor(t.Danger)
		ui.Text(c, firstNonEmpty(note, "The agent reported an error.")).
			FontSize(13).TextColor(t.Danger).Wrap()
	})
}

// viewProse renders message text, tinting `inline code` runs. upstream renders
// markdown through Streamdown; the native build styles the one inline form that
// carries real meaning in agent output.
func viewProse(c *ui.Context, text string, t *ui.Theme) {
	if strings.TrimSpace(text) == "" {
		return
	}
	ui.RichText(c, proseSpans(text, t)...).FontSize(14).LineHeight(1.5).Wrap()
}

// proseSpans splits text on backticks: even runs are body text, odd runs are
// inline code (monospace, tinted) — the upstream `inline code` look.
func proseSpans(text string, t *ui.Theme) []ui.Span {
	parts := strings.Split(text, "`")
	spans := make([]ui.Span, 0, len(parts))
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i%2 == 1 {
			spans = append(spans, ui.Span{
				Text:       p,
				Font:       "Consolas, Menlo, monospace",
				Background: t.SurfacePressed,
			})
			continue
		}
		spans = append(spans, ui.Span{Text: p})
	}
	return spans
}

// ─── tool-call cards ───────────────────────────────────────────────────────

// viewToolCard renders one tool call as upstream's Tool shell: a full-width card
// whose header carries the tool glyph, its title, a rounded-full status badge
// and a chevron that flips with the open state. A settled failure starts open
// so it is never hidden behind a click (upstream's AgentCapsule defaultOpen).
func (s *Shell) viewToolCard(c *ui.Context, key string, tc *toolCard) {
	t := c.Theme()
	failed := tc.status == "failed" || tc.status == "rejected"
	open := s.toolIsOpen(tc.id, failed)

	ui.Column(c.Key(key)).FillWidth().Radius(6).Border(1, t.Border.Alpha(0.7)).Children(func() {
		head := ui.Row(c).FillWidth().Padding(8, 12, 8, 12).Gap(8).AlignItems(ui.Center)
		head.Children(func() {
			// upstream's ToolHeader: the tool glyph, the title, then a rounded-full
			// status badge that carries the state icon and its wording, and a
			// chevron on the trailing edge.
			ui.Icon(c, iconWrench).Width(15).Height(15).TextColor(t.TextMuted)
			ui.Text(c, tc.title).FontSize(14).Bold().MaxLines(1).
				TextColor(condColor(failed, t.Danger, t.Text))
			ui.Spacer(c)
			ui.Row(c).Radius(999).Background(t.SurfaceHover).Padding(2, 8, 2, 8).
				Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, toolGlyph(tc.status)).Width(12).Height(12).
					TextColor(toolStatusColor(t, tc.status))
				ui.Text(c, toolStatusLabel(tc.status)).FontSize(11).
					TextColor(toolStatusColor(t, tc.status))
			})
			ui.Icon(c, chevronGlyph(open)).Width(14).Height(14).TextColor(t.TextMuted)
		})
		if head.Clicked() {
			s.toggleTool(tc.id, failed)
		}
		if open {
			ui.Divider(c)
			ui.Column(c).FillWidth().Padding(12, 12, 12, 12).Gap(6).Children(func() {
				ui.Text(c, firstNonEmpty(tc.output, tc.kind, "No output recorded.")).
					FontSize(12).TextColor(t.TextMuted).Wrap()
			})
		}
	})
}

// toolGlyph picks the card's leading glyph, mirroring upstream's status icons.
func toolGlyph(status string) *ui.SVG {
	switch status {
	case "success":
		return iconCheck
	case "failed", "rejected":
		return iconCross
	case "running":
		return iconClock
	default:
		return iconWrench
	}
}

// chevronGlyph points right when collapsed and down when expanded.
func chevronGlyph(open bool) *ui.SVG {
	if open {
		return iconChevronD
	}
	return iconChevron
}

// toolStatusColor maps a tool status onto the theme's semantic colors, matching
// upstream's status icon tints (green check, red cross, amber clock).
func toolStatusColor(t *ui.Theme, status string) ui.Color {
	switch status {
	case "success":
		return t.Success
	case "failed", "rejected":
		return t.Danger
	case "running", "pending":
		return t.Warning
	default:
		return t.TextMuted
	}
}

// toolStatusLabel is upstream's status wording (Folder.chat.tool.status.*).
func toolStatusLabel(status string) string {
	switch status {
	case "success":
		return "Completed"
	case "failed":
		return "Failed"
	case "rejected":
		return "Rejected"
	case "running":
		return "Running"
	case "pending":
		return "Queued"
	default:
		return firstNonEmpty(status, "Pending")
	}
}

// condColor returns a when cond, else b.
func condColor(cond bool, a, b ui.Color) ui.Color {
	if cond {
		return a
	}
	return b
}

// toolIsOpen reports whether a tool card is expanded. Cards with no recorded
// choice fall back to their default (open on failure).
func (s *Shell) toolIsOpen(id string, fallback bool) bool {
	if id == "" {
		return fallback
	}
	if v, ok := s.toolOpen[id]; ok {
		return v
	}
	return fallback
}

// toggleTool flips a tool card's open state.
func (s *Shell) toggleTool(id string, fallback bool) {
	if id == "" {
		return
	}
	s.toolOpen[id] = !s.toolIsOpen(id, fallback)
}

// ─── historical turns ──────────────────────────────────────────────────────

// viewHistoricalTurn renders one parsed transcript turn. Its blocks are either
// text (prose) or tool_use (a tool card); consecutive text blocks join into one
// bubble, matching the split the live stream produces.
func (s *Shell) viewHistoricalTurn(c *ui.Context, key string, m map[string]any) {
	role := str(m, "role")
	blocks, _ := m["blocks"].([]any)
	if len(blocks) == 0 {
		s.viewMessageBubble(c, key, role, turnText(m))
		return
	}
	var prose strings.Builder
	part := 0
	flush := func() {
		if prose.Len() == 0 {
			return
		}
		s.viewMessageBubble(c, key+"-p"+itoa("", part), role, prose.String())
		prose.Reset()
		part++
	}
	for _, raw := range blocks {
		b, _ := raw.(map[string]any)
		if str(b, "type") == "tool_use" {
			flush()
			s.viewToolCard(c, key+"-t"+itoa("", part), &toolCard{
				title:  firstNonEmpty(str(b, "tool_name"), "Tool"),
				kind:   str(b, "tool_name"),
				status: "success",
				output: str(b, "input_preview"),
			})
			part++
			continue
		}
		prose.WriteString(str(b, "text"))
	}
	flush()
}

// ─── composer dock ─────────────────────────────────────────────────────────

// viewComposerDock renders the composer: upstream's rounded-xl chrome behind a
// hairline border, the draft input, and a footer row with the actions on the
// right. When the connection is degraded a status strip is drawn above the
// chrome (composer-dock-strip).
func (s *Shell) viewComposerDock(c *ui.Context, connID, status, statusMsg string) {
	t := c.Theme()
	if strip := composerStrip(status, statusMsg); strip != "" {
		ui.Row(c.Key("composer-strip")).FillWidth().Gap(8).AlignItems(ui.Center).
			Background(t.Warning.Alpha(0.10)).Radius(8).Padding(6, 12, 6, 12).Children(func() {
			ui.Icon(c, iconInfo).Width(13).Height(13).TextColor(t.Warning)
			ui.Text(c, strip).FontSize(12).TextColor(t.Warning).MaxLines(1)
		})
	}
	ui.Column(c.Key("composer")).FillWidth().Radius(12).
		Border(1, t.Text.Alpha(0.2)).Children(func() {
		ui.Column(c).FillWidth().Padding(12, 12, 4, 12).Children(func() {
			ui.TextInput(c.Key("composer-input"), &s.composer).
				FillWidth().Placeholder("Message the agent…")
		})
		ui.Row(c).FillWidth().Padding(0, 8, 8, 8).Gap(8).AlignItems(ui.Center).Children(func() {
			if hint := composerHint(status); hint != "" {
				ui.Text(c, hint).FontSize(11).TextColor(t.TextMuted).MaxLines(1)
			}
			ui.Spacer(c)
			if connID != "" {
				if ui.Button(c.Key("cancel"), "Stop").Clicked() {
					s.cancelTurn(connID)
				}
			}
			if ui.PrimaryButton(c.Key("send"), "Send").Disabled(s.composer == "").Clicked() {
				s.sendPrompt(s.selectedConv)
			}
		})
	})
}

// composerHint is the muted left-hand line in the composer footer.
func composerHint(status string) string {
	switch status {
	case "prompting":
		return "Agent is working…"
	case "connecting":
		return "Connecting to the agent…"
	case "error":
		return "Last turn failed"
	case "disconnected":
		return "Disconnected"
	default:
		return ""
	}
}

// composerStrip is the warning line drawn above the composer when the
// connection is degraded, or "" when there is nothing to report.
func composerStrip(status, msg string) string {
	switch status {
	case "error":
		return firstNonEmpty(msg, "The last turn failed.")
	case "connecting":
		return "Connecting to the agent…"
	case "disconnected":
		return "The agent connection is closed. Send a prompt to reconnect."
	default:
		return ""
	}
}
