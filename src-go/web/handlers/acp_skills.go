package handlers

// Agent skills management (upstream commands/acp.rs: acp_list_agent_skills /
// acp_read_agent_skill / acp_save_agent_skill / acp_delete_agent_skill, plus
// acp_current_platform). Skills live in each agent CLI's own skill store as
// either a SKILL.md-bearing directory or (for some agents) a flat <id>.md
// file. All errors wire as task_execution_failed to match the upstream
// handler mapping (AcpError -> AppCommandError::task_execution_failed).

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"yoyocode/src-go/web"
)

// skillStorageKind: whether an agent accepts directory bundles only, or also
// flat markdown files.
type skillStorageKind int

const (
	skillDirOnly skillStorageKind = iota
	skillDirOrMarkdown
)

type skillScope int

const (
	skillScopeGlobal skillScope = iota
	skillScopeProject
)

func skillScopeFromWire(s string) (skillScope, bool) {
	switch s {
	case "global":
		return skillScopeGlobal, true
	case "project":
		return skillScopeProject, true
	}
	return 0, false
}

func (s skillScope) wire() string {
	if s == skillScopeProject {
		return "project"
	}
	return "global"
}

type skillLayout int

const (
	skillLayoutMarkdownFile skillLayout = iota
	skillLayoutDirectory
)

func (l skillLayout) wire() string {
	if l == skillLayoutDirectory {
		return "skill_directory"
	}
	return "markdown_file"
}

func skillLayoutFromWire(s string) (skillLayout, bool) {
	switch s {
	case "skill_directory":
		return skillLayoutDirectory, true
	case "markdown_file":
		return skillLayoutMarkdownFile, true
	}
	return 0, false
}

type skillStorageSpec struct {
	kind       skillStorageKind
	globalDirs []string
	projectRel []string
}

func skillHomeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

// expandEnvHome resolves an env var that may be unset ("" -> fallback),
// honoring a leading ~ / ~/ the way the upstream path helpers do.
func expandEnvHome(envVar, fallback string) string {
	v := strings.TrimSpace(os.Getenv(envVar))
	if v == "" {
		return fallback
	}
	if v == "~" {
		return skillHomeDir()
	}
	if rest, ok := strings.CutPrefix(v, "~/"); ok {
		return filepath.Join(skillHomeDir(), rest)
	}
	return v
}

func hermesHomeDir() string {
	return expandEnvHome("HERMES_HOME", filepath.Join(skillHomeDir(), ".hermes"))
}
func grokHomeDir() string { return expandEnvHome("GROK_HOME", filepath.Join(skillHomeDir(), ".grok")) }
func kimiHomeDir() string {
	return expandEnvHome("KIMI_CODE_HOME", filepath.Join(skillHomeDir(), ".kimi-code"))
}
func dshHomeDir() string { return expandEnvHome("DSH_HOME", filepath.Join(skillHomeDir(), ".dsh")) }
func dshAgentsDir() string {
	return expandEnvHome("DSH_AGENTS_HOME", filepath.Join(skillHomeDir(), ".agents"))
}
func piAgentDir() string { return filepath.Join(skillHomeDir(), ".pi", "agent") }
func qoderConfigDir() string {
	return expandEnvHome("QODER_CLI_HOME", filepath.Join(skillHomeDir(), ".qoder"))
}
func geminiHomeDir() string {
	return expandEnvHome("GEMINI_HOME", filepath.Join(skillHomeDir(), ".gemini"))
}

func join(parts ...string) string { return filepath.Join(parts...) }

// skillStorageSpecFor mirrors upstream skill_storage_spec(agent_type). Returns
// (spec, true) for a supported agent, (_, false) otherwise.
func skillStorageSpecFor(agentType string) (skillStorageSpec, bool) {
	home := skillHomeDir()
	agents := join(home, ".agents", "skills")
	switch agentType {
	case "claude_code":
		return skillStorageSpec{skillDirOnly, []string{join(home, ".claude", "skills")}, []string{".claude/skills"}}, true
	case "codex":
		return skillStorageSpec{skillDirOrMarkdown,
			[]string{join(codexHomeDir(), "skills"), join(codexHomeDir(), "skills", ".system"), agents},
			[]string{".codex/skills", ".agents/skills"}}, true
	case "open_code":
		return skillStorageSpec{skillDirOnly,
			[]string{join(home, ".config", "opencode", "skills"), agents},
			[]string{".agents/skills", ".opencode/skills"}}, true
	case "gemini":
		return skillStorageSpec{skillDirOnly,
			[]string{join(home, ".gemini", "skills"), agents},
			[]string{".gemini/skills", ".agents/skills"}}, true
	case "open_claw":
		return skillStorageSpec{skillDirOnly, []string{join(home, ".openclaw", "skills")}, []string{"skills"}}, true
	case "cline":
		return skillStorageSpec{skillDirOnly,
			[]string{agents, join(home, ".cline", "skills")},
			[]string{".agents/skills", ".cline/skills", ".clinerules/skills", ".claude/skills"}}, true
	case "hermes":
		return skillStorageSpec{skillDirOnly, []string{join(hermesHomeDir(), "skills")}, nil}, true
	case "code_buddy":
		return skillStorageSpec{skillDirOnly, []string{join(home, ".codebuddy", "skills")}, []string{".codebuddy/skills"}}, true
	case "kimi_code":
		return skillStorageSpec{skillDirOnly,
			[]string{join(kimiHomeDir(), "skills"), agents},
			[]string{".kimi-code/skills", ".agents/skills"}}, true
	case "pi":
		return skillStorageSpec{skillDirOrMarkdown,
			[]string{join(piAgentDir(), "skills"), agents},
			[]string{".pi/skills", ".agents/skills"}}, true
	case "grok":
		return skillStorageSpec{skillDirOnly, []string{join(grokHomeDir(), "skills")}, []string{".grok/skills"}}, true
	case "cursor":
		return skillStorageSpec{skillDirOnly,
			[]string{join(home, ".cursor", "skills"), agents, join(home, ".cursor", "skills-cursor")},
			[]string{".cursor/skills", ".agents/skills"}}, true
	case "deepseek":
		return skillStorageSpec{skillDirOrMarkdown,
			[]string{join(dshHomeDir(), "skills"), join(dshAgentsDir(), "skills")},
			[]string{".dsh/skills", ".agents/skills"}}, true
	case "qoder":
		return skillStorageSpec{skillDirOnly,
			[]string{join(qoderConfigDir(), "skills"), join(qoderConfigDir(), ".agents", "skills")},
			[]string{".qoder/skills", ".agents/skills"}}, true
	case "antigravity":
		return skillStorageSpec{skillDirOnly,
			[]string{join(geminiHomeDir(), "config", "skills"), join(geminiHomeDir(), "antigravity-cli", "skills")},
			[]string{".gemini/skills", ".agents/skills"}}, true
	}
	return skillStorageSpec{}, false
}

// projectSkillBase: the directory project-relative skill dirs hang off.
// DeepSeek/KimiCode walk up to the nearest ancestor holding .git.
func projectSkillBase(agentType, workspace string) string {
	if agentType != "deepseek" && agentType != "kimi_code" {
		return workspace
	}
	cur := workspace
	for {
		if _, err := os.Stat(join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return workspace
		}
		cur = parent
	}
}

func validateSkillID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", web.ErrTaskExecution("skill id cannot be empty")
	}
	if strings.HasPrefix(id, ".") {
		return "", web.ErrTaskExecution("skill id cannot start with a dot (.)")
	}
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return "", web.ErrTaskExecution("skill id cannot contain path separators")
	}
	for _, c := range id {
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
		if !ok {
			return "", web.ErrTaskExecution("skill id can only include letters, numbers, '-', '_' and '.'")
		}
	}
	return id, nil
}

func scopedSkillDirs(agentType string, scope skillScope, workspace string) ([]string, error) {
	spec, ok := skillStorageSpecFor(agentType)
	if !ok {
		return nil, web.ErrTaskExecution(agentType + " skills are not supported in Settings yet")
	}
	if scope == skillScopeGlobal {
		return spec.globalDirs, nil
	}
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return nil, web.ErrTaskExecution("workspace_path is required for project scoped skills")
	}
	base := projectSkillBase(agentType, ws)
	out := make([]string, 0, len(spec.projectRel))
	for _, rel := range spec.projectRel {
		out = append(out, join(base, filepath.FromSlash(rel)))
	}
	return out, nil
}

func preferredScopeSkillDir(agentType string, scope skillScope, workspace string) (string, error) {
	dirs, err := scopedSkillDirs(agentType, scope, workspace)
	if err != nil {
		return "", err
	}
	if len(dirs) == 0 {
		return "", web.ErrTaskExecution("no skill directory resolved for this agent")
	}
	return dirs[0], nil
}

func isMarkdownFile(p string) bool {
	return strings.EqualFold(filepath.Ext(p), ".md")
}

func skillContentPath(layout skillLayout, skillPath string) string {
	if layout == skillLayoutDirectory {
		return join(skillPath, "SKILL.md")
	}
	return skillPath
}

// parseFrontmatterScalar reads a single-line YAML scalar with optional quotes.
func parseFrontmatterScalar(rest string) (string, bool) {
	val := strings.TrimSpace(rest)
	if strings.HasPrefix(val, "|") || strings.HasPrefix(val, ">") {
		return "", false
	}
	unq := val
	if len(val) >= 2 {
		if val[0] == '"' && val[len(val)-1] == '"' {
			unq = val[1 : len(val)-1]
		} else if val[0] == '\'' && val[len(val)-1] == '\'' {
			unq = val[1 : len(val)-1]
		}
	}
	unq = strings.TrimSpace(unq)
	if unq == "" {
		return "", false
	}
	return unq, true
}

// readSkillDescription extracts short-description (any indent) or top-level
// description from the leading YAML frontmatter. Reads the first 4 KiB only.
func readSkillDescription(contentPath string) *string {
	f, err := os.Open(contentPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	head := string(buf[:n])
	sc := bufio.NewScanner(strings.NewReader(head))
	sc.Buffer(make([]byte, 0, 8192), 8192)
	if !sc.Scan() {
		return nil
	}
	if strings.TrimSpace(sc.Text()) != "---" {
		return nil
	}
	var short, long *string
	for sc.Scan() {
		line := sc.Text()
		te := strings.TrimRight(line, " \t")
		if te == "---" || te == "..." {
			break
		}
		isTop := len(line) > 0 && !(line[0] == ' ' || line[0] == '\t')
		stripped := strings.TrimSpace(line)
		if short == nil {
			if rest, ok := strings.CutPrefix(stripped, "short-description:"); ok {
				if val, ok := parseFrontmatterScalar(rest); ok {
					v := val
					short = &v
					break
				}
			}
		}
		if isTop && long == nil {
			if rest, ok := strings.CutPrefix(line, "description:"); ok {
				if val, ok := parseFrontmatterScalar(rest); ok {
					v := val
					long = &v
				}
			}
		}
	}
	if short != nil {
		return short
	}
	return long
}

func buildSkillItem(id string, scope skillScope, layout skillLayout, skillPath string) map[string]any {
	desc := readSkillDescription(skillContentPath(layout, skillPath))
	var descVal any = nil
	if desc != nil {
		descVal = *desc
	}
	return map[string]any{
		"id":          id,
		"name":        id,
		"scope":       scope.wire(),
		"layout":      layout.wire(),
		"path":        skillPath,
		"description": descVal,
		"read_only":   false,
	}
}

// isReadOnlySkillPath mirrors upstream: Codex's .system, Cursor's bundled
// skills-cursor, DeepSeek's .system are listed but never written.
func isReadOnlySkillPath(agentType, skillPath string) bool {
	var roRoot string
	switch agentType {
	case "codex":
		roRoot = join(codexHomeDir(), "skills", ".system")
	case "cursor":
		roRoot = join(skillHomeDir(), ".cursor", "skills-cursor")
	case "deepseek":
		roRoot = join(dshHomeDir(), "skills", ".system")
	default:
		return false
	}
	rel, err := filepath.Rel(roRoot, skillPath)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

func listSkillsFromDir(scope skillScope, dir string, kind skillStorageKind) ([]map[string]any, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, web.ErrTaskExecution("failed to read skills directory: " + err.Error())
	}
	byID := map[string]map[string]any{}
	order := []string{}
	for _, e := range entries {
		p := join(dir, e.Name())
		id := e.Name()
		info, serr := os.Stat(p)
		if serr != nil {
			continue
		}
		if info.IsDir() {
			if _, err := os.Stat(join(p, "SKILL.md")); err != nil {
				continue
			}
			if _, dup := byID[id]; !dup {
				order = append(order, id)
			}
			byID[id] = buildSkillItem(id, scope, skillLayoutDirectory, p)
			continue
		}
		if !info.IsDir() && kind == skillDirOrMarkdown && isMarkdownFile(p) {
			stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			if stem == "" {
				stem = id
			}
			if _, exists := byID[stem]; exists {
				continue
			}
			order = append(order, stem)
			byID[stem] = buildSkillItem(stem, scope, skillLayoutMarkdownFile, p)
		}
	}
	sort.Strings(order)
	out := make([]map[string]any, 0, len(order))
	seen := map[string]bool{}
	for _, id := range order {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, byID[id])
	}
	return out, nil
}

func locateExistingSkill(dir string, kind skillStorageKind, skillID string, scope skillScope) (map[string]any, bool) {
	sd := join(dir, skillID)
	if info, err := os.Stat(sd); err == nil && info.IsDir() {
		if _, err := os.Stat(join(sd, "SKILL.md")); err == nil {
			return buildSkillItem(skillID, scope, skillLayoutDirectory, sd), true
		}
	}
	if kind == skillDirOrMarkdown {
		fp := join(dir, skillID+".md")
		if info, err := os.Stat(fp); err == nil && !info.IsDir() {
			return buildSkillItem(skillID, scope, skillLayoutMarkdownFile, fp), true
		}
	}
	return nil, false
}

func locateExistingSkillAcrossDirs(dirs []string, kind skillStorageKind, skillID string, scope skillScope) (map[string]any, bool) {
	for _, d := range dirs {
		if item, ok := locateExistingSkill(d, kind, skillID, scope); ok {
			return item, true
		}
	}
	return nil, false
}

// removeSkillEntry removes a symlink as the link only; otherwise removes a dir
// recursively or unlinks a file.
func removeSkillEntry(p string) error {
	info, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(p)
	}
	if info.IsDir() {
		return os.RemoveAll(p)
	}
	return os.Remove(p)
}

// RegisterACPSkills wires the agent-skills CRUD endpoints + acp_current_platform.
func RegisterACPSkills(reg *Registry) {
	reg.Register("acp_current_platform", func(c *web.Context) (any, error) {
		return acpCurrentPlatform(), nil
	})

	reg.Register("acp_list_agent_skills", func(c *web.Context) (any, error) {
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		if agentType == "" {
			return nil, web.ErrInvalidInput("agentType required")
		}
		workspace := strVal(c.Body["workspacePath"], c.Body["workspace_path"])
		spec, ok := skillStorageSpecFor(agentType)
		if !ok {
			return map[string]any{
				"supported": false,
				"message":   agentType + " 暂不支持在设置页管理 Skills",
				"locations": []any{},
				"skills":    []any{},
			}, nil
		}
		locations := []map[string]any{}
		byKey := map[string]map[string]any{}
		keyOrder := []string{}
		add := func(scope skillScope, dir string) error {
			_, statErr := os.Stat(dir)
			locations = append(locations, map[string]any{
				"scope":  scope.wire(),
				"path":   dir,
				"exists": statErr == nil,
			})
			listed, err := listSkillsFromDir(scope, dir, spec.kind)
			if err != nil {
				return err
			}
			for _, sk := range listed {
				key := scope.wire() + ":" + sk["id"].(string)
				if _, dup := byKey[key]; !dup {
					byKey[key] = sk
					keyOrder = append(keyOrder, key)
				}
			}
			return nil
		}
		for _, dir := range spec.globalDirs {
			if err := add(skillScopeGlobal, dir); err != nil {
				return nil, err
			}
		}
		if ws := strings.TrimSpace(workspace); ws != "" {
			base := projectSkillBase(agentType, ws)
			for _, rel := range spec.projectRel {
				if err := add(skillScopeProject, join(base, filepath.FromSlash(rel))); err != nil {
					return nil, err
				}
			}
		}
		skills := make([]map[string]any, 0, len(keyOrder))
		for _, k := range keyOrder {
			sk := byKey[k]
			if isReadOnlySkillPath(agentType, sk["path"].(string)) {
				sk["read_only"] = true
			}
			skills = append(skills, sk)
		}
		scopeRank := func(w string) int {
			if w == "project" {
				return 1
			}
			return 0
		}
		sort.SliceStable(skills, func(i, j int) bool {
			ri, rj := scopeRank(skills[i]["scope"].(string)), scopeRank(skills[j]["scope"].(string))
			if ri != rj {
				return ri < rj
			}
			return skills[i]["name"].(string) < skills[j]["name"].(string)
		})
		return map[string]any{
			"supported": true,
			"message":   nil,
			"locations": locations,
			"skills":    skills,
		}, nil
	})

	reg.Register("acp_read_agent_skill", func(c *web.Context) (any, error) {
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		if agentType == "" {
			return nil, web.ErrInvalidInput("agentType required")
		}
		scope, ok := skillScopeFromWire(strVal(c.Body["scope"]))
		if !ok {
			return nil, web.ErrInvalidInput("scope must be 'global' or 'project'")
		}
		spec, ok := skillStorageSpecFor(agentType)
		if !ok {
			return nil, web.ErrTaskExecution(agentType + " skills are not supported in Settings yet")
		}
		id, err := validateSkillID(strVal(c.Body["skillId"], c.Body["skill_id"]))
		if err != nil {
			return nil, err
		}
		workspace := strVal(c.Body["workspacePath"], c.Body["workspace_path"])
		dirs, err := scopedSkillDirs(agentType, scope, workspace)
		if err != nil {
			return nil, err
		}
		skill, ok := locateExistingSkillAcrossDirs(dirs, spec.kind, id, scope)
		if !ok {
			return nil, web.ErrTaskExecution("skill not found: " + id)
		}
		if isReadOnlySkillPath(agentType, skill["path"].(string)) {
			skill["read_only"] = true
		}
		layout := skillLayoutMarkdownFile
		if skill["layout"].(string) == "skill_directory" {
			layout = skillLayoutDirectory
		}
		content, rerr := os.ReadFile(skillContentPath(layout, skill["path"].(string)))
		if rerr != nil {
			return nil, web.ErrTaskExecution("failed to read skill content: " + rerr.Error())
		}
		return map[string]any{"skill": skill, "content": string(content)}, nil
	})

	reg.Register("acp_save_agent_skill", func(c *web.Context) (any, error) {
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		if agentType == "" {
			return nil, web.ErrInvalidInput("agentType required")
		}
		scope, ok := skillScopeFromWire(strVal(c.Body["scope"]))
		if !ok {
			return nil, web.ErrInvalidInput("scope must be 'global' or 'project'")
		}
		spec, ok := skillStorageSpecFor(agentType)
		if !ok {
			return nil, web.ErrTaskExecution(agentType + " skills are not supported in Settings yet")
		}
		id, err := validateSkillID(strVal(c.Body["skillId"], c.Body["skill_id"]))
		if err != nil {
			return nil, err
		}
		content := strVal(c.Body["content"])
		workspace := strVal(c.Body["workspacePath"], c.Body["workspace_path"])
		dirs, err := scopedSkillDirs(agentType, scope, workspace)
		if err != nil {
			return nil, err
		}
		preferred, err := preferredScopeSkillDir(agentType, scope, workspace)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(preferred, 0o755); err != nil {
			return nil, web.ErrTaskExecution("failed to create skills directory: " + err.Error())
		}
		existing, had := locateExistingSkillAcrossDirs(dirs, spec.kind, id, scope)
		if had && isReadOnlySkillPath(agentType, existing["path"].(string)) {
			return nil, web.ErrTaskExecution("skill '" + id + "' is a built-in system skill and cannot be modified")
		}
		var skill map[string]any
		if had {
			skill = existing
		} else {
			newLayout := skillLayoutDirectory
			if spec.kind == skillDirOrMarkdown {
				newLayout = skillLayoutMarkdownFile
				if w, ok := skillLayoutFromWire(strVal(c.Body["layout"])); ok {
					newLayout = w
				}
			}
			var skillPath string
			if newLayout == skillLayoutDirectory {
				skillPath = join(preferred, id)
			} else {
				skillPath = join(preferred, id+".md")
			}
			skill = buildSkillItem(id, scope, newLayout, skillPath)
		}
		layout := skillLayoutMarkdownFile
		if skill["layout"].(string) == "skill_directory" {
			layout = skillLayoutDirectory
		}
		skillPath := skill["path"].(string)
		contentPath := skillContentPath(layout, skillPath)
		if layout == skillLayoutDirectory {
			if err := os.MkdirAll(skillPath, 0o755); err != nil {
				return nil, web.ErrTaskExecution("failed to create skill directory '" + skillPath + "': " + err.Error())
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(contentPath), 0o755); err != nil {
				return nil, web.ErrTaskExecution("failed to create skill parent directory: " + err.Error())
			}
		}
		if err := os.WriteFile(contentPath, []byte(content), 0o644); err != nil {
			return nil, web.ErrTaskExecution("failed to write skill content: " + err.Error())
		}
		if desc := readSkillDescription(contentPath); desc != nil {
			skill["description"] = *desc
		} else {
			skill["description"] = nil
		}
		return skill, nil
	})

	reg.Register("acp_delete_agent_skill", func(c *web.Context) (any, error) {
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		if agentType == "" {
			return nil, web.ErrInvalidInput("agentType required")
		}
		scope, ok := skillScopeFromWire(strVal(c.Body["scope"]))
		if !ok {
			return nil, web.ErrInvalidInput("scope must be 'global' or 'project'")
		}
		spec, ok := skillStorageSpecFor(agentType)
		if !ok {
			return nil, web.ErrTaskExecution(agentType + " skills are not supported in Settings yet")
		}
		id, err := validateSkillID(strVal(c.Body["skillId"], c.Body["skill_id"]))
		if err != nil {
			return nil, err
		}
		workspace := strVal(c.Body["workspacePath"], c.Body["workspace_path"])
		dirs, err := scopedSkillDirs(agentType, scope, workspace)
		if err != nil {
			return nil, err
		}
		skill, ok := locateExistingSkillAcrossDirs(dirs, spec.kind, id, scope)
		if !ok {
			return nil, web.ErrTaskExecution("skill not found: " + id)
		}
		if isReadOnlySkillPath(agentType, skill["path"].(string)) {
			return nil, web.ErrTaskExecution("skill '" + id + "' is a built-in system skill and cannot be deleted")
		}
		if err := removeSkillEntry(skill["path"].(string)); err != nil {
			return nil, web.ErrTaskExecution("failed to delete skill entry: " + err.Error())
		}
		return nil, nil
	})
}

// acpCurrentPlatform returns the registry platform token for this machine
// (acp::registry::current_platform): <os>-<arch>, e.g. "darwin-aarch64",
// "windows-x86_64". The arch names follow the upstream (x86_64 / aarch64).
func acpCurrentPlatform() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	return runtime.GOOS + "-" + arch
}
