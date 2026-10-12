package handlers

// Custom (user-authored) skills — the central store at ~/.yoyocode/skills/<id>/
// SKILL.md (upstream commands/custom_skills.rs). This implements the
// store-CRUD surface: list / read / create / save / duplicate / import /
// delete. The link engine (custom_apply_links / custom_list_all_install_statuses
// / custom_import_from_agent), which symlinks a central skill into each agent's
// own skill dir, is a separate pass. Original Go reimplementation of the wire
// contract; reserved-id checks against bundled packs are a no-op here because
// the Go build ships none, so every store dir is a custom skill.

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"yoyocode/src-go/web"
)

func customCentralDir() string {
	return filepath.Join(skillHomeDir(), ".yoyocode", "skills")
}

func customCentralPath(id string) string { return filepath.Join(customCentralDir(), id) }
func customSkillMd(id string) string     { return filepath.Join(customCentralPath(id), "SKILL.md") }

// readFrontmatterName extracts a top-level name: scalar from a SKILL.md.
func readFrontmatterName(skillMd string) string {
	f, err := os.Open(skillMd)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	head := string(buf[:n])
	lines := strings.Split(head, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		te := strings.TrimRight(line, " \t\r")
		if te == "---" || te == "..." {
			break
		}
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			if rest, ok := strings.CutPrefix(line, "name:"); ok {
				val := strings.TrimSpace(rest)
				val = strings.Trim(val, "\"'")
				val = strings.TrimSpace(val)
				if val != "" {
					return val
				}
			}
		}
	}
	return ""
}

func buildCustomItem(id string) map[string]any {
	md := customSkillMd(id)
	name := readFrontmatterName(md)
	if name == "" {
		name = id
	}
	var descVal any
	if d := readSkillDescription(md); d != nil {
		descVal = *d
	}
	return map[string]any{
		"id":          id,
		"name":        name,
		"description": descVal,
		"centralPath": customCentralPath(id),
	}
}

func collectCustomIDs() ([]string, error) {
	central := customCentralDir()
	entries, err := os.ReadDir(central)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, web.ErrIO(err.Error())
	}
	ids := []string{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if _, verr := validateSkillID(name); verr != nil {
			continue
		}
		p := filepath.Join(central, name)
		info, serr := os.Stat(p)
		if serr != nil || !info.IsDir() {
			continue
		}
		if fi, e2 := os.Stat(filepath.Join(p, "SKILL.md")); e2 != nil || fi.IsDir() {
			continue
		}
		ids = append(ids, name)
	}
	sort.Strings(ids)
	return ids, nil
}

func deriveIDFromName(name string) string {
	var b strings.Builder
	prevDash := false
	for _, ch := range strings.TrimSpace(name) {
		c := ch
		if c >= 'A' && c <= 'Z' {
			c = c - 'A' + 'a'
		}
		isAlnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if isAlnum || c == '.' || c == '_' {
			b.WriteRune(c)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-.")
}

func pathIsSymlink(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func isMarkdownPath(p string) bool { return strings.EqualFold(filepath.Ext(p), ".md") }

func copyFileContents(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFileContents(path, target)
	})
}

// RegisterCustomSkills wires the central-store custom-skill CRUD endpoints.
func RegisterCustomSkills(reg *Registry) {
	reg.Register("custom_list", func(c *web.Context) (any, error) {
		ids, err := collectCustomIDs()
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			out = append(out, buildCustomItem(id))
		}
		return out, nil
	})

	reg.Register("custom_read_skill", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "id"))
		if err != nil {
			return nil, err
		}
		md := customSkillMd(id)
		if fi, e := os.Stat(md); e != nil || fi.IsDir() {
			return nil, web.ErrNotFound("custom skill not found: " + id)
		}
		b, rerr := os.ReadFile(md)
		if rerr != nil {
			return nil, web.ErrIO(rerr.Error())
		}
		return string(b), nil
	})

	reg.Register("custom_create_skill", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "id"))
		if err != nil {
			return nil, err
		}
		dir := customCentralPath(id)
		if _, e := os.Stat(dir); e == nil || pathIsSymlink(dir) {
			return nil, web.ErrAlreadyExists("a skill with id '" + id + "' already exists")
		}
		if e := os.MkdirAll(dir, 0o755); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		if e := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(GetString(c, "content")), 0o644); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		return buildCustomItem(id), nil
	})

	reg.Register("custom_save_skill", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "id"))
		if err != nil {
			return nil, err
		}
		dir := customCentralPath(id)
		if pathIsSymlink(dir) {
			return nil, web.ErrIO("'" + id + "' is a symlink, not an editable custom skill")
		}
		if fi, e := os.Stat(filepath.Join(dir, "SKILL.md")); e != nil || fi.IsDir() {
			return nil, web.ErrNotFound("custom skill not found: " + id)
		}
		if e := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(GetString(c, "content")), 0o644); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		return buildCustomItem(id), nil
	})

	reg.Register("custom_duplicate_skill", func(c *web.Context) (any, error) {
		srcID, err := validateSkillID(GetString(c, "sourceId", "source_id"))
		if err != nil {
			return nil, err
		}
		if fi, e := os.Stat(customSkillMd(srcID)); e != nil || fi.IsDir() {
			return nil, web.ErrNotFound("custom skill not found: " + srcID)
		}
		newID, err2 := validateSkillID(GetString(c, "newId", "new_id"))
		if err2 != nil {
			return nil, err2
		}
		dst := customCentralPath(newID)
		if _, e := os.Stat(dst); e == nil || pathIsSymlink(dst) {
			return nil, web.ErrAlreadyExists("a skill with id '" + newID + "' already exists")
		}
		if e := copyDirRecursive(customCentralPath(srcID), dst); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		return buildCustomItem(newID), nil
	})

	reg.Register("custom_import_skill", func(c *web.Context) (any, error) {
		src := strings.TrimSpace(GetString(c, "sourcePath", "source_path"))
		if src == "" {
			return nil, web.ErrInvalidInput("sourcePath required")
		}
		info, e := os.Stat(src)
		if e != nil {
			return nil, web.ErrInvalidInput("invalid skill source: path does not exist: " + src)
		}
		derived := strings.TrimSpace(GetString(c, "id"))
		if derived == "" {
			base := filepath.Base(src)
			if !info.IsDir() {
				base = strings.TrimSuffix(base, filepath.Ext(base))
			}
			derived = deriveIDFromName(base)
		}
		id, verr := validateSkillID(derived)
		if verr != nil {
			return nil, verr
		}
		dst := customCentralPath(id)
		if _, se := os.Stat(dst); se == nil || pathIsSymlink(dst) {
			return nil, web.ErrAlreadyExists("a skill with id '" + id + "' already exists")
		}
		if info.IsDir() {
			if fi, me := os.Stat(filepath.Join(src, "SKILL.md")); me != nil || fi.IsDir() {
				return nil, web.ErrInvalidInput("invalid skill source: folder has no SKILL.md")
			}
			if ce := copyDirRecursive(src, dst); ce != nil {
				return nil, web.ErrIO(ce.Error())
			}
		} else if isMarkdownPath(src) {
			if ce := copyFileContents(src, filepath.Join(dst, "SKILL.md")); ce != nil {
				return nil, web.ErrIO(ce.Error())
			}
		} else {
			return nil, web.ErrInvalidInput("invalid skill source: source must be a folder containing SKILL.md, or a .md file")
		}
		return buildCustomItem(id), nil
	})

	reg.Register("custom_delete_skills", func(c *web.Context) (any, error) {
		raw := GetRaw(c, "ids")
		var ids []string
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &ids)
		}
		out := make([]map[string]any, 0, len(ids))
		for _, rawID := range ids {
			res := map[string]any{"id": rawID, "ok": true, "error": nil}
			if e := deleteOneCustomSkill(rawID); e != nil {
				res["ok"] = false
				res["error"] = e.Error()
			}
			out = append(out, res)
		}
		return out, nil
	})
}

func deleteOneCustomSkill(rawID string) error {
	id, err := validateSkillID(rawID)
	if err != nil {
		return err
	}
	central := customCentralPath(id)
	if pathIsSymlink(central) {
		return removeSkillEntry(central)
	}
	if _, e := os.Stat(central); e == nil {
		root := customCentralDir()
		rel, rerr := filepath.Rel(root, central)
		if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return web.ErrIO("refusing to delete outside the central store")
		}
		if de := os.RemoveAll(central); de != nil {
			return web.ErrIO(de.Error())
		}
	}
	return nil
}
