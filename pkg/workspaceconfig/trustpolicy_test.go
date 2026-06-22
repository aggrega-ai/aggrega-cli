// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import "testing"

// TestTrustPolicyLoadsFromConfig proves the full per-registry trust policy
// round-trips from the workspace file through schema validation and is
// readable via RegistryTrustPolicy.
func TestTrustPolicyLoadsFromConfig(t *testing.T) {
	t.Parallel()
	content := `
workspace: {}
registries:
  - host: keyed.example.com
    requires_signatures: true
    public_key: |
      -----BEGIN PUBLIC KEY-----
      MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE
      -----END PUBLIC KEY-----
    advisory: true
  - host: keyless.example.com
    trusted_identities:
      - identity: dev@example.com
        issuer: https://accounts.google.com
      - identity_regexp: '^https://github.com/example/'
        issuer: https://token.actions.githubusercontent.com
    trusted_root: /etc/aggrega/trusted-root.json
`
	path := writeFixture(t, content)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	keyed, found := cfg.RegistryTrustPolicy("keyed.example.com")
	if !found {
		t.Fatal("keyed.example.com should be found")
	}
	if keyed.PublicKey == "" {
		t.Error("keyed.example.com should carry a pinned public key")
	}
	if !keyed.Advisory {
		t.Error("keyed.example.com should be advisory")
	}

	keyless, found := cfg.RegistryTrustPolicy("keyless.example.com")
	if !found {
		t.Fatal("keyless.example.com should be found")
	}
	if len(keyless.TrustedIdentities) != 2 {
		t.Fatalf("keyless trusted identities = %d, want 2", len(keyless.TrustedIdentities))
	}
	if keyless.TrustedIdentities[0].Identity != "dev@example.com" {
		t.Errorf("identity[0] = %q", keyless.TrustedIdentities[0].Identity)
	}
	if keyless.TrustedIdentities[1].IdentityRegexp == "" {
		t.Error("identity[1] should use a regexp matcher")
	}
	if keyless.TrustedRoot != "/etc/aggrega/trusted-root.json" {
		t.Errorf("trusted_root = %q", keyless.TrustedRoot)
	}
}

// TestHubBaselineMandatoryByDefault proves the embedded hub baseline
// applies even with no operator config: hub.aggrega.ai resolves a trust
// policy requiring signatures with the maintainer + CI identities.
func TestHubBaselineMandatoryByDefault(t *testing.T) {
	t.Parallel()
	cfg := Default()
	hub, found := cfg.RegistryTrustPolicy(HubBaselineHost)
	if !found {
		t.Fatal("hub baseline should always be found")
	}
	if !hub.RequiresSignatures {
		t.Error("hub baseline must require signatures")
	}
	if len(hub.TrustedIdentities) < 2 {
		t.Fatalf("hub baseline should carry the maintainer + CI identities, got %d", len(hub.TrustedIdentities))
	}
	foundMaintainer := false
	foundCI := false
	for _, id := range hub.TrustedIdentities {
		if id.Issuer == "https://accounts.google.com" {
			foundMaintainer = true
		}
		if id.Issuer == "https://token.actions.githubusercontent.com" {
			foundCI = true
		}
	}
	if !foundMaintainer || !foundCI {
		t.Errorf("hub baseline missing an OR-identity: maintainer=%v ci=%v", foundMaintainer, foundCI)
	}
}

// TestHubBaselineOverridable proves an explicit operator entry for the hub
// host overrides the embedded baseline.
func TestHubBaselineOverridable(t *testing.T) {
	t.Parallel()
	content := `
workspace: {}
registries:
  - host: hub.aggrega.ai
    requires_signatures: false
    trusted_identities:
      - identity: enterprise@acme.example
        issuer: https://acme.example/oidc
`
	path := writeFixture(t, content)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	hub, found := cfg.RegistryTrustPolicy(HubBaselineHost)
	if !found {
		t.Fatal("hub should be found via the operator entry")
	}
	if hub.RequiresSignatures {
		t.Error("operator entry should override the baseline RequiresSignatures")
	}
	if len(hub.TrustedIdentities) != 1 || hub.TrustedIdentities[0].Identity != "enterprise@acme.example" {
		t.Errorf("operator entry should replace baseline identities, got %v", hub.TrustedIdentities)
	}
}

// TestUnknownRegistryNotFound proves an unconfigured non-hub host returns
// found=false so the verify pass applies the default-for-unknown policy.
func TestUnknownRegistryNotFound(t *testing.T) {
	t.Parallel()
	cfg := Default()
	pol, found := cfg.RegistryTrustPolicy("random.example.com")
	if found {
		t.Error("unconfigured non-hub host should not be found")
	}
	if pol.Host != "random.example.com" {
		t.Errorf("zero policy should carry the host, got %q", pol.Host)
	}
}

// TestGlobalSkipLoadsFromConfig proves the global loud opt-out round-trips
// from the workspace file.
func TestGlobalSkipLoadsFromConfig(t *testing.T) {
	t.Parallel()
	content := `
workspace: {}
skip_signature_verification: true
`
	path := writeFixture(t, content)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.SkipSignatureVerification {
		t.Error("skip_signature_verification should round-trip true")
	}
	if Default().SkipSignatureVerification {
		t.Error("global skip should default false")
	}
}
