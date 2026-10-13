package app

// pet_panel.go mirrors upstream src/app/pet-panel: the live list of active agent
// sessions, one row per ACP connection. The row follows upstream
// session-display.ts — status precedence waiting > error > running, sorted so
// the sessions that need the user come first — and a session blocked on the
// user shows its permission options inline (PanelPermissionCard). The port's
// backend returns the faithful payload shape (pet_list_active_sessions) but no
// sessions yet, because the ACP engine does not track live connections; the
// panel renders its honest empty state until they appear.

import (
	"fmt"
	"sort"

	"github.com/egoist/mygo/ui"
)

// The two upstream pet routes folded into the single Pet section: pet-panel
// (the active-session list) and pet (the sprite).
const (
	petTabSessions = "sessions"
	petTabSprite   = "sprite"
)

// petStatusKind buckets a session for its row pill, matching upstream
// sessionStatusKind: a pending permission is "waiting", an errored connection is
// "error", everything else is "running".
func petStatusKind(sess map[string]any) string {
	if p, ok := sess["pending"].(map[string]any); ok && p != nil {
		return "waiting"
	}
	if pickStr(sess, "status") == "error" {
		return "error"
	}
	return "running"
}

// petSortRank orders the panel list so the sessions that need the user come
// first (upstream sessionSortRank).
func petSortRank(sess map[string]any) int {
	switch petStatusKind(sess) {
	case "waiting":
		return 0
	case "error":
		return 1
	default:
		return 2
	}
}

// sortPetSessions orders the list in place by petSortRank (stable, so equal
// ranks keep their arrival order).
func sortPetSessions(sessions []map[string]any) {
	sort.SliceStable(sessions, func(i, j int) bool {
		return petSortRank(sessions[i]) < petSortRank(sessions[j])
	})
}

// petStatusLabel is the row pill's text.
func petStatusLabel(kind string) string {
	switch kind {
	case "waiting":
		return "waiting"
	case "error":
		return "error"
	default:
		return "running"
	}
}

// pickStr returns the first non-empty string among keys — the panel reads both
// the upstream camelCase entry keys and the port's snake_case aliases.
func pickStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := str(m, k); v != "" {
			return v
		}
	}
	return ""
}

// pickInt coerces the first present non-zero numeric key to an int.
func pickInt(m map[string]any, keys ...string) int {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if n := intOf(v); n != 0 {
				return n
			}
		}
	}
	return 0
}

// viewPetPanel renders the active-session list (pet_list_active_sessions).
func (s *Shell) viewPetPanel(c *ui.Context) {
	t := c.Theme()
	payload := asObj(s.ensure("pet-sessions", "pet_list_active_sessions", nil))
	sessions := asListVal(payload["sessions"])

	ui.Row(c.Key("pp-head")).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Active sessions").FontSize(18).Bold()
		if len(sessions) > 0 {
			ui.Textf(c, "(%d)", len(sessions)).FontSize(12).TextColor(t.TextMuted)
		}
		ui.Spacer(c)
		if ui.Button(c.Key("pp-refresh"), "Refresh").Clicked() {
			s.invalidate("pet-sessions")
		}
	})
	if e := s.errFor("pet-sessions"); e != "" {
		ui.Text(c, "backend error: "+e).TextColor(t.Danger)
		return
	}
	if payload == nil {
		ui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
		return
	}
	if len(sessions) == 0 {
		// Upstream's empty state: a glyph, a line and a hint, centred.
		ui.Column(c.Key("pp-empty")).Grow(1).Center().Gap(6).Children(func() {
			if iconPet != nil {
				ui.Icon(c, iconPet).Width(26).Height(26).TextColor(t.TextMuted)
			}
			ui.Text(c, "No active sessions").FontSize(14).Bold()
			ui.Text(c, "Sessions appear here while agents are working.").FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	sortPetSessions(sessions)
	ui.Scroll(c.Key("pp-list")).Grow(1).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			for i, sess := range sessions {
				s.petSessionRow(c, i, sess)
			}
		})
	})
}

// petSessionRow renders one session: an agent glyph, the title (with an optional
// "sub-agent of …" line), a status pill, and — when the session is blocked on
// the user — its inline permission card.
func (s *Shell) petSessionRow(c *ui.Context, i int, sess map[string]any) {
	t := c.Theme()
	agent := pickStr(sess, "agent_type", "agentType")
	title := firstNonEmpty(pickStr(sess, "title"), agent, "(untitled)")
	kind := petStatusKind(sess)
	dot := t.Accent
	switch kind {
	case "waiting":
		dot = t.Warning
	case "error":
		dot = t.Danger
	}
	row := ui.Row(c.Key(itoa("pp", i))).PaddingX(8).PaddingY(6).Gap(8).Radius(6).AlignItems(ui.Center)
	row.Children(func() {
		if iconPet != nil {
			ui.Icon(c, iconPet).Width(14).Height(14).TextColor(t.TextMuted)
		}
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Text(c, title).MaxLines(1).TextColor(t.Text)
			if parent, ok := sess["parent"].(map[string]any); ok && parent != nil {
				if ptitle := firstNonEmpty(pickStr(parent, "title"), pickStr(parent, "agent_type", "agentType")); ptitle != "" {
					ui.Text(c, "sub-agent of · "+ptitle).FontSize(10).MaxLines(1).TextColor(t.TextMuted)
				}
			}
		})
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "\u25CF").FontSize(8).TextColor(dot)
			ui.Text(c, petStatusLabel(kind)).FontSize(10).TextColor(t.TextMuted)
		})
	})
	if row.Clicked() {
		s.jumpToSession(sess)
	}
	if pending, ok := sess["pending"].(map[string]any); ok && pending != nil {
		s.petPermissionCard(c, i, pickStr(sess, "connection_id", "connectionId"), pending)
	}
}

// petPermissionCard renders the compact pending-permission view inline, with its
// option buttons posting straight to the session's connection
// (acp_respond_permission) — upstream PanelPermissionCard.
func (s *Shell) petPermissionCard(c *ui.Context, i int, connID string, pending map[string]any) {
	t := c.Theme()
	reqID := pickStr(pending, "request_id", "requestId")
	ui.Column(c.Key(itoa("ppp", i))).FillWidth().Radius(6).
		Border(1, t.Warning.Alpha(0.35)).Background(t.Warning.Alpha(0.07)).
		Padding(8, 8, 8, 8).Gap(6).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if iconAlert != nil {
				ui.Icon(c, iconAlert).Width(12).Height(12).TextColor(t.Warning)
			}
			ui.Text(c, "Permission requested").FontSize(12).Bold()
		})
		if tc, ok := pending["tool_call"].(map[string]any); ok && tc != nil {
			if lbl := firstNonEmpty(pickStr(tc, "label"), pickStr(tc, "title")); lbl != "" {
				ui.Text(c, lbl).FontSize(12).TextColor(t.TextMuted).MaxLines(2).Wrap()
			}
		}
		ui.Row(c).Gap(6).Wrap().Children(func() {
			opts, _ := pending["options"].([]any)
			for j, raw := range opts {
				o, _ := raw.(map[string]any)
				oid := firstNonEmpty(pickStr(o, "option_id", "optionId"), pickStr(o, "id"))
				name := firstNonEmpty(pickStr(o, "name"), pickStr(o, "label"), oid)
				if ui.Button(c.Key(itoa("ppo", j)), name).Clicked() {
					s.respondPermission(connID, reqID, oid)
				}
			}
		})
	})
}

// jumpToSession selects the session's conversation in the workspace — the
// single-window analog of upstream focusConversation. A delegation sub-agent is
// never openable as a tab, so its row jumps to the conversation that delegated
// it.
func (s *Shell) jumpToSession(sess map[string]any) {
	target := sess
	if parent, ok := sess["parent"].(map[string]any); ok && parent != nil {
		target = parent
	}
	if fid := pickInt(target, "folder_id", "folderId"); fid > 0 {
		s.selectedFolder = fmt.Sprintf("%d", fid)
	}
	if cid := pickInt(target, "conversation_id", "conversationId"); cid > 0 {
		s.selectedConv = fmt.Sprintf("%d", cid)
	}
	s.section = "workspace"
	s.workbench = ""
	if s.selectedFolder != "" {
		s.invalidate("convs:" + s.selectedFolder)
	}
}
