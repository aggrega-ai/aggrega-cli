// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import "testing"

// TestRegistriesLoadFromConfig proves the per-registry signing.required field
// round-trips from the workspace file through schema validation and is readable
// via RegistrySigningRequiredForKind.
func TestRegistriesLoadFromConfig(t *testing.T) {
	t.Parallel()
	content := `
workspace: {}
registries:
  - host: hub.aggrega.ai
    signing:
      required: true
  - host: dev.local:5000
    signing:
      required: false
`
	path := writeFixture(t, content)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !cfg.RegistrySigningRequiredForKind("hub.aggrega.ai", "Component") {
		t.Error("hub.aggrega.ai should require signatures")
	}
	if cfg.RegistrySigningRequiredForKind("dev.local:5000", "Component") {
		t.Error("dev.local:5000 should not require signatures")
	}
	// A registry absent from the config does not require signatures.
	if cfg.RegistrySigningRequiredForKind("other.example.com", "Component") {
		t.Error("unconfigured registry should not require signatures")
	}
}

// TestRegistriesDefaultEmpty proves a config with no registries block
// reports no signature requirement for any host — the unconfigured
// default.
func TestRegistriesDefaultEmpty(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if cfg.RegistrySigningRequiredForKind("hub.aggrega.ai", "Component") {
		t.Error("default config should not require signatures for any registry")
	}
	if len(cfg.Registries) != 0 {
		t.Errorf("default Registries = %v, want empty", cfg.Registries)
	}
}
