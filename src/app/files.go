package app

// files.go mirrors the file browser of upstream's workspace aux panel
// (src/components/layout/aux-panel-file-tree-tab + components/files/*): a
// folder's files, a directory list you walk into (with a breadcrumb) and a
// preview of the selected file. It is a compact subset — the upstream panel
// also does rename/move/upload/git-changes tabs — built over the already-ported
// backend: get_file_tree (one level at a time, maxDepth=1) and
// read_file_preview.

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

// viewFiles renders the folder file browser for the selected folder.
func (s *Shell) viewFiles(c *ui.Context) {
	t := c.Theme()
	root := s.folderPathByID(s.selectedFolder)
	ui.Row(c.Key("files-head")).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Files").FontSize(22).Bold()
		ui.Spacer(c)
		if ui.Button(c.Key("files-refresh"), "Refresh").Clicked() && root != "" {
			s.invalidate("ft:" + s.fileCurAbs(root))
		}
	})
	if s.selectedFolder == "" || root == "" {
		ui.Text(c, "Select a folder to browse its files.").FontSize(12).TextColor(t.TextMuted)
		return
	}
	curAbs := s.fileCurAbs(root)
	ui.Row(c).Grow(1).Children(func() {
		// Directory list column.
		ui.Column(c.Key("files-list")).Width(320).FillHeight().Background(t.Surface).Children(func() {
			ui.Row(c.Key("files-crumb")).PaddingX(8).PaddingY(6).Gap(6).AlignItems(ui.Center).Children(func() {
				if s.fileDir != "" {
					if ui.Button(c.Key("files-up"), "◂").Clicked() {
						s.fileUp()
					}
				}
				crumb := "/"
				if s.fileDir != "" {
					crumb = "/" + s.fileDir
				}
				ui.Text(c, crumb).FontSize(11).TextColor(t.TextMuted).MaxLines(1)
			})
			ui.Divider(c)
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Gap(1).Children(func() {
					s.fileListLevel(c, root, curAbs)
				})
			})
		})
		ui.Divider(c)
		// Preview column.
		ui.Column(c.Key("files-preview")).Grow(1).FillHeight().Padding(12).Gap(6).Children(func() {
			s.viewFilePreview(c, root)
		})
	})
}

// fileListLevel renders the immediate entries of curAbs: directories first
// (click to walk in), then files (click to preview).
func (s *Shell) fileListLevel(c *ui.Context, root, curAbs string) {
	t := c.Theme()
	nodes := asList(s.ensure("ft:"+curAbs, "get_file_tree", map[string]any{"path": curAbs, "maxDepth": 1}))
	if nodes == nil {
		ui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
		return
	}
	if len(nodes) == 0 {
		ui.Text(c, "Empty directory").FontSize(12).TextColor(t.TextMuted)
		return
	}
	for _, n := range nodes {
		name := str(n, "name")
		rel := name
		if s.fileDir != "" {
			rel = s.fileDir + "/" + name
		}
		if str(n, "kind") == "dir" {
			row := ui.Row(c.Key("fd-" + rel)).PaddingX(8).PaddingY(4).Gap(8).AlignItems(ui.Center)
			row.Children(func() {
				if iconFolder != nil {
					ui.Icon(c, iconFolder).Width(13).Height(13).TextColor(t.TextMuted)
				}
				ui.Text(c, name).MaxLines(1).TextColor(t.Text)
			})
			if row.Clicked() {
				s.fileDir = rel
				s.fileSel = ""
			}
			continue
		}
		sel := s.fileSel == rel
		row := ui.Row(c.Key("ff-" + rel)).PaddingX(8).PaddingY(4).Gap(8).AlignItems(ui.Center)
		if sel {
			row.Background(t.SurfacePressed)
		}
		row.Children(func() {
			ui.Text(c, name).MaxLines(1).TextColor(t.Text)
		})
		if row.Clicked() {
			s.fileSel = rel
		}
	}
}

// viewFilePreview renders the selected file's content (read_file_preview).
func (s *Shell) viewFilePreview(c *ui.Context, root string) {
	t := c.Theme()
	if s.fileSel == "" {
		ui.Text(c, "Select a file to preview.").FontSize(12).TextColor(t.TextMuted)
		return
	}
	key := "filepreview:" + s.fileSel
	raw := s.ensure(key, "read_file_preview", map[string]any{"rootPath": root, "path": s.fileSel})
	if e := s.errFor(key); e != "" {
		ui.Text(c, s.fileSel).FontSize(12).Bold().MaxLines(1)
		ui.Text(c, "Cannot preview: "+e).FontSize(12).TextColor(t.Danger).Wrap()
		return
	}
	if raw == nil {
		ui.Text(c, s.fileSel).FontSize(12).Bold().MaxLines(1)
		ui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
		return
	}
	ui.Text(c, s.fileSel).FontSize(12).Bold().MaxLines(1)
	ui.Divider(c)
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Text(c, str(asObj(raw), "content")).FontSize(12).TextColor(t.Text).Wrap()
	})
}

// fileCurAbs is the absolute path of the browser's current directory.
func (s *Shell) fileCurAbs(root string) string {
	if s.fileDir == "" {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(s.fileDir))
}

// fileUp walks the browser up one directory.
func (s *Shell) fileUp() {
	if s.fileDir == "" {
		return
	}
	if i := strings.LastIndex(s.fileDir, "/"); i >= 0 {
		s.fileDir = s.fileDir[:i]
	} else {
		s.fileDir = ""
	}
	s.fileSel = ""
}
