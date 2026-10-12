package handlers

// Folder links (B11): symlink a directory into a workspace folder to build a
// multi-folder workspace. list/preview/create/rename/repair/remove, mirroring
// commands/folder_links.rs + folder_link_service.rs. Names are sanitized and
// de-duplicated; status is recomputed from disk on every list; create appends a
// local git info/exclude rule. Symlinks use os.Symlink with a Windows junction
// (mklink /J) fallback where the privilege is missing.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const (
	maxLinkNameLen   = 96
	maxNameProbes    = 99
	fallbackLinkName = "linked-folder"
)

var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ── name derivation (pure) ──

func sanitizeLinkName(raw string) string {
	var b strings.Builder
	for _, c := range raw {
		if c < 0x20 || c == 0x7f {
			continue
		}
		switch c {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			b.WriteRune('-')
		default:
			b.WriteRune(c)
		}
	}
	trimmed := strings.TrimRight(strings.TrimSpace(b.String()), ". ")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return fallbackLinkName
	}
	r := []rune(trimmed)
	if len(r) > maxLinkNameLen {
		r = r[:maxLinkNameLen]
	}
	limited := strings.TrimSpace(strings.TrimRight(string(r), ". "))
	if limited == "" {
		return fallbackLinkName
	}
	stem := strings.ToLower(strings.SplitN(limited, ".", 2)[0])
	if windowsReserved[stem] {
		return limited + "-dir"
	}
	return limited
}

func deriveLinkName(targetPath string) string {
	base := filepath.Base(targetPath)
	if base != "" && base != "." && base != string(filepath.Separator) {
		s := sanitizeLinkName(base)
		if s != fallbackLinkName {
			return s
		}
	}
	parts := strings.FieldsFunc(targetPath, func(r rune) bool { return r == '/' || r == '\\' })
	kept := []string{}
	for _, seg := range parts {
		if seg != "" && seg != "." && seg != ".." {
			kept = append(kept, seg)
		}
	}
	s := sanitizeLinkName(strings.Join(kept, "-"))
	if s == "" {
		return fallbackLinkName
	}
	return s
}

func nameKey(name string) string { return strings.ToLower(name) }

func uniqueLinkName(base string, taken map[string]bool) (string, bool) {
	if !taken[nameKey(base)] {
		return base, true
	}
	for n := 2; n <= maxNameProbes; n++ {
		cand := base + "-" + itoa(n)
		if !taken[nameKey(cand)] {
			return cand, true
		}
	}
	return "", false
}

func itoa(n int) string { return strconv.Itoa(n) }

func occupiedNames(root string) map[string]bool {
	names := map[string]bool{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return names
	}
	for _, e := range entries {
		names[nameKey(e.Name())] = true
	}
	return names
}

// ── canonicalization + target validation ──

func canonical(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", false
	}
	return resolved, true
}

// validateTarget returns (canonicalTarget, rejectionCode). rejectionCode is
// empty when the target is acceptable.
func validateTarget(canonicalRoot, rawTarget string) (string, string) {
	trimmed := strings.TrimSpace(rawTarget)
	if trimmed == "" {
		return "", "not_found"
	}
	c, ok := canonical(trimmed)
	if !ok {
		return "", "not_found"
	}
	info, err := os.Stat(c)
	if err != nil || !info.IsDir() {
		return "", "not_a_directory"
	}
	if c == canonicalRoot {
		return "", "same_as_root"
	}
	if pathStartsWith(canonicalRoot, c) {
		return "", "ancestor_of_root"
	}
	if pathStartsWith(c, canonicalRoot) {
		return "", "inside_root"
	}
	return c, ""
}

// pathStartsWith reports whether child is at or below parent.
func pathStartsWith(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

// ── symlink create / remove / read ──

func createDirSymlink(target, link string) error {
	if err := os.Symlink(target, link); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	// Windows: fall back to a directory junction (needs no privilege).
	out, jerr := exec.Command("cmd", "/C", "mklink", "/J", link, target).CombinedOutput()
	if jerr != nil {
		return web.ErrIO("Failed to create the folder link. Enable Developer Mode in Windows settings, or run the app as administrator.").WithDetail(strings.TrimSpace(string(out)))
	}
	return nil
}

// removeLinkEntry removes the link entry (never its contents). Returns one of
// "removed"/"absent"/"not_a_link".
func removeLinkEntry(link string) (string, error) {
	info, err := os.Lstat(link)
	if err != nil {
		if os.IsNotExist(err) {
			return "absent", nil
		}
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 && !isWindowsReparse(info) {
		return "not_a_link", nil
	}
	if err := os.Remove(link); err != nil {
		return "", err
	}
	return "removed", nil
}

// isWindowsReparse treats a Windows directory junction (ModeDir|ModeIrregular
// without ModeSymlink) as a removable link entry.
func isWindowsReparse(info os.FileInfo) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	return info.Mode()&os.ModeIrregular != 0
}

func readLinkTarget(link string) (string, bool) {
	info, err := os.Lstat(link)
	if err != nil {
		return "", false
	}
	if info.Mode()&os.ModeSymlink == 0 && !isWindowsReparse(info) {
		return "", false
	}
	c, ok := canonical(link)
	if !ok {
		return "", false
	}
	return c, true
}

func linkStatus(root, name, target string) string {
	linkPath := filepath.Join(root, name)
	info, err := os.Lstat(linkPath)
	if err != nil {
		return "missing"
	}
	if info.Mode()&os.ModeSymlink == 0 && !isWindowsReparse(info) {
		return "conflicted"
	}
	resolved, ok := canonical(linkPath)
	if !ok {
		return "broken"
	}
	expected, ok := canonical(target)
	if !ok {
		return "broken"
	}
	if expected == resolved {
		return "ok"
	}
	return "conflicted"
}

// ── DB access (folder_link rows) ──

type folderLinkRow struct {
	id         int64
	folderID   int64
	name       string
	targetPath string
}

func folderLinkList(d *db.Database, folderID int64) []folderLinkRow {
	out := []folderLinkRow{}
	rows, err := d.Conn.Query(`SELECT id, folder_id, name, target_path FROM folder_link WHERE folder_id = ? ORDER BY id ASC`, folderID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r folderLinkRow
		if rows.Scan(&r.id, &r.folderID, &r.name, &r.targetPath) == nil {
			out = append(out, r)
		}
	}
	return out
}

func folderLinkByID(d *db.Database, id int64) (folderLinkRow, bool) {
	var r folderLinkRow
	err := d.Conn.QueryRow(`SELECT id, folder_id, name, target_path FROM folder_link WHERE id = ?`, id).Scan(&r.id, &r.folderID, &r.name, &r.targetPath)
	if err != nil {
		return r, false
	}
	return r, true
}

func folderLinkInsert(d *db.Database, folderID int64, name, target string) (folderLinkRow, error) {
	now := nowRFC3339()
	res, err := d.Conn.Exec(`INSERT INTO folder_link (folder_id, name, target_path, created_at, updated_at) VALUES (?,?,?,?,?)`, folderID, name, target, now, now)
	if err != nil {
		return folderLinkRow{}, err
	}
	id, _ := res.LastInsertId()
	return folderLinkRow{id: id, folderID: folderID, name: name, targetPath: target}, nil
}

func linkDetail(root string, r folderLinkRow) map[string]any {
	return map[string]any{
		"id": r.id, "folderId": r.folderID, "name": r.name,
		"targetPath": r.targetPath, "status": linkStatus(root, r.name, r.targetPath),
	}
}

func loadFolderRoot(d *db.Database, folderID int64) (string, error) {
	path := folderPath(d, folderID)
	if path == "" {
		return "", web.ErrNotFound("Folder not found")
	}
	return path, nil
}

// ── git exclude ──

func addToGitExclude(root, name string) {
	gitDir, ok := gitRevParsePath(root, "--git-common-dir")
	if !ok {
		return
	}
	toplevel, ok := gitRevParsePath(root, "--show-toplevel")
	if !ok {
		return
	}
	infoDir := filepath.Join(gitDir, "info")
	if os.MkdirAll(infoDir, 0o755) != nil {
		return
	}
	excludePath := filepath.Join(infoDir, "exclude")
	existing, _ := os.ReadFile(excludePath)
	line := gitExcludeLine(toplevel, root, name)
	for _, l := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(l) == line {
			return
		}
	}
	next := string(existing)
	if next != "" && !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	if !strings.Contains(next, "# yoyocode workspace links") {
		next += "# yoyocode workspace links\n"
	}
	next += line + "\n"
	_ = os.WriteFile(excludePath, []byte(next), 0o644)
}

func gitExcludeLine(toplevel, root, name string) string {
	cr, ok1 := canonical(root)
	ct, ok2 := canonical(toplevel)
	prefix := ""
	if ok1 && ok2 {
		if rel, err := filepath.Rel(ct, cr); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			prefix = strings.ReplaceAll(rel, "\\", "/")
		}
	}
	if prefix == "" {
		return "/" + name
	}
	return "/" + strings.Trim(prefix, "/") + "/" + name
}

func gitRevParsePath(root, arg string) (string, bool) {
	stdout, _, ok := gitRun(root, "rev-parse", arg)
	if !ok {
		return "", false
	}
	raw := strings.TrimSpace(stdout)
	if raw == "" {
		return "", false
	}
	if filepath.IsAbs(raw) {
		return raw, true
	}
	return filepath.Join(root, raw), true
}

// ── command handlers ──

func RegisterFolderLinks(reg *Registry) {
	reg.Register("list_folder_links", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		folderID := GetInt(c, "folderId", "folder_id")
		root, e := loadFolderRoot(d, folderID)
		if e != nil {
			return nil, e
		}
		out := []map[string]any{}
		for _, r := range folderLinkList(d, folderID) {
			out = append(out, linkDetail(root, r))
		}
		return out, nil
	})

	reg.Register("preview_folder_links", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		folderID := GetInt(c, "folderId", "folder_id")
		root, e := loadFolderRoot(d, folderID)
		if e != nil {
			return nil, e
		}
		canonicalRoot, ok := canonical(root)
		if !ok {
			return nil, web.ErrNotFound("Workspace folder does not exist")
		}
		existing := folderLinkList(d, folderID)
		onDisk := occupiedNames(root)
		taken := map[string]bool{}
		for k := range onDisk {
			taken[k] = true
		}
		for _, l := range existing {
			taken[nameKey(l.name)] = true
		}
		plans := []map[string]any{}
		for _, raw := range GetStringSlice(c, "paths") {
			plans = append(plans, previewOne(raw, canonicalRoot, existing, onDisk, taken))
		}
		return plans, nil
	})

	reg.Register("create_folder_links", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		folderID := GetInt(c, "folderId", "folder_id")
		root, e := loadFolderRoot(d, folderID)
		if e != nil {
			return nil, e
		}
		canonicalRoot, ok := canonical(root)
		if !ok {
			return nil, web.ErrNotFound("Workspace folder does not exist")
		}
		gitExclude := true
		if v, ok := c.Body["gitExclude"].(bool); ok {
			gitExclude = v
		} else if v, ok := c.Body["git_exclude"].(bool); ok {
			gitExclude = v
		}
		existing := folderLinkList(d, folderID)
		taken := occupiedNames(root)
		for _, l := range existing {
			taken[nameKey(l.name)] = true
		}
		linkedTargets := map[string]bool{}
		for _, l := range existing {
			if ct, ok := canonical(l.targetPath); ok {
				linkedTargets[ct] = true
			}
		}
		items, _ := c.Body["items"].([]any)
		created := []map[string]any{}
		for _, it := range items {
			m, _ := it.(map[string]any)
			if m == nil {
				continue
			}
			pathArg := asString(pick(m, "path"))
			canon, rej := validateTarget(canonicalRoot, pathArg)
			if rej != "" {
				continue
			}
			if linkedTargets[canon] {
				continue
			}
			base := ""
			if nm := strings.TrimSpace(asString(pick(m, "name"))); nm != "" {
				base = sanitizeLinkName(nm)
			} else {
				base = deriveLinkName(canon)
			}
			name, ok := uniqueLinkName(base, taken)
			if !ok {
				return nil, web.ErrAlreadyExists("Could not find a free name for the linked folder").WithDetail(base)
			}
			linkPath := filepath.Join(root, name)
			if e := createDirSymlink(canon, linkPath); e != nil {
				return nil, e
			}
			row, ie := folderLinkInsert(d, folderID, name, canon)
			if ie != nil {
				_, _ = removeLinkEntry(linkPath)
				return nil, web.ErrDatabase(ie.Error())
			}
			taken[nameKey(name)] = true
			linkedTargets[canon] = true
			if gitExclude {
				addToGitExclude(root, name)
			}
			created = append(created, linkDetail(root, row))
		}
		if len(created) > 0 {
			Emit(c, "folder://links-changed", map[string]any{"folder_id": folderID})
		}
		return created, nil
	})

	reg.Register("rename_folder_link", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		linkID := GetInt(c, "linkId", "link_id")
		row, ok := folderLinkByID(d, linkID)
		if !ok {
			return nil, web.ErrNotFound("Folder link not found")
		}
		root, e := loadFolderRoot(d, row.folderID)
		if e != nil {
			return nil, e
		}
		sanitized := sanitizeLinkName(GetString(c, "newName", "new_name"))
		if sanitized == row.name {
			return linkDetail(root, row), nil
		}
		taken := occupiedNames(root)
		delete(taken, nameKey(row.name))
		for _, other := range folderLinkList(d, row.folderID) {
			if other.id != linkID {
				taken[nameKey(other.name)] = true
			}
		}
		if taken[nameKey(sanitized)] {
			return nil, web.ErrAlreadyExists("That name is already used in this folder").WithDetail(sanitized)
		}
		from := filepath.Join(root, row.name)
		to := filepath.Join(root, sanitized)
		if _, isLink := readLinkTarget(from); isLink {
			if e := os.Rename(from, to); e != nil {
				return nil, web.ErrIO("Failed to rename the folder link").WithDetail(e.Error())
			}
		} else if _, statErr := os.Lstat(from); statErr == nil {
			return nil, web.ErrInvalidInput("That entry is no longer a folder link")
		} else {
			if e := createDirSymlink(row.targetPath, to); e != nil {
				return nil, e
			}
		}
		_, _ = d.Conn.Exec(`UPDATE folder_link SET name = ?, updated_at = ? WHERE id = ?`, sanitized, nowRFC3339(), linkID)
		row.name = sanitized
		Emit(c, "folder://links-changed", map[string]any{"folder_id": row.folderID})
		return linkDetail(root, row), nil
	})

	reg.Register("repair_folder_link", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		linkID := GetInt(c, "linkId", "link_id")
		row, ok := folderLinkByID(d, linkID)
		if !ok {
			return nil, web.ErrNotFound("Folder link not found")
		}
		root, e := loadFolderRoot(d, row.folderID)
		if e != nil {
			return nil, e
		}
		linkPath := filepath.Join(root, row.name)
		target, ok := canonical(row.targetPath)
		if !ok {
			return nil, web.ErrNotFound("The linked folder no longer exists").WithDetail(row.targetPath)
		}
		info, statErr := os.Lstat(linkPath)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || isWindowsReparse(info) {
				if _, e := removeLinkEntry(linkPath); e != nil {
					return nil, web.ErrIO("Failed to replace the folder link").WithDetail(e.Error())
				}
			} else {
				return nil, web.ErrAlreadyExists("Something else already occupies that name").WithDetail(row.name)
			}
		}
		if e := createDirSymlink(target, linkPath); e != nil {
			return nil, e
		}
		Emit(c, "folder://links-changed", map[string]any{"folder_id": row.folderID})
		return linkDetail(root, row), nil
	})

	reg.Register("remove_folder_link", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		linkID := GetInt(c, "linkId", "link_id")
		row, ok := folderLinkByID(d, linkID)
		if !ok {
			return nil, nil
		}
		deleteLink := true
		if v, ok := c.Body["deleteLink"].(bool); ok {
			deleteLink = v
		} else if v, ok := c.Body["delete_link"].(bool); ok {
			deleteLink = v
		}
		root, _ := loadFolderRoot(d, row.folderID)
		if deleteLink && root != "" {
			linkPath := filepath.Join(root, row.name)
			if _, e := removeLinkEntry(linkPath); e != nil {
				return nil, web.ErrIO("Failed to remove the folder link").WithDetail(linkPath + ": " + e.Error())
			}
		}
		_, _ = d.Conn.Exec(`DELETE FROM folder_link WHERE id = ?`, linkID)
		Emit(c, "folder://links-changed", map[string]any{"folder_id": row.folderID})
		return nil, nil
	})
}

// previewOne builds the dry-run plan for one picked directory.
func previewOne(raw, canonicalRoot string, existing []folderLinkRow, onDisk, taken map[string]bool) map[string]any {
	mk := func(fields map[string]any) map[string]any {
		base := map[string]any{
			"targetPath": raw, "baseName": "", "name": "", "renamed": false,
			"collidesWithExistingEntry": false, "rejection": nil, "existingLinkName": nil,
		}
		for k, v := range fields {
			base[k] = v
		}
		return base
	}
	canon, rej := validateTarget(canonicalRoot, raw)
	if rej != "" {
		return mk(map[string]any{"rejection": rej})
	}
	// Already linked here?
	for _, l := range existing {
		if lc, ok := canonical(l.targetPath); ok && lc == canon {
			return mk(map[string]any{"baseName": deriveLinkName(canon), "rejection": "already_linked", "existingLinkName": l.name})
		}
	}
	base := deriveLinkName(canon)
	name, ok := uniqueLinkName(base, taken)
	if !ok {
		return mk(map[string]any{"baseName": base, "rejection": "name_unavailable", "collidesWithExistingEntry": true})
	}
	renamed := name != base
	taken[nameKey(name)] = true
	return mk(map[string]any{
		"baseName": base, "name": name, "renamed": renamed,
		"collidesWithExistingEntry": renamed && onDisk[nameKey(base)],
	})
}
