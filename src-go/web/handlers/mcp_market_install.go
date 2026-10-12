package handlers

// MCP marketplace detail + install (B11, mcp increment 4a), mirroring
// commands/mcp.rs: mcp_get_marketplace_server_detail and
// mcp_install_from_marketplace. Smithery is fully ported here (its connection
// configSchema -> install options -> resolved spec); the official registry's
// package/remote install-option resolution is larger and staged for the next
// increment, so this build serves the official detail/install branches a clear
// dependency-missing error while the Smithery branch and the spec_override
// install path (which needs no provider fetch) work for both providers.

import (
	"encoding/json"
	"io"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"yoyocode/src-go/web"
)

// ─── install option model + selection ───────────────────────────────────────

type installOption struct {
	id          string
	protocol    string
	label       string
	description any
	spec        map[string]any
	parameters  []map[string]any
}

func (o installOption) toWire() map[string]any {
	params := o.parameters
	if params == nil {
		params = []map[string]any{}
	}
	return map[string]any{
		"id":          o.id,
		"protocol":    o.protocol,
		"label":       o.label,
		"description": o.description,
		"spec":        o.spec,
		"parameters":  params,
	}
}

func normalizeProtocolValue(raw string) string {
	if n := normalizeMCPType(raw); n != "" {
		return n
	}
	return strings.TrimSpace(raw)
}

func protocolPriority(protocol string) int {
	switch normalizeProtocolValue(protocol) {
	case "stdio":
		return 0
	case "http":
		return 1
	case "sse":
		return 2
	default:
		return 10
	}
}

func selectDefaultInstallOption(options []installOption) *installOption {
	if len(options) == 0 {
		return nil
	}
	best := 0
	for i := 1; i < len(options); i++ {
		if protocolPriority(options[i].protocol) < protocolPriority(options[best].protocol) {
			best = i
		}
	}
	return &options[best]
}

func collectProtocolsFromOptions(options []installOption) []string {
	seen := map[string]bool{}
	for _, o := range options {
		seen[normalizeProtocolValue(o.protocol)] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func selectOptionFromList(options []installOption, sel installSelection) (*installOption, error) {
	if sel.optionID != "" {
		for i := range options {
			if options[i].id == sel.optionID {
				return &options[i], nil
			}
		}
		return nil, mcpNotFound("selected install option not found: " + sel.optionID)
	}
	if sel.protocol != "" {
		best := -1
		for i := range options {
			if normalizeProtocolValue(options[i].protocol) != sel.protocol {
				continue
			}
			if best < 0 || protocolPriority(options[i].protocol) < protocolPriority(options[best].protocol) {
				best = i
			}
		}
		if best < 0 {
			return nil, mcpNotFound("no install option found for protocol '" + sel.protocol + "'")
		}
		return &options[best], nil
	}
	d := selectDefaultInstallOption(options)
	if d == nil {
		return nil, mcpNotFound("server does not provide installable options")
	}
	return d, nil
}

// installSelection is the parsed option_id / protocol / parameter_values triple.
type installSelection struct {
	optionID        string
	protocol        string
	parameterValues map[string]any
}

func newInstallSelection(c *web.Context) (installSelection, error) {
	sel := installSelection{parameterValues: map[string]any{}}
	sel.optionID = strings.TrimSpace(GetString(c, "optionId", "option_id"))
	if p := strings.TrimSpace(GetString(c, "protocol")); p != "" {
		sel.protocol = normalizeProtocolValue(p)
	}
	if v, ok := c.Body["parameterValues"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return sel, mcpInvalidInput("parameter_values must be a JSON object")
		}
		sel.parameterValues = m
	} else if v, ok := c.Body["parameter_values"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return sel, mcpInvalidInput("parameter_values must be a JSON object")
		}
		sel.parameterValues = m
	}
	return sel, nil
}

// ─── shared value helpers ───────────────────────────────────────────────────

func keyLooksSecret(name string) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, "token") || strings.Contains(l, "secret") ||
		strings.Contains(l, "password") || strings.Contains(l, "api_key") || strings.HasSuffix(l, "key")
}

// valueAsText renders a scalar/array/object as text, or nil for empty/null.
func valueAsText(v any) any {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil
		}
		return s
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case nil:
		return nil
	default: // array / object
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return nil
	}
}

func appendQueryParam(u, key, value string) string {
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + url.QueryEscape(key) + "=" + url.QueryEscape(value)
}

// ─── smithery install resolution ────────────────────────────────────────────

type smitheryConfigField struct {
	key          string
	description  any
	required     bool
	secret       bool
	kind         string
	defaultValue any
	enumValues   []string
	location     string
}

func smitheryConnType(conn map[string]any) string { return mstr(conn, "type") }

func smitheryConnectionProtocol(conn map[string]any) string {
	switch normalizeMCPType(smitheryConnType(conn)) {
	case "sse":
		return "sse"
	default:
		return "http"
	}
}

func smitheryConnectionURL(conn map[string]any, fallback string) string {
	if u := mstr(conn, "deploymentUrl"); u != "" {
		return u
	}
	return strings.TrimSpace(fallback)
}

func smitheryPropertyKind(prop map[string]any) string {
	mapType := func(typ string) string {
		switch typ {
		case "boolean":
			return "boolean"
		case "number":
			return "number"
		case "integer":
			return "integer"
		case "object", "array":
			return "json"
		default:
			return "string"
		}
	}
	switch raw := prop["type"].(type) {
	case string:
		return mapType(strings.TrimSpace(raw))
	case []any:
		for _, item := range raw {
			if s, ok := item.(string); ok && s != "null" {
				return mapType(s)
			}
		}
	}
	return "string"
}

func smitheryFieldLocation(key string, prop map[string]any, secret bool) string {
	explicit := strings.TrimSpace(mstr(prop, "x-from"))
	if strings.EqualFold(explicit, "header") {
		return "header"
	}
	if strings.EqualFold(explicit, "query") {
		return "query"
	}
	if secret || keyLooksSecret(key) {
		return "header"
	}
	return "query"
}

func parseSmitheryConfigFields(schema any) []smitheryConfigField {
	root, ok := asObj(schema)
	if !ok {
		return nil
	}
	required := map[string]bool{}
	if arr, ok := asArr(root["required"]); ok {
		for _, r := range arr {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}
	properties, ok := asObj(root["properties"])
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(properties))
	for k := range properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fields := []smitheryConfigField{}
	for _, key := range keys {
		prop, ok := asObj(properties[key])
		if !ok {
			continue
		}
		kind := smitheryPropertyKind(prop)
		secret := false
		if b, ok := prop["writeOnly"].(bool); ok && b {
			secret = true
		}
		if keyLooksSecret(key) {
			secret = true
		}
		location := smitheryFieldLocation(key, prop, secret)
		enumValues := []string{}
		if arr, ok := asArr(prop["enum"]); ok {
			for _, e := range arr {
				if s, ok := e.(string); ok {
					enumValues = append(enumValues, s)
				}
			}
		}
		fields = append(fields, smitheryConfigField{
			key:          key,
			description:  mcpOptStr(prop, "description"),
			required:     required[key],
			secret:       secret,
			kind:         kind,
			defaultValue: prop["default"],
			enumValues:   enumValues,
			location:     location,
		})
	}
	return fields
}

func smitheryParameterFields(conn map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, f := range parseSmitheryConfigFields(conn["configSchema"]) {
		out = append(out, map[string]any{
			"key":           f.key,
			"label":         f.key,
			"description":   f.description,
			"required":      f.required,
			"secret":        f.secret,
			"kind":          f.kind,
			"default_value": f.defaultValue,
			"placeholder":   nil,
			"enum_values":   f.enumValues,
			"location":      f.location,
		})
	}
	return out
}

func resolveSmitheryConnectionSpec(conn map[string]any, fallbackURL string, values map[string]any, enforceRequired bool) (map[string]any, error) {
	protocol := smitheryConnectionProtocol(conn)
	u := smitheryConnectionURL(conn, fallbackURL)
	if u == "" {
		return nil, mcpConfigInvalid("smithery connection missing deployment URL")
	}
	nextURL := u
	headers := map[string]any{}
	for _, field := range parseSmitheryConfigFields(conn["configSchema"]) {
		value, ok := values[field.key]
		if !ok || value == nil {
			value = field.defaultValue
		}
		if value == nil {
			if enforceRequired && field.required {
				return nil, mcpInvalidInput("missing required configuration '" + field.key + "'")
			}
			continue
		}
		if field.location == "header" {
			if text := valueAsText(value); text != nil {
				headers[field.key] = text
			} else if enforceRequired && field.required {
				return nil, mcpInvalidInput("invalid configuration value '" + field.key + "'")
			}
			continue
		}
		if text := valueAsText(value); text != nil {
			nextURL = appendQueryParam(nextURL, field.key, text.(string))
		} else if enforceRequired && field.required {
			return nil, mcpInvalidInput("invalid configuration value '" + field.key + "'")
		}
	}
	spec := map[string]any{"type": protocol, "url": nextURL}
	if len(headers) > 0 {
		spec["headers"] = headers
	}
	return canonicalizeSpec(spec, "smithery install")
}

func smitheryOptionID(index int, protocol string) string {
	return "smithery:connection:" + strconv.Itoa(index) + ":" + protocol
}

func parseSmitheryOptionID(optionID string) (int, bool) {
	parts := strings.Split(optionID, ":")
	if len(parts) < 3 || parts[0] != "smithery" || parts[1] != "connection" {
		return 0, false
	}
	idx, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, false
	}
	return idx, true
}

func buildSmitheryInstallOptions(detail map[string]any) ([]installOption, error) {
	connections, _ := asArr(detail["connections"])
	fallback := mstr(detail, "deploymentUrl")
	options := []installOption{}
	for index, c := range connections {
		conn, ok := asObj(c)
		if !ok {
			continue
		}
		protocol := smitheryConnectionProtocol(conn)
		spec, err := resolveSmitheryConnectionSpec(conn, fallback, map[string]any{}, false)
		if err != nil {
			continue
		}
		var desc any
		if d := mstr(conn, "deploymentUrl"); d != "" {
			desc = d
		}
		options = append(options, installOption{
			id:          smitheryOptionID(index, protocol),
			protocol:    protocol,
			label:       protocol + " (connection " + strconv.Itoa(index+1) + ")",
			description: desc,
			spec:        spec,
			parameters:  smitheryParameterFields(conn),
		})
	}
	if len(options) == 0 && fallback != "" {
		spec, err := canonicalizeSpec(map[string]any{"type": "http", "url": fallback}, "smithery fallback")
		if err != nil {
			return nil, err
		}
		options = append(options, installOption{
			id: "smithery:fallback:http", protocol: "http", label: "http",
			description: fallback, spec: spec, parameters: []map[string]any{},
		})
	}
	if len(options) == 0 {
		return nil, mcpNotFound("smithery server '" + mstr(detail, "qualifiedName") + "' does not provide installable connection info")
	}
	return options, nil
}

func resolveSmitheryInstallSpec(detail map[string]any, sel installSelection) (map[string]any, error) {
	options, err := buildSmitheryInstallOptions(detail)
	if err != nil {
		return nil, err
	}
	selected, err := selectOptionFromList(options, sel)
	if err != nil {
		return nil, err
	}
	if index, ok := parseSmitheryOptionID(selected.id); ok {
		connections, _ := asArr(detail["connections"])
		if index < 0 || index >= len(connections) {
			return nil, mcpNotFound("selected smithery connection is out of range: " + strconv.Itoa(index))
		}
		conn, _ := asObj(connections[index])
		return resolveSmitheryConnectionSpec(conn, mstr(detail, "deploymentUrl"), sel.parameterValues, true)
	}
	return canonicalizeSpec(selected.spec, "smithery selected option")
}

// ─── smithery fetch ─────────────────────────────────────────────────────────

func fetchSmitheryServerDetail(serverID string) (map[string]any, error) {
	resp, err := mcpMarketGet("failed to fetch smithery server detail",
		"https://api.smithery.ai/servers/"+serverID, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, mcpNetwork("smithery server detail request failed: HTTP " + strconv.Itoa(resp.StatusCode))
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, mcpNetwork("failed to parse smithery server detail: failed to read response body: " + err.Error())
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, mcpNetwork("failed to parse smithery server detail: invalid JSON response: " + err.Error())
	}
	obj, ok := asObj(v)
	if !ok {
		return nil, mcpNetwork("failed to parse smithery server detail: not an object")
	}
	return obj, nil
}

func fetchSmitheryServerSummary(serverID string) map[string]any {
	q := url.Values{}
	q.Set("limit", "30")
	q.Set("q", serverID)
	resp, err := mcpMarketGet("failed to fetch smithery server summary", "https://api.smithery.ai/servers", q)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	obj, _ := asObj(v)
	servers, _ := asArr(obj["servers"])
	for _, s := range servers {
		if m, ok := asObj(s); ok && mstr(m, "qualifiedName") == serverID {
			return m
		}
	}
	return nil
}

// pickStr returns the first trimmed non-empty of detail[key] then summary[key].
func pickStr(detail, summary map[string]any, key string) any {
	if s := mstr(detail, key); s != "" {
		return s
	}
	if summary != nil {
		if s := mstr(summary, key); s != "" {
			return s
		}
	}
	return nil
}

// ─── handlers ───────────────────────────────────────────────────────────────

func mcpGetMarketplaceServerDetail(c *web.Context) (any, error) {
	providerID, err := GetStringOrErr(c, "providerId", "provider_id")
	if err != nil {
		return nil, err
	}
	serverID, err := GetStringOrErr(c, "serverId", "server_id")
	if err != nil {
		return nil, err
	}
	switch providerID {
	case marketplaceSmithery:
		detail, err := fetchSmitheryServerDetail(serverID)
		if err != nil {
			return nil, err
		}
		summary := fetchSmitheryServerSummary(serverID)
		options, err := buildSmitheryInstallOptions(detail)
		if err != nil {
			return nil, err
		}
		def := selectDefaultInstallOption(options)
		if def == nil {
			return nil, mcpNotFound("smithery server '" + mstr(detail, "qualifiedName") + "' does not provide installable connection info")
		}
		description := pickStr(detail, summary, "description")
		if description == nil {
			description = "No description"
		}
		remote, _ := detail["remote"].(bool)
		verified, _ := detail["verified"].(bool)
		if summary != nil {
			if sv, ok := summary["verified"].(bool); ok && sv {
				verified = true
			}
		}
		optionWire := make([]map[string]any, 0, len(options))
		for _, o := range options {
			optionWire = append(optionWire, o.toWire())
		}
		var downloads, score, isDeployed any = detail["useCount"], detail["score"], detail["isDeployed"]
		if summary != nil {
			if downloads == nil {
				downloads = summary["useCount"]
			}
			if score == nil {
				score = summary["score"]
			}
			if isDeployed == nil {
				isDeployed = summary["isDeployed"]
			}
		}
		return map[string]any{
			"provider_id":       marketplaceSmithery,
			"server_id":         mstr(detail, "qualifiedName"),
			"name":              detail["displayName"],
			"description":       description,
			"homepage":          pickStr(detail, summary, "homepage"),
			"remote":            remote,
			"verified":          verified,
			"icon_url":          pickStr(detail, summary, "iconUrl"),
			"latest_version":    nil,
			"protocols":         collectProtocolsFromOptions(options),
			"owner":             pickStr(detail, summary, "owner"),
			"namespace":         pickStr(detail, summary, "namespace"),
			"downloads":         downloads,
			"score":             score,
			"is_deployed":       isDeployed,
			"default_option_id": def.id,
			"install_options":   optionWire,
			"spec":              def.spec,
		}, nil
	case marketplaceOfficial:
		detail, err := fetchOfficialServerDetail(serverID)
		if err != nil {
			return nil, err
		}
		server, _ := asObj(detail["server"])
		if server == nil {
			return nil, mcpConfigInvalid("official MCP server detail missing server object")
		}
		item := officialEntryToItem(detail)
		options, err := buildOfficialInstallOptions(server)
		if err != nil {
			return nil, err
		}
		def := selectDefaultInstallOption(options)
		if def == nil {
			return nil, mcpNotFound("official MCP server '" + mstr(server, "name") + "' does not expose an installable transport")
		}
		optionWire := make([]map[string]any, 0, len(options))
		for _, o := range options {
			optionWire = append(optionWire, o.toWire())
		}
		out := map[string]any{}
		for k, v := range item {
			out[k] = v
		}
		out["default_option_id"] = def.id
		out["install_options"] = optionWire
		out["spec"] = def.spec
		return out, nil
	default:
		return nil, mcpInvalidInput("unsupported marketplace provider: " + providerID)
	}
}

func mcpInstallFromMarketplace(c *web.Context) (any, error) {
	providerID, err := GetStringOrErr(c, "providerId", "provider_id")
	if err != nil {
		return nil, err
	}
	serverID, err := GetStringOrErr(c, "serverId", "server_id")
	if err != nil {
		return nil, err
	}
	apps := normalizeApps(GetStringSlice(c, "apps"))
	if len(apps) == 0 {
		return nil, mcpInvalidInput("at least one target app is required")
	}
	sel, err := newInstallSelection(c)
	if err != nil {
		return nil, err
	}

	var canonical map[string]any
	if override, ok := c.Body["specOverride"].(map[string]any); ok {
		canonical, err = canonicalizeSpec(override, "marketplace install override")
	} else if override, ok := c.Body["spec_override"].(map[string]any); ok {
		canonical, err = canonicalizeSpec(override, "marketplace install override")
	} else {
		switch providerID {
		case marketplaceSmithery:
			detail, derr := fetchSmitheryServerDetail(serverID)
			if derr != nil {
				return nil, derr
			}
			canonical, err = resolveSmitheryInstallSpec(detail, sel)
		case marketplaceOfficial:
			detail, derr := fetchOfficialServerDetail(serverID)
			if derr != nil {
				return nil, derr
			}
			server, _ := asObj(detail["server"])
			if server == nil {
				return nil, mcpConfigInvalid("official MCP server detail missing server object")
			}
			canonical, err = resolveOfficialInstallSpec(server, sel)
		default:
			return nil, mcpInvalidInput("unsupported marketplace provider: " + providerID)
		}
	}
	if err != nil {
		return nil, err
	}

	hostable := []McpAppType{}
	excluded := []McpAppType{}
	for _, app := range apps {
		if appCanHostSpec(app, canonical) {
			hostable = append(hostable, app)
		} else {
			excluded = append(excluded, app)
		}
	}
	if len(hostable) == 0 {
		return nil, mcpInvalidInput("none of the selected agents can host this MCP server's transport (e.g. Codex does not support SSE)")
	}
	for _, app := range excluded {
		if _, err := removeServerForApp(app, serverID); err != nil {
			return nil, err
		}
	}
	for _, app := range hostable {
		if err := upsertServerForApp(app, serverID, canonical); err != nil {
			return nil, err
		}
	}
	saved := findLocalServer(serverID)
	if saved == nil {
		return nil, mcpConfigInvalid("installed server '" + serverID + "', but failed to load it from local configuration")
	}
	return saved, nil
}
