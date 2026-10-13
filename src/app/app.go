// Package app is the mygo native-UI layer of YoYoCode: the Go analog of the
// upstream Next.js page tree under src/app. Each upstream page maps to one
// view function in a like-named file here (workspace.go, settings.go, pet.go,
// …) so the structure tracks upstream for easy follow-on diffs.
//
// All data comes from the in-process upstream backend over loopback HTTP, through
// the exact same /api contract mcode-app uses — the UI never reaches into the
// DB directly.
package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/egoist/mygo/ui"
)

// Backend is a thin loopback client for the in-process upstream server.
type Backend struct {
	URL   string
	Token string
}

func (b *Backend) raw(cmd string, body map[string]any) (json.RawMessage, error) {
	if body == nil {
		body = map[string]any{}
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, b.URL+"/api/"+cmd, bytes.NewReader(buf))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+b.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, &apiError{cmd: cmd, body: string(data)}
	}
	return json.RawMessage(data), nil
}

type apiError struct{ cmd, body string }

func (e *apiError) Error() string { return e.cmd + ": " + e.body }

// Shell is the root application state: which top-level section and settings
// sub-page are active, plus an async cache of backend responses keyed by an
// arbitrary string. The view never blocks on IO — it asks the cache for a key,
// which kicks off a background fetch and repaints when it lands.
type Shell struct {
	B *Backend

	section        string // workspace | settings | pet
	settingsPage   string // general | web-service | quick-messages | …
	selectedFolder string
	selectedConv   string
	composer       string
	newAgent       string
	toolOpen       map[string]bool // conversation detail: tool card id → expanded
	appearanceMode string          // system | light | dark (from get_appearance_mode)
	actionMsg      string
	renaming       bool
	renameText     string
	skillsAgent    string            // Settings → Skills: selected agent wire type
	skillsScope    string            // "global" | "project"
	workbench      string            // "" (conversations) | automations | tasks | forge | canvas
	petTab         string            // Pet section: sessions | sprite
	loginToken     string            // login gate: token input
	loginErr       string            // login gate: last probe error
	newSkillID     string            // Settings → Skills: new custom-skill id input
	agentVer       map[string]string // Settings → Agents: last detected installed version
	agentLatest    map[string]string // Settings → Agents: latest available (when newer)

	stream     *Stream
	connByConv map[string]string // conversation id → live ACP connection id

	mu       sync.Mutex
	svc      ui.Services
	cache    map[string]json.RawMessage
	inflight map[string]bool
	errs     map[string]string
}

// New builds the root shell.
func New(b *Backend) *Shell {
	return &Shell{
		B:              b,
		section:        "workspace",
		settingsPage:   "general",
		appearanceMode: "system",
		cache:          map[string]json.RawMessage{},
		inflight:       map[string]bool{},
		errs:           map[string]string{},
		connByConv:     map[string]string{},
		toolOpen:       map[string]bool{},
		agentVer:       map[string]string{},
		agentLatest:    map[string]string{},
	}
}

// ensure returns the cached raw response for key, kicking off a one-shot
// background fetch (cmd+body) the first time and whenever the cache was
// invalidated. Returns nil while the fetch is in flight.
func (s *Shell) ensure(key, cmd string, body map[string]any) json.RawMessage {
	s.mu.Lock()
	if raw, ok := s.cache[key]; ok {
		s.mu.Unlock()
		return raw
	}
	if s.inflight[key] {
		s.mu.Unlock()
		return nil
	}
	s.inflight[key] = true
	svc := s.svc
	s.mu.Unlock()

	go func() {
		raw, err := s.B.raw(cmd, body)
		s.mu.Lock()
		s.inflight[key] = false
		if err != nil {
			s.errs[key] = err.Error()
		} else {
			s.cache[key] = raw
			delete(s.errs, key)
		}
		s.mu.Unlock()
		svc.Invalidate() // repaint on the UI thread; safe from any goroutine
	}()
	return nil
}

// invalidate drops a cached key so the next ensure refetches it.
func (s *Shell) invalidate(key string) {
	s.mu.Lock()
	delete(s.cache, key)
	s.mu.Unlock()
}

func (s *Shell) errFor(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errs[key]
}

// list / obj decode a cached raw response; nil until the fetch lands.
func asList(raw json.RawMessage) []map[string]any {
	if raw == nil {
		return nil
	}
	var v []map[string]any
	_ = json.Unmarshal(raw, &v)
	return v
}

// asListVal converts an already-decoded value (e.g. a nested array inside an
// object response) into a slice of objects, skipping non-object elements.
func asListVal(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, el := range arr {
		if m, ok := el.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func asObj(raw json.RawMessage) map[string]any {
	if raw == nil {
		return nil
	}
	var v map[string]any
	_ = json.Unmarshal(raw, &v)
	return v
}

// decodeString decodes a cached raw response that is a bare JSON string
// (e.g. get_home_directory); "" until the fetch lands or on any mismatch.
func decodeString(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var v string
	_ = json.Unmarshal(raw, &v)
	return v
}

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// View is the root view: a top-level sidebar (Workspace / Settings / Pet) and
// the active section's page. Mirrors the upstream shell that hosts src/app.
func (s *Shell) View(c *ui.Context) {
	// Unauthenticated: show the login gate (upstream src/app/login). The desktop
	// build always boots with an in-process token, so — exactly like upstream's
	// `isDesktop()` skip — the gate never shows there; it is the surface for a
	// tokenless (remote/web) boot.
	if s.B.Token == "" {
		s.svc = c.Services()
		c.SetTheme(resolveTheme(s.appearanceMode))
		s.viewLogin(c)
		return
	}
	// Resolve the appearance mode from the backend (cached one-shot fetch;
	// defaults to system=light until it arrives) and apply the matching theme.
	if raw := s.ensure("appearance-mode", "get_appearance_mode", nil); raw != nil {
		if m := asObj(raw); m != nil {
			if mode := str(m, "mode"); mode != "" {
				s.appearanceMode = mode
			}
		}
	}
	c.SetTheme(resolveTheme(s.appearanceMode)) // upstream neutral light/dark palette
	s.svc = c.Services()
	s.ensureStream()
	t := c.Theme()
	// Window = content row on top of a slim status bar (upstream's bottom chrome).
	ui.Column(c).Fill().Children(func() {
		// Computer-use strip, pinned above the content while anything is shared
		// (a no-op on a backend that refuses computer use).
		s.viewComputerIndicator(c)
		ui.Row(c.Key("body")).Grow(1).Children(func() {
			// Left navigation rail.
			ui.Column(c.Key("nav")).Width(186).FillHeight().Background(t.Surface).Padding(12).Gap(10).Children(func() {
				ui.Row(c.Key("brand")).Gap(8).AlignItems(ui.Center).Children(func() {
					if iconWorkspace != nil {
						ui.Icon(c, iconWorkspace).Width(18).Height(18).TextColor(t.Text)
					}
					ui.Text(c, "YoYoCode").FontSize(16).Bold()
				})
				ui.Sidebar(c, &s.section, func() {
					ui.SidebarItem(c, "workspace", iconWorkspace, "Workspace")
					ui.SidebarItem(c, "settings", iconSettings, "Settings")
					ui.SidebarItem(c, "pet", iconPet, "Pet")
				}).Grow(1)
			})
			ui.Divider(c)
			ui.Column(c.Key("page")).Grow(1).FillHeight().Children(func() {
				switch s.section {
				case "settings":
					s.viewSettings(c)
				case "pet":
					s.viewPet(c)
				default:
					s.viewWorkspace(c)
				}
			})
		})
		ui.Divider(c)
		s.viewStatusBar(c)
	})
}

// viewStatusBar is the slim bottom chrome: a liveness dot + backend address on
// the left, product/version on the right — modeled on upstream's status bar.
func (s *Shell) viewStatusBar(c *ui.Context) {
	t := c.Theme()
	connected := s.stream != nil && s.stream.isReady()
	dot := t.TextMuted
	label := "connecting"
	if connected {
		dot = t.Success
		label = "connected"
	}
	ui.Row(c.Key("statusbar")).Background(t.Surface).PaddingX(12).PaddingY(5).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "\u25CF").FontSize(9).TextColor(dot)
		ui.Text(c, label).FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, s.B.URL).FontSize(11).TextColor(t.TextMuted)
		ui.Spacer(c)
		ui.Text(c, "YoYoCode v0.1.0").FontSize(11).TextColor(t.TextMuted)
	})
}
