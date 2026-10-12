package handlers

// acp_env_diagnostics (upstream commands/acp.rs: acp_env_diagnostics_core and
// its pure helpers). A read-only environment report: probes node/npm/npx + PATH,
// derives a one-line "likely cause" verdict, and renders a copyable text blob.
// The verdict/report/text machinery is a faithful port of the Rust pure
// functions; collect_diag_inputs probes the live environment. Node version-
// manager candidate dirs cover the common managers (nvm/fnm/volta/asdf/mise/n/
// homebrew/system).

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"yoyocode/src-go/acp"
	"yoyocode/src-go/web"
)

const (
	diagProbeTimeout = 5 * time.Second
	npmPrefixTimeout = 1500 * time.Millisecond
)

// diagSafeEnvKeys mirrors DIAG_SAFE_ENV_KEYS.
var diagSafeEnvKeys = []string{
	"SHELL", "NVM_DIR", "FNM_DIR", "FNM_MULTISHELL_PATH", "VOLTA_HOME",
	"ASDF_DATA_DIR", "MISE_DATA_DIR", "N_PREFIX", "HOMEBREW_PREFIX",
	"npm_config_prefix", "LANG",
}

// ── pure helpers ────────────────────────────────────────────────────────────

// redactSecret mirrors redact_secret: mask the values of secret-ish keys,
// multibyte-safe.
func redactSecret(key, value string) string {
	lower := strings.ToLower(key)
	secretish := false
	for _, needle := range []string{"key", "token", "secret", "password", "passwd", "auth", "credential"} {
		if strings.Contains(lower, needle) {
			secretish = true
			break
		}
	}
	if !secretish {
		return value
	}
	chars := []rune(value)
	n := len(chars)
	if n == 0 {
		return ""
	}
	if n <= 8 {
		return strings.Repeat("\u2022", n)
	}
	mid := n - 4
	if mid > 16 {
		mid = 16
	}
	return string(chars[:2]) + strings.Repeat("\u2022", mid) + string(chars[n-2:])
}

// parseNodeMajor mirrors parse_node_major.
func parseNodeMajor(v string) (int, bool) {
	s := strings.TrimPrefix(strings.TrimSpace(v), "v")
	first, _, _ := strings.Cut(s, ".")
	n, err := strconv.Atoi(first)
	if err != nil {
		return 0, false
	}
	return n, true
}

func diagExeName(cmd string) string {
	if runtime.GOOS == "windows" {
		return cmd + ".cmd"
	}
	return cmd
}

// ── input model ─────────────────────────────────────────────────────────────

type cmdProbe struct {
	path    string
	version string
}

type adapterProbe struct {
	nativeCmd       string
	nativePath      string
	nativeVersion   string
	sharedConfigDir string
}

type agentDiag struct {
	name            string
	cmd             string
	distribution    string
	pkg             string
	nodeRequired    string
	launchable      string
	resolveNpx      string
	systemPrefixBin string
	userPrefixBin   string
	homebrewBin     string
	detectedVersion string
	dbVersion       string
	adapter         *adapterProbe
}

type terminalProbe struct {
	ran         bool
	extraDirs   []string
	cmdResolved string
	note        string
}

type diagInputs struct {
	os              string
	arch            string
	appVersion      string
	appPath         []string
	pathLogs        []string
	node            cmdProbe
	npm             cmdProbe
	npx             cmdProbe
	npmPrefixG      string
	npmPrefixGMs    int64
	npmRootG        string
	npmConfigPrefix string
	cachedPrefix    string
	nodeCandidates  [][2]string // (dir, "1"/"0" has node)
	selectedNodeDir string
	safeEnv         [][2]string
	agent           *agentDiag
	terminal        terminalProbe
}

// ── probing ─────────────────────────────────────────────────────────────────

// diagRun runs a command with a timeout, returning its first non-empty stdout
// line.
func diagRun(program string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), diagProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, program, args...).Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

func diagCmdProbe(cmd string, versionArgs ...string) cmdProbe {
	p, err := exec.LookPath(cmd)
	if err != nil {
		return cmdProbe{}
	}
	return cmdProbe{path: p, version: diagRun(p, versionArgs...)}
}

// nodeBinDirCandidates mirrors node_bin_dir_candidates for the common version
// managers. Order matters: the first candidate holding a node binary is marked
// selected.
func nodeBinDirCandidates(home string) []string {
	out := []string{}
	add := func(p string) {
		if p != "" {
			out = append(out, p)
		}
	}
	envOr := func(key, rel string) string {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
		if home == "" {
			return ""
		}
		return filepath.Join(home, rel)
	}
	if runtime.GOOS != "windows" {
		// nvm: newest versions/node/<ver>/bin
		if nvm := envOr("NVM_DIR", ".nvm"); nvm != "" {
			versions := filepath.Join(nvm, "versions", "node")
			if entries, err := os.ReadDir(versions); err == nil {
				newest := ""
				var bestKey [3]int
				for _, e := range entries {
					if !e.IsDir() {
						continue
					}
					k := semverKey(e.Name())
					if newest == "" || less3(bestKey, k) {
						bestKey = k
						newest = e.Name()
					}
				}
				if newest != "" {
					add(filepath.Join(versions, newest, "bin"))
				}
			}
		}
		// fnm
		if fnm := envOr("FNM_MULTISHELL_PATH", ""); fnm != "" {
			add(filepath.Join(fnm, "bin"))
		}
		// volta
		if volta := envOr("VOLTA_HOME", ".volta"); volta != "" {
			add(filepath.Join(volta, "bin"))
		}
		// n
		if n := envOr("N_PREFIX", ""); n != "" {
			add(filepath.Join(n, "bin"))
		}
		// homebrew
		if hb := strings.TrimSpace(os.Getenv("HOMEBREW_PREFIX")); hb != "" {
			add(filepath.Join(hb, "bin"))
		} else if runtime.GOARCH == "arm64" {
			add("/opt/homebrew/bin")
		} else {
			add("/usr/local/bin")
		}
		add("/usr/bin")
	} else {
		// nvm-windows / system node
		if nvm := strings.TrimSpace(os.Getenv("NVM_SYMLINK")); nvm != "" {
			add(nvm)
		}
		if pf := strings.TrimSpace(os.Getenv("ProgramFiles")); pf != "" {
			add(filepath.Join(pf, "nodejs"))
		}
	}
	return out
}

func semverKey(name string) [3]int {
	s := strings.TrimPrefix(name, "v")
	parts := strings.Split(s, ".")
	var k [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		if n, err := strconv.Atoi(parts[i]); err == nil {
			k[i] = n
		}
	}
	return k
}

func less3(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func homeDirOrDefault() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

// collectDiagInputs gathers the environment report (base env always; agent +
// terminal probe when an agent is named).
func collectDiagInputs(c *web.Context, agentType string) diagInputs {
	inp := diagInputs{
		os:         runtime.GOOS,
		arch:       runtime.GOARCH,
		appVersion: appVersionString(),
	}
	if pathOS := os.Getenv("PATH"); pathOS != "" {
		inp.appPath = filepath.SplitList(pathOS)
	}
	inp.pathLogs = recentPathLogs(20)

	inp.node = diagCmdProbe("node", "-v")
	inp.npm = diagCmdProbe("npm", "-v")
	inp.npx = diagCmdProbe("npx", "-v")

	if npmPath, err := exec.LookPath("npm"); err == nil {
		start := time.Now()
		inp.npmPrefixG = diagRun(npmPath, "prefix", "-g")
		inp.npmPrefixGMs = time.Since(start).Milliseconds()
		inp.npmRootG = diagRun(npmPath, "root", "-g")
		inp.npmConfigPrefix = diagRun(npmPath, "config", "get", "prefix")
	}

	home := homeDirOrDefault()
	nodeBin := "node"
	if runtime.GOOS == "windows" {
		nodeBin = "node.exe"
	}
	for _, dir := range nodeBinDirCandidates(home) {
		has := fileExists(filepath.Join(dir, nodeBin))
		if has && inp.selectedNodeDir == "" {
			inp.selectedNodeDir = dir
		}
		flag := "0"
		if has {
			flag = "1"
		}
		inp.nodeCandidates = append(inp.nodeCandidates, [2]string{dir, flag})
	}

	for _, key := range diagSafeEnvKeys {
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			inp.safeEnv = append(inp.safeEnv, [2]string{key, redactSecret(key, val)})
		}
	}

	if agentType != "" {
		a := collectAgentDiag(c, agentType, inp.npmPrefixG)
		inp.terminal = diagTerminalProbe(a.cmd, inp.appPath)
		inp.agent = &a
	}
	return inp
}

// collectAgentDiag builds an AgentDiag from the registry launch spec + the DB
// installed_version, probing launchability and the known prefix bins.
func collectAgentDiag(c *web.Context, agentType, npmPrefixG string) agentDiag {
	a := agentDiag{cmd: agentType, distribution: "npx"}
	if info, ok := acp.FindBuiltinAgent(agentType); ok {
		a.name = info.Name
		a.distribution = info.Distribution
	}
	if spec, ok := acp.LaunchSpecFor(agentType); ok {
		a.distribution = spec.Kind
		a.pkg = spec.Package
		if spec.Cmd != "" {
			a.cmd = spec.Cmd
		}
	}
	if a.name == "" {
		a.name = agentType
	}

	// launchability: npx/binary → resolve the command on PATH; uvx → resolve uvx.
	exeName := diagExeName(a.cmd)
	if p, err := exec.LookPath(exeName); err == nil {
		a.launchable = p
		if a.distribution == "npx" {
			a.resolveNpx = p
		}
	} else if a.distribution == "uvx" {
		if p, err := exec.LookPath("uvx"); err == nil {
			a.launchable = p
		}
	}

	// prefix bins
	binName := diagExeName(a.cmd)
	if npmPrefixG != "" {
		cand := filepath.Join(npmPrefixG, "bin", binName)
		if runtime.GOOS == "windows" {
			cand = filepath.Join(npmPrefixG, binName)
		}
		if fileExists(cand) {
			a.systemPrefixBin = cand
		}
	}
	userPrefix := filepath.Join(homeDirOrDefault(), ".yoyocode", "npm-global", "bin", binName)
	if fileExists(userPrefix) {
		a.userPrefixBin = userPrefix
	}
	if runtime.GOOS == "darwin" {
		for _, hb := range []string{"/opt/homebrew/bin/" + binName, "/usr/local/bin/" + binName} {
			if fileExists(hb) {
				a.homebrewBin = hb
				break
			}
		}
	}

	if a.pkg != "" {
		a.detectedVersion = npmGlobalVersion(packageNameFromSpec(a.pkg))
	}
	if d := stateDB(c); d != nil && d.Conn != nil {
		var ver sql.NullString
		_ = d.Conn.QueryRow(`SELECT installed_version FROM agent_setting WHERE agent_type = ?`, agentType).Scan(&ver)
		if ver.Valid && strings.TrimSpace(ver.String) != "" {
			a.dbVersion = ver.String
		}
	}
	return a
}

// diagTerminalProbe compares the login-shell PATH against the app PATH (Unix
// only; Windows returns a note).
func diagTerminalProbe(cmd string, appPath []string) terminalProbe {
	if runtime.GOOS == "windows" {
		return terminalProbe{note: "login-shell probe is Unix-only"}
	}
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		return terminalProbe{note: "no $SHELL set"}
	}
	script := "command -v " + cmd + " 2>/dev/null; printf '\\037'; printf '%s' \"$PATH\""
	ctx, cancel := context.WithTimeout(context.Background(), diagProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, shell, "-lic", script).Output()
	if err != nil {
		return terminalProbe{note: "login-shell probe failed"}
	}
	resolved, pathPart, _ := strings.Cut(string(out), "\u001f")
	appSet := map[string]bool{}
	for _, d := range appPath {
		appSet[d] = true
	}
	extra := []string{}
	for _, d := range strings.Split(pathPart, ":") {
		d = strings.TrimSpace(d)
		if d != "" && !appSet[d] {
			extra = append(extra, d)
		}
	}
	return terminalProbe{
		ran:         true,
		cmdResolved: strings.TrimSpace(resolved),
		extraDirs:   extra,
	}
}

// appVersionString returns the app version (best-effort).
func appVersionString() string {
	if v := strings.TrimSpace(os.Getenv("YOYOCODE_APP_VERSION")); v != "" {
		return v
	}
	return "0.0.0-dev"
}

// recentPathLogs returns recent [PATH] log lines (Go has no in-process log ring
// yet, so this is empty — mirrors the field, not the source).
func recentPathLogs(limit int) []string { return []string{} }

// ── verdict + report ─────────────────────────────────────────────────────────

func diagCheck(label, value, status, hint string) map[string]any {
	m := map[string]any{"label": label, "value": value, "status": status}
	if hint != "" {
		m["hint"] = hint
	} else {
		m["hint"] = nil
	}
	return m
}

func diagVerdict(level, code, summary string) map[string]any {
	return map[string]any{"level": level, "code": code, "summary": summary}
}

// computeVerdict mirrors compute_verdict.
func computeVerdict(inp *diagInputs) map[string]any {
	agent := inp.agent
	if agent == nil {
		if inp.node.path == "" {
			return diagVerdict("fail", "node_missing", "Node.js was not found on the app's PATH.")
		}
		if inp.npm.path == "" {
			return diagVerdict("fail", "npm_missing", "npm was not found on the app's PATH.")
		}
		return diagVerdict("ok", "ok", "The base Node.js environment looks healthy.")
	}

	if agent.distribution != "npx" {
		if agent.launchable != "" {
			return diagVerdict("ok", "ok", "The agent is launchable — the environment looks healthy.")
		}
		if agent.dbVersion != "" {
			return diagVerdict("fail", "installed_but_unresolved", "The agent is recorded as installed, but the app cannot locate its runtime.")
		}
		return diagVerdict("info", "not_installed", "This agent does not appear to be installed.")
	}

	if inp.node.path == "" {
		return diagVerdict("fail", "node_missing", "Node.js was not found on the app's PATH.")
	}
	if inp.npm.path == "" {
		return diagVerdict("fail", "npm_missing", "npm was not found on the app's PATH.")
	}
	if agent.nodeRequired != "" && inp.node.version != "" {
		if rmaj, ok1 := parseNodeMajor(agent.nodeRequired); ok1 {
			if nmaj, ok2 := parseNodeMajor(inp.node.version); ok2 && nmaj < rmaj {
				return diagVerdict("fail", "node_too_old", "The active Node.js is older than this agent requires.")
			}
		}
	}

	if agent.resolveNpx == "" {
		if agent.dbVersion == "" && agent.detectedVersion == "" && inp.terminal.cmdResolved != "" {
			return diagVerdict("fail", "terminal_only_path", "The command resolves in your terminal but not in the app process — a GUI PATH gap.")
		}
		if agent.adapter != nil && agent.dbVersion == "" && agent.detectedVersion == "" {
			if agent.adapter.nativePath != "" {
				return diagVerdict("info", "adapter_missing_native_present", "Your own vendor CLI is installed, but YoYoCode launches a separate ACP adapter, which is not.")
			}
			return diagVerdict("info", "adapter_missing", "YoYoCode launches a separate ACP adapter for this agent, which is not installed.")
		}
		if agent.dbVersion != "" || agent.detectedVersion != "" {
			if agent.userPrefixBin != "" {
				return diagVerdict("fail", "user_prefix_not_on_path", "Installed into the EACCES fallback prefix (~/.yoyocode/npm-global), which is not on the app's PATH.")
			}
			if agent.homebrewBin != "" {
				return diagVerdict("fail", "homebrew_bin_not_on_path", "Installed under Homebrew's bin, which is not on the app's PATH (Apple Silicon keg split).")
			}
			if inp.terminal.cmdResolved != "" {
				return diagVerdict("fail", "terminal_only_path", "The command resolves in your terminal but not in the app process — a GUI PATH gap.")
			}
			if inp.npmPrefixGMs > npmPrefixTimeout.Milliseconds() {
				return diagVerdict("warn", "npm_prefix_timeout", "`npm prefix -g` exceeded the 1.5s probe timeout, so fallback resolution was skipped.")
			}
			return diagVerdict("fail", "installed_but_unresolved", "The agent is recorded as installed, but the app cannot locate its executable.")
		}
		return diagVerdict("info", "not_installed", "This agent does not appear to be installed.")
	}

	return diagVerdict("ok", "ok", "The agent's command resolves — the environment looks healthy.")
}

func cmdProbeValue(p cmdProbe) string {
	switch {
	case p.path != "" && p.version != "":
		return p.version + "  (" + p.path + ")"
	case p.path != "":
		return p.path
	default:
		return "NOT FOUND"
	}
}

func cmdProbeStatus(p cmdProbe) string {
	if p.path != "" {
		return "ok"
	}
	return "fail"
}

func strOrDef(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// buildReport mirrors build_report.
func buildReport(inp *diagInputs, generatedAt, agentType string) map[string]any {
	verdict := computeVerdict(inp)
	sections := []map[string]any{}

	// 1. Runtime
	runtimeChecks := []map[string]any{
		diagCheck("os / arch", inp.os+" / "+inp.arch, "info", ""),
		diagCheck("app version", inp.appVersion, "info", ""),
	}
	fixFailed := false
	for _, l := range inp.pathLogs {
		if strings.Contains(l, "fix_path_env failed") {
			fixFailed = true
			break
		}
	}
	fixVal, fixStatus := "no failure logged", "info"
	if fixFailed {
		fixVal, fixStatus = "failed at startup", "warn"
	}
	runtimeChecks = append(runtimeChecks, diagCheck("fix_path_env", fixVal, fixStatus, "app imports the login-shell PATH at startup; a failure leaves a narrow GUI PATH"))
	for _, kv := range inp.safeEnv {
		runtimeChecks = append(runtimeChecks, diagCheck(kv[0], kv[1], "info", ""))
	}
	runtimeChecks = append(runtimeChecks, diagCheck("PATH", itoaInt(len(inp.appPath))+" entries (full list in copied text)", "info", ""))
	sections = append(sections, map[string]any{"title": "Runtime", "checks": runtimeChecks})

	// 2. Node / npm
	sections = append(sections, map[string]any{"title": "Node / npm", "checks": []map[string]any{
		diagCheck("node", cmdProbeValue(inp.node), cmdProbeStatus(inp.node), ""),
		diagCheck("npm", cmdProbeValue(inp.npm), cmdProbeStatus(inp.npm), ""),
		diagCheck("npx", cmdProbeValue(inp.npx), cmdProbeStatus(inp.npx), ""),
	}})

	// 3. npm global prefix
	prefixSlow := inp.npmPrefixGMs > npmPrefixTimeout.Milliseconds()
	prefixStatus := "info"
	prefixHint := ""
	if prefixSlow {
		prefixStatus = "warn"
		prefixHint = "exceeds the 1.5s gate used at detection time"
	}
	sections = append(sections, map[string]any{"title": "npm global prefix", "checks": []map[string]any{
		diagCheck("npm prefix -g", strOrDef(inp.npmPrefixG, "N/A")+"  ("+itoa64(inp.npmPrefixGMs)+" ms)", prefixStatus, prefixHint),
		diagCheck("npm root -g", strOrDef(inp.npmRootG, "N/A"), "info", ""),
		diagCheck("npm config get prefix", strOrDef(inp.npmConfigPrefix, "N/A"), "info", ""),
		diagCheck("cached prefix", strOrDef(inp.cachedPrefix, "N/A"), "info", ""),
	}})

	// 4. Target agent
	if a := inp.agent; a != nil {
		launchLabel := a.cmd
		switch a.distribution {
		case "npx":
			launchLabel = a.cmd + " (resolve_npx_command)"
		case "binary":
			launchLabel = a.cmd + " (cached / system binary)"
		default:
			launchLabel = a.cmd + " (uvx launchable)"
		}
		launchStatus := "fail"
		if a.launchable != "" {
			launchStatus = "ok"
		}
		launchHint := ""
		if a.distribution == "npx" {
			launchHint = "this is exactly what the new-session page checks"
		}
		checks := []map[string]any{diagCheck(launchLabel, strOrDef(a.launchable, "NOT RESOLVED"), launchStatus, launchHint)}
		if a.pkg != "" {
			checks = append(checks, diagCheck("package", a.pkg, "info", ""))
		}
		if a.distribution == "npx" {
			sysStatus := "info"
			if a.systemPrefixBin != "" {
				sysStatus = "ok"
			}
			checks = append(checks, diagCheck("<npm prefix -g>/bin/<cmd>", strOrDef(a.systemPrefixBin, "absent"), sysStatus, ""))
			userStatus := "info"
			userHint := ""
			if a.userPrefixBin != "" {
				userStatus = "warn"
				userHint = "EACCES fallback dir — reached by the connect gate only if it's on PATH"
			}
			checks = append(checks, diagCheck("~/.yoyocode/npm-global/bin/<cmd>", strOrDef(a.userPrefixBin, "absent"), userStatus, userHint))
			if runtime.GOOS == "darwin" {
				hbStatus := "info"
				if a.homebrewBin != "" {
					hbStatus = "warn"
				}
				checks = append(checks, diagCheck("homebrew bin/<cmd>", strOrDef(a.homebrewBin, "absent"), hbStatus, ""))
			}
			checks = append(checks, diagCheck("detect_npm_global_version", strOrDef(a.detectedVersion, "none"), "info", "covers both prefixes — what the Settings badge uses"))
		}
		if ad := a.adapter; ad != nil {
			nativeVal := "not found"
			if ad.nativePath != "" && ad.nativeVersion != "" {
				nativeVal = ad.nativeVersion + "  (" + ad.nativePath + ")"
			} else if ad.nativePath != "" {
				nativeVal = ad.nativePath
			}
			checks = append(checks, diagCheck(ad.nativeCmd+" (your own CLI)", nativeVal, "info", "YoYoCode never launches this — it launches the ACP adapter above, a separate package that shares the same config dir"))
			checks = append(checks, diagCheck("shared config dir", ad.sharedConfigDir, "info", "read by BOTH your CLI and the adapter — no second login"))
		}
		checks = append(checks, diagCheck("DB installed_version", strOrDef(a.dbVersion, "none"), "info", ""))
		if a.nodeRequired != "" {
			ok := true
			if nmaj, ok1 := parseNodeMajor(inp.node.version); ok1 {
				if rmaj, ok2 := parseNodeMajor(a.nodeRequired); ok2 {
					ok = nmaj >= rmaj
				}
			}
			st := "ok"
			hint := ""
			if !ok {
				st = "fail"
				hint = "the active Node.js is older than required"
			}
			checks = append(checks, diagCheck("node_required", "\u2265 "+a.nodeRequired, st, hint))
		}
		sections = append(sections, map[string]any{"title": "Agent: " + a.name + " (" + a.distribution + ")", "checks": checks})
	}

	// 5. Node version-manager candidates
	if len(inp.nodeCandidates) > 0 {
		checks := []map[string]any{}
		for _, dc := range inp.nodeCandidates {
			dir, has := dc[0], dc[1] == "1"
			st := "info"
			if has {
				st = "ok"
			}
			hint := ""
			if inp.selectedNodeDir == dir {
				hint = "\u2190 selected (first with a node binary)"
			}
			checks = append(checks, diagCheck("candidate", dir, st, hint))
		}
		sections = append(sections, map[string]any{"title": "Node version-manager candidates", "checks": checks})
	}

	// 6. Terminal comparison
	var termChecks []map[string]any
	if inp.terminal.ran {
		extraVal := "none"
		extraStatus := "ok"
		extraHint := ""
		if len(inp.terminal.extraDirs) > 0 {
			extraVal = itoaInt(len(inp.terminal.extraDirs)) + " (see copied text)"
			extraStatus = "warn"
			extraHint = "the app can't see these dirs — the likely GUI PATH gap"
		}
		termChecks = []map[string]any{
			diagCheck("command -v <cmd> (login shell)", strOrDef(inp.terminal.cmdResolved, "not found"), "info", ""),
			diagCheck("PATH dirs in terminal but not app", extraVal, extraStatus, extraHint),
		}
	} else {
		termChecks = []map[string]any{diagCheck("login shell probe", strOrDef(inp.terminal.note, "did not run"), "info", "")}
	}
	sections = append(sections, map[string]any{"title": "Terminal comparison", "checks": termChecks})

	plainText := renderPlainText(inp, verdict, sections, generatedAt, agentType)

	var agentTypeVal any
	if agentType != "" {
		agentTypeVal = agentType
	}
	return map[string]any{
		"generated_at": generatedAt,
		"agent_type":   agentTypeVal,
		"verdict":      verdict,
		"sections":     sections,
		"plain_text":   plainText,
	}
}

func glyphFor(status string) string {
	switch status {
	case "ok":
		return "OK  "
	case "warn":
		return "WARN"
	case "fail":
		return "FAIL"
	default:
		return "--  "
	}
}

// renderPlainText mirrors render_plain_text.
func renderPlainText(inp *diagInputs, verdict map[string]any, sections []map[string]any, generatedAt, agentType string) string {
	var b strings.Builder
	b.WriteString("===== YoYoCode environment diagnostics =====\n")
	b.WriteString("generated: " + generatedAt + "\n")
	if agentType != "" {
		b.WriteString("agent: " + agentType + "\n")
	}
	b.WriteString("verdict [" + asString(verdict["code"]) + "]: " + asString(verdict["summary"]) + "\n")
	for _, sec := range sections {
		b.WriteString("\n## " + asString(sec["title"]) + "\n")
		checks, _ := sec["checks"].([]map[string]any)
		for _, c := range checks {
			b.WriteString("  [" + glyphFor(asString(c["status"])) + "] " + asString(c["label"]) + ": " + asString(c["value"]) + "\n")
			if h := asString(c["hint"]); h != "" {
				b.WriteString("        \u21b3 " + h + "\n")
			}
		}
	}
	b.WriteString("\n## PATH (app process, in order)\n")
	for _, d := range inp.appPath {
		b.WriteString("  " + d + "\n")
	}
	if len(inp.terminal.extraDirs) > 0 {
		b.WriteString("\n## PATH dirs in terminal but NOT in app\n")
		for _, d := range inp.terminal.extraDirs {
			b.WriteString("  " + d + "\n")
		}
	}
	if len(inp.pathLogs) > 0 {
		b.WriteString("\n## recent [PATH] logs\n")
		for _, l := range inp.pathLogs {
			b.WriteString("  " + l + "\n")
		}
	}
	b.WriteString("===== end =====\n")
	return b.String()
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// RegisterACPEnvDiagnostics wires acp_env_diagnostics.
func RegisterACPEnvDiagnostics(reg *Registry) {
	reg.Register("acp_env_diagnostics", func(c *web.Context) (any, error) {
		agentType := strings.TrimSpace(GetString(c, "agentType", "agent_type"))
		inp := collectDiagInputs(c, agentType)
		generatedAt := time.Now().Format("2006-01-02 15:04:05 -0700")
		return buildReport(&inp, generatedAt, agentType), nil
	})
}
