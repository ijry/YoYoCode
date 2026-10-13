package app

// changes.go mirrors the git-changes surface of upstream's workspace aux panel
// (src/components/layout/aux-panel-git-changes-tab): the folder's working-tree
// changes and the diff of the selected file. It is a compact, read-only subset —
// the upstream tab also stages/unstages/discards and commits — built over the
// already-ported backend: git_status (porcelain v1) and git_diff.

import (
	"encoding/json"
	"strings"

	"github.com/egoist/mygo/ui"
)

// viewChanges renders the folder's git changes + the selected file's diff.
func (s *Shell) viewChanges(c *ui.Context) {
	t := c.Theme()
	root := s.folderPathByID(s.selectedFolder)
	if root != "" {
		ui.Row(c.Key("ch-head")).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			if ui.Button(c.Key("ch-refresh"), "Refresh").Clicked() {
				s.invalidate("gitstatus:" + root)
				if s.diffSel != "" {
					s.invalidate("gitdiff:" + s.diffSel)
				}
			}
		})
	}
	if s.selectedFolder == "" || root == "" {
		ui.Text(c, "Select a folder to see its git changes.").FontSize(12).TextColor(t.TextMuted)
		return
	}
	files := asList(s.ensure("gitstatus:"+root, "git_status", map[string]any{"path": root}))
	if e := s.errFor("gitstatus:" + root); e != "" {
		ui.Text(c, "Not a git repository (or git failed): "+e).FontSize(12).TextColor(t.TextMuted).Wrap()
		return
	}
	ui.Row(c).Grow(1).Children(func() {
		// Changed-files column.
		ui.Column(c.Key("ch-list")).Width(320).FillHeight().Background(t.Surface).Children(func() {
			ui.Scroll(c).Grow(1).Padding(6).Children(func() {
				ui.Column(c).Gap(1).Children(func() {
					if files == nil {
						ui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
						return
					}
					if len(files) == 0 {
						ui.Text(c, "No changes").FontSize(12).TextColor(t.TextMuted)
						return
					}
					for _, f := range files {
						file := str(f, "file")
						code := str(f, "status")
						sel := s.diffSel == file
						row := ui.Row(c.Key("ch-" + file)).PaddingX(8).PaddingY(4).Gap(8).AlignItems(ui.Center)
						if sel {
							row.Background(t.SurfacePressed)
						}
						// Porcelain code → accent: untracked/added green, deleted
						// red, modified/renamed amber, anything else muted.
						col := t.TextMuted
						switch {
						case code == "??" || strings.HasPrefix(code, "A"):
							col = t.Success
						case strings.HasPrefix(code, "D"):
							col = t.Danger
						case strings.Contains(code, "M") || strings.HasPrefix(code, "R"):
							col = t.Warning
						}
						row.Children(func() {
							ui.Text(c, code).FontSize(11).Width(24).TextColor(col)
							ui.Text(c, file).MaxLines(1).TextColor(t.Text)
						})
						if row.Clicked() {
							s.diffSel = file
						}
					}
				})
			})
		})
		ui.Divider(c)
		// Diff column.
		ui.Column(c.Key("ch-diff")).Grow(1).FillHeight().Padding(12).Gap(6).Children(func() {
			s.viewDiff(c, root)
		})
	})
}

// viewDiff renders the selected file's unified diff (git_diff).
func (s *Shell) viewDiff(c *ui.Context, root string) {
	t := c.Theme()
	if s.diffSel == "" {
		ui.Text(c, "Select a changed file to view its diff.").FontSize(12).TextColor(t.TextMuted)
		return
	}
	key := "gitdiff:" + s.diffSel
	raw := s.ensure(key, "git_diff", map[string]any{"path": root, "file": s.diffSel})
	ui.Text(c, s.diffSel).FontSize(12).Bold().MaxLines(1)
	if e := s.errFor(key); e != "" {
		ui.Text(c, "Cannot diff: "+e).FontSize(12).TextColor(t.Danger).Wrap()
		return
	}
	if raw == nil {
		ui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
		return
	}
	var text string
	_ = json.Unmarshal(raw, &text) // git_diff returns a bare JSON string
	ui.Divider(c)
	if strings.TrimSpace(text) == "" {
		ui.Text(c, "No diff (the file may be untracked).").FontSize(12).TextColor(t.TextMuted)
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Text(c, text).FontSize(11).TextColor(t.Text).Wrap()
	})
}
