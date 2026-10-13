package handlers

// acp_pi_test.go covers the BYO-pi command resolution behind
// acp_validate_pi_command.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePiCommandPath(t *testing.T) {
	if _, ok := resolvePiCommandPath(""); ok {
		t.Error("empty command should not resolve")
	}
	if _, ok := resolvePiCommandPath("   "); ok {
		t.Error("blank command should not resolve")
	}
	if _, ok := resolvePiCommandPath(filepath.Join(t.TempDir(), "definitely-nope")); ok {
		t.Error("a missing path should not resolve")
	}
	// A real executable file resolves.
	exe := filepath.Join(t.TempDir(), "mybin")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := resolvePiCommandPath(exe)
	if !ok {
		t.Fatal("an existing executable should resolve")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("resolved path = %q, want absolute", got)
	}
	// A directory does not resolve.
	if _, ok := resolvePiCommandPath(t.TempDir()); ok {
		t.Error("a directory should not resolve")
	}
}

func TestProbePiVersionMissing(t *testing.T) {
	if v := probePiVersion(filepath.Join(t.TempDir(), "nope")); v != "" {
		t.Errorf("probe of a missing binary = %q, want empty", v)
	}
}
