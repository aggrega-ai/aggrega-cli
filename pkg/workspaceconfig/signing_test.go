// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import "testing"

// TestSigningConfigLoadsFromConfig proves the signing block round-trips from
// the workspace file through schema validation at both the global and the
// per-registry level.
func TestSigningConfigLoadsFromConfig(t *testing.T) {
	t.Parallel()
	content := `
workspace: {}
signing:
  fulcio_url: https://fulcio.global.example
  rekor_url: https://rekor.global.example
  key_path: /keys/global.pem
registries:
  - host: hub.aggrega.ai
    signing:
      fulcio_url: https://fulcio.hub.example
      key_path: /keys/hub.pem
`
	cfg, err := load(writeFixture(t, content))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Signing.FulcioURL != "https://fulcio.global.example" {
		t.Errorf("global fulcio_url = %q", cfg.Signing.FulcioURL)
	}
	if len(cfg.Registries) != 1 || cfg.Registries[0].Signing.FulcioURL != "https://fulcio.hub.example" {
		t.Errorf("per-registry signing did not load: %+v", cfg.Registries)
	}
}

// TestResolveSigningPrecedence proves per-registry signing overrides the
// global block field by field, with empty per-registry fields falling
// through to the global value. CLI-flag precedence is applied above this
// method by the cmd layer; defaults are applied below it in buildSigner.
func TestResolveSigningPrecedence(t *testing.T) {
	t.Parallel()
	cfg := Config{
		Signing: SigningConfig{
			FulcioURL:    "https://fulcio.global",
			RekorURL:     "https://rekor.global",
			TimestampURL: "https://tsa.global",
			KeyPath:      "/keys/global.pem",
		},
		Registries: []RegistryConfig{
			{
				Host: "hub.aggrega.ai",
				Signing: SigningConfig{
					FulcioURL: "https://fulcio.hub",
					KeyPath:   "/keys/hub.pem",
				},
			},
		},
	}

	// Per-registry wins where set; global fills the unset fields.
	got := cfg.ResolveSigning("hub.aggrega.ai")
	if got.FulcioURL != "https://fulcio.hub" {
		t.Errorf("FulcioURL = %q, want per-registry override", got.FulcioURL)
	}
	if got.KeyPath != "/keys/hub.pem" {
		t.Errorf("KeyPath = %q, want per-registry override", got.KeyPath)
	}
	if got.RekorURL != "https://rekor.global" {
		t.Errorf("RekorURL = %q, want global fallthrough", got.RekorURL)
	}
	if got.TimestampURL != "https://tsa.global" {
		t.Errorf("TimestampURL = %q, want global fallthrough", got.TimestampURL)
	}

	// An unconfigured host resolves to the global block unchanged.
	other := cfg.ResolveSigning("other.example")
	if other != cfg.Signing {
		t.Errorf("unconfigured host = %+v, want global %+v", other, cfg.Signing)
	}
}
