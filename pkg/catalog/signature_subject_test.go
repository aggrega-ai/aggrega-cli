// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestAVerdictPluginIsBoundAsAPluginOfTheClosureWouldBe holds the binding a
// verdict-role plugin's signature is asked about under to the closure's own: a
// plugin no closure axis holds is bound exactly as a plugin reference on the mixed
// entity axis is, so the pull that verifies it and the build that holds it read one
// host, one location and one kind.
func TestAVerdictPluginIsBoundAsAPluginOfTheClosureWouldBe(t *testing.T) {
	t.Parallel()
	const registry = "registry.example.com/team"
	for _, uri := range []string{"linter/house-style:1.0.0", "governance/opa-gate:2.1.0", "governance/opa-gate"} {
		got, ok := catalog.PluginSignatureSubjectOf(registry, uri)
		want, wantOK := catalog.SignatureSubjectOf(registry, closure.VerifiableAxis{Axis: "Entities"},
			closure.VerifiableRef{URI: uri, KindHint: "plugin"})
		if ok != wantOK || got != want {
			t.Errorf("%s: bound as %+v (%t); the closure binds a plugin reference as %+v (%t)", uri, got, ok, want, wantOK)
		}
		if !ok {
			t.Errorf("%s: a host-less plugin identity under a registry must bind", uri)
			continue
		}
		if got.Kind != "Plugin" || got.Host != "registry.example.com" || got.Location == "" {
			t.Errorf("%s: bound as %+v; want kind Plugin under the registry's host, at a location", uri, got)
		}
	}

	// With no registry to bind under, a host-less identity names no registry, so its
	// signature is never asked about.
	if got, ok := catalog.PluginSignatureSubjectOf("", "linter/house-style:1.0.0"); ok {
		t.Errorf("a host-less identity with no registry to bind under must not bind; got %+v", got)
	}
}
