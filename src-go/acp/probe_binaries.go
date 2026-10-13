package acp

// probe_binaries.go resolves the vendor CLI an agent probe would actually
// launch: the managed binary cache first, then the user's own install (PATH /
// ~/.local/bin) — the same order `ResolveSpawn` uses.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// FindBestCachedBinary returns the highest-versioned cached binary for a binary
// agent under cacheRoot, plus the version label. `false` when the agent has no
// binary recipe on this platform or nothing is cached.
func FindBestCachedBinary(cacheRoot, agentType string) (string, string, bool) {
	if strings.TrimSpace(cacheRoot) == "" {
		return "", "", false
	}
	if _, ok := BinaryDistFor(agentType); !ok {
		return "", "", false
	}
	entries, err := os.ReadDir(filepath.Join(cacheRoot, "agent-binaries", agentType))
	if err != nil {
		return "", "", false
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	if len(versions) == 0 {
		return "", "", false
	}
	sort.Slice(versions, func(i, j int) bool { return VersionCompare(versions[i], versions[j]) < 0 })
	// Walk from the newest down, so a version dir without a launchable entry
	// falls through to the next one.
	for i := len(versions) - 1; i >= 0; i-- {
		p := CachedBinaryPath(cacheRoot, agentType, versions[i])
		if p == "" {
			continue
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, versions[i], true
		}
	}
	return "", "", false
}

// VersionCompare orders two version labels the way upstream does: numeric
// dot-separated parts, missing parts treated as 0, ties broken by the raw
// strings.
func VersionCompare(a, b string) int {
	ap, bp := parseVersionParts(a), parseVersionParts(b)
	n := len(ap)
	if len(bp) > n {
		n = len(bp)
	}
	for i := 0; i < n; i++ {
		var x, y uint32
		if i < len(ap) {
			x = ap[i]
		}
		if i < len(bp) {
			y = bp[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return strings.Compare(a, b)
}

func parseVersionParts(input string) []uint32 {
	s := strings.TrimLeftFunc(input, func(r rune) bool { return r < '0' || r > '9' })
	parts := strings.Split(s, ".")
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		digits := ""
		for _, r := range p {
			if r < '0' || r > '9' {
				break
			}
			digits += string(r)
		}
		v, err := strconv.ParseUint(digits, 10, 32)
		if err != nil {
			v = 0
		}
		out = append(out, uint32(v))
	}
	return out
}

// ResolveSystemAgentBinary finds a vendor CLI on PATH, then at
// `~/.local/bin/<cmd>` (with the Windows `.exe` suffix). Empty when absent.
func ResolveSystemAgentBinary(cmd string) string {
	if p, err := exec.LookPath(cmd); err == nil {
		return p
	}
	exe := cmd
	if runtime.GOOS == "windows" {
		exe = cmd + ".exe"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	cand := filepath.Join(home, ".local", "bin", exe)
	if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
		return cand
	}
	return ""
}
