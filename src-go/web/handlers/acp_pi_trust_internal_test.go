package handlers

// acp_pi_trust_internal_test.go covers the pi trust helpers: resource discovery,
// the nearest-ancestor decision walk, the trust.json read-modify-write (incl.
// the never-clobber-unparseable rule), the acknowledgement store, the
// proper-lockfile-style lock, and the verbatim-path simplification.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPiProjectTrustResourcesWithHome(t *testing.T) {
	ws := t.TempDir()
	mk := func(rel string) {
		p := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk(".pi/extensions")
	mk(".pi/themes")
	mk(".agents/skills")
	if err := os.WriteFile(filepath.Join(ws, ".pi", "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	res := piProjectTrustResourcesWithHome(ws, home)
	byKind := map[string]piProjectResource{}
	for _, r := range res {
		byKind[r.Kind] = r
	}
	// The four workspace-local resources (the walk may also surface an ancestor
	// `.agents/skills`, which is a property of where the temp dir lives).
	for _, kind := range []string{".pi/extensions", ".pi/settings.json", ".pi/themes"} {
		if _, ok := byKind[kind]; !ok {
			t.Fatalf("missing %s in %+v", kind, res)
		}
	}
	wantSkills := filepath.Join(piCanonicalPath(ws), ".agents", "skills")
	var wsSkills *piProjectResource
	for i := range res {
		if res[i].Path == wantSkills {
			wsSkills = &res[i]
		}
	}
	if wsSkills == nil {
		t.Fatalf("workspace .agents/skills missing in %+v", res)
	}
	if wsSkills.ExecutesCode {
		t.Errorf("workspace .agents/skills = %+v", *wsSkills)
	}
	if !byKind[".pi/extensions"].ExecutesCode {
		t.Error(".pi/extensions must be flagged as executing code")
	}
	if !byKind[".pi/settings.json"].ExecutesCode {
		t.Error(".pi/settings.json must be flagged as executing code")
	}
	if byKind[".pi/themes"].ExecutesCode {
		t.Error(".pi/themes must not be flagged as executing code")
	}

	// The user's OWN <home>/.agents/skills is excluded, even when it is an
	// ancestor of the workspace.
	homeWS := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeWS, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(homeWS, "proj")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	excluded := filepath.Join(piCanonicalPath(homeWS), ".agents", "skills")
	for _, r := range piProjectTrustResourcesWithHome(inner, homeWS) {
		if r.Path == excluded {
			t.Fatalf("the user-scope home .agents/skills must be excluded, got %+v", r)
		}
	}

	// An empty workspace contributes nothing of its own.
	empty := t.TempDir()
	emptyRoot := piCanonicalPath(empty)
	for _, r := range piProjectTrustResourcesWithHome(empty, home) {
		if strings.HasPrefix(r.Path, emptyRoot) {
			t.Fatalf("empty workspace contributed %+v", r)
		}
	}
}

func TestPiNearestTrustDecisionWalksAncestors(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	trustFile := filepath.Join(t.TempDir(), "trust.json")
	rootKey := piCanonicalPath(root)
	childKey := piCanonicalPath(child)
	if err := os.WriteFile(trustFile, []byte(`{"`+escapeJSON(rootKey)+`":true,"`+escapeJSON(childKey)+`":null}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// The child's own entry is null → the walk continues to the ancestor.
	path, verdict, found := piNearestTrustDecision(trustFile, child)
	if !found || !verdict || path != rootKey {
		t.Fatalf("got (%q, %v, %v), want (%q, true, true)", path, verdict, found, rootKey)
	}

	// Nothing decides an unrelated dir.
	if _, _, found := piNearestTrustDecision(trustFile, t.TempDir()); found {
		t.Fatal("unrelated dir must not find a decision")
	}
	// A missing file decides nothing.
	if _, _, found := piNearestTrustDecision(filepath.Join(t.TempDir(), "absent.json"), child); found {
		t.Fatal("missing trust file must not decide")
	}
}

func TestPiWriteTrustDecisionRoundTrip(t *testing.T) {
	ws := t.TempDir()
	trustFile := filepath.Join(t.TempDir(), "trust.json")
	key := piCanonicalPath(ws)

	yes := true
	if err := piWriteTrustDecisionAt(trustFile, ws, &yes); err != nil {
		t.Fatal(err)
	}
	entries := piTrustEntriesAt(trustFile)
	if len(entries) != 1 || entries[0].Path != key || !entries[0].Trusted {
		t.Fatalf("entries = %+v", entries)
	}
	// No lock directory left behind.
	if _, err := os.Stat(trustFile + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("lock dir should be released, err = %v", err)
	}

	// A second, unrelated entry survives the next write.
	other := t.TempDir()
	if err := piWriteTrustDecisionAt(trustFile, other, &yes); err != nil {
		t.Fatal(err)
	}
	if got := piTrustEntriesAt(trustFile); len(got) != 2 {
		t.Fatalf("entries = %+v", got)
	}

	// nil revokes just this workspace.
	if err := piWriteTrustDecisionAt(trustFile, ws, nil); err != nil {
		t.Fatal(err)
	}
	got := piTrustEntriesAt(trustFile)
	if len(got) != 1 || got[0].Path == key {
		t.Fatalf("entries = %+v", got)
	}

	// Revoking an absent entry is a no-op (and creates no file).
	fresh := filepath.Join(t.TempDir(), "trust.json")
	if err := piWriteTrustDecisionAt(fresh, ws, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatalf("revoking an absent entry must not create the file, err = %v", err)
	}
}

func TestPiWriteTrustDecisionNeverClobbersUnparseable(t *testing.T) {
	trustFile := filepath.Join(t.TempDir(), "trust.json")
	if err := os.WriteFile(trustFile, []byte("this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	yes := true
	err := piWriteTrustDecisionAt(trustFile, t.TempDir(), &yes)
	if err == nil || !strings.Contains(err.Error(), "not a JSON object") {
		t.Fatalf("err = %v", err)
	}
	b, _ := os.ReadFile(trustFile)
	if string(b) != "this is not json" {
		t.Fatalf("the unparseable file must be left alone, got %q", string(b))
	}
	// A JSON array is not an object either.
	if err := os.WriteFile(trustFile, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := piWriteTrustDecisionAt(trustFile, t.TempDir(), &yes); err == nil {
		t.Fatal("a JSON array must be refused")
	}
}

func TestPiTrustEntriesFiltersAndSorts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	doc := `{"/z":true,"/a":false,"/n":null,"/s":"x","/b":true}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	got := piTrustEntriesAt(path)
	if len(got) != 3 {
		t.Fatalf("entries = %+v", got)
	}
	want := []struct {
		p string
		t bool
	}{{"/a", false}, {"/b", true}, {"/z", true}}
	for i, w := range want {
		if got[i].Path != w.p || got[i].Trusted != w.t {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], w)
		}
	}
	if piTrustEntriesAt(filepath.Join(t.TempDir(), "absent.json")) == nil {
		t.Fatal("a missing file must yield a non-nil empty slice")
	}
}

func TestPiSetTrustAcknowledgedAt(t *testing.T) {
	ackFile := filepath.Join(t.TempDir(), "ack.json")
	ws := t.TempDir()
	key := piCanonicalPath(ws)

	if err := piSetTrustAcknowledgedAt(ackFile, ws, true); err != nil {
		t.Fatal(err)
	}
	if !piTrustIsAcknowledgedAt(ackFile, key) {
		t.Fatal("should be acknowledged")
	}
	// Clearing removes it.
	if err := piSetTrustAcknowledgedAt(ackFile, ws, false); err != nil {
		t.Fatal(err)
	}
	if piTrustIsAcknowledgedAt(ackFile, key) {
		t.Fatal("should no longer be acknowledged")
	}
	// Clearing an absent entry creates nothing.
	fresh := filepath.Join(t.TempDir(), "ack2.json")
	if err := piSetTrustAcknowledgedAt(fresh, ws, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatalf("clearing an absent record must not create the file, err = %v", err)
	}
}

func TestPiTrustLockStaleAndContention(t *testing.T) {
	trustFile := filepath.Join(t.TempDir(), "trust.json")
	lock := trustFile + ".lock"

	// A fresh lock is not stale and blocks acquisition.
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	if piTrustLockIsStale(lock) {
		t.Fatal("a fresh lock must not be stale")
	}
	if _, err := acquirePiTrustLock(trustFile); err == nil {
		t.Fatal("a held lock must block acquisition")
	} else if !strings.Contains(err.Error(), "locked by another process") {
		t.Fatalf("err = %v", err)
	}

	// An old lock is stale and gets taken over.
	old := time.Now().Add(-30 * time.Second)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if !piTrustLockIsStale(lock) {
		t.Fatal("an old lock must be stale")
	}
	release, err := acquirePiTrustLock(trustFile)
	if err != nil {
		t.Fatalf("stale lock should be taken over: %v", err)
	}
	release()
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("release should remove the lock dir, err = %v", err)
	}
	// A missing lock dir counts as stale (unreadable metadata must not wedge).
	if !piTrustLockIsStale(filepath.Join(t.TempDir(), "nope.lock")) {
		t.Fatal("a missing lock must count as stale")
	}
}

func TestSimplifyVerbatimPath(t *testing.T) {
	if got := simplifyVerbatimPath(`\\?\C:\x\y`); got != `C:\x\y` {
		t.Fatalf("drive = %q", got)
	}
	if got := simplifyVerbatimPath(`\\?\unc\server\share`); got != `\\server\share` {
		t.Fatalf("unc = %q", got)
	}
	if got := simplifyVerbatimPath(`\\?\UNC\server\share`); got != `\\server\share` {
		t.Fatalf("upper unc = %q", got)
	}
	if got := simplifyVerbatimPath(`C:\plain`); got != `C:\plain` {
		t.Fatalf("plain = %q", got)
	}
	// A verbatim path with no drive letter is left alone (Node never emits it).
	if got := simplifyVerbatimPath(`\\?\Volume{x}\y`); got != `\\?\Volume{x}\y` {
		t.Fatalf("non-drive = %q", got)
	}
}

func TestPiProjectTrustStateAtEndToEnd(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".pi", "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	trustFile := filepath.Join(t.TempDir(), "trust.json")
	ackFile := filepath.Join(t.TempDir(), "ack.json")
	key := piCanonicalPath(ws)
	if err := os.WriteFile(trustFile, []byte(`{"`+escapeJSON(key)+`":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := piSetTrustAcknowledgedAt(ackFile, ws, true); err != nil {
		t.Fatal(err)
	}

	state := piProjectTrustStateAt(trustFile, ackFile, ws)
	if state.Workspace != key {
		t.Fatalf("workspace = %q, want %q", state.Workspace, key)
	}
	if state.Decision == nil || !*state.Decision {
		t.Fatalf("decision = %v", state.Decision)
	}
	if state.DecidedAt == nil || *state.DecidedAt != key {
		t.Fatalf("decidedAt = %v", state.DecidedAt)
	}
	if !state.Acknowledged {
		t.Fatal("should be acknowledged")
	}
	if len(state.Resources) != 1 || state.Resources[0].Kind != ".pi/extensions" {
		t.Fatalf("resources = %+v", state.Resources)
	}
	if state.TrustFile != trustFile {
		t.Fatalf("trustFile = %q", state.TrustFile)
	}

	// An undecided workspace reports a null decision but keeps its resources.
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, ".pi", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	state = piProjectTrustStateAt(trustFile, ackFile, other)
	if state.Decision != nil || state.DecidedAt != nil {
		t.Fatalf("undecided: decision=%v decidedAt=%v", state.Decision, state.DecidedAt)
	}
	if state.Acknowledged {
		t.Fatal("undecided must not be acknowledged")
	}
	if len(state.Resources) != 1 || state.Resources[0].Kind != ".pi/skills" {
		t.Fatalf("resources = %+v", state.Resources)
	}
}

func TestPiAgentDirInWorkspaceAnchorsRelative(t *testing.T) {
	ws := t.TempDir()
	// An absolute override is used as-is.
	abs := filepath.Join(t.TempDir(), "agent")
	if got := piAgentDirInWorkspace(map[string]string{"PI_CODING_AGENT_DIR": abs}, ws); got != abs {
		t.Fatalf("absolute = %q", got)
	}
	// A relative one is anchored in the workspace.
	got := piAgentDirInWorkspace(map[string]string{"PI_CODING_AGENT_DIR": filepath.Join("rel", "agent")}, ws)
	if got != filepath.Join(ws, "rel", "agent") {
		t.Fatalf("relative = %q", got)
	}
}

func TestPiProjectTrustStateWireShapeIsCamelCase(t *testing.T) {
	state := piProjectTrustState{
		Workspace: "/w",
		Resources: []piProjectResource{{Path: "/p", Kind: "k", ExecutesCode: true}},
		TrustFile: "/t",
	}
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"workspace"`, `"resources"`, `"decision"`, `"decidedAt"`, `"trustFile"`, `"acknowledged"`, `"executesCode"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
	if strings.Contains(s, "executes_code") || strings.Contains(s, "trust_file") {
		t.Fatalf("wire must be camelCase: %s", s)
	}
}

func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
