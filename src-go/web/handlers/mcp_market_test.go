package handlers

import (
	"reflect"
	"testing"

	"yoyocode/src-go/web"
)

func TestTransportProtocol(t *testing.T) {
	cases := map[string]string{"stdio": "stdio", "streamable-http": "http", "sse": "sse", "bogus": ""}
	for in, want := range cases {
		if got := transportProtocol(in); got != want {
			t.Errorf("transportProtocol(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOfficialServerProtocols(t *testing.T) {
	server := map[string]any{
		"remotes":  []any{map[string]any{"type": "streamable-http"}, map[string]any{"type": "sse"}},
		"packages": []any{map[string]any{"transport": map[string]any{"type": "stdio"}}},
	}
	got := officialServerProtocols(server)
	want := []string{"http", "sse", "stdio"} // sorted
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("protocols = %v, want %v", got, want)
	}
}

func TestOfficialEntryToItem(t *testing.T) {
	entry := map[string]any{
		"server": map[string]any{
			"name":        "io.github.foo/bar",
			"title":       "  Bar Server  ",
			"description": "",
			"websiteUrl":  "",
			"repository":  map[string]any{"url": "https://github.com/foo/bar"},
			"remotes":     []any{map[string]any{"type": "http", "url": "https://r"}},
			"version":     "1.2.3",
			"icons":       []any{map[string]any{"src": ""}, map[string]any{"src": "https://icon.png"}},
		},
		"_meta": map[string]any{
			"io.modelcontextprotocol.registry/official": map[string]any{"status": "active"},
		},
	}
	item := officialEntryToItem(entry)
	if item["server_id"] != "io.github.foo/bar" {
		t.Errorf("server_id = %v", item["server_id"])
	}
	if item["name"] != "Bar Server" {
		t.Errorf("name = %v (should use trimmed title)", item["name"])
	}
	if item["description"] != "No description" {
		t.Errorf("description = %v (empty should fall back)", item["description"])
	}
	if item["homepage"] != "https://github.com/foo/bar" {
		t.Errorf("homepage = %v (should fall back to repo url)", item["homepage"])
	}
	if item["remote"] != true {
		t.Errorf("remote = %v", item["remote"])
	}
	if item["verified"] != true {
		t.Errorf("verified = %v", item["verified"])
	}
	if item["icon_url"] != "https://icon.png" {
		t.Errorf("icon_url = %v (should pick first non-empty)", item["icon_url"])
	}
	if item["latest_version"] != "1.2.3" {
		t.Errorf("latest_version = %v", item["latest_version"])
	}
	if item["provider_id"] != "official_registry" {
		t.Errorf("provider_id = %v", item["provider_id"])
	}
	// Official items carry null for the smithery-only fields.
	for _, k := range []string{"owner", "namespace", "downloads", "score", "is_deployed"} {
		if item[k] != nil {
			t.Errorf("%s should be nil for official, got %v", k, item[k])
		}
	}
}

func TestSmitheryItemFromSummary(t *testing.T) {
	item := smitheryItemFromSummary(map[string]any{
		"qualifiedName": "foo/bar",
		"displayName":   "Foo Bar",
		"description":   "",
		"iconUrl":       " https://i.png ",
		"owner":         "foo",
		"remote":        true,
		"verified":      false,
		"useCount":      float64(42),
		"score":         float64(0.9),
		"isDeployed":    true,
	})
	if item["server_id"] != "foo/bar" || item["name"] != "Foo Bar" {
		t.Errorf("id/name = %v / %v", item["server_id"], item["name"])
	}
	if item["description"] != "No description" {
		t.Errorf("description fallback = %v", item["description"])
	}
	if item["icon_url"] != "https://i.png" {
		t.Errorf("icon_url trim = %v", item["icon_url"])
	}
	protocols := item["protocols"].([]string)
	if len(protocols) != 1 || protocols[0] != "http" {
		t.Errorf("remote protocols = %v, want [http]", protocols)
	}
	if item["downloads"] != float64(42) || item["score"] != float64(0.9) || item["is_deployed"] != true {
		t.Errorf("passthrough fields wrong: %v", item)
	}
	// A non-remote server gets an empty protocol array (not nil).
	item2 := smitheryItemFromSummary(map[string]any{"qualifiedName": "x", "displayName": "X", "remote": false})
	if p, ok := item2["protocols"].([]string); !ok || len(p) != 0 {
		t.Errorf("non-remote protocols = %v, want []", item2["protocols"])
	}
}

func TestSearchMarketplaceUnknownProvider(t *testing.T) {
	// Dispatch rejects an unknown provider before any network call.
	_, err := mcpSearchMarketplace(&web.Context{Body: map[string]any{"providerId": "nope"}})
	if err == nil {
		t.Fatal("expected unsupported-provider error")
	}
	// Missing provider id is a required-field error.
	if _, err := mcpSearchMarketplace(&web.Context{Body: map[string]any{}}); err == nil {
		t.Fatal("expected missing providerId error")
	}
}
