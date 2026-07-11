// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUpsertProviderBinding_PreservesExistingContent proves the config writer
// adds a provider_bindings entry into an existing workspace config without
// disturbing the other keys, updates an existing entry in place, and produces a
// schema-valid file the resolver accepts.
func TestUpsertProviderBinding_PreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := os.WriteFile(path, []byte(`workspace: {}
offline: true
default_registry: hub.aggrega.ai
`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := UpsertProviderBinding(path, "helm", "oci://hub.aggrega.ai/plugin/provider/helm"); err != nil {
		t.Fatalf("UpsertProviderBinding (add): %v", err)
	}
	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after add: %v", err)
	}
	cfg := rw.Config()
	if cfg.ProviderBindings["helm"] != "oci://hub.aggrega.ai/plugin/provider/helm" {
		t.Fatalf("binding not written; got %+v", cfg.ProviderBindings)
	}
	if cfg.DefaultRegistry != "hub.aggrega.ai" || !cfg.Offline {
		t.Errorf("existing keys not preserved: default_registry=%q offline=%v", cfg.DefaultRegistry, cfg.Offline)
	}

	// Update in place must not add a second entry.
	if err := UpsertProviderBinding(path, "helm", "oci://hub.aggrega.ai/plugin/provider/compose"); err != nil {
		t.Fatalf("UpsertProviderBinding (update): %v", err)
	}
	rw, err = Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after update: %v", err)
	}
	cfg = rw.Config()
	if cfg.ProviderBindings["helm"] != "oci://hub.aggrega.ai/plugin/provider/compose" {
		t.Fatalf("binding not updated; got %+v", cfg.ProviderBindings)
	}
	if len(cfg.ProviderBindings) != 1 {
		t.Errorf("update must not add a second entry; got %+v", cfg.ProviderBindings)
	}
}

// TestUpsertProviderBinding_CreatesMissingFile proves the writer creates a fresh,
// schema-valid config (with a workspace marker) when no file exists yet.
func TestUpsertProviderBinding_CreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	if err := UpsertProviderBinding(path, "compose", "oci://hub.aggrega.ai/plugin/provider/compose"); err != nil {
		t.Fatalf("UpsertProviderBinding: %v", err)
	}
	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve created file: %v", err)
	}
	if got := rw.Config().ProviderBindings["compose"]; got != "oci://hub.aggrega.ai/plugin/provider/compose" {
		t.Fatalf("binding not written to fresh file; got %q", got)
	}
}
