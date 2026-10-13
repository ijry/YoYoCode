package app

// login.go mirrors upstream src/app/login: the token gate. Upstream's desktop
// build skips it (isDesktop() → straight to the workspace); this native build
// always boots with an in-process token, so the gate only appears for a
// tokenless boot. It validates the entered token by probing /api/health, exactly
// like upstream (200 → valid, 401 → invalid).

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/egoist/mygo/ui"
)

// viewLogin renders the token gate.
func (s *Shell) viewLogin(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(14).Children(func() {
		ui.Column(c).Center().Gap(6).Children(func() {
			ui.Text(c, "YoYoCode").FontSize(26).Bold()
			ui.Text(c, "Enter this server's access token to connect.").FontSize(13).TextColor(t.TextMuted)
		})
		ui.Column(c).Width(340).Gap(8).Children(func() {
			ui.Row(c).Children(func() {
				ui.TextInput(c.Key("login-token"), &s.loginToken).Placeholder("Access token").Grow(1)
			})
			if s.loginErr != "" {
				ui.Text(c, s.loginErr).FontSize(12).TextColor(t.Danger).Wrap()
			}
			if ui.PrimaryButton(c.Key("login-connect"), "Connect").Disabled(s.loginToken == "").Clicked() {
				s.connectToken(s.loginToken)
			}
		})
		ui.Text(c, "The token is shown under Settings → Web Service.").FontSize(11).TextColor(t.TextMuted)
	})
}

// connectToken probes the backend with the candidate token (off the UI thread)
// and adopts it on success, repainting the shell into the workspace.
func (s *Shell) connectToken(token string) {
	svc := s.svc
	go func() {
		ok, status, err := probeHealth(s.B.URL, token)
		s.mu.Lock()
		switch {
		case err != nil:
			s.loginErr = "Connection failed: " + err.Error()
		case ok:
			s.B.Token = token
			s.loginErr = ""
			delete(s.cache, "appearance-mode")
		case status == http.StatusUnauthorized:
			s.loginErr = "Invalid token"
		default:
			s.loginErr = fmt.Sprintf("Connection failed (%d)", status)
		}
		s.mu.Unlock()
		svc.Invalidate()
	}()
}

// probeHealth POSTs /api/health with the candidate token — the same probe
// upstream's login page runs. 200 → the token is valid; 401 → it is not.
func probeHealth(baseURL, token string) (ok bool, status int, err error) {
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/health", bytes.NewReader([]byte("{}")))
	req.Header.Set("content-type", "application/json")
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK, resp.StatusCode, nil
}
