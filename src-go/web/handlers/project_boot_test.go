package handlers_test

// project_boot: package-manager detection shape, project-name validation
// (which short-circuits before shelling out), and the HyperFrames skill
// detect/install contracts. The create/install paths that invoke external
// CLIs are only exercised through their validation guards.

import "testing"

func TestProjectBoot(t *testing.T) {
	srv := newTestServer(t)

	// detect_package_manager echoes the name and reports a boolean installed.
	_, pm := call(t, srv, "detect_package_manager", map[string]any{"name": "pnpm"})
	if pm["name"] != "pnpm" {
		t.Fatalf("pm name = %v", pm["name"])
	}
	if _, ok := pm["installed"].(bool); !ok {
		t.Fatalf("pm installed not a bool: %v", pm)
	}
	if _, ok := pm["version"]; !ok {
		t.Fatalf("pm missing version key: %v", pm)
	}

	// create_shadcn_project validation: bad project name.
	if code, out := call(t, srv, "create_shadcn_project", map[string]any{
		"projectName": "../escape", "template": "next", "targetDir": "/tmp", "packageManager": "npm",
	}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("bad project name: %d %v", code, out)
	}
	// leading dash rejected.
	if code, out := call(t, srv, "create_shadcn_project", map[string]any{
		"projectName": "-rf", "template": "next", "targetDir": "/tmp",
	}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("dash project name: %d %v", code, out)
	}
	// missing template.
	if code, out := call(t, srv, "create_shadcn_project", map[string]any{
		"projectName": "ok", "template": "", "targetDir": "/tmp",
	}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("missing template: %d %v", code, out)
	}

	// create_hyperframes_project validation: missing target dir.
	if code, out := call(t, srv, "create_hyperframes_project", map[string]any{
		"projectName": "vid", "example": "blank", "targetDir": "",
	}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("missing target dir: %d %v", code, out)
	}

	// detect_hyperframes_skills → six agents, not installed.
	_, agents := callArr(t, srv, "detect_hyperframes_skills", map[string]any{})
	if len(agents) != 6 {
		t.Fatalf("hyperframes agents = %d, want 6", len(agents))
	}
	a0 := agents[0].(map[string]any)
	if a0["agent"] != "claude-code" || a0["installed"] != false {
		t.Fatalf("first agent = %v", a0)
	}

	// install_hyperframes_skills validation: empty list + unsupported agent.
	if code, out := call(t, srv, "install_hyperframes_skills", map[string]any{"agents": []any{}}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("empty agents: %d %v", code, out)
	}
	if code, out := call(t, srv, "install_hyperframes_skills", map[string]any{"agents": []any{"not-an-agent"}}); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("unsupported agent: %d %v", code, out)
	}
}
