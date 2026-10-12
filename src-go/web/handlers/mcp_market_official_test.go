package handlers

import (
	"strings"
	"testing"
)

func sampleOfficialServer() map[string]any {
	return map[string]any{
		"name": "io.github.foo/bar",
		"packages": []any{
			map[string]any{
				"registryType":     "npm",
				"identifier":       "foobar",
				"version":          "1.2.3",
				"runtimeHint":      "npx",
				"packageArguments": []any{map[string]any{"type": "positional", "value": "serve"}},
				"environmentVariables": []any{
					map[string]any{"name": "API_KEY", "isRequired": true, "isSecret": true},
				},
				"transport": map[string]any{"type": "stdio"},
			},
		},
		"remotes": []any{
			map[string]any{
				"type":      "streamable-http",
				"url":       "https://api/{region}",
				"variables": []any{map[string]any{"name": "region", "isRequired": true}},
				"headers":   []any{map[string]any{"name": "Authorization", "isRequired": true, "isSecret": true}},
			},
		},
	}
}

func TestBuildOfficialInstallOptions(t *testing.T) {
	opts, err := buildOfficialInstallOptions(sampleOfficialServer())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(opts) != 2 {
		t.Fatalf("want 2 options, got %d: %+v", len(opts), opts)
	}
	byID := map[string]installOption{}
	for _, o := range opts {
		byID[o.id] = o
	}
	stdio, ok := byID["official:package:0:stdio"]
	if !ok {
		t.Fatalf("no stdio package option: %v", byID)
	}
	if stdio.spec["command"] != "npx" {
		t.Errorf("stdio command = %v", stdio.spec["command"])
	}
	args := stdio.spec["args"].([]any)
	if len(args) != 3 || args[0] != "-y" || args[1] != "foobar@1.2.3" || args[2] != "serve" {
		t.Errorf("stdio args = %v", args)
	}
	// stdio parameters expose the env var and the package arg.
	var hasEnv bool
	for _, p := range stdio.parameters {
		if p["key"] == "env.API_KEY" {
			hasEnv = true
			if p["secret"] != true || p["location"] != "env" || p["required"] != true {
				t.Errorf("env param = %v", p)
			}
		}
	}
	if !hasEnv {
		t.Errorf("missing env.API_KEY parameter: %v", stdio.parameters)
	}
	remote, ok := byID["official:remote:0:http"]
	if !ok || remote.protocol != "http" {
		t.Fatalf("no remote http option: %v", byID)
	}
}

func TestResolveOfficialStdioWithValues(t *testing.T) {
	server := sampleOfficialServer()
	spec, err := resolveOfficialInstallSpec(server, installSelection{
		optionID:        "official:package:0:stdio",
		parameterValues: map[string]any{"env.API_KEY": "K"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec["env"].(map[string]any)["API_KEY"] != "K" {
		t.Errorf("env = %v", spec["env"])
	}
	// Missing the required env var under install enforcement errors.
	if _, err := resolveOfficialInstallSpec(server, installSelection{optionID: "official:package:0:stdio"}); err == nil {
		t.Error("expected missing required env error")
	}
}

func TestResolveOfficialRemoteWithValues(t *testing.T) {
	server := sampleOfficialServer()
	spec, err := resolveOfficialInstallSpec(server, installSelection{
		optionID: "official:remote:0:http",
		parameterValues: map[string]any{
			"variables.region":      "us",
			"headers.Authorization": "Bearer x",
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	u := spec["url"].(string)
	if u != "https://api/us" {
		t.Errorf("url = %q, want brace-substituted https://api/us", u)
	}
	if spec["headers"].(map[string]any)["Authorization"] != "Bearer x" {
		t.Errorf("headers = %v", spec["headers"])
	}
	// Missing required variable under enforcement errors.
	if _, err := resolveOfficialInstallSpec(server, installSelection{optionID: "official:remote:0:http"}); err == nil {
		t.Error("expected missing required variable error")
	}
}

func TestPackageIdentifierWithVersion(t *testing.T) {
	// Scoped npm package keeps identifier as-is (the @ already pins scope).
	if got := packageIdentifierWithVersion(map[string]any{"identifier": "@foo/bar", "version": "1.2.3", "registryType": "npm"}, "npx"); got != "@foo/bar" {
		t.Errorf("scoped npx = %q", got)
	}
	// pypi + uvx appends ==version.
	if got := packageIdentifierWithVersion(map[string]any{"identifier": "pkg", "version": "2.0", "registryType": "pypi"}, "uvx"); got != "pkg==2.0" {
		t.Errorf("pypi uvx = %q", got)
	}
	// version "latest" is dropped.
	if got := packageIdentifierWithVersion(map[string]any{"identifier": "pkg", "version": "latest", "registryType": "npm"}, "npx"); got != "pkg" {
		t.Errorf("latest = %q", got)
	}
}

func TestOfficialKVListAcceptsArrayAndMap(t *testing.T) {
	arr := officialKVList([]any{map[string]any{"name": "A", "value": "1"}, map[string]any{"value": "noname"}})
	if len(arr) != 1 || arr[0]["name"] != "A" {
		t.Errorf("array form = %v (nameless entry must drop)", arr)
	}
	m := officialKVList(map[string]any{"B": "2", "C": map[string]any{"isSecret": true}})
	byName := map[string]map[string]any{}
	for _, kv := range m {
		byName[kv["name"].(string)] = kv
	}
	if byName["B"]["value"] != "2" || byName["C"]["isSecret"] != true {
		t.Errorf("map form = %v", m)
	}
}

func TestContainsUnresolvedPlaceholder(t *testing.T) {
	if !containsUnresolvedPlaceholder("{token}") || containsUnresolvedPlaceholder("plain") {
		t.Error("placeholder detection wrong")
	}
	// A kv whose only value is a placeholder is treated as required with no default.
	kv := map[string]any{"name": "X", "value": "{API_KEY}"}
	if _, ok := valOrDefault(kv); ok {
		t.Error("placeholder value should not be a usable default")
	}
	if !kvIsRequired(kv) {
		t.Error("placeholder-only kv should be required")
	}
	_ = strings.TrimSpace
}
