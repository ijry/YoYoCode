package handlers

import (
	"strings"
	"testing"

	"yoyocode/src-go/web"
)

func sampleSmitheryDetail() map[string]any {
	return map[string]any{
		"qualifiedName": "foo/bar",
		"displayName":   "Foo",
		"deploymentUrl": "https://api.smithery.ai/@foo/bar/mcp",
		"remote":        true,
		"connections": []any{
			map[string]any{
				"type":          "http",
				"deploymentUrl": "https://api.smithery.ai/@foo/bar/mcp",
				"configSchema": map[string]any{
					"type":     "object",
					"required": []any{"apiKey"},
					"properties": map[string]any{
						"apiKey": map[string]any{"type": "string", "description": "key", "writeOnly": true},
						"region": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}

func TestProtocolPriorityAndDefault(t *testing.T) {
	opts := []installOption{{id: "a", protocol: "sse"}, {id: "b", protocol: "stdio"}, {id: "c", protocol: "http"}}
	if d := selectDefaultInstallOption(opts); d == nil || d.id != "b" {
		t.Fatalf("default = %v, want stdio option b", d)
	}
}

func TestBuildSmitheryInstallOptions(t *testing.T) {
	opts, err := buildSmitheryInstallOptions(sampleSmitheryDetail())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("want 1 option, got %d", len(opts))
	}
	o := opts[0]
	if o.id != "smithery:connection:0:http" || o.protocol != "http" {
		t.Fatalf("option id/protocol = %s / %s", o.id, o.protocol)
	}
	// Two parameters: apiKey (secret -> header) and region (query).
	byKey := map[string]map[string]any{}
	for _, p := range o.parameters {
		byKey[p["key"].(string)] = p
	}
	if byKey["apiKey"]["secret"] != true || byKey["apiKey"]["location"] != "header" {
		t.Errorf("apiKey param = %v", byKey["apiKey"])
	}
	if byKey["apiKey"]["required"] != true {
		t.Errorf("apiKey should be required")
	}
	if byKey["region"]["location"] != "query" {
		t.Errorf("region location = %v", byKey["region"]["location"])
	}
}

func TestResolveSmitheryConnectionSpec(t *testing.T) {
	detail := sampleSmitheryDetail()
	conn := detail["connections"].([]any)[0].(map[string]any)
	spec, err := resolveSmitheryConnectionSpec(conn, mstr(detail, "deploymentUrl"),
		map[string]any{"apiKey": "K", "region": "us"}, true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec["type"] != "http" {
		t.Errorf("type = %v", spec["type"])
	}
	if u, _ := spec["url"].(string); !strings.Contains(u, "region=us") {
		t.Errorf("url missing query param: %v", spec["url"])
	}
	headers, _ := spec["headers"].(map[string]any)
	if headers["apiKey"] != "K" {
		t.Errorf("apiKey header = %v", headers["apiKey"])
	}
	// Missing required apiKey under enforcement is an error.
	if _, err := resolveSmitheryConnectionSpec(conn, mstr(detail, "deploymentUrl"), map[string]any{"region": "us"}, true); err == nil {
		t.Error("expected missing-required error")
	}
}

func TestResolveSmitheryInstallSpecSelection(t *testing.T) {
	detail := sampleSmitheryDetail()
	spec, err := resolveSmitheryInstallSpec(detail, installSelection{
		optionID:        "smithery:connection:0:http",
		parameterValues: map[string]any{"apiKey": "K"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec["type"] != "http" || spec["headers"].(map[string]any)["apiKey"] != "K" {
		t.Fatalf("spec = %v", spec)
	}
}

func TestMarketDetailInstallOfficialStaged(t *testing.T) {
	mcpTestHome(t)
	ctx := func(b map[string]any) *web.Context { return &web.Context{Body: b} }
	if _, err := mcpGetMarketplaceServerDetail(ctx(map[string]any{"providerId": "official_registry", "serverId": "x"})); err == nil {
		t.Error("expected staged error for official detail")
	}
	// Official install without an override is staged.
	if _, err := mcpInstallFromMarketplace(ctx(map[string]any{
		"providerId": "official_registry", "serverId": "x", "apps": []any{"gemini"},
	})); err == nil {
		t.Error("expected staged error for official install")
	}
}

func TestInstallWithSpecOverride(t *testing.T) {
	mcpTestHome(t)
	ctx := &web.Context{Body: map[string]any{
		"providerId":   "official_registry",
		"serverId":     "myserver",
		"apps":         []any{"gemini", "cline"},
		"specOverride": map[string]any{"type": "http", "url": "https://api"},
	}}
	res, err := mcpInstallFromMarketplace(ctx)
	if err != nil {
		t.Fatalf("install override: %v", err)
	}
	out := res.(map[string]any)
	if out["id"] != "myserver" {
		t.Fatalf("installed id = %v", out["id"])
	}
	apps := out["apps"].([]McpAppType)
	if len(apps) != 2 || apps[0] != "gemini" || apps[1] != "cline" {
		t.Fatalf("apps = %v, want [gemini cline] by enum order", apps)
	}
}
