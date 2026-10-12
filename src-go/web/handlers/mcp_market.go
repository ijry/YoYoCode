package handlers

// MCP marketplace search (B11, mcp increment 3a), mirroring the read-only
// marketplace half of commands/mcp.rs: mcp_search_marketplace for both the
// official registry (registry.modelcontextprotocol.io) and Smithery
// (api.smithery.ai). Server detail and install (the install-option resolution)
// are staged for the next increment.
//
// Items are mapped straight from the providers' JSON into the stable
// McpMarketplaceItem wire shape (serde default field names → snake_case),
// optionals emitted as null and protocols always an array, exactly as upstream.

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"yoyocode/src-go/web"
)

const mcpMarketUserAgent = "yoyocode-mcp-market/1.0"

var marketHTTPClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
		TLSHandshakeTimeout: 8 * time.Second,
	},
}

func mcpNetwork(msg string) error  { return web.ErrNetwork(msg) }
func mcpNotFound(msg string) error { return web.ErrNotFound(msg) }

func formatMarketNetworkError(context string, err error) string {
	msg := err.Error()
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return context + ": request timed out. Please check network/proxy settings and retry: " + msg
	}
	return context + ": " + msg
}

// mcpMarketGet issues a GET with the marketplace user-agent, retrying up to three
// times on a 429 / 5xx or a transport error with a linear backoff.
func mcpMarketGet(context, rawURL string, query url.Values) (*http.Response, error) {
	const maxAttempts = 3
	var lastErr string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, mcpNetwork(context + ": " + err.Error())
		}
		if query != nil {
			req.URL.RawQuery = query.Encode()
		}
		req.Header.Set("User-Agent", mcpMarketUserAgent)
		resp, err := marketHTTPClient.Do(req)
		if err != nil {
			lastErr = formatMarketNetworkError(context, err)
			if attempt < maxAttempts {
				time.Sleep(time.Duration(attempt) * 350 * time.Millisecond)
			}
			continue
		}
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < maxAttempts {
			resp.Body.Close()
			time.Sleep(time.Duration(attempt) * 350 * time.Millisecond)
			continue
		}
		return resp, nil
	}
	if lastErr == "" {
		lastErr = context + ": request failed"
	}
	return nil, mcpNetwork(lastErr)
}

func parseJSONValueResponse(resp *http.Response, context string) (any, error) {
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, mcpNetwork(context + ": failed to read response body: " + err.Error())
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, mcpNetwork(context + ": invalid JSON response: " + err.Error())
	}
	return v, nil
}

// ─── small value helpers ────────────────────────────────────────────────────

func mstr(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// mcpOptStr returns a trimmed non-empty string or nil (serde Option<String>).
func mcpOptStr(m map[string]any, key string) any {
	if s := mstr(m, key); s != "" {
		return s
	}
	return nil
}

func asObj(v any) (map[string]any, bool) { m, ok := v.(map[string]any); return m, ok }
func asArr(v any) ([]any, bool)          { a, ok := v.([]any); return a, ok }

// ─── official registry ──────────────────────────────────────────────────────

func transportProtocol(kind string) string {
	switch normalizeMCPType(kind) {
	case "stdio", "http", "sse":
		return normalizeMCPType(kind)
	default:
		return ""
	}
}

func firstNonEmptyIconSrc(icons any) any {
	arr, ok := asArr(icons)
	if !ok {
		return nil
	}
	for _, it := range arr {
		if m, ok := asObj(it); ok {
			if s := mstr(m, "src"); s != "" {
				return s
			}
		}
	}
	return nil
}

func officialServerProtocols(server map[string]any) []string {
	seen := map[string]bool{}
	collect := func(kind string) {
		if p := transportProtocol(kind); p != "" {
			seen[p] = true
		}
	}
	if remotes, ok := asArr(server["remotes"]); ok {
		for _, r := range remotes {
			if m, ok := asObj(r); ok {
				collect(mstr(m, "type"))
			}
		}
	}
	if packages, ok := asArr(server["packages"]); ok {
		for _, p := range packages {
			if m, ok := asObj(p); ok {
				if tr, ok := asObj(m["transport"]); ok {
					collect(mstr(tr, "type"))
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func officialEntryToItem(entry map[string]any) map[string]any {
	server, _ := asObj(entry["server"])
	if server == nil {
		server = map[string]any{}
	}
	name := mstr(server, "title")
	if name == "" {
		name, _ = server["name"].(string)
	}
	description := mstr(server, "description")
	if description == "" {
		description = "No description"
	}
	var homepage any
	if h := mstr(server, "websiteUrl"); h != "" {
		homepage = h
	} else if repo, ok := asObj(server["repository"]); ok {
		if u := mstr(repo, "url"); u != "" {
			homepage = u
		}
	}
	remote := false
	if remotes, ok := asArr(server["remotes"]); ok {
		remote = len(remotes) > 0
	}
	verified := false
	if meta, ok := asObj(entry["_meta"]); ok {
		if official, ok := asObj(meta["io.modelcontextprotocol.registry/official"]); ok {
			verified = mstr(official, "status") == "active"
		}
	}
	serverID, _ := server["name"].(string)
	return map[string]any{
		"provider_id":    marketplaceOfficial,
		"server_id":      serverID,
		"name":           name,
		"description":    description,
		"homepage":       homepage,
		"remote":         remote,
		"verified":       verified,
		"icon_url":       firstNonEmptyIconSrc(server["icons"]),
		"latest_version": mcpOptStr(server, "version"),
		"protocols":      officialServerProtocols(server),
		"owner":          nil,
		"namespace":      nil,
		"downloads":      nil,
		"score":          nil,
		"is_deployed":    nil,
	}
}

func searchOfficialRegistry(query string, limit int) (any, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("version", "latest")
	q.Set("search", strings.TrimSpace(query))
	resp, err := mcpMarketGet("failed to query official MCP registry",
		"https://registry.modelcontextprotocol.io/v0.1/servers", q)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, mcpNetwork("official MCP registry request failed: HTTP " + strconv.Itoa(resp.StatusCode))
	}
	payload, err := parseJSONValueResponse(resp, "failed to parse official MCP registry response")
	if err != nil {
		return nil, err
	}
	obj, _ := asObj(payload)
	entries, ok := asArr(obj["servers"])
	if !ok {
		return nil, mcpConfigInvalid("failed to parse official MCP registry response: missing servers array")
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		if m, ok := asObj(e); ok {
			out = append(out, officialEntryToItem(m))
		}
	}
	return out, nil
}

// ─── smithery ───────────────────────────────────────────────────────────────

func smitheryItemFromSummary(item map[string]any) map[string]any {
	remote, _ := item["remote"].(bool)
	verified, _ := item["verified"].(bool)
	description := mstr(item, "description")
	if description == "" {
		description = "No description"
	}
	protocols := []string{}
	if remote {
		protocols = []string{"http"}
	}
	return map[string]any{
		"provider_id":    marketplaceSmithery,
		"server_id":      mstr(item, "qualifiedName"),
		"name":           item["displayName"],
		"description":    description,
		"homepage":       mcpOptStr(item, "homepage"),
		"remote":         remote,
		"verified":       verified,
		"icon_url":       mcpOptStr(item, "iconUrl"),
		"latest_version": nil,
		"protocols":      protocols,
		"owner":          mcpOptStr(item, "owner"),
		"namespace":      mcpOptStr(item, "namespace"),
		"downloads":      item["useCount"],
		"score":          item["score"],
		"is_deployed":    item["isDeployed"],
	}
}

func searchSmithery(query string, limit int) (any, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("q", strings.TrimSpace(query))
	resp, err := mcpMarketGet("failed to query smithery marketplace", "https://api.smithery.ai/servers", q)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, mcpNetwork("smithery marketplace request failed: HTTP " + strconv.Itoa(resp.StatusCode))
	}
	payload, err := parseJSONValueResponse(resp, "failed to parse smithery response")
	if err != nil {
		return nil, err
	}
	obj, _ := asObj(payload)
	servers, _ := asArr(obj["servers"])
	out := make([]map[string]any, 0, len(servers))
	for _, s := range servers {
		if m, ok := asObj(s); ok {
			out = append(out, smitheryItemFromSummary(m))
		}
	}
	return out, nil
}

// ─── handler ────────────────────────────────────────────────────────────────

func mcpSearchMarketplace(c *web.Context) (any, error) {
	providerID, err := GetStringOrErr(c, "providerId", "provider_id")
	if err != nil {
		return nil, err
	}
	query := GetString(c, "query")
	limit := int(GetInt(c, "limit"))
	if limit == 0 {
		limit = 30
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	switch providerID {
	case marketplaceOfficial:
		return searchOfficialRegistry(query, limit)
	case marketplaceSmithery:
		return searchSmithery(query, limit)
	default:
		return nil, mcpInvalidInput("unsupported marketplace provider: " + providerID)
	}
}
