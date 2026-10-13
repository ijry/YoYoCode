package handlers

// acp_pi_trust.go ports the pi project-trust commands:
//
//   - acp_pi_project_trust_state — the approval UI's effective state for one
//     workspace (repo-shipped resources + the verdict in force + who decided).
//   - acp_pi_set_project_trust — the ONLY place we write into pi's trust.json,
//     and only from a user action.
//   - acp_pi_acknowledge_project_trust — records in OUR OWN store that the user
//     has seen and kept an existing grant (pi's file is untouched).
//   - acp_pi_list_trust_entries — every decision in pi's trust.json, for review.
//
// pi keys `trust.json` by the directory's realpath and guards the file with a
// `proper-lockfile` DIRECTORY lock (`<trust.json>.lock`, created with mkdir);
// honoring that protocol is what keeps a decision written here from interleaving
// with one the user makes inside pi.
//
// Wire note: all four types are camelCase (`executesCode`, `decidedAt`,
// `trustFile`).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// piTrustRequiringConfigResources mirrors pi's own
// TRUST_REQUIRING_PROJECT_CONFIG_RESOURCES (core/trust-manager.js). It fails
// safe in both directions: under-detecting only means we stay quiet about
// resources pi would ignore anyway (its non-interactive default is "don't
// load"), and over-detecting only costs one extra prompt.
var piTrustRequiringConfigResources = []string{
	"settings.json",
	"extensions",
	"skills",
	"prompts",
	"themes",
	"SYSTEM.md",
	"APPEND_SYSTEM.md",
}

// piTrustLockStale is how long a lock may sit untouched before it counts as
// abandoned by a crashed process. Mirrors proper-lockfile's default `stale`
// window so we and pi agree on when a leftover lock stops holding everyone up.
const piTrustLockStale = 10 * time.Second

// piTrustWriteLock serializes OUR trust writes. Two surfaces can issue them at
// once (the approval banner and the settings list's revoke buttons) and the
// write is a read-modify-write, so without this one call could drop the entry
// another just added. The cross-process lock does not cover this: both callers
// live in one process.
var piTrustWriteLock sync.Mutex

type piProjectResource struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	ExecutesCode bool   `json:"executesCode"`
}

type piProjectTrustState struct {
	Workspace    string              `json:"workspace"`
	Resources    []piProjectResource `json:"resources"`
	Decision     *bool               `json:"decision"`
	DecidedAt    *string             `json:"decidedAt"`
	TrustFile    string              `json:"trustFile"`
	Acknowledged bool                `json:"acknowledged"`
}

type piTrustEntry struct {
	Path    string `json:"path"`
	Trusted bool   `json:"trusted"`
}

func registerACPPiTrust(reg *Registry) {
	reg.Register("acp_pi_project_trust_state", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		workspace := strVal(c.Body["workspace"])
		state, err := piProjectTrustStateFor(d, catalogDataDir(c), workspace)
		if err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		return state, nil
	})

	reg.Register("acp_pi_set_project_trust", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		workspace := strVal(c.Body["workspace"])
		var trusted *bool
		if v, ok := c.Body["trusted"]; ok && v != nil {
			if b, ok := v.(bool); ok {
				trusted = &b
			}
		}
		if err := piSetProjectTrust(d, catalogDataDir(c), workspace, trusted); err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		return nil, nil
	})

	reg.Register("acp_pi_acknowledge_project_trust", func(c *web.Context) (any, error) {
		workspace := strVal(c.Body["workspace"])
		if err := piSetTrustAcknowledgedAt(piTrustAckPath(catalogDataDir(c)), workspace, true); err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		return nil, nil
	})

	reg.Register("acp_pi_list_trust_entries", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		dir, err := piSettingsDirFromDB(d)
		if err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		return piTrustEntriesAt(filepath.Join(dir, "trust.json")), nil
	})
}

// ─── state ───────────────────────────────────────────────────────────────────

func piProjectTrustStateFor(d *db.Database, dataDir, workspace string) (piProjectTrustState, error) {
	env := agentEnvJSON(d, "pi")
	trustFile := filepath.Join(piAgentDirInWorkspace(env, workspace), "trust.json")
	return piProjectTrustStateAt(trustFile, piTrustAckPath(dataDir), workspace), nil
}

// piProjectTrustStateAt builds the effective state for cwd against explicit
// files (split out so it is directly unit-testable against a tempdir).
func piProjectTrustStateAt(trustFile, ackFile, cwd string) piProjectTrustState {
	decidedPath, verdict, decided := piNearestTrustDecision(trustFile, cwd)
	workspace := piCanonicalPath(cwd)
	state := piProjectTrustState{
		Workspace:    workspace,
		Resources:    piProjectTrustResourcesWithHome(cwd, mcpHome()),
		TrustFile:    trustFile,
		Acknowledged: piTrustIsAcknowledgedAt(ackFile, workspace),
	}
	if decided {
		v := verdict
		p := decidedPath
		state.Decision = &v
		state.DecidedAt = &p
	}
	return state
}

// piNearestTrustDecision mirrors pi's findNearestTrustEntry: walk cwd upward and
// take the first entry whose value is a real boolean. A `null` entry does NOT
// stop the walk (pi only accepts true/false), which is how "clear this folder's
// decision and defer to an ancestor" works.
func piNearestTrustDecision(trustFile, cwd string) (path string, verdict bool, found bool) {
	obj := readJSONObjectOrEmpty(trustFile)
	if len(obj) == 0 {
		return "", false, false
	}
	current := piCanonicalPath(cwd)
	for {
		if v, ok := obj[current]; ok {
			if b, isBool := v.(bool); isBool {
				return current, b, true
			}
		}
		parent := filepath.Dir(current)
		if parent == current || parent == "." {
			return "", false, false
		}
		current = parent
	}
}

// piProjectTrustResourcesWithHome lists the repo-shipped pi resources in cwd
// that pi will only load once the workspace is trusted. Empty ⇒ pi trusts the
// project trivially, so there is nothing to ask the user about.
//
// Mirrors pi's hasTrustRequiringProjectResources, but collects every match so
// the approval UI can name the files.
func piProjectTrustResourcesWithHome(cwd, home string) []piProjectResource {
	found := []piProjectResource{}
	start := piCanonicalPath(cwd)

	configDir := filepath.Join(start, ".pi")
	for _, entry := range piTrustRequiringConfigResources {
		path := filepath.Join(configDir, entry)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		found = append(found, piProjectResource{
			Path: path,
			Kind: ".pi/" + entry,
			// Extensions are modules pi imports (top-level code runs); project
			// settings can pull in and install project packages.
			ExecutesCode: entry == "extensions" || entry == "settings.json",
		})
	}

	// pi also honors `.agents/skills` from cwd upward, EXCEPT the user's own
	// `~/.agents/skills` (that is a user-scope store, not a repo's).
	userAgentsSkills := filepath.Join(piCanonicalPath(home), ".agents", "skills")
	current := start
	for {
		candidate := filepath.Join(current, ".agents", "skills")
		if candidate != userAgentsSkills {
			if _, err := os.Stat(candidate); err == nil {
				found = append(found, piProjectResource{
					Path: candidate, Kind: ".agents/skills", ExecutesCode: false,
				})
			}
		}
		parent := filepath.Dir(current)
		if parent == current || parent == "." {
			break
		}
		current = parent
	}
	return found
}

// ─── acknowledgement store (our own file, never pi's) ────────────────────────

// piTrustAckPath is our own record of which trusted workspaces the user has
// confirmed. Deliberately NOT stored in pi's trust.json: that file is pi's, has
// no field for this, and its entries carry no provenance.
func piTrustAckPath(dataDir string) string {
	return filepath.Join(dataDir, "pi-project-trust-ack.json")
}

func piTrustIsAcknowledgedAt(ackFile, workspace string) bool {
	v, ok := readJSONObjectOrEmpty(ackFile)[workspace]
	if !ok {
		return false
	}
	b, isBool := v.(bool)
	return isBool && b
}

// piSetTrustAcknowledgedAt records (or clears) the user's confirmation that cwd
// is trusted. Clearing matters: after a revoke, a later grant must be disclosed
// again rather than ride on a stale "already seen".
func piSetTrustAcknowledgedAt(ackFile, cwd string, acknowledged bool) error {
	key := piCanonicalPath(cwd)
	obj := readJSONObjectOrEmpty(ackFile)
	if acknowledged {
		obj[key] = true
	} else if _, ok := obj[key]; !ok {
		// Nothing recorded → don't create a file just to record an absence.
		return nil
	} else {
		delete(obj, key)
	}
	return writeJSONObjectPretty(ackFile, obj)
}

// ─── trust.json read/write ───────────────────────────────────────────────────

// piSetProjectTrust records an explicit decision for workspace in pi's
// trust.json. `trusted == nil` removes our entry so the folder falls back to any
// ancestor decision, then to pi's own default (the revoke path).
func piSetProjectTrust(d *db.Database, dataDir, workspace string, trusted *bool) error {
	env := agentEnvJSON(d, "pi")
	path := filepath.Join(piAgentDirInWorkspace(env, workspace), "trust.json")
	if err := piWriteTrustDecisionAt(path, workspace, trusted); err != nil {
		return err
	}
	// Any verdict here IS the user answering, so a granted folder is
	// acknowledged by construction and must not re-block the launch. A declined
	// or revoked one drops the record, so a future grant gets disclosed again.
	granted := trusted != nil && *trusted
	return piSetTrustAcknowledgedAt(piTrustAckPath(dataDir), workspace, granted)
}

// piWriteTrustDecisionAt writes one decision into path.
func piWriteTrustDecisionAt(path, cwd string, trusted *bool) error {
	key := piCanonicalPath(cwd)

	piTrustWriteLock.Lock()
	defer piTrustWriteLock.Unlock()

	unlock, err := acquirePiTrustLock(path)
	if err != nil {
		return err
	}
	defer unlock()

	// Strict read: a missing file is fine (we create one), but a file that exists
	// and doesn't parse as a JSON object must NOT be rewritten from scratch.
	obj := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		var parsed any
		if err := json.Unmarshal(b, &parsed); err != nil {
			return fmt.Errorf("pi's trust file at %s is not a JSON object; fix or remove it before changing project trust", path)
		}
		m, ok := parsed.(map[string]any)
		if !ok {
			return fmt.Errorf("pi's trust file at %s is not a JSON object; fix or remove it before changing project trust", path)
		}
		obj = m
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s failed: %v", path, err)
	}

	if trusted != nil {
		obj[key] = *trusted
	} else if _, ok := obj[key]; !ok {
		// Nothing of ours to clear (the decision came from an ancestor); don't
		// create a file just to record an absence.
		return nil
	} else {
		delete(obj, key)
	}
	return writeJSONObjectPretty(path, obj)
}

// piTrustLockIsStale reports whether a lock directory is old enough that whoever
// made it is presumed gone. Unreadable metadata counts as stale: a lock we can't
// reason about must not wedge project-trust decisions forever.
func piTrustLockIsStale(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return true
	}
	return time.Since(fi.ModTime()) > piTrustLockStale
}

// acquirePiTrustLock takes pi's cross-process trust lock, retrying on contention
// the way pi does (10 attempts, 20ms apart) before giving up. Returns a release
// func.
func acquirePiTrustLock(trustFile string) (func(), error) {
	path := trustFile + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create pi agent directory failed: %v", err)
	}
	for attempt := 1; attempt <= 10; attempt++ {
		err := os.Mkdir(path, 0o755)
		if err == nil {
			return func() { _ = os.Remove(path) }, nil
		}
		if os.IsExist(err) {
			if piTrustLockIsStale(path) {
				// Same tie-break proper-lockfile uses, so a lock orphaned by a
				// crash doesn't block every future decision.
				_ = os.Remove(path)
				continue
			}
			if attempt < 10 {
				time.Sleep(20 * time.Millisecond)
			}
			continue
		}
		return nil, fmt.Errorf("lock %s failed: %v", path, err)
	}
	return nil, fmt.Errorf("pi's trust store at %s is locked by another process; try again in a moment", trustFile)
}

// piTrustEntriesAt reads decisions out of path for the settings-page review
// list. `null` means "no decision" in pi; nothing to review or revoke.
func piTrustEntriesAt(path string) []piTrustEntry {
	obj := readJSONObjectOrEmpty(path)
	entries := make([]piTrustEntry, 0, len(obj))
	for p, v := range obj {
		if b, ok := v.(bool); ok {
			entries = append(entries, piTrustEntry{Path: p, Trusted: b})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

// ─── shared pi path helpers ──────────────────────────────────────────────────

// piSettingsDirFromDB is pi's coding-agent dir as the child sees it, refused
// when it is not absolute (a relative dir names a different directory in every
// workspace).
func piSettingsDirFromDB(d *db.Database) (string, error) {
	dir := piAgentDirForRuntimeEnv(agentEnvJSON(d, "pi"))
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("pi agent directory must be absolute or start with ~/ before editing native settings (this agent's pi uses %q, relative to each workspace)", dir)
	}
	return dir, nil
}

// piAgentDirInWorkspace anchors a relative pi agent dir where pi anchors it:
// pi-acp starts pi with the session's workspace as its cwd, so a relative
// PI_CODING_AGENT_DIR names a directory inside THAT workspace.
func piAgentDirInWorkspace(runtimeEnv map[string]string, workspace string) string {
	dir := piAgentDirForRuntimeEnv(runtimeEnv)
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(workspace, dir)
}

// piCanonicalPath mirrors Node's realpathSync: absolute + symlinks resolved,
// with Windows' verbatim (`\\?\`) prefix dropped (Node never emits that form, so
// leaving it on would produce keys pi can't match). Falls back to the path as
// given when it can't be resolved, which keeps this usable for display.
func piCanonicalPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			return simplifyVerbatimPath(resolved)
		}
	}
	return simplifyVerbatimPath(path)
}

func simplifyVerbatimPath(path string) string {
	if rest, ok := stripPrefixFold(path, `\\?\UNC\`); ok {
		return `\\` + rest
	}
	if rest, ok := strings.CutPrefix(path, `\\?\`); ok {
		if len(rest) >= 2 && isASCIIAlpha(rest[0]) && rest[1] == ':' {
			return rest
		}
	}
	return path
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// stripPrefixFold is strings.CutPrefix with an ASCII-case-insensitive prefix
// comparison (Windows accepts `\\?\unc\` as readily as `\\?\UNC\`).
func stripPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) {
		return s, false
	}
	if !strings.EqualFold(s[:len(prefix)], prefix) {
		return s, false
	}
	return s[len(prefix):], true
}
