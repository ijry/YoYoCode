package handlers

// acp_pi_catalog.go ports acp_list_pi_model_capabilities: ask pi itself for its
// model catalog — the same executable and agent dir a session would use —
// without an ACP session, a prompt, or the network.
//
// We spawn pi in RPC mode against a private empty working directory, write one
// `get_available_models` request, and read the response line. The route is
// infallible: every failure mode is reported as a `status` (not_found /
// relative_path / failed / timed_out) so the panel can word its hint correctly
// ("pi does not list this model" is only true when pi actually answered).
//
// Wire note: `PiModelCapability` is camelCase on the wire
// (`thinkingLevelMap`), and `PiCatalogStatus` is snake_case
// (`ok`/`not_found`/`relative_path`/`failed`/`timed_out`).

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// piCatalogRequestID is the request id we send and match on. It is OUR id (pi
// echoes whatever it was given), so it carries this project's brand rather than
// the upstream one.
const piCatalogRequestID = "yoyocode-models"

// piCatalogDeadline bounds the whole query.
const piCatalogDeadline = 12 * time.Second

type piModelCapability struct {
	Provider  string `json:"provider"`
	ID        string `json:"id"`
	Reasoning bool   `json:"reasoning"`
	// ThinkingLevelMap is the per-level mapping pi reports; values are nullable.
	ThinkingLevelMap map[string]*string `json:"thinkingLevelMap"`
}

// piModelCatalog is pi's model catalog for the settings panel: Models is empty
// unless Status is "ok".
type piModelCatalog struct {
	Status string              `json:"status"`
	Models []piModelCapability `json:"models"`
}

func piUnavailable(status string) piModelCatalog {
	return piModelCatalog{Status: status, Models: []piModelCapability{}}
}

func registerACPPiCatalog(reg *Registry) {
	reg.Register("acp_list_pi_model_capabilities", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		return listPiModelCatalog(d), nil
	})
}

// listPiModelCatalog is the upstream list_pi_model_catalog_core: build the
// agent's runtime env, then query pi with it.
func listPiModelCatalog(d *db.Database) piModelCatalog {
	return queryPiModelCatalog(agentEnvJSON(d, "pi"), piCatalogDeadline)
}

// piCommandForRuntimeEnv is the command pi-acp spawns: its `PI_ACP_PI_COMMAND`
// as the child sees it (an exactly-empty value removes the variable), else our
// own env's value, else `pi`. Used verbatim, so it is not trimmed.
func piCommandForRuntimeEnv(runtimeEnv map[string]string) string {
	if v, ok := runtimeEnv["PI_ACP_PI_COMMAND"]; ok {
		if v == "" {
			return "pi"
		}
		return v
	}
	if v := os.Getenv("PI_ACP_PI_COMMAND"); v != "" {
		return v
	}
	return "pi"
}

// piCommandLooksLikePath reports whether a pi command is a path (spawned as-is)
// rather than a name looked up on PATH. `C:pi` is a drive-relative path on
// Windows, not a name.
func piCommandLooksLikePath(command string) bool {
	return strings.Contains(command, "/") ||
		strings.Contains(command, "\\") ||
		(len(command) > 1 && command[1] == ':') ||
		filepath.IsAbs(command)
}

// piAgentDirForRuntimeEnv is pi's coding-agent dir as the child sees it:
// `PI_CODING_AGENT_DIR` through pi's tilde rule (an exactly-empty value removes
// it, falling back to the default), else `~/.pi/agent`.
func piAgentDirForRuntimeEnv(runtimeEnv map[string]string) string {
	home := mcpHome()
	value := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR"))
	if v, ok := runtimeEnv["PI_CODING_AGENT_DIR"]; ok {
		value = v
	}
	if strings.TrimSpace(value) == "" {
		return filepath.Join(home, ".pi", "agent")
	}
	return expandHomePrefix(value, home)
}

// piRuntimePathsAreStable reports whether a relative command or agent dir would
// name a different file in every workspace — this query has no workspace, so
// such a configuration is refused rather than guessed at.
func piRuntimePathsAreStable(runtimeEnv map[string]string) bool {
	command := piCommandForRuntimeEnv(runtimeEnv)
	relativeCommand := piCommandLooksLikePath(command) && !filepath.IsAbs(command)
	return !relativeCommand && filepath.IsAbs(piAgentDirForRuntimeEnv(runtimeEnv))
}

// resolvePiCommandIn resolves the command pi-acp will spawn the way that spawn
// resolves it: a path against cwd (the child's working directory), a bare name
// on the child's PATH — env's when the launch sets one, else ours.
//
// A relative PATH entry (an empty one means "the cwd") is anchored in cwd here,
// because that is where the child looks.
func resolvePiCommandIn(command string, env map[string]string, cwd string) (string, bool) {
	if command == "" {
		return "", false
	}
	if piCommandLooksLikePath(command) {
		candidate := command
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(cwd, command)
		}
		if !piPathIsExecutable(candidate) {
			return "", false
		}
		if abs, err := filepath.Abs(candidate); err == nil {
			return abs, true
		}
		return candidate, true
	}
	pathVar, ok := env["PATH"]
	if !ok {
		pathVar = os.Getenv("PATH")
	}
	for _, dir := range filepath.SplitList(pathVar) {
		if dir == "" {
			dir = cwd
		} else if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		candidate := filepath.Join(dir, command)
		if piPathIsExecutable(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// queryPiModelCatalog asks pi for its catalog. Every failure maps to a status;
// it never errors.
func queryPiModelCatalog(runtimeEnv map[string]string, deadline time.Duration) piModelCatalog {
	if !piRuntimePathsAreStable(runtimeEnv) {
		return piUnavailable("relative_path")
	}
	// A private, empty working directory: pi runs where no repository or other
	// user put anything, and a relative PATH entry (an empty one means the cwd)
	// cannot reach a `pi` someone planted in the shared temp dir.
	queryDir, err := os.MkdirTemp("", "yoyocode-pi-catalog-")
	if err != nil {
		return piUnavailable("failed")
	}
	defer os.RemoveAll(queryDir)

	program, ok := resolvePiCommandIn(piCommandForRuntimeEnv(runtimeEnv), runtimeEnv, queryDir)
	if !ok {
		return piUnavailable("not_found")
	}

	cmd := exec.Command(program,
		"--mode", "rpc",
		// Without --offline, pi's RPC mode starts a background model refresh
		// that renews EXPIRED OAuth logins (rotating the refresh token), and it
		// exits on stdin EOF without waiting for that — so a query this short
		// could cut a token rotation off mid-flight. Offline, pi still lists the
		// same models from its registry and auth store.
		"--offline",
		"--no-session",
		"--no-extensions",
		"--no-skills",
		"--no-prompt-templates",
		"--no-context-files",
		"--no-approve",
	)
	cmd.Dir = queryDir
	cmd.Env = probeEnv(runtimeEnv)
	cmd.Stderr = nil // → the null device
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return piUnavailable("failed")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return piUnavailable("failed")
	}
	if err := cmd.Start(); err != nil {
		return piUnavailable("failed")
	}

	result := make(chan piModelCatalog, 1)
	go func() {
		if _, err := io.WriteString(stdin, `{"id":"`+piCatalogRequestID+`","type":"get_available_models"}`+"\n"); err != nil {
			result <- piUnavailable("failed")
			return
		}
		_ = stdin.Close()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			models, matched, ok := parsePiModelCapabilities(sc.Text())
			if !matched {
				continue
			}
			if !ok {
				result <- piUnavailable("failed")
				return
			}
			result <- piModelCatalog{Status: "ok", Models: models}
			return
		}
		result <- piUnavailable("failed")
	}()

	select {
	case res := <-result:
		reapPiProcess(cmd)
		return res
	case <-time.After(deadline):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		reapPiProcess(cmd)
		return piUnavailable("timed_out")
	}
}

// reapPiProcess waits for the child to exit, killing it if it does not within a
// second so a stuck pi cannot leak.
func reapPiProcess(cmd *exec.Cmd) {
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	}
}

// parsePiModelCapabilities reads one line of pi's RPC output. `matched=false`
// for anything that is not the answer to the catalog request (pi may emit other
// lines first); `matched=true, ok=false` for an answer that refuses or cannot be
// read.
func parsePiModelCapabilities(line string) (models []piModelCapability, matched bool, ok bool) {
	var response map[string]any
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		return nil, false, false
	}
	id, _ := response["id"].(string)
	typ, _ := response["type"].(string)
	command, _ := response["command"].(string)
	if id != piCatalogRequestID || typ != "response" || command != "get_available_models" {
		return nil, false, false
	}
	if success, _ := response["success"].(bool); !success {
		return nil, true, false
	}
	data, _ := response["data"].(map[string]any)
	raw, okRaw := data["models"]
	if !okRaw {
		return nil, true, false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, true, false
	}
	var out []piModelCapability
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, true, false
	}
	for i := range out {
		if out[i].ThinkingLevelMap == nil {
			out[i].ThinkingLevelMap = map[string]*string{}
		}
	}
	if out == nil {
		out = []piModelCapability{}
	}
	return out, true, true
}
