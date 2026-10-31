// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryindex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TrustPolicyFingerprint returns a stable fingerprint of everything an index
// build's trust verdicts depend on for one host: whether verification is switched
// off (for every registry, or for this one), the pinned public key, the keyless
// identities trusted to sign artifacts and deprecation markers, and the pinned
// trust root a keyless signature is checked against. Two policies with the same
// fingerprint judge every signature and every marker the same way; a change to any
// of those inputs changes it.
//
// It covers the policy as configured, not the material a configured path or the
// public trust root resolves to: a pinned trust root is fingerprinted by its path,
// and the live public trust root by its absence. The settings that decide what a
// pull or a publish requires — whether a signature is required, which kinds,
// whether a failure is advisory — are left out, since an index build reads none of
// them, and a change to one of them is no reason to judge a signature again.
//
// The fingerprint is a SHA-256 over a fixed-order encoding, so it is identical for
// identical input across runs and machines.
func TrustPolicyFingerprint(wsCfg workspaceconfig.Config, policy workspaceconfig.RegistryConfig) string {
	type identity struct {
		Identity       string `json:"identity"`
		IdentityRegexp string `json:"identityRegexp"`
		Issuer         string `json:"issuer"`
	}
	identities := func(in []workspaceconfig.TrustedIdentity) []identity {
		out := make([]identity, 0, len(in))
		for _, id := range in {
			out = append(out, identity{Identity: id.Identity, IdentityRegexp: id.IdentityRegexp, Issuer: id.Issuer})
		}
		return out
	}
	basis := struct {
		SkipEverywhere        bool       `json:"skipEverywhere"`
		SkipHere              bool       `json:"skipHere"`
		PublicKey             string     `json:"publicKey"`
		TrustedIdentities     []identity `json:"trustedIdentities"`
		DeprecationIdentities []identity `json:"deprecationIdentities"`
		TrustedRoot           string     `json:"trustedRoot"`
	}{
		SkipEverywhere:        wsCfg.SkipSignatureVerification,
		SkipHere:              policy.SkipSignatureVerification,
		PublicKey:             policy.PublicKey,
		TrustedIdentities:     identities(policy.TrustedIdentities),
		DeprecationIdentities: identities(policy.TrustedDeprecationIdentities),
		TrustedRoot:           policy.TrustedRoot,
	}
	// Marshalling a struct of strings, booleans and slices of them cannot fail.
	data, _ := json.Marshal(basis)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
