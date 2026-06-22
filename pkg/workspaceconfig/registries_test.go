// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import "testing"

// TestRegistriesLoadFromConfig proves the per-registry requires-signatures
// field round-trips from the workspace file through schema validation and
// is readable via RegistryRequiresSignatures.
func TestRegistriesLoadFromConfig(t *testing.T) {
	t.Parallel()
	content := `
workspace: {}
registries:
  - host: hub.aggrega.ai
    requires_signatures: true
  - host: dev.local:5000
    requires_signatures: false
`
	path := writeFixture(t, content)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !cfg.RegistryRequiresSignatures("hub.aggrega.ai") {
		t.Error("hub.aggrega.ai should require signatures")
	}
	if cfg.RegistryRequiresSignatures("dev.local:5000") {
		t.Error("dev.local:5000 should not require signatures")
	}
	// A registry absent from the config does not require signatures.
	if cfg.RegistryRequiresSignatures("other.example.com") {
		t.Error("unconfigured registry should not require signatures")
	}
}

// TestRegistriesDefaultEmpty proves a config with no registries block
// reports no signature requirement for any host — the unconfigured
// default.
func TestRegistriesDefaultEmpty(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if cfg.RegistryRequiresSignatures("hub.aggrega.ai") {
		t.Error("default config should not require signatures for any registry")
	}
	if len(cfg.Registries) != 0 {
		t.Errorf("default Registries = %v, want empty", cfg.Registries)
	}
}
