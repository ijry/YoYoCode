package handlers

// acp_fetch_registry_catalog / acp_add_registry_agent (upstream
// commands/custom_agents.rs + acp/remote_registry.rs). Reads the public Agent
// Client Protocol registry — an open catalog of ACP-speaking agents — annotates
// each entry with what this build can do with it, and one-click-adds an entry
// as a custom agent. The registry is a protocol document, not a model-provider
// catalog, so it is in scope.

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"yoyocode/src-go/acp"
	"yoyocode/src-go/web"
)

const acpRegistryURL = "https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json"

// builtinRegistryAliases mirrors BUILTIN_REGISTRY_ALIASES: registry ids that
// name an agent upstream already ships under a different id.
var builtinRegistryAliases = []string{"kimi", "qoder"}

type registryAgentItem struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Version      *string        `json:"version"`
	Website      *string        `json:"website"`
	Repository   *string        `json:"repository"`
	License      *string        `json:"license"`
	Icon         *string        `json:"icon"`
	Distribution map[string]any `json:"distribution"`
}

type registryPayload struct {
	Agents []registryAgentItem `json:"agents"`
}

// acpCurrentPlatformKey returns the registry platform key for this machine
// (e.g. darwin-aarch64, linux-x86_64, windows-x86_64), mirroring
// registry::current_platform. Kept beside acpCurrentPlatform (custom agents),
// which uses the same spelling.
func acpRegistryPlatformKey() string { return acpCurrentPlatform() }

// distributionKindsOf mirrors distribution_kinds_of: binary → npx → uvx.
func distributionKindsOf(spec map[string]any) []string {
	kinds := []string{}
	if bin, ok := spec["binary"].(map[string]any); ok && len(bin) > 0 {
		kinds = append(kinds, "binary")
	}
	if _, ok := spec["npx"].(map[string]any); ok {
		kinds = append(kinds, "npx")
	}
	if _, ok := spec["uvx"].(map[string]any); ok {
		kinds = append(kinds, "uvx")
	}
	return kinds
}

// kindRunsHere mirrors kind_runs_here: npx/uvx are platform-independent; a
// binary channel needs a non-empty archive for this platform.
func kindRunsHere(kind string, spec map[string]any) bool {
	if kind != "binary" {
		return true
	}
	bin, _ := spec["binary"].(map[string]any)
	here, _ := bin[acpRegistryPlatformKey()].(map[string]any)
	if here == nil {
		return false
	}
	archive := firstNonEmptyStr(caStr(here, "archive"), caStr(here, "url"))
	return strings.TrimSpace(archive) != ""
}

func defaultKindFor(spec map[string]any) string {
	kinds := distributionKindsOf(spec)
	for _, k := range kinds {
		if kindRunsHere(k, spec) {
			return k
		}
	}
	if len(kinds) > 0 {
		return kinds[0]
	}
	return ""
}

// effectiveRegistryKind mirrors effective_kind.
func effectiveRegistryKind(spec map[string]any, explicit string) (string, error) {
	if explicit != "" {
		for _, k := range distributionKindsOf(spec) {
			if k == explicit {
				return k, nil
			}
		}
		return "", web.ErrConfigInvalid("agent does not publish a " + explicit + " distribution")
	}
	k := defaultKindFor(spec)
	if k == "" {
		return "", web.ErrConfigInvalid("agent publishes no usable distribution")
	}
	return k, nil
}

// isBuiltinRegistryID mirrors is_builtin_registry_id.
func isBuiltinRegistryID(id string) bool {
	for _, a := range builtinRegistryAliases {
		if a == id {
			return true
		}
	}
	for _, a := range acp.BuiltinAgents {
		if a.RegistryID == id {
			return true
		}
	}
	return false
}

// fetchRegistryCatalog fetches + annotates the ACP registry (fetch_catalog).
func fetchRegistryCatalog(installedIDs map[string]bool) ([]map[string]any, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, acpRegistryURL, nil)
	req.Header.Set("user-agent", "yoyocode")
	resp, err := client.Do(req)
	if err != nil {
		return nil, web.ErrNetwork("failed to fetch ACP registry: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, web.ErrNetwork("failed to fetch ACP registry: HTTP " + itoaInt(resp.StatusCode))
	}
	var payload registryPayload
	if e := json.NewDecoder(resp.Body).Decode(&payload); e != nil {
		return nil, web.ErrConfigInvalid("failed to parse ACP registry JSON: " + e.Error())
	}
	out := make([]map[string]any, 0, len(payload.Agents))
	for _, item := range payload.Agents {
		spec := item.Distribution
		if spec == nil {
			spec = map[string]any{}
		}
		kinds := distributionKindsOf(spec)
		supported := false
		for _, k := range kinds {
			if kindRunsHere(k, spec) {
				supported = true
				break
			}
		}
		out = append(out, map[string]any{
			"registryId":          item.ID,
			"name":                item.Name,
			"description":         item.Description,
			"version":             optStrP(item.Version),
			"iconUrl":             optStrP(item.Icon),
			"website":             optStrP(item.Website),
			"repository":          optStrP(item.Repository),
			"license":             optStrP(item.License),
			"distributionKinds":   toAnySlice(kinds),
			"builtin":             isBuiltinRegistryID(item.ID),
			"installed":           installedIDs[item.ID],
			"supportedOnPlatform": supported,
			"spec":                spec,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(asString(out[i]["name"])) < strings.ToLower(asString(out[j]["name"]))
	})
	return out, nil
}

func optStrP(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// RegisterACPRegistryCatalog wires the remote-registry endpoints.
func RegisterACPRegistryCatalog(reg *Registry) {
	reg.Register("acp_fetch_registry_catalog", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		installed := map[string]bool{}
		if rows, qerr := d.Conn.Query(`SELECT registry_id FROM custom_agent`); qerr == nil {
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					installed[id] = true
				}
			}
			rows.Close()
		}
		return fetchRegistryCatalog(installed)
	})

	reg.Register("acp_add_registry_agent", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		registryID := strings.TrimSpace(GetString(c, "registryId", "registry_id"))
		if registryID == "" {
			return nil, web.ErrInvalidInput("registryId required")
		}
		if isBuiltinRegistryID(registryID) {
			return nil, web.ErrConfigInvalid(registryID + " is already built into YoYoCode")
		}
		explicitKind := ""
		if k := strings.TrimSpace(GetString(c, "distributionKind", "distribution_kind")); k != "" {
			if _, ok := parseDistributionKind(k); !ok {
				return nil, web.ErrConfigInvalid("unknown distribution kind: " + k)
			}
			explicitKind = k
		}

		installed := map[string]bool{}
		catalog, ferr := fetchRegistryCatalog(installed)
		if ferr != nil {
			return nil, ferr
		}
		var entry map[string]any
		for _, e := range catalog {
			if asString(e["registryId"]) == registryID {
				entry = e
				break
			}
		}
		if entry == nil {
			return nil, web.ErrConfigInvalid(registryID + " is not in the ACP registry")
		}
		spec, _ := entry["spec"].(map[string]any)
		if spec == nil {
			spec = map[string]any{}
		}
		kind, kerr := effectiveRegistryKind(spec, explicitKind)
		if kerr != nil {
			return nil, kerr
		}

		def := &customAgentDef{
			registryID:       registryID,
			name:             asString(entry["name"]),
			description:      asString(entry["description"]),
			version:          asString(entry["version"]),
			distributionKind: kind,
			spec:             spec,
			source:           "registry",
			supportsMcp:      true,
		}
		if ic := asString(entry["iconUrl"]); ic != "" {
			def.iconURL = &ic
		}
		if problem := validateCustomDef(def); problem != "" {
			return nil, web.ErrTaskExecution(problem)
		}
		if e := upsertCustomAgent(d.Conn, def); e != nil {
			return nil, web.ErrTaskExecution(e.Error())
		}
		Emit(c, "agents://changed", map[string]any{})
		return nil, nil
	})
}
