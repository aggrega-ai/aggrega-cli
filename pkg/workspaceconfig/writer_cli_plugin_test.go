// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// placeholderPin returns a pin for a CLI plugin nobody installed: a well-formed
// content digest, "sha256:" and 64 lowercase hexadecimal digits, the spelling the
// install writer records and the configuration schema accepts. Every write these
// tests make is schema-gated and every read validates the file, so a pin spelled
// any other way is refused before the writer or cascade mechanics under test are
// reached. The digit it repeats keeps each pin distinct from the others; what a
// digest is plays no part in what these tests assert.
func placeholderPin(fill byte) string {
	return "sha256:" + strings.Repeat(string(fill), 64)
}

// The pins the writer and cascade tests write, one per pin, each distinct.
var (
	digestA       = placeholderPin('a')
	digestB       = placeholderPin('b')
	digestC       = placeholderPin('c')
	digestV1      = placeholderPin('1')
	digestV2      = placeholderPin('2')
	digestV2b     = placeholderPin('3')
	digestBar     = placeholderPin('d')
	rootDigest    = placeholderPin('e')
	projectDigest = placeholderPin('f')
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

	if err := UpsertCLIPluginRef(path, "oci://hub.aggrega.ai/plugin/cli/greet:1.0.0", digestA); err != nil {
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
	if len(cfg.Plugins.CLI) != 1 || cfg.Plugins.CLI[0].URI != "oci://hub.aggrega.ai/plugin/cli/greet:1.0.0" || cfg.Plugins.CLI[0].Digest != digestA {
		t.Fatalf("pin not written; got %+v", cfg.Plugins.CLI)
	}

	// Re-pinning the same uri updates the digest in place, not a duplicate.
	if err := UpsertCLIPluginRef(path, "oci://hub.aggrega.ai/plugin/cli/greet:1.0.0", digestB); err != nil {
		t.Fatalf("UpsertCLIPluginRef (re-pin): %v", err)
	}
	rw, err = Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after re-pin: %v", err)
	}
	if got := rw.Config().Plugins.CLI; len(got) != 1 || got[0].Digest != digestB {
		t.Fatalf("re-pin should update the one entry's digest in place; got %+v", got)
	}

	// A distinct uri appends a second pin.
	if err := UpsertCLIPluginRef(path, "oci://hub.aggrega.ai/plugin/cli/other:2.0.0", digestC); err != nil {
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

// TestUpsertCLIPluginRef_UpgradesInPlace proves that adding a newer version of
// an already-added CLI plugin upgrades its one entry in place — replacing both
// the uri and the digest at the preserved list position — rather than appending
// a second entry for the same plugin. It also proves same-version re-add is
// idempotent, a distinct plugin still appends, and a registry reference carrying
// a host:port collapses correctly (the port colon is not mistaken for a version
// tag).
func TestUpsertCLIPluginRef_UpgradesInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := os.WriteFile(path, []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	load := func(what string) []CLIPluginRef {
		t.Helper()
		rw, err := Resolve(dir, "")
		if err != nil {
			t.Fatalf("resolve after %s: %v", what, err)
		}
		return rw.Config().Plugins.CLI
	}

	// Upgrade collapses to one entry: a newer version of the same plugin
	// replaces both fields of the single entry, keeping its list position.
	if err := UpsertCLIPluginRef(path, "oci://hub.aggrega.ai/plugin/cli/moo:0.2.1", digestV1); err != nil {
		t.Fatalf("UpsertCLIPluginRef (add v0.2.1): %v", err)
	}
	if err := UpsertCLIPluginRef(path, "oci://hub.aggrega.ai/plugin/cli/moo:0.2.2", digestV2); err != nil {
		t.Fatalf("UpsertCLIPluginRef (upgrade to v0.2.2): %v", err)
	}
	got := load("upgrade")
	if len(got) != 1 {
		t.Fatalf("upgrade should collapse to one entry; got %+v", got)
	}
	if got[0].URI != "oci://hub.aggrega.ai/plugin/cli/moo:0.2.2" {
		t.Errorf("upgrade should replace the uri in place; got %q", got[0].URI)
	}
	if got[0].Digest != digestV2 {
		t.Errorf("upgrade should replace the digest in place; got %q", got[0].Digest)
	}

	// Idempotent same-version re-add: re-pins the digest of the one entry.
	if err := UpsertCLIPluginRef(path, "oci://hub.aggrega.ai/plugin/cli/moo:0.2.2", digestV2b); err != nil {
		t.Fatalf("UpsertCLIPluginRef (re-add v0.2.2): %v", err)
	}
	got = load("re-add")
	if len(got) != 1 {
		t.Fatalf("same-version re-add should stay at one entry; got %+v", got)
	}
	if got[0].URI != "oci://hub.aggrega.ai/plugin/cli/moo:0.2.2" {
		t.Errorf("same-version re-add should leave the uri unchanged; got %q", got[0].URI)
	}
	if got[0].Digest != digestV2b {
		t.Errorf("same-version re-add should re-pin the digest; got %q", got[0].Digest)
	}

	// A distinct plugin appends a second entry.
	if err := UpsertCLIPluginRef(path, "oci://hub.aggrega.ai/plugin/cli/bar:1.0.0", digestBar); err != nil {
		t.Fatalf("UpsertCLIPluginRef (add distinct bar): %v", err)
	}
	got = load("append distinct")
	if len(got) != 2 {
		t.Fatalf("a distinct plugin should append; got %+v", got)
	}
	// The upgraded moo entry keeps index 0; bar lands at index 1.
	if got[0].URI != "oci://hub.aggrega.ai/plugin/cli/moo:0.2.2" {
		t.Errorf("original entry should keep its list position; got %q at index 0", got[0].URI)
	}
	if got[1].URI != "oci://hub.aggrega.ai/plugin/cli/bar:1.0.0" {
		t.Errorf("distinct plugin should append at index 1; got %q", got[1].URI)
	}

	// OCI-safety: a host:port reference collapses on version-less identity — the
	// :5000 port colon must not be read as a version, so two versions of the same
	// plugin behind a ported registry are the same entry.
	freshDir := t.TempDir()
	freshPath := filepath.Join(freshDir, ConfigFilename)
	if err := os.WriteFile(freshPath, []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}
	if err := UpsertCLIPluginRef(freshPath, "oci://reg.example.com:5000/plugin/cli/zoo:1.0.0", digestA); err != nil {
		t.Fatalf("UpsertCLIPluginRef (ported add zoo:1.0.0): %v", err)
	}
	if err := UpsertCLIPluginRef(freshPath, "oci://reg.example.com:5000/plugin/cli/zoo:2.0.0", digestB); err != nil {
		t.Fatalf("UpsertCLIPluginRef (ported upgrade zoo:2.0.0): %v", err)
	}
	rw, err := Resolve(freshDir, "")
	if err != nil {
		t.Fatalf("resolve fresh: %v", err)
	}
	pg := rw.Config().Plugins.CLI
	if len(pg) != 1 {
		t.Fatalf("host:port versions should collapse to one entry; got %+v", pg)
	}
	if pg[0].URI != "oci://reg.example.com:5000/plugin/cli/zoo:2.0.0" {
		t.Errorf("ported upgrade should replace the uri in place; got %q", pg[0].URI)
	}
	if pg[0].Digest != digestB {
		t.Errorf("ported upgrade should replace the digest in place; got %q", pg[0].Digest)
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
		[]byte("workspace: {}\nplugins:\n  cli:\n    - uri: oci://hub/plugin/cli/root-tool:1.0.0\n      digest: "+rootDigest+"\n"), 0o644); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	nested := filepath.Join(root, "project")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, ConfigFilename),
		[]byte("plugins:\n  cli:\n    - uri: oci://hub/plugin/cli/project-tool:2.0.0\n      digest: "+projectDigest+"\n"), 0o644); err != nil {
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
