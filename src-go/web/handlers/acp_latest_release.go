package handlers

// acp_fetch_agent_latest_release (upstream acp/latest_release.rs): the newest
// published version of an agent's npm package, offered only when it is strictly
// newer than upstream's pinned version. npx agents that support a custom version
// are looked up on the public npm registry; everything else (uvx, binary that
// this build does not download) returns null. Original Go reimplementation.
//
// A network failure returns null (graceful) rather than erroring, so an offline
// backend's settings page simply shows "up to date" instead of breaking.

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"yoyocode/src-go/acp"
	"yoyocode/src-go/web"
)

// versionFromSpec extracts the trailing @version from an npm spec, preserving a
// leading @scope: "@scope/pkg@1.2.3" -> "1.2.3", "pkg@1.2.3" -> "1.2.3", none -> "".
func versionFromSpec(spec string) string {
	spec = strings.TrimSpace(spec)
	name := packageNameFromSpec(spec)
	if len(spec) > len(name) && strings.HasPrefix(spec, name) {
		rest := spec[len(name):]
		if strings.HasPrefix(rest, "@") {
			return rest[1:]
		}
	}
	return ""
}

func npmDistTagsURL(pkgName string) string {
	return "https://registry.npmjs.org/-/package/" + strings.ReplaceAll(pkgName, "/", "%2F") + "/dist-tags"
}

func fetchNpmLatest(pkgName string) string {
	if pkgName == "" {
		return ""
	}
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Get(npmDistTagsURL(pkgName))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	var tags map[string]any
	if json.Unmarshal(body, &tags) != nil {
		return ""
	}
	if v, ok := tags["latest"].(string); ok {
		return v
	}
	return ""
}

func sanitizeCustomVersion(input string) string {
	t := strings.TrimSpace(input)
	t = strings.TrimPrefix(t, "v")
	t = strings.TrimPrefix(t, "V")
	if t == "" || !(t[0] >= '0' && t[0] <= '9') {
		return ""
	}
	if !strings.Contains(t, ".") {
		return ""
	}
	for _, c := range t {
		ok := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '.' || c == '-' || c == '+'
		if !ok {
			return ""
		}
	}
	return t
}

// versionSegments parses the leading numeric run of each dotted segment.
func versionSegments(v string) []int64 {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	parts := strings.Split(v, ".")
	out := make([]int64, 0, len(parts))
	for _, seg := range parts {
		end := 0
		for end < len(seg) && seg[end] >= '0' && seg[end] <= '9' {
			end++
		}
		n, _ := strconv.ParseInt(seg[:end], 10, 64)
		out = append(out, n)
	}
	return out
}

func isStrictlyNewer(candidate, pin string) bool {
	cs := versionSegments(candidate)
	ps := versionSegments(pin)
	n := len(cs)
	if len(ps) > n {
		n = len(ps)
	}
	for i := 0; i < n; i++ {
		var ours, theirs int64
		if i < len(cs) {
			ours = cs[i]
		}
		if i < len(ps) {
			theirs = ps[i]
		}
		if ours != theirs {
			return ours > theirs
		}
	}
	return false
}

// RegisterACPLatestRelease wires acp_fetch_agent_latest_release.
func RegisterACPLatestRelease(reg *Registry) {
	reg.Register("acp_fetch_agent_latest_release", func(c *web.Context) (any, error) {
		agentType := strVal(c.Body["agentType"], c.Body["agent_type"])
		if agentType == "" {
			return nil, web.ErrInvalidInput("agentType required")
		}
		meta, ok := acp.FindBuiltinAgent(agentType)
		if !ok || !meta.SupportsCustomVersion {
			return nil, nil
		}
		spec, specOK := acp.LaunchSpecFor(agentType)
		if !specOK || spec.Kind != "npx" {
			// uvx / binary are not downloaded by this build → nothing to offer.
			return nil, nil
		}
		pin := versionFromSpec(spec.Package)
		if strings.TrimSpace(pin) == "" {
			return nil, nil
		}
		latest := fetchNpmLatest(packageNameFromSpec(spec.Package))
		if latest == "" {
			return nil, nil
		}
		version := sanitizeCustomVersion(latest)
		if version == "" || !isStrictlyNewer(version, pin) {
			return nil, nil
		}
		return map[string]any{"version": version}, nil
	})
}
