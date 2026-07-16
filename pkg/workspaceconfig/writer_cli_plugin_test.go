// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUpsertCLIPluginRef_PinReAndCompose proves the plugins.cli writer adds a
// {uri, digest} pin into an existing workspace config without disturbing other
// keys, re-pins an existing uri in place rather than duplicating it, appends a
// distinct uri, and produces a schema-valid file the resolver accepts.
func TestUpsertCLIPluginRef_PinReAndCompose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := os.WriteFile(path, []byte("workspace: {}\noffline: true\ndefault_registry: hub.aggrega.ai\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := UpsertCLIPluginRef(path, "oci://hub.aggrega.ai/plugin/cli/greet:1.0.0", "digest-a"); err != nil {
		t.Fatalf("UpsertCLIPluginRef (add): %v", err)
	}

	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after add: %v", err)
	}
	cfg := rw.Config()
	if cfg.DefaultRegistry != "hub.aggrega.ai" || !cfg.Offline {
		t.Errorf("existing keys not preserved: default_registry=%q offline=%v", cfg.DefaultRegistry, cfg.Offline)
	}
	if len(cfg.Plugins.CLI) != 1 || cfg.Plugins.CLI[0].URI != "oci://hub.aggrega.ai/plugin/cli/greet:1.0.0" || cfg.Plugins.CLI[0].Digest != "digest-a" {
		t.Fatalf("pin not written; got %+v", cfg.Plugins.CLI)
	}

	// Re-pinning the same uri updates the digest in place, not a duplicate.
	if err := UpsertCLIPluginRef(path, "oci://hub.aggrega.ai/plugin/cli/greet:1.0.0", "digest-b"); err != nil {
		t.Fatalf("UpsertCLIPluginRef (re-pin): %v", err)
	}
	rw, err = Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after re-pin: %v", err)
	}
	if got := rw.Config().Plugins.CLI; len(got) != 1 || got[0].Digest != "digest-b" {
		t.Fatalf("re-pin should update the one entry's digest in place; got %+v", got)
	}

	// A distinct uri appends a second pin.
	if err := UpsertCLIPluginRef(path, "oci://hub.aggrega.ai/plugin/cli/other:2.0.0", "digest-c"); err != nil {
		t.Fatalf("UpsertCLIPluginRef (append distinct): %v", err)
	}
	rw, err = Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after append: %v", err)
	}
	if got := rw.Config().Plugins.CLI; len(got) != 2 {
		t.Fatalf("a distinct uri should append; got %+v", got)
	}
}

// TestCLIPluginRefComposesAcrossCascade proves the plugins.cli list composes
// rather than whole-replaces across the config cascade: a CLI pin in an ancestor
// (root) config and one in a nested (project) config both survive resolution
// from the nested directory, in cascade order (ancestor first). This is the
// behavior that makes a global CLI plugin register everywhere while a workspace
// one adds to it rather than shadowing it.
func TestCLIPluginRefComposesAcrossCascade(t *testing.T) {
	root := t.TempDir()
	// Ancestor config anchors the workspace root and pins one CLI plugin.
	if err := os.WriteFile(filepath.Join(root, ConfigFilename),
		[]byte("workspace: {}\nplugins:\n  cli:\n    - uri: oci://hub/plugin/cli/root-tool:1.0.0\n      digest: root-digest\n"), 0o644); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	nested := filepath.Join(root, "project")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, ConfigFilename),
		[]byte("plugins:\n  cli:\n    - uri: oci://hub/plugin/cli/project-tool:2.0.0\n      digest: project-digest\n"), 0o644); err != nil {
		t.Fatalf("seed nested: %v", err)
	}

	rw, err := Resolve(nested, "")
	if err != nil {
		t.Fatalf("resolve nested: %v", err)
	}
	got := rw.Config().Plugins.CLI
	if len(got) != 2 {
		t.Fatalf("both CLI pins should compose across the cascade; got %+v", got)
	}
	if got[0].URI != "oci://hub/plugin/cli/root-tool:1.0.0" || got[1].URI != "oci://hub/plugin/cli/project-tool:2.0.0" {
		t.Fatalf("cascade compose order should be ancestor-first; got %+v", got)
	}

	// Resolving from the root alone sees only the root pin — the nested project's
	// CLI plugin does not leak upward.
	rootOnly, err := Resolve(root, "")
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	if g := rootOnly.Config().Plugins.CLI; len(g) != 1 || g[0].URI != "oci://hub/plugin/cli/root-tool:1.0.0" {
		t.Fatalf("root resolution should carry only the root pin; got %+v", g)
	}
}
