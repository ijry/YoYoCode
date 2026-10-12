package handlers

// Project bootstrap (B11), mirroring commands/project_boot.rs: detect a package
// manager, scaffold a shadcn / HyperFrames project through the matching runner,
// and detect / install the HyperFrames agent skills. The create/install actions
// shell out to the real CLIs (npx/pnpm/yarn/bunx) and surface dependency_missing
// when Node tooling is absent. Skill-dir enumeration is not yet wired, so
// detect_hyperframes_skills reports the six supported agents as not-installed.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"yoyocode/src-go/web"
)

var hyperframesSkillAgents = []string{"claude-code", "codex", "opencode", "gemini-cli", "openclaw", "cline"}

func RegisterProjectBoot(reg *Registry) {
	reg.Register("detect_package_manager", func(c *web.Context) (any, error) {
		return detectPackageManager(GetString(c, "name")), nil
	})
	reg.Register("create_shadcn_project", func(c *web.Context) (any, error) {
		return createShadcnProject(c)
	})
	reg.Register("detect_hyperframes_skills", func(c *web.Context) (any, error) {
		out := []map[string]any{}
		for _, a := range hyperframesSkillAgents {
			out = append(out, map[string]any{"agent": a, "installed": false})
		}
		return out, nil
	})
	reg.Register("install_hyperframes_skills", func(c *web.Context) (any, error) {
		return installHyperframesSkills(GetStringSlice(c, "agents"))
	})
	reg.Register("create_hyperframes_project", func(c *web.Context) (any, error) {
		return createHyperframesProject(c)
	})
}

// ── detect ──

func detectPackageManager(name string) map[string]any {
	program := "npm"
	switch name {
	case "bun":
		program = "bun"
	case "pnpm":
		program = "pnpm"
	case "yarn":
		program = "yarn"
	}
	out, err := exec.Command(program, "--version").Output()
	if err == nil {
		return map[string]any{"name": name, "installed": true, "version": strings.TrimSpace(string(out))}
	}
	return map[string]any{"name": name, "installed": false, "version": nil}
}

// ── validation ──

func validateProjectName(name string) error {
	if name == "" {
		return web.ErrInvalidInput("Project name is required")
	}
	if strings.HasPrefix(name, "-") {
		return web.ErrInvalidInput("Project name must not start with '-'")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") || filepath.IsAbs(name) {
		return web.ErrInvalidInput("Project name must be a single folder name (no path separators, '..', or absolute paths)")
	}
	return nil
}

// runnerFor maps a package manager to its fetch-and-run runner.
func runnerFor(pm string, npxYes bool) (string, []string) {
	switch pm {
	case "pnpm":
		return "pnpm", []string{"dlx"}
	case "yarn":
		return "yarn", []string{"dlx"}
	case "bun":
		return "bunx", nil
	default:
		if npxYes {
			return "npx", []string{"--yes"}
		}
		return "npx", nil
	}
}

func dirExistsNonEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// runScaffold runs the scaffolding command and maps spawn/exit faults to the
// same errors the desktop build returns.
func runScaffold(program string, args []string, targetDir string) error {
	cmd := exec.Command(program, args...)
	cmd.Dir = targetDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if execIsNotFound(err) {
			return web.ErrDependencyMissing(program + " is not installed. Please install Node.js first.")
		}
		if _, ok := err.(*exec.ExitError); ok {
			detail := strings.TrimSpace(string(out))
			if detail == "" {
				detail = err.Error()
			}
			return web.ErrExternalCommand("Project creation command failed").WithDetail(detail)
		}
		return web.ErrExternalCommand("Failed to execute project creation command").WithDetail(err.Error())
	}
	return nil
}

func execIsNotFound(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "executable file not found") ||
		strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "cannot find"))
}

// ── create shadcn ──

func createShadcnProject(c *web.Context) (any, error) {
	projectName := strings.TrimSpace(GetString(c, "projectName", "project_name"))
	template := strings.TrimSpace(GetString(c, "template"))
	presetCode := strings.TrimSpace(GetString(c, "presetCode", "preset_code"))
	packageManager := strings.TrimSpace(GetString(c, "packageManager", "package_manager"))
	targetDir := strings.TrimSpace(GetString(c, "targetDir", "target_dir"))
	if err := validateProjectName(projectName); err != nil {
		return nil, err
	}
	if template == "" {
		return nil, web.ErrInvalidInput("Template is required")
	}
	if targetDir == "" {
		return nil, web.ErrInvalidInput("Target directory is required")
	}
	fullPath := filepath.Join(targetDir, projectName)
	if dirExistsNonEmpty(fullPath) {
		return nil, web.ErrAlreadyExists("Target directory already exists and is not empty")
	}
	program, prefix := runnerFor(packageManager, false)
	args := append(append([]string{}, prefix...), "shadcn@latest", "init", "-n", projectName, "-t", template, "-p", presetCode, "-y")
	if err := runScaffold(program, args, targetDir); err != nil {
		return nil, err
	}
	return fullPath, nil
}

// ── create hyperframes ──

func createHyperframesProject(c *web.Context) (any, error) {
	projectName := strings.TrimSpace(GetString(c, "projectName", "project_name"))
	example := strings.TrimSpace(GetString(c, "example"))
	resolution := strings.TrimSpace(GetString(c, "resolution"))
	packageManager := strings.TrimSpace(GetString(c, "packageManager", "package_manager"))
	targetDir := strings.TrimSpace(GetString(c, "targetDir", "target_dir"))
	if err := validateProjectName(projectName); err != nil {
		return nil, err
	}
	if targetDir == "" {
		return nil, web.ErrInvalidInput("Target directory is required")
	}
	fullPath := filepath.Join(targetDir, projectName)
	if dirExistsNonEmpty(fullPath) {
		return nil, web.ErrAlreadyExists("Target directory already exists and is not empty")
	}
	program, prefix := runnerFor(packageManager, true)
	if example == "" {
		example = "blank"
	}
	args := append(append([]string{}, prefix...), "hyperframes@latest", "init", projectName, "--example", example)
	if resolution != "" {
		args = append(args, "--resolution", resolution)
	}
	args = append(args, "--non-interactive")
	if err := runScaffold(program, args, targetDir); err != nil {
		return nil, err
	}
	return fullPath, nil
}

// ── install hyperframes skills ──

func installHyperframesSkills(agents []string) (any, error) {
	selected := []string{}
	for _, a := range agents {
		if t := strings.TrimSpace(a); t != "" {
			selected = append(selected, t)
		}
	}
	if len(selected) == 0 {
		return nil, web.ErrInvalidInput("No agents selected")
	}
	for _, a := range selected {
		if !containsStr(hyperframesSkillAgents, a) {
			return nil, web.ErrInvalidInput("Unsupported agent: " + a)
		}
	}
	for _, agent := range selected {
		cmd := exec.Command("npx", "--yes", "skills@latest", "add", "heygen-com/hyperframes", "--skill", "*", "-g", "-y", "--agent", agent)
		cmd.Env = append(os.Environ(), "GIT_CLONE_PROTECTION_ACTIVE=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			if execIsNotFound(err) {
				return nil, web.ErrDependencyMissing("npx is not installed. Please install Node.js first.")
			}
			return nil, web.ErrExternalCommand("Failed to run skills install for " + agent).WithDetail(strings.TrimSpace(string(out)))
		}
	}
	return nil, nil
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
