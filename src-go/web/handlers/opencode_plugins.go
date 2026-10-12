package handlers

// opencode_list_plugins (upstream acp/opencode_plugins.rs + commands/acp.rs
// opencode_list_plugins_core). Inspects ~/.config/opencode/opencode.json and
// ~/.cache/opencode to report which declared opencode plugins are installed,
// need migration from the pre-1.18 flat layout, are path plugins, or are
// missing. Original Go reimplementation of opencode's plugin discovery; the
// wire shape (PluginCheckSummary) mirrors upstream field-for-field.

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"yoyocode/src-go/web"
)

// RegisterOpencodePlugins wires opencode_list_plugins.
func RegisterOpencodePlugins(reg *Registry) {
	reg.Register("opencode_list_plugins", func(c *web.Context) (any, error) {
		// opencode_list_plugins_core passes no project root.
		summary, err := checkOpencodePlugins("")
		if err != "" {
			return nil, web.ErrTaskExecution(err)
		}
		return summary, nil
	})
}

func ocXDGConfigHome() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".config")
	}
	return ""
}

func ocXDGCacheHome() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return v
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".cache")
	}
	return ""
}

func ocConfigPath() string {
	base := ocXDGConfigHome()
	if base == "" {
		return ""
	}
	return filepath.Join(base, "opencode", "opencode.json")
}

func ocCacheDir() string {
	base := ocXDGCacheHome()
	if base == "" {
		return ""
	}
	return filepath.Join(base, "opencode")
}

func hasWindowsDrivePrefix(spec string) bool {
	b := []byte(spec)
	return len(b) >= 3 &&
		((b[0] >= 'A' && b[0] <= 'Z') || (b[0] >= 'a' && b[0] <= 'z')) &&
		b[1] == ':' && (b[2] == '/' || b[2] == '\\')
}

func isAbsoluteSpec(spec string) bool {
	if filepath.IsAbs(spec) || hasWindowsDrivePrefix(spec) {
		return true
	}
	return runtime.GOOS == "windows" && (strings.HasPrefix(spec, "\\") || strings.HasPrefix(spec, "/"))
}

func isPathSpec(spec string) bool {
	return strings.HasPrefix(spec, ".") ||
		strings.HasPrefix(spec, "/") ||
		strings.HasPrefix(spec, "file:") ||
		strings.Contains(spec, "://") ||
		isAbsoluteSpec(spec)
}

func lexicalJoin(base, relative string) string {
	out := base
	for _, comp := range strings.Split(filepath.ToSlash(relative), "/") {
		switch comp {
		case "", ".":
			// drop
		case "..":
			out = filepath.Dir(out)
		default:
			out = filepath.Join(out, comp)
		}
	}
	return out
}

func fileURLBodyToPath(body string) (string, bool) {
	var pathPart string
	switch {
	case hasWindowsDrivePrefix(body):
		pathPart = body
	case strings.HasPrefix(body, "localhost/"):
		pathPart = "/" + strings.TrimPrefix(body, "localhost/")
	case strings.HasPrefix(body, "/"):
		pathPart = body
	default:
		return "", false
	}
	decoded, err := url.PathUnescape(pathPart)
	if err != nil {
		return "", false
	}
	if rest, ok := strings.CutPrefix(decoded, "/"); ok && hasWindowsDrivePrefix(rest) {
		return rest, true
	}
	return decoded, true
}

func resolvePathPluginTarget(spec, relativeBase string) (string, bool) {
	if body, ok := strings.CutPrefix(spec, "file://"); ok {
		return fileURLBodyToPath(body)
	}
	if isAbsoluteSpec(spec) {
		return spec, true
	}
	if !strings.HasPrefix(spec, ".") {
		return "", false
	}
	if relativeBase == "" {
		return "", false
	}
	return lexicalJoin(relativeBase, spec), true
}

// parsePluginSpec returns (name, declaredSpec) or ("","",false).
func parsePluginSpec(spec string) (string, string, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", false
	}
	full := spec
	if isPathSpec(spec) {
		return full, full, true
	}
	if strings.HasPrefix(spec, "@") {
		withoutAt := spec[1:]
		slash := strings.Index(withoutAt, "/")
		if slash < 0 {
			return "", "", false
		}
		afterSlash := withoutAt[slash+1:]
		if versionAt := strings.Index(afterSlash, "@"); versionAt >= 0 {
			name := spec[:1+slash+1+versionAt]
			return name, full, true
		}
		return spec, full, true
	}
	if at := strings.Index(spec, "@"); at >= 0 {
		name := spec[:at]
		if name == "" {
			return "", "", false
		}
		return name, full, true
	}
	return spec, full, true
}

func effectiveSpec(declaredSpec, name string) string {
	if declaredSpec == name {
		return name + "@latest"
	}
	return declaredSpec
}

func sanitizeSpec(spec string) string {
	if runtime.GOOS != "windows" {
		return spec
	}
	return strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', ':', '"', '|', '?', '*':
			return '_'
		}
		if r < 32 {
			return '_'
		}
		return r
	}, spec)
}

func pluginPackageDir(cacheDir, effSpec string) string {
	return filepath.Join(cacheDir, "packages", sanitizeSpec(effSpec))
}

func modernPkgJSON(cacheDir, effSpec, name string) string {
	return filepath.Join(pluginPackageDir(cacheDir, effSpec), "node_modules", name, "package.json")
}

func legacyPkgJSON(cacheDir, name string) string {
	return filepath.Join(cacheDir, "node_modules", name, "package.json")
}

func readPkgVersion(pkgJSON string) any {
	content, err := os.ReadFile(pkgJSON)
	if err != nil {
		return nil
	}
	var doc map[string]any
	if json.Unmarshal(content, &doc) != nil {
		return nil
	}
	if v, ok := doc["version"].(string); ok {
		return v
	}
	return nil
}

func classifyPlugin(cacheDir, name, declaredSpec, relativeBase string) (status string, version any, resolved any) {
	if isPathSpec(declaredSpec) {
		target, ok := resolvePathPluginTarget(declaredSpec, relativeBase)
		if !ok {
			return "path", nil, nil
		}
		if fileExists(target) {
			return "path", nil, target
		}
		return "path_missing", nil, target
	}
	eff := effectiveSpec(declaredSpec, name)
	modern := modernPkgJSON(cacheDir, eff, name)
	legacy := legacyPkgJSON(cacheDir, name)
	if fileExists(modern) {
		return "installed", readPkgVersion(modern), nil
	}
	if fileExists(legacy) {
		return "needs_migration", readPkgVersion(legacy), nil
	}
	return "missing", nil, nil
}

func hasProjectOpencodeConfig(root string) bool {
	if root == "" {
		return false
	}
	for _, c := range []string{
		filepath.Join(root, "opencode.json"),
		filepath.Join(root, "opencode.jsonc"),
		filepath.Join(root, ".opencode", "opencode.json"),
		filepath.Join(root, ".opencode", "opencode.jsonc"),
	} {
		if fileExists(c) {
			return true
		}
	}
	return false
}

func checkOpencodePlugins(projectRoot string) (map[string]any, string) {
	configPath := ocConfigPath()
	if configPath == "" {
		return nil, "Cannot determine opencode config directory"
	}
	cacheDir := ocCacheDir()
	if cacheDir == "" {
		return nil, "Cannot determine opencode cache directory"
	}
	hasHint := hasProjectOpencodeConfig(projectRoot)

	empty := func() map[string]any {
		return map[string]any{
			"config_path":             configPath,
			"cache_dir":               cacheDir,
			"plugins":                 []any{},
			"has_project_config_hint": hasHint,
		}
	}

	if !fileExists(configPath) {
		return empty(), ""
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, "Failed to read " + configPath + ": " + err.Error()
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return nil, "Failed to parse " + configPath
	}
	pluginArr, ok := doc["plugin"].([]any)
	if !ok {
		return empty(), ""
	}

	seen := map[string]bool{}
	plugins := []map[string]any{}
	for _, item := range pluginArr {
		specStr, ok := item.(string)
		if !ok {
			continue
		}
		name, declared, ok := parsePluginSpec(specStr)
		if !ok {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		status, version, resolved := classifyPlugin(cacheDir, name, declared, projectRoot)
		plugins = append(plugins, map[string]any{
			"name":              name,
			"declared_spec":     declared,
			"installed_version": version,
			"status":            status,
			"resolved_path":     resolved,
		})
	}
	return map[string]any{
		"config_path":             configPath,
		"cache_dir":               cacheDir,
		"plugins":                 plugins,
		"has_project_config_hint": hasHint,
	}, ""
}
