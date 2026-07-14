// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUpsertPluginSocketRef_PreservesExistingContent proves the config-socket
// writer adds a plugins.<socket> attachment into an existing workspace config
// without disturbing the other keys, is idempotent (re-adding the same URI does
// not duplicate it), appends a distinct URI, and produces a schema-valid file
// the resolver accepts.
func TestUpsertPluginSocketRef_PreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := os.WriteFile(path, []byte(`workspace: {}
offline: true
default_registry: hub.aggrega.ai
`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := UpsertPluginSocketRef(path, "mutator", "mutator/tag-injector:1.0.0"); err != nil {
		t.Fatalf("UpsertPluginSocketRef (add): %v", err)
	}

	// Existing unrelated content must survive byte-for-byte.
	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after add: %v", err)
	}
	cfg := rw.Config()
	if cfg.DefaultRegistry != "hub.aggrega.ai" || !cfg.Offline {
		t.Errorf("existing keys not preserved: default_registry=%q offline=%v", cfg.DefaultRegistry, cfg.Offline)
	}
	if len(cfg.Plugins.Mutator) != 1 || cfg.Plugins.Mutator[0].URI != "mutator/tag-injector:1.0.0" {
		t.Fatalf("attachment not written; got %+v", cfg.Plugins.Mutator)
	}

	// Idempotent: re-adding the identical URI must not duplicate it.
	if err := UpsertPluginSocketRef(path, "mutator", "mutator/tag-injector:1.0.0"); err != nil {
		t.Fatalf("UpsertPluginSocketRef (re-add): %v", err)
	}
	rw, err = Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after re-add: %v", err)
	}
	if got := rw.Config().Plugins.Mutator; len(got) != 1 {
		t.Errorf("re-adding the same URI must not duplicate it; got %+v", got)
	}

	// A distinct URI appends a second attachment.
	if err := UpsertPluginSocketRef(path, "mutator", "mutator/other:2.0.0"); err != nil {
		t.Fatalf("UpsertPluginSocketRef (append distinct): %v", err)
	}
	rw, err = Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after append: %v", err)
	}
	if got := rw.Config().Plugins.Mutator; len(got) != 2 {
		t.Fatalf("a distinct URI must append; got %+v", got)
	}
}

// TestUpsertPluginSocketRef_CreatesMissingFile proves the writer creates a fresh,
// schema-valid config (with a workspace marker) when no file exists yet.
func TestUpsertPluginSocketRef_CreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := UpsertPluginSocketRef(path, "policy", "policy/deny-gate:1.0.0"); err != nil {
		t.Fatalf("UpsertPluginSocketRef: %v", err)
	}
	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve created file: %v", err)
	}
	got := rw.Config().Plugins.Policy
	if len(got) != 1 || got[0].URI != "policy/deny-gate:1.0.0" {
		t.Fatalf("attachment not written to fresh file; got %+v", got)
	}
}

// TestUpsertPluginSocketRef_RejectsUnknownSocket proves an unknown socket key is
// refused by the merged-document schema validation rather than silently written,
// so the writer stays a loud guardrail against a malformed plugins block.
func TestUpsertPluginSocketRef_RejectsUnknownSocket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := UpsertPluginSocketRef(path, "bogus", "bogus/x:1.0.0"); err == nil {
		t.Fatal("UpsertPluginSocketRef accepted an unknown socket key; the schema gate must reject it")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("a rejected write left a config file on disk; the malformed document must never reach disk")
	}
}
