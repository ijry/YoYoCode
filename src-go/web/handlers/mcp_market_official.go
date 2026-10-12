package handlers

// MCP official-registry install resolution (B11, mcp increment 4b), mirroring
// the official half of commands/mcp.rs: build_official_install_options and
// resolve_official_install_spec_with_selection, plus their package/remote/
// argument/key-value helpers. With this in place both marketplace providers
// (official registry + Smithery) are fully supported by detail and install.

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

func officialRemoteOptionID(index int, protocol string) string {
	return "official:remote:" + strconv.Itoa(index) + ":" + protocol
}
func officialPackageOptionID(index int, protocol string) string {
	return "official:package:" + strconv.Itoa(index) + ":" + protocol
}

// parseOfficialOptionID returns (source, index) for "official:<source>:<idx>:...".
func parseOfficialOptionID(optionID string) (string, int, bool) {
	parts := strings.Split(optionID, ":")
	if len(parts) < 3 || parts[0] != "official" {
		return "", 0, false
	}
	idx, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", 0, false
	}
	return parts[1], idx, true
}

// ─── key/value inputs (headers, variables, env) ─────────────────────────────

func containsUnresolvedPlaceholder(s string) bool {
	return strings.Contains(s, "{") && strings.Contains(s, "}")
}

// officialKVList normalizes a headers/variables/env value that upstream accepts
// as either an array of {name,...} objects or a name->(string|object) map.
func officialKVList(v any) []map[string]any {
	out := []map[string]any{}
	switch t := v.(type) {
	case []any:
		for _, item := range t {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := mstr(m, "name")
			if name == "" {
				continue
			}
			out = append(out, normalizeKV(name, m))
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			name := strings.TrimSpace(key)
			if name == "" {
				continue
			}
			if s, ok := t[key].(string); ok {
				kv := map[string]any{"name": name}
				if ts := strings.TrimSpace(s); ts != "" {
					kv["value"] = ts
				}
				out = append(out, kv)
				continue
			}
			if obj, ok := t[key].(map[string]any); ok {
				out = append(out, normalizeKV(name, obj))
			} else {
				out = append(out, map[string]any{"name": name})
			}
		}
	}
	return out
}

func normalizeKV(name string, m map[string]any) map[string]any {
	kv := map[string]any{"name": name}
	for _, k := range []string{"value", "default", "description", "format", "valueHint"} {
		if s := mstr(m, k); s != "" {
			kv[k] = s
		}
	}
	if b, ok := m["isRequired"].(bool); ok {
		kv["isRequired"] = b
	}
	if b, ok := m["isSecret"].(bool); ok {
		kv["isSecret"] = b
	}
	return kv
}

// valOrDefault returns value, else default (trimmed non-empty, non-placeholder).
func valOrDefault(m map[string]any) (string, bool) {
	v := mstr(m, "value")
	if v == "" {
		v = mstr(m, "default")
	}
	if v == "" || containsUnresolvedPlaceholder(v) {
		return "", false
	}
	return v, true
}

func kvIsRequired(kv map[string]any) bool {
	if b, ok := kv["isRequired"].(bool); ok && b {
		return true
	}
	hasPlaceholder := containsUnresolvedPlaceholder(mstr(kv, "value")) || containsUnresolvedPlaceholder(mstr(kv, "default"))
	if hasPlaceholder {
		return true
	}
	_, ok := valOrDefault(kv)
	return !ok
}

// ─── value conversion ───────────────────────────────────────────────────────

func inferParameterKind(format string) string {
	switch strings.TrimSpace(format) {
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

func officialTextToValue(kind, value string) any {
	t := strings.TrimSpace(value)
	switch kind {
	case "boolean":
		return strings.EqualFold(t, "true")
	case "number":
		if n, err := strconv.ParseFloat(t, 64); err == nil {
			return n
		}
		return t
	case "integer":
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return float64(n)
		}
		return t
	default:
		return t
	}
}

func readParamText(values map[string]any, key string) (string, bool) {
	v, ok := values[key]
	if !ok {
		return "", false
	}
	if text := valueAsText(v); text != nil {
		return text.(string), true
	}
	return "", false
}

// ─── remote transport resolution ────────────────────────────────────────────

func applyTransportVariables(baseURL string, variables []map[string]any, values map[string]any, enforce bool) (string, error) {
	u := baseURL
	for _, item := range variables {
		keyName := mstr(item, "name")
		if keyName == "" {
			continue
		}
		fieldKey := "variables." + keyName
		text, ok := readParamText(values, fieldKey)
		if !ok {
			text, ok = valOrDefault(item)
		}
		if ok {
			encoded := url.QueryEscape(text)
			brace := "{" + keyName + "}"
			moustache := "{{" + keyName + "}}"
			if strings.Contains(u, brace) {
				u = strings.ReplaceAll(u, brace, encoded)
			} else if strings.Contains(u, moustache) {
				u = strings.ReplaceAll(u, moustache, encoded)
			} else {
				u = appendQueryParam(u, keyName, text)
			}
			continue
		}
		if enforce && kvIsRequired(item) {
			return "", mcpInvalidInput("missing required variable '" + keyName + "'")
		}
	}
	return u, nil
}

func remoteSpecFromTransport(transport map[string]any, values map[string]any, enforce bool) (map[string]any, error) {
	kind := mstr(transport, "type")
	canonicalType := normalizeMCPType(kind)
	if canonicalType != "http" && canonicalType != "sse" {
		return nil, mcpInvalidInput("unsupported transport type '" + kind + "'")
	}
	baseURL := mstr(transport, "url")
	if baseURL == "" {
		return nil, mcpInvalidInput("remote transport missing URL")
	}
	u, err := applyTransportVariables(baseURL, officialKVList(transport["variables"]), values, enforce)
	if err != nil {
		return nil, err
	}
	spec := map[string]any{"type": canonicalType, "url": u}
	headers := map[string]any{}
	for _, item := range officialKVList(transport["headers"]) {
		keyName := mstr(item, "name")
		if keyName == "" {
			continue
		}
		text, ok := readParamText(values, "headers."+keyName)
		if !ok {
			text, ok = valOrDefault(item)
		}
		if ok {
			headers[keyName] = text
			continue
		}
		if enforce && kvIsRequired(item) {
			return nil, mcpInvalidInput("missing required header '" + keyName + "'")
		}
	}
	if len(headers) > 0 {
		spec["headers"] = headers
	}
	return canonicalizeSpec(spec, "official transport")
}

func officialRemoteParameterFields(transport map[string]any) []map[string]any {
	fields := []map[string]any{}
	mk := func(items []map[string]any, prefix, location string) {
		for _, item := range items {
			key := mstr(item, "name")
			if key == "" {
				continue
			}
			kind := inferParameterKind(mstr(item, "format"))
			var def any
			if d, ok := valOrDefault(item); ok {
				def = officialTextToValue(kind, d)
			}
			secret := false
			if b, ok := item["isSecret"].(bool); ok {
				secret = b
			}
			if keyLooksSecret(key) {
				secret = true
			}
			fields = append(fields, map[string]any{
				"key":           prefix + key,
				"label":         key,
				"description":   mcpOptStr(item, "description"),
				"required":      kvIsRequired(item),
				"secret":        secret,
				"kind":          kind,
				"default_value": def,
				"placeholder":   mcpOptStr(item, "valueHint"),
				"enum_values":   []string{},
				"location":      location,
			})
		}
	}
	mk(officialKVList(transport["headers"]), "headers.", "header")
	mk(officialKVList(transport["variables"]), "variables.", "query")
	return fields
}

// ─── stdio package resolution ───────────────────────────────────────────────

func argumentKey(scope string, index int) string { return scope + "." + strconv.Itoa(index) }

func argumentIsRequired(arg map[string]any) bool {
	b, ok := arg["isRequired"].(bool)
	return ok && b
}

func resolveArgumentValue(arg map[string]any, scope string, index int, values map[string]any) (string, bool) {
	if text, ok := readParamText(values, argumentKey(scope, index)); ok {
		return text, true
	}
	return valOrDefault(arg)
}

func appendArgumentValue(target *[]any, arg map[string]any, scope string, index int, values map[string]any, enforce bool) error {
	kind := mstr(arg, "type")
	if kind == "" {
		kind = "positional"
	}
	resolved, ok := resolveArgumentValue(arg, scope, index, values)
	if kind == "named" {
		name := mstr(arg, "name")
		if name == "" {
			return nil
		}
		if ok {
			*target = append(*target, name, resolved)
			return nil
		}
		if enforce && argumentIsRequired(arg) {
			return mcpInvalidInput("missing required argument '" + name + "'")
		}
		return nil
	}
	if ok {
		*target = append(*target, resolved)
		return nil
	}
	if enforce && argumentIsRequired(arg) {
		name := mstr(arg, "name")
		if name == "" {
			name = "positional"
		}
		return mcpInvalidInput("missing required argument '" + name + "'")
	}
	return nil
}

func objList(v any) []map[string]any {
	arr, ok := asArr(v)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, it := range arr {
		if m, ok := asObj(it); ok {
			out = append(out, m)
		}
	}
	return out
}

func packageIdentifierWithVersion(pkg map[string]any, runtime string) string {
	identifier := mstr(pkg, "identifier")
	if identifier == "" {
		return ""
	}
	version := mstr(pkg, "version")
	if version == "" || version == "latest" {
		return identifier
	}
	if runtime == "uvx" {
		if mstr(pkg, "registryType") == "pypi" {
			return identifier + "==" + version
		}
		return identifier
	}
	if runtime == "npx" {
		if strings.Contains(identifier, "@") || strings.HasPrefix(identifier, "http") {
			return identifier
		}
		return identifier + "@" + version
	}
	return identifier
}

func officialStdioParameterFields(pkg map[string]any) []map[string]any {
	fields := []map[string]any{}
	argFields := func(args []map[string]any, scope, fallbackLabel string) {
		for index, arg := range args {
			kind := inferParameterKind(mstr(arg, "format"))
			label := mstr(arg, "name")
			if label == "" {
				label = fallbackLabel + " " + strconv.Itoa(index+1)
			}
			var def any
			if d, ok := valOrDefault(arg); ok {
				def = officialTextToValue(kind, d)
			}
			fields = append(fields, map[string]any{
				"key":           argumentKey(scope, index),
				"label":         label,
				"description":   mcpOptStr(arg, "description"),
				"required":      argumentIsRequired(arg),
				"secret":        false,
				"kind":          kind,
				"default_value": def,
				"placeholder":   mcpOptStr(arg, "valueHint"),
				"enum_values":   []string{},
				"location":      "arg",
			})
		}
	}
	argFields(objList(pkg["runtimeArguments"]), "runtime_arguments", "runtime arg")
	argFields(objList(pkg["packageArguments"]), "package_arguments", "package arg")
	for _, item := range officialKVList(pkg["environmentVariables"]) {
		key := mstr(item, "name")
		if key == "" {
			continue
		}
		kind := inferParameterKind(mstr(item, "format"))
		var def any
		if d, ok := valOrDefault(item); ok {
			def = officialTextToValue(kind, d)
		}
		secret := false
		if b, ok := item["isSecret"].(bool); ok {
			secret = b
		}
		if keyLooksSecret(key) {
			secret = true
		}
		fields = append(fields, map[string]any{
			"key":           "env." + key,
			"label":         key,
			"description":   mcpOptStr(item, "description"),
			"required":      kvIsRequired(item),
			"secret":        secret,
			"kind":          kind,
			"default_value": def,
			"placeholder":   mcpOptStr(item, "valueHint"),
			"enum_values":   []string{},
			"location":      "env",
		})
	}
	return fields
}

func resolveOfficialStdioPackage(pkg map[string]any, values map[string]any, enforce bool) (map[string]any, error) {
	runtime := mstr(pkg, "runtimeHint")
	if runtime == "" {
		switch mstr(pkg, "registryType") {
		case "npm":
			runtime = "npx"
		case "pypi":
			runtime = "uvx"
		}
	}
	if runtime == "" {
		return nil, mcpConfigInvalid("official package '" + mstr(pkg, "identifier") + "' missing runtime hint")
	}
	args := []any{}
	if runtime == "npx" {
		args = append(args, "-y")
	}
	runtimeArgs := objList(pkg["runtimeArguments"])
	for index, arg := range runtimeArgs {
		if err := appendArgumentValue(&args, arg, "runtime_arguments", index, values, enforce); err != nil {
			return nil, err
		}
	}
	pkgID := packageIdentifierWithVersion(pkg, runtime)
	if pkgID == "" {
		return nil, mcpConfigInvalid("official package identifier is empty")
	}
	args = append(args, pkgID)
	packageArgs := objList(pkg["packageArguments"])
	for index, arg := range packageArgs {
		if err := appendArgumentValue(&args, arg, "package_arguments", index, values, enforce); err != nil {
			return nil, err
		}
	}
	env := map[string]any{}
	for _, item := range officialKVList(pkg["environmentVariables"]) {
		key := mstr(item, "name")
		if key == "" {
			continue
		}
		text, ok := readParamText(values, "env."+key)
		if !ok {
			text, ok = valOrDefault(item)
		}
		if ok {
			env[key] = text
			continue
		}
		if enforce && kvIsRequired(item) {
			return nil, mcpInvalidInput("missing required environment variable '" + key + "'")
		}
	}
	spec := map[string]any{"type": "stdio", "command": runtime}
	if len(args) > 0 {
		spec["args"] = args
	}
	if len(env) > 0 {
		spec["env"] = env
	}
	return spec, nil
}

// ─── build options + resolve + fetch ────────────────────────────────────────

func buildOfficialInstallOptions(server map[string]any) ([]installOption, error) {
	options := []installOption{}
	for index, p := range objList(server["packages"]) {
		transport, _ := asObj(p["transport"])
		protocol := transportProtocol(mstr(transport, "type"))
		if protocol == "" {
			continue
		}
		if protocol == "stdio" {
			spec, err := resolveOfficialStdioPackage(p, map[string]any{}, false)
			if err != nil {
				continue // skip invalid stdio package, like upstream's warn+skip
			}
			runtime := mstr(p, "runtimeHint")
			if runtime == "" {
				runtime = "runtime"
			}
			options = append(options, installOption{
				id:          officialPackageOptionID(index, protocol),
				protocol:    protocol,
				label:       "stdio (" + runtime + ")",
				description: "Run package " + mstr(p, "identifier"),
				spec:        spec,
				parameters:  officialStdioParameterFields(p),
			})
			continue
		}
		spec, err := remoteSpecFromTransport(transport, map[string]any{}, false)
		if err != nil {
			continue
		}
		options = append(options, installOption{
			id:          officialPackageOptionID(index, protocol),
			protocol:    protocol,
			label:       protocol + " (package)",
			description: "Remote package " + mstr(p, "identifier"),
			spec:        spec,
			parameters:  officialRemoteParameterFields(transport),
		})
	}
	for index, transport := range objList(server["remotes"]) {
		protocol := transportProtocol(mstr(transport, "type"))
		if protocol == "" {
			continue
		}
		spec, err := remoteSpecFromTransport(transport, map[string]any{}, false)
		if err != nil {
			continue
		}
		var desc any
		if u := mstr(transport, "url"); u != "" {
			desc = u
		}
		options = append(options, installOption{
			id:          officialRemoteOptionID(index, protocol),
			protocol:    protocol,
			label:       protocol + " (remote)",
			description: desc,
			spec:        spec,
			parameters:  officialRemoteParameterFields(transport),
		})
	}
	if len(options) == 0 {
		return nil, mcpNotFound("official MCP server '" + mstr(server, "name") + "' does not expose an installable transport")
	}
	return options, nil
}

func resolveOfficialInstallSpec(server map[string]any, sel installSelection) (map[string]any, error) {
	options, err := buildOfficialInstallOptions(server)
	if err != nil {
		return nil, err
	}
	selected, err := selectOptionFromList(options, sel)
	if err != nil {
		return nil, err
	}
	source, index, ok := parseOfficialOptionID(selected.id)
	if ok && source == "package" {
		pkgs := objList(server["packages"])
		if index < 0 || index >= len(pkgs) {
			return nil, mcpNotFound("selected package option index is out of range: " + strconv.Itoa(index))
		}
		pkg := pkgs[index]
		if normalizeProtocolValue(selected.protocol) == "stdio" {
			return resolveOfficialStdioPackage(pkg, sel.parameterValues, true)
		}
		transport, _ := asObj(pkg["transport"])
		return remoteSpecFromTransport(transport, sel.parameterValues, true)
	}
	if ok && source == "remote" {
		remotes := objList(server["remotes"])
		if index < 0 || index >= len(remotes) {
			return nil, mcpNotFound("selected remote option index is out of range: " + strconv.Itoa(index))
		}
		return remoteSpecFromTransport(remotes[index], sel.parameterValues, true)
	}
	return nil, mcpInvalidInput("unsupported official install option '" + selected.id + "'")
}

// fetchOfficialServerDetail GETs the latest version of a server; the response is
// the {server, _meta} envelope.
func fetchOfficialServerDetail(serverName string) (map[string]any, error) {
	u := "https://registry.modelcontextprotocol.io/v0.1/servers/" + url.PathEscape(serverName) + "/versions/latest"
	resp, err := mcpMarketGet("failed to fetch official MCP server detail", u, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, mcpNetwork("official MCP server detail request failed: HTTP " + strconv.Itoa(resp.StatusCode))
	}
	payload, err := parseJSONValueResponse(resp, "failed to parse official MCP server detail")
	if err != nil {
		return nil, err
	}
	obj, ok := asObj(payload)
	if !ok {
		return nil, mcpNetwork("failed to parse official MCP server detail: not an object")
	}
	return obj, nil
}
