package app

// Settings mirrors upstream src/app/settings/* : a sub-navigation plus one
// pane per page. Pages whose backend is already implemented render live data;
// the rest are placeholders tracked against the upstream page tree.

import (
	"encoding/json"

	"github.com/egoist/mygo/ui"
)

// settingsPages is the upstream settings page tree (src/app/settings/*), in
// sidebar order. id matches the upstream route segment.
var settingsPages = []struct{ id, label string }{
	{"general", "General"},
	{"appearance", "Appearance"},
	{"agents", "Agents"},
	{"version-control", "Version Control"},
	{"mcp", "MCP"},
	{"quick-messages", "Quick Messages"},
	{"web-service", "Web Service"},
	{"shortcuts", "Shortcuts"},
	{"skills", "Skills"},
	{"skill-packs", "Skill Packs"},
	{"experts", "Experts"},
	{"science", "Science"},
	{"office-tools", "Office Tools"},
	{"chat-channels", "Chat Channels"},
	{"collaboration", "Collaboration"},
	{"computer-use", "Computer Use"},
	{"browser", "Browser"},
	{"logs", "Logs"},
	{"system", "System"},
}

func (s *Shell) viewSettings(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Fill().Children(func() {
		// Settings sub-navigation.
		ui.Column(c.Key("set-nav")).Width(190).FillHeight().Background(t.Surface).Padding(10).Gap(2).Children(func() {
			ui.Text(c, "Settings").FontSize(12).Bold().TextColor(t.TextMuted)
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Sidebar(c, &s.settingsPage, func() {
					for _, p := range settingsPages {
						ui.SidebarItem(c, p.id, nil, p.label)
					}
				})
			})
		})
		// Active page.
		ui.Column(c.Key("set-page")).Grow(1).FillHeight().Padding(20).Gap(12).Children(func() {
			switch s.settingsPage {
			case "general":
				s.viewSettingsGeneral(c)
			case "appearance":
				s.viewSettingsAppearance(c)
			case "web-service":
				s.viewSettingsWebService(c)
			case "quick-messages":
				s.viewSettingsQuickMessages(c)
			case "agents":
				s.viewSettingsAgents(c)
			case "version-control":
				s.viewSettingsVersionControl(c)
			case "system":
				s.viewSettingsSystem(c)
			case "mcp":
				s.viewSettingsMCP(c)
			case "logs":
				s.viewSettingsLogs(c)
			case "skills":
				s.viewSettingsSkills(c)
			case "chat-channels":
				s.viewSettingsChatChannels(c)
			case "computer-use":
				s.viewSettingsComputerUse(c)
			case "experts":
				s.viewSettingsExperts(c)
			default:
				label := s.settingsPage
				for _, p := range settingsPages {
					if p.id == s.settingsPage {
						label = p.label
					}
				}
				ui.Text(c, label).FontSize(22).Bold()
				ui.Text(c, "This page mirrors upstream src/app/settings/"+s.settingsPage+" and is coming next.").TextColor(t.TextMuted)
			}
		})
	})
}

func (s *Shell) viewSettingsGeneral(c *ui.Context) {
	t := c.Theme()
	cfg := asObj(s.ensure("sys-lang", "get_system_language_settings", nil))
	ui.Text(c, "General").FontSize(22).Bold()
	if e := s.errFor("sys-lang"); e != "" {
		ui.Text(c, "backend error: "+e).TextColor(t.Danger)
		return
	}
	if cfg == nil {
		ui.Text(c, "Loading…").TextColor(t.TextMuted)
		return
	}
	lang := str(cfg, "language")
	if lang == "" {
		lang = "(system default)"
	}
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "Interface language").TextColor(t.TextMuted)
		ui.Text(c, lang).Bold()
	})
}

func (s *Shell) viewSettingsWebService(c *ui.Context) {
	t := c.Theme()
	status := asObj(s.ensure("web-status", "get_web_server_status", nil))
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, "Web Service").FontSize(22).Bold()
		ui.Spacer(c)
		if ui.Button(c.Key("ws-refresh2"), "Refresh").Clicked() {
			s.invalidate("web-status")
		}
	})
	if e := s.errFor("web-status"); e != "" {
		ui.Text(c, "backend error: "+e).TextColor(t.Danger)
		return
	}
	// get_web_server_status returns null when stopped, else {port, token,
	// addresses}. In the native build the in-process server is always running.
	running := status != nil
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "Status").TextColor(t.TextMuted)
		if running {
			ui.Text(c, "running").Bold().TextColor(t.Success)
		} else {
			ui.Text(c, "stopped").Bold().TextColor(t.TextMuted)
		}
	})
	if running {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "Port").TextColor(t.TextMuted)
			ui.Textf(c, "%v", status["port"]).Bold()
		})
		if addrs, ok := status["addresses"].([]any); ok && len(addrs) > 0 {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "Address").TextColor(t.TextMuted)
				ui.Textf(c, "%v", addrs[0]).Bold()
			})
		}
	}
}

func (s *Shell) viewSettingsQuickMessages(c *ui.Context) {
	t := c.Theme()
	msgs := asList(s.ensure("quick-messages", "quick_messages_list", nil))
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, "Quick Messages").FontSize(22).Bold()
		ui.Spacer(c)
		if ui.Button(c.Key("qm-refresh"), "Refresh").Clicked() {
			s.invalidate("quick-messages")
		}
	})
	if e := s.errFor("quick-messages"); e != "" {
		ui.Text(c, "backend error: "+e).TextColor(t.Danger)
		return
	}
	if msgs == nil {
		ui.Text(c, "Loading…").TextColor(t.TextMuted)
		return
	}
	if len(msgs) == 0 {
		ui.Text(c, "No quick messages yet.").TextColor(t.TextMuted)
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			for i, m := range msgs {
				title := str(m, "title")
				content := str(m, "content")
				ui.Column(c.Key(itoa("qm", i))).Padding(8).Gap(2).Children(func() {
					ui.Text(c, title).Bold()
					ui.Text(c, content).FontSize(12).TextColor(t.TextMuted).MaxLines(2)
				})
			}
		})
	})
}

func itoa(prefix string, i int) string {
	const digits = "0123456789"
	if i == 0 {
		return prefix + "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{digits[i%10]}, b...)
		i /= 10
	}
	return prefix + string(b)
}

func (s *Shell) viewSettingsAgents(c *ui.Context) {
	t := c.Theme()
	agents := asList(s.ensure("agents", "acp_list_agents", nil))
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Agents").FontSize(22).Bold()
		ui.Spacer(c)
		if ui.Button(c.Key("ag-refresh"), "Refresh").Clicked() {
			s.invalidate("agents")
		}
	})
	if e := s.errFor("agents"); e != "" {
		ui.Text(c, "backend error: "+e).TextColor(t.Danger)
		return
	}
	if agents == nil {
		ui.Text(c, "Loading…").TextColor(t.TextMuted)
		return
	}
	ui.Textf(c, "%d agents in the built-in catalog", len(agents)).FontSize(12).TextColor(t.TextMuted)
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			for i, a := range agents {
				name := firstNonEmpty(str(a, "name"), str(a, "agent_type"))
				at := str(a, "agent_type")
				desc := str(a, "description")
				available := false
				if v, ok := a["available"].(bool); ok {
					available = v
				}
				ver := s.agentVer[at]
				latest := s.agentLatest[at]
				ui.Column(c.Key(itoa("ag", i))).Gap(2).Padding(10).Background(t.Surface).Radius(8).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, name).Bold()
						ui.Text(c, at).FontSize(11).TextColor(t.TextMuted)
						ui.Spacer(c)
						if ver != "" {
							ui.Text(c, "v"+ver).FontSize(11).TextColor(t.Success)
						} else if available {
							ui.Text(c, "● available").FontSize(11).TextColor(t.Success)
						} else {
							ui.Text(c, "○ not installed").FontSize(11).TextColor(t.TextMuted)
						}
						if ui.Button(c.Key(itoa("agchk", i)), "Check").Clicked() {
							s.checkAgentVersion(at)
						}
					})
					if latest != "" {
						ui.Text(c, "update available: v"+latest).FontSize(11).TextColor(t.Warning)
					}
					if desc != "" {
						ui.Text(c, desc).FontSize(12).TextColor(t.TextMuted).MaxLines(3).Wrap()
					}
				})
			}
		})
	})
}

// checkAgentVersion probes an agent's locally installed version and whether a
// newer release is available, on demand (both calls can take a few seconds, so
// this is a button rather than an on-open sweep). Results feed the Agents list.
func (s *Shell) checkAgentVersion(agentType string) {
	go func() {
		if raw, err := s.B.raw("acp_detect_agent_local_version", map[string]any{"agentType": agentType}); err == nil {
			var v string
			if json.Unmarshal(raw, &v) == nil {
				s.mu.Lock()
				s.agentVer[agentType] = v
				s.mu.Unlock()
			}
		}
		if raw, err := s.B.raw("acp_fetch_agent_latest_release", map[string]any{"agentType": agentType}); err == nil {
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil && m != nil {
				if lv, ok := m["version"].(string); ok {
					s.mu.Lock()
					s.agentLatest[agentType] = lv
					s.mu.Unlock()
				}
			}
		}
		s.svc.Invalidate()
	}()
}

func (s *Shell) viewSettingsVersionControl(c *ui.Context) {
	t := c.Theme()
	git := asObj(s.ensure("git-settings", "get_git_settings", nil))
	accts := asObj(s.ensure("gh-accounts", "get_github_accounts", nil))
	ui.Text(c, "Version Control").FontSize(22).Bold()
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "Custom git path").TextColor(t.TextMuted)
		p := "(system git)"
		if git != nil {
			if v := str(git, "custom_path"); v != "" {
				p = v
			}
		}
		ui.Text(c, p).Bold()
	})
	ui.Spacer(c).Height(4)
	ui.Text(c, "Forge accounts").FontSize(13).Bold()
	var list []any
	if accts != nil {
		list, _ = accts["accounts"].([]any)
	}
	if len(list) == 0 {
		ui.Text(c, "No accounts configured").FontSize(12).TextColor(t.TextMuted)
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			for i, raw := range list {
				a, _ := raw.(map[string]any)
				ui.Column(c.Key(itoa("gh", i))).Gap(2).Padding(10).Background(t.Surface).Radius(8).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, firstNonEmpty(str(a, "label"), str(a, "username"), str(a, "id"))).Bold()
						ui.Spacer(c)
						ui.Text(c, firstNonEmpty(str(a, "provider"), str(a, "forge"))).FontSize(11).TextColor(t.TextMuted)
					})
					if host := firstNonEmpty(str(a, "server_url"), str(a, "serverUrl"), str(a, "host")); host != "" {
						ui.Text(c, host).FontSize(12).TextColor(t.TextMuted)
					}
				})
			}
		})
	})
}

func (s *Shell) viewSettingsSystem(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "System").FontSize(22).Bold()
	home := decodeString(s.ensure("home-dir", "get_home_directory", nil))
	kv := func(k, v string) {
		ui.Row(c.Key("sys-" + k)).Gap(8).Children(func() {
			ui.Text(c, k).TextColor(t.TextMuted).Width(140)
			ui.Text(c, v).Bold().MaxLines(1)
		})
	}
	if home != "" {
		kv("Home directory", home)
	}
	kv("Backend", s.B.URL)
	kv("Build", "YoYoCode v0.1.0")
}

func (s *Shell) viewSettingsMCP(c *ui.Context) {
	t := c.Theme()
	data := asObj(s.ensure("mcp-scan", "mcp_scan_local", nil))
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "MCP").FontSize(22).Bold()
		ui.Spacer(c)
		if ui.Button(c.Key("mcp-refresh"), "Rescan").Clicked() {
			s.invalidate("mcp-scan")
		}
	})
	if e := s.errFor("mcp-scan"); e != "" {
		ui.Text(c, "backend error: "+e).TextColor(t.Danger)
		return
	}
	if data == nil {
		ui.Text(c, "Scanning…").TextColor(t.TextMuted)
		return
	}
	servers, _ := data["servers"].([]any)
	warnings, _ := data["warnings"].([]any)
	ui.Textf(c, "%d local MCP server(s)", len(servers)).FontSize(12).TextColor(t.TextMuted)
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			if len(servers) == 0 {
				ui.Text(c, "No MCP servers found in agent configs.").FontSize(12).TextColor(t.TextMuted)
			}
			for i, raw := range servers {
				srv, _ := raw.(map[string]any)
				ui.Column(c.Key(itoa("mcp", i))).Gap(2).Padding(10).Background(t.Surface).Radius(8).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, firstNonEmpty(str(srv, "id"), str(srv, "name"))).Bold()
						ui.Spacer(c)
						ui.Text(c, firstNonEmpty(str(srv, "type"), str(srv, "transport"))).FontSize(11).TextColor(t.TextMuted)
					})
					if cmd := firstNonEmpty(str(srv, "command"), str(srv, "url")); cmd != "" {
						ui.Text(c, cmd).FontSize(12).TextColor(t.TextMuted).MaxLines(2).Wrap()
					}
				})
			}
			for i, raw := range warnings {
				w, _ := raw.(map[string]any)
				msg := firstNonEmpty(str(w, "message"), str(w, "warning"))
				if msg == "" {
					continue
				}
				ui.Text(c.Key(itoa("mcpw", i)), "⚠ "+msg).FontSize(11).TextColor(t.Warning).Wrap()
			}
		})
	})
}

func (s *Shell) viewSettingsLogs(c *ui.Context) {
	t := c.Theme()
	settings := asObj(s.ensure("log-settings", "get_log_settings", nil))
	files := asList(s.ensure("log-files", "list_log_files", nil))
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Logs").FontSize(22).Bold()
		ui.Spacer(c)
		if ui.Button(c.Key("log-refresh"), "Refresh").Clicked() {
			s.invalidate("log-files")
			s.invalidate("log-settings")
		}
	})
	if settings != nil {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "Level").TextColor(t.TextMuted)
			ui.Text(c, firstNonEmpty(str(settings, "level"), "info")).Bold()
		})
	}
	ui.Text(c, "Log files").FontSize(13).Bold()
	if files == nil {
		ui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
		return
	}
	if len(files) == 0 {
		ui.Text(c, "No log files yet.").FontSize(12).TextColor(t.TextMuted)
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(1).Children(func() {
			for i, f := range files {
				name := str(f, "name")
				var kb float64
				if sz, ok := f["size_bytes"].(float64); ok {
					kb = sz / 1024
				}
				ui.Row(c.Key(itoa("lf", i))).PaddingX(8).PaddingY(5).Gap(8).Children(func() {
					ui.Text(c, name).Grow(1).MaxLines(1)
					ui.Textf(c, "%.1f KB", kb).FontSize(11).TextColor(t.TextMuted)
				})
			}
		})
	})
}

// viewSettingsSkills mirrors upstream src/app/settings/skills: pick an agent
// (and global/project scope) and list the skills upstream sees in that agent's
// own skill store, via acp_list_agent_skills. Read-only (CLI-bundled) skills
// are flagged.
func (s *Shell) viewSettingsSkills(c *ui.Context) {
	t := c.Theme()
	agents := asList(s.ensure("agents", "acp_list_agents", nil))
	options := make([]string, 0, len(agents))
	for _, a := range agents {
		if at := str(a, "agent_type"); at != "" {
			options = append(options, at)
		}
	}
	if s.skillsAgent == "" && len(options) > 0 {
		s.skillsAgent = options[0]
	}
	if s.skillsScope == "" {
		s.skillsScope = "global"
	}

	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Skills").FontSize(22).Bold()
		ui.Spacer(c)
		if ui.Button(c.Key("sk-refresh"), "Refresh").Clicked() {
			s.invalidate(s.skillsCacheKey())
		}
	})
	ui.Text(c, "Skills YoYoCode can see in the selected agent's own store.").FontSize(12).TextColor(t.TextMuted)

	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Agent").FontSize(12).TextColor(t.TextMuted)
		if len(options) > 0 {
			ui.Select(c.Key("sk-agent"), &s.skillsAgent, options).Width(220)
		} else {
			ui.Text(c, "(loading agents…)").FontSize(12).TextColor(t.TextMuted)
		}
		ui.Text(c, "Scope").FontSize(12).TextColor(t.TextMuted)
		ui.Select(c.Key("sk-scope"), &s.skillsScope, []string{"global", "project"}).Width(130)
	})

	if s.skillsAgent == "" {
		return
	}
	key := s.skillsCacheKey()
	res := asObj(s.ensure(key, "acp_list_agent_skills", map[string]any{
		"agentType": s.skillsAgent,
	}))
	if e := s.errFor(key); e != "" {
		ui.Text(c, "backend error: "+e).TextColor(t.Danger)
		return
	}
	if res == nil {
		ui.Text(c, "Loading…").TextColor(t.TextMuted)
		return
	}
	if supported, _ := res["supported"].(bool); !supported {
		msg := str(res, "message")
		if msg == "" {
			msg = "This agent does not support managing skills here."
		}
		ui.Text(c, msg).FontSize(12).TextColor(t.TextMuted).Wrap()
		return
	}
	skills := asListVal(res["skills"])
	// Scope filter is client-side: the backend lists both scopes (project only
	// when a workspace is passed, which the settings view does not), so for now
	// show whatever the chosen scope matches.
	shown := make([]map[string]any, 0, len(skills))
	for _, sk := range skills {
		if str(sk, "scope") == s.skillsScope {
			shown = append(shown, sk)
		}
	}
	ui.Textf(c, "%d skill(s) in %s scope", len(shown), s.skillsScope).FontSize(12).TextColor(t.TextMuted)
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			if len(shown) == 0 {
				ui.Text(c, "No skills found in this scope.").FontSize(12).TextColor(t.TextMuted)
				return
			}
			for i, sk := range shown {
				name := firstNonEmpty(str(sk, "name"), str(sk, "id"))
				desc := str(sk, "description")
				layout := str(sk, "layout")
				ro := false
				if v, ok := sk["read_only"].(bool); ok {
					ro = v
				}
				ui.Column(c.Key(itoa("sk", i))).Gap(2).Padding(10).Background(t.Surface).Radius(8).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, name).Bold()
						ui.Text(c, layout).FontSize(11).TextColor(t.TextMuted)
						ui.Spacer(c)
						if ro {
							ui.Text(c, "read-only").FontSize(11).TextColor(t.Warning)
						}
					})
					if desc != "" {
						ui.Text(c, desc).FontSize(12).TextColor(t.TextMuted).MaxLines(3).Wrap()
					}
				})
			}
		})
	})

	// Custom skills (central store ~/.yoyocode/skills) — create / list / delete,
	// shared across agents (upstream custom_* skill routes).
	ui.Divider(c)
	ui.Text(c, "Custom skills (central store)").FontSize(14).Bold()
	ui.Text(c, "Authored once in ~/.yoyocode/skills, enable per agent from the matrix.").FontSize(11).TextColor(t.TextMuted)
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key("sk-newid"), &s.newSkillID).Placeholder("new-skill-id").Grow(1)
		if ui.PrimaryButton(c.Key("sk-create"), "Create").Disabled(s.newSkillID == "").Clicked() {
			s.createCustomSkill(s.newSkillID)
		}
	})
	custom := asList(s.ensure("custom-skills", "custom_list", nil))
	if custom == nil {
		ui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
	} else if len(custom) == 0 {
		ui.Text(c, "No custom skills yet.").FontSize(12).TextColor(t.TextMuted)
	} else {
		ui.Column(c).Gap(4).Children(func() {
			for i, ck := range custom {
				id := str(ck, "id")
				name := firstNonEmpty(str(ck, "name"), id)
				desc := str(ck, "description")
				ui.Row(c.Key(itoa("csk", i))).Gap(8).PaddingX(8).PaddingY(5).Background(t.Surface).Radius(6).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(1).Children(func() {
						ui.Text(c, name).MaxLines(1)
						if desc != "" {
							ui.Text(c, desc).FontSize(10).TextColor(t.TextMuted).MaxLines(1)
						}
					})
					if ui.Button(c.Key(itoa("cskdel", i)), "Delete").Clicked() {
						s.deleteCustomSkill(id)
					}
				})
			}
		})
	}
}

// createCustomSkill creates a new central-store custom skill with a minimal
// SKILL.md scaffold, then refreshes the list.
func (s *Shell) createCustomSkill(id string) {
	scaffold := "---\nname: " + id + "\ndescription: \n---\n\n# " + id + "\n"
	go func() {
		_, _ = s.B.raw("custom_create_skill", map[string]any{"id": id, "content": scaffold})
		s.newSkillID = ""
		s.invalidate("custom-skills")
		s.svc.Invalidate()
	}()
}

// deleteCustomSkill removes a custom skill from the central store.
func (s *Shell) deleteCustomSkill(id string) {
	go func() {
		_, _ = s.B.raw("custom_delete_skills", map[string]any{"ids": []any{id}})
		s.invalidate("custom-skills")
		s.svc.Invalidate()
	}()
}

func (s *Shell) skillsCacheKey() string {
	return "skills:" + s.skillsAgent
}

// viewSettingsChatChannels lists configured chat-channel integrations
// (list_chat_channels) — Lark / Telegram / WeChat outbound bots.
func (s *Shell) viewSettingsChatChannels(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Chat Channels").FontSize(22).Bold()
		ui.Spacer(c)
		if ui.Button(c.Key("cc-refresh"), "Refresh").Clicked() {
			s.invalidate("chat-channels")
		}
	})
	ui.Text(c, "Outbound chat integrations (Lark / Telegram / WeChat).").FontSize(12).TextColor(t.TextMuted)
	list := asList(s.ensure("chat-channels", "list_chat_channels", nil))
	if list == nil {
		ui.Text(c, "Loading…").TextColor(t.TextMuted)
		return
	}
	if len(list) == 0 {
		ui.Text(c, "No chat channels configured.").FontSize(12).TextColor(t.TextMuted)
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			for i, ch := range list {
				name := firstNonEmpty(str(ch, "name"), "(unnamed)")
				ctype := str(ch, "channel_type")
				enabled := false
				if v, ok := ch["enabled"].(bool); ok {
					enabled = v
				}
				ui.Column(c.Key(itoa("cc", i))).Gap(2).Padding(10).Background(t.Surface).Radius(8).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, name).Bold()
						ui.Text(c, ctype).FontSize(11).TextColor(t.TextMuted)
						ui.Spacer(c)
						if enabled {
							ui.Text(c, "● enabled").FontSize(11).TextColor(t.Success)
						} else {
							ui.Text(c, "○ disabled").FontSize(11).TextColor(t.TextMuted)
						}
					})
				})
			}
		})
	})
}

// viewSettingsComputerUse reports whether this backend serves computer use
// (computer_available) and on which platform.
func (s *Shell) viewSettingsComputerUse(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "Computer Use").FontSize(22).Bold()
	info := asObj(s.ensure("computer-available", "computer_available", nil))
	if info == nil {
		ui.Text(c, "Loading…").TextColor(t.TextMuted)
		return
	}
	available := false
	if v, ok := info["available"].(bool); ok {
		available = v
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Status").TextColor(t.TextMuted)
		if available {
			ui.Text(c, "available").Bold().TextColor(t.Success)
		} else {
			ui.Text(c, "not available on this backend").Bold().TextColor(t.TextMuted)
		}
	})
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "Platform").TextColor(t.TextMuted)
		ui.Text(c, firstNonEmpty(str(info, "platform"), "unknown")).Bold()
	})
	if !available {
		ui.Text(c, "Computer use is served only by a desktop session started with YOYOCODE_COMPUTER_USE=1.").FontSize(11).TextColor(t.TextMuted).Wrap()
	}
}

// viewSettingsExperts lists the bundled expert skill packs (experts_list).
// This build ships none, so the list is empty by design.
func (s *Shell) viewSettingsExperts(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "Experts").FontSize(22).Bold()
	ui.Text(c, "Bundled expert skill packs. Enable per agent from the matrix.").FontSize(12).TextColor(t.TextMuted)
	list := asList(s.ensure("experts-list", "experts_list", nil))
	if list == nil {
		ui.Text(c, "Loading…").TextColor(t.TextMuted)
		return
	}
	if len(list) == 0 {
		ui.Text(c, "No bundled expert packs in this build. Author your own under Skills → Custom skills.").FontSize(12).TextColor(t.TextMuted).Wrap()
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			for i, e := range list {
				id := str(e, "id")
				name := firstNonEmpty(str(e, "name"), id)
				ui.Row(c.Key(itoa("exp", i))).Gap(8).Padding(10).Background(t.Surface).Radius(8).Children(func() {
					ui.Text(c, name).Bold()
				})
			}
		})
	})
}

// viewSettingsAppearance mirrors upstream src/app/settings/appearance +
// components/settings/appearance-settings.tsx: pick the theme mode
// (system / light / dark), persisted via update_appearance_mode and applied to
// the whole window on the next paint.
func (s *Shell) viewSettingsAppearance(c *ui.Context) {
	t := c.Theme()
	mode := s.appearanceMode
	if raw := s.ensure("appearance-mode", "get_appearance_mode", nil); raw != nil {
		if m := asObj(raw); m != nil {
			if v := str(m, "mode"); v != "" {
				mode = v
			}
		}
	}
	ui.Text(c, "Appearance").FontSize(22).Bold()
	if e := s.errFor("appearance-mode"); e != "" {
		ui.Text(c, "backend error: "+e).TextColor(t.Danger)
		return
	}

	ui.Text(c, "Theme mode").FontSize(12).Bold().TextColor(t.TextMuted)
	ui.Text(c, "Choose how YoYoCode looks. \u201cSystem\u201d follows the OS where supported.").FontSize(12).TextColor(t.TextMuted)
	ui.Row(c).Gap(8).Children(func() {
		for _, opt := range []struct{ id, label string }{
			{"system", "System"},
			{"light", "Light"},
			{"dark", "Dark"},
		} {
			active := mode == opt.id
			key := "appear-" + opt.id
			clicked := false
			if active {
				clicked = ui.PrimaryButton(c.Key(key), opt.label).Clicked()
			} else {
				clicked = ui.Button(c.Key(key), opt.label).Clicked()
			}
			if clicked {
				s.setAppearanceMode(opt.id)
			}
		}
	})

	ui.Divider(c)
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "Current").TextColor(t.TextMuted)
		ui.Text(c, mode).Bold()
	})
	// A small live swatch so the choice is legible before it is committed.
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Width(28).Height(20).Background(resolveTheme(mode).Background).Border(1, resolveTheme(mode).Border)
		ui.Column(c).Width(28).Height(20).Background(resolveTheme(mode).Surface).Border(1, resolveTheme(mode).Border)
		ui.Column(c).Width(28).Height(20).Background(resolveTheme(mode).Accent).Border(1, resolveTheme(mode).Border)
	})
}

// setAppearanceMode persists the mode and repaints the shell so the theme
// applies immediately.
func (s *Shell) setAppearanceMode(mode string) {
	svc := s.svc
	s.appearanceMode = mode // optimistic: repaint with the new theme at once
	go func() {
		_, err := s.B.raw("update_appearance_mode", map[string]any{"mode": mode})
		s.mu.Lock()
		if err != nil {
			s.actionMsg = "appearance update failed: " + err.Error()
		} else {
			delete(s.cache, "appearance-mode")
		}
		s.mu.Unlock()
		svc.Invalidate()
	}()
}
