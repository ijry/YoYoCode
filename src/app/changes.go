package app

// changes.go mirrors the git-changes surface of upstream's workspace aux panel
// (src/components/layout/aux-panel-git-changes-tab): the folder's working-tree
// changes, the selected file's diff, and the write actions — stage a file,
// discard a file (two-step, since it is destructive) and commit everything.
// Built over the already-ported backend: git_status, git_diff, git_add_files,
// git_rollback_file and git_commit.

import (
	"encoding/json"
	"strings"

	"github.com/egoist/mygo/ui"
)

// viewChanges renders the folder's git changes, the selected diff and the write
// actions.
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
		// Changed-files column + commit box.
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
							s.discardConfirm = ""
						}
					}
				})
			})
			ui.Divider(c)
			ui.Column(c.Key("ch-commit")).Padding(8).Gap(6).Children(func() {
				ui.Row(c).Children(func() {
					ui.TextInput(c.Key("ch-msg"), &s.commitMsg).Placeholder("Commit message").Grow(1)
				})
				if ui.PrimaryButton(c.Key("ch-commit-btn"), "Commit all").Disabled(s.commitMsg == "" || len(files) == 0).Clicked() {
					s.commitAll(root, files)
				}
				if s.actionMsg != "" {
					ui.Text(c, s.actionMsg).FontSize(11).TextColor(t.TextMuted).MaxLines(1)
				}
			})
		})
		ui.Divider(c)
		// Diff column.
		ui.Column(c.Key("ch-diff")).Grow(1).FillHeight().Padding(12).Gap(6).Children(func() {
			s.viewDiff(c, root)
		})
	})
}

// viewDiff renders the selected file's unified diff plus its write actions
// (stage, discard).
func (s *Shell) viewDiff(c *ui.Context, root string) {
	t := c.Theme()
	if s.diffSel == "" {
		ui.Text(c, "Select a changed file to view its diff.").FontSize(12).TextColor(t.TextMuted)
		return
	}
	ui.Row(c.Key("ch-diff-head")).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, s.diffSel).FontSize(12).Bold().MaxLines(1)
		ui.Spacer(c)
		if ui.Button(c.Key("ch-stage"), "Stage").Clicked() {
			s.stageFile(root, s.diffSel)
		}
		// Discard reverts the file to HEAD (and deletes it when untracked), so it
		// takes a second, explicit click.
		if s.discardConfirm == s.diffSel {
			if ui.Button(c.Key("ch-discard-yes"), "Confirm discard").Clicked() {
				s.discardFile(root, s.diffSel)
				s.discardConfirm = ""
			}
			if ui.Button(c.Key("ch-discard-no"), "Cancel").Clicked() {
				s.discardConfirm = ""
			}
		} else if ui.Button(c.Key("ch-discard"), "Discard").Clicked() {
			s.discardConfirm = s.diffSel
		}
	})
	key := "gitdiff:" + s.diffSel
	raw := s.ensure(key, "git_diff", map[string]any{"path": root, "file": s.diffSel})
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

// stageFile stages one file (git_add_files).
func (s *Shell) stageFile(root, file string) {
	svc := s.svc
	go func() {
		_, err := s.B.raw("git_add_files", map[string]any{"path": root, "files": []any{file}})
		s.mu.Lock()
		if err != nil {
			s.actionMsg = "stage: " + err.Error()
		} else {
			s.actionMsg = "staged " + file
		}
		delete(s.cache, "gitstatus:"+root)
		s.mu.Unlock()
		svc.Invalidate()
	}()
}

// discardFile reverts one file to HEAD (git_rollback_file); an untracked file is
// removed. Destructive — the UI asks for a second click first.
func (s *Shell) discardFile(root, file string) {
	svc := s.svc
	go func() {
		_, err := s.B.raw("git_rollback_file", map[string]any{"path": root, "file": file})
		s.mu.Lock()
		if err != nil {
			s.actionMsg = "discard: " + err.Error()
		} else {
			s.actionMsg = "discarded " + file
		}
		delete(s.cache, "gitstatus:"+root)
		delete(s.cache, "gitdiff:"+file)
		s.mu.Unlock()
		svc.Invalidate()
	}()
}

// commitAll stages and commits every working-tree change (git_commit).
func (s *Shell) commitAll(root string, files []map[string]any) {
	msg := s.commitMsg
	if msg == "" || len(files) == 0 {
		return
	}
	names := make([]any, 0, len(files))
	for _, f := range files {
		if n := str(f, "file"); n != "" {
			names = append(names, n)
		}
	}
	s.commitMsg = ""
	svc := s.svc
	go func() {
		_, err := s.B.raw("git_commit", map[string]any{"path": root, "message": msg, "files": names})
		s.mu.Lock()
		if err != nil {
			s.actionMsg = "commit: " + err.Error()
		} else {
			s.actionMsg = "committed"
		}
		delete(s.cache, "gitstatus:"+root)
		s.mu.Unlock()
		svc.Invalidate()
	}()
}
