// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"strings"
	"testing"
)

// TestUnderDeclaredNotices covers the inverse-of-cross-check advisory: it reports
// each export, capability, or contract the binary offers that the entity omits,
// and returns nothing when the entity's curated surface covers the binary.
func TestUnderDeclaredNotices(t *testing.T) {
	t.Parallel()
	desc := PluginDescriptor{
		Exports:      []PluginExport{{Name: "render_node"}, {Name: "link_publish"}, {Name: "link_resolve"}},
		Capabilities: []string{"helm", "compose"},
		Contract:     &ProviderContract{InputKinds: InputKinds{Sourced: true}},
	}

	// A surface that curates a strict subset yields one notice per omitted entry.
	notices := UnderDeclaredNotices(EntityCuratedSurface{
		ExportNames:  []string{"render_node"},
		Capabilities: []string{"helm"},
		HasContract:  true,
	}, desc)
	joined := strings.Join(notices, "\n")
	for _, want := range []string{"link_publish", "link_resolve", "compose"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected a notice mentioning %q; got:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "render_node") || strings.Contains(joined, "\"helm\"") {
		t.Errorf("declared entries should not be reported as under-declared; got:\n%s", joined)
	}

	// A surface that authors no contract while the binary reports one gets the
	// contract notice.
	noContract := UnderDeclaredNotices(EntityCuratedSurface{
		ExportNames:  []string{"render_node", "link_publish", "link_resolve"},
		Capabilities: []string{"helm", "compose"},
		HasContract:  false,
	}, desc)
	if len(noContract) != 1 || !strings.Contains(noContract[0], "render-behavior contract") {
		t.Errorf("an omitted contract should yield exactly the contract notice; got %v", noContract)
	}

	// A surface covering everything yields no notices.
	full := UnderDeclaredNotices(EntityCuratedSurface{
		ExportNames:  []string{"render_node", "link_publish", "link_resolve"},
		Capabilities: []string{"helm", "compose"},
		HasContract:  true,
	}, desc)
	if len(full) != 0 {
		t.Errorf("a fully-covering surface should yield no notices; got %v", full)
	}
}
