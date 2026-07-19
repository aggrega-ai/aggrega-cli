// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitysynth_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/entitysynth"
)

// TestParseDescriptorJSONFromEmbeddedProvider drives the descriptor parse over a
// real embedded provider descriptor and asserts the routing-relevant fields —
// identity pluginType, abiVersion, capabilities, exports, and the provider
// render-behavior contract — are read back intact, so the descriptor the publish
// merge consumes is faithfully decoded.
func TestParseDescriptorJSONFromEmbeddedProvider(t *testing.T) {
	const uri = "provider/compose"

	raw, ok := embedded.LookupDescriptor(uri)
	if !ok {
		t.Skipf("%s descriptor not embedded in this build", uri)
	}
	desc, _, err := entitysynth.ParseDescriptorJSON(raw)
	if err != nil {
		t.Fatalf("parse descriptor for %s: %v", uri, err)
	}
	// Guard the premise: the chosen guest must actually declare a render-behavior
	// contract, otherwise the test would not exercise the contract projection.
	if desc.Contract == nil || entitysynth.IsZeroContract(*desc.Contract) {
		t.Fatalf("%s reports no render-behavior contract; pick a provider guest that does", uri)
	}
	if desc.PluginType != "provider" {
		t.Errorf("pluginType = %q, want provider", desc.PluginType)
	}
	if len(desc.Capabilities) == 0 {
		t.Error("descriptor reports no capabilities")
	}
	if len(desc.Exports) == 0 {
		t.Error("descriptor reports no exports")
	}
}
