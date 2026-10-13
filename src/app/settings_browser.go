package app

// settings_browser.go mirrors upstream src/app/settings/browser
// (components/settings/browser-settings): the browser-tools toggles (DB-backed
// via get/set_browser_tools_settings) plus the loopback bridge status
// (browser_bridge_status). A compact subset of the upstream panel.

import "github.com/egoist/mygo/ui"

func (s *Shell) viewSettingsBrowser(c *ui.Context) {
	t := c.Theme()
	settings := asObj(s.ensure("browser-tools", "get_browser_tools_settings", nil))
	status := asObj(s.ensure("browser-bridge", "browser_bridge_status", nil))

	ui.Row(c.Key("br-head")).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Browser").FontSize(22).Bold()
		ui.Spacer(c)
		if ui.Button(c.Key("br-refresh"), "Refresh").Clicked() {
			s.invalidate("browser-tools")
			s.invalidate("browser-bridge")
		}
	})
	if e := s.errFor("browser-tools"); e != "" {
		ui.Text(c, "backend error: "+e).TextColor(t.Danger)
		return
	}
	ui.Text(c, "Browser tools").FontSize(13).Bold()
	ui.Text(c, "Let agents drive a loopback browser through the bridge.").FontSize(11).TextColor(t.TextMuted)
	if settings == nil {
		ui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
		return
	}
	enabled := truthy(settings["enabled"])
	evalOn := truthy(settings["eval"])
	s.browserToggleRow(c, "br-enabled", "Enabled", enabled, func() { s.setBrowserTools(!enabled, evalOn) })
	s.browserToggleRow(c, "br-eval", "Allow eval", evalOn, func() { s.setBrowserTools(enabled, !evalOn) })

	ui.Divider(c)
	ui.Text(c, "Bridge").FontSize(13).Bold()
	if status == nil {
		ui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
		return
	}
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "Status").TextColor(t.TextMuted)
		if truthy(status["enabled"]) {
			ui.Text(c, "running").Bold().TextColor(t.Success)
		} else {
			ui.Text(c, "stopped").Bold().TextColor(t.TextMuted)
		}
	})
	if ports, ok := status["ports"].([]any); ok && len(ports) > 0 {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "Ports").TextColor(t.TextMuted)
			ui.Textf(c, "%v", ports).Bold()
		})
	}
	if host := str(status, "public_host"); host != "" {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "Public host").TextColor(t.TextMuted)
			ui.Text(c, host).Bold()
		})
	}
}

// browserToggleRow renders a labelled on/off row with a toggle button.
func (s *Shell) browserToggleRow(c *ui.Context, key, label string, on bool, toggle func()) {
	t := c.Theme()
	ui.Row(c.Key(key)).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).TextColor(t.TextMuted).Width(140)
		if on {
			ui.Text(c, "on").Bold().TextColor(t.Success)
		} else {
			ui.Text(c, "off").Bold().TextColor(t.TextMuted)
		}
		if ui.Button(c.Key(key+"-btn"), "Toggle").Clicked() {
			toggle()
		}
	})
}

// setBrowserTools persists the browser-tools settings (set_browser_tools_settings).
func (s *Shell) setBrowserTools(enabled, evalOn bool) {
	svc := s.svc
	go func() {
		_, err := s.B.raw("set_browser_tools_settings", map[string]any{
			"settings": map[string]any{"enabled": enabled, "eval": evalOn},
		})
		s.mu.Lock()
		if err != nil {
			s.actionMsg = "browser settings: " + err.Error()
		} else {
			delete(s.cache, "browser-tools")
		}
		s.mu.Unlock()
		svc.Invalidate()
	}()
}
