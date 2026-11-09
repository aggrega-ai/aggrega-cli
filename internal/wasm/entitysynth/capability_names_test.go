// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitysynth_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/entitysynth"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// syntheticGuestVersion stands in for a guest's stamped build-provenance version.
// It is deliberately not release-shaped, so cutting a release touches no test.
const syntheticGuestVersion = "0.0.0-synthetic"

// TestPublicationRefusesAColonInAServedProviderCapability drives the publication
// merge — the step that turns a guest's describe() self-report into the Plugin
// entity a publication carries — over a provider reporting a colon-bearing
// served capability, and asserts the merge refuses it wrapping the
// manifest-rejected condition, so no published entity can advertise a name no
// Recipe can select. The control is the same guest reporting a colon-free name,
// which merges.
func TestPublicationRefusesAColonInAServedProviderCapability(t *testing.T) {
	t.Parallel()
	overlay := types.Plugin{Metadata: types.PluginMetadata{Name: "acme-renderer", PluginType: "provider"}}
	descriptor := func(capability string) wasm.PluginDescriptor {
		return wasm.PluginDescriptor{
			AbiVersion:   wasm.ABIVersion,
			PluginType:   string(wasm.PluginTypeProvider),
			Capabilities: []string{capability},
			Exports:      []wasm.PluginExport{{Name: "render_node"}},
			Metadata:     &wasm.PluginDescriptorMeta{Version: syntheticGuestVersion},
		}
	}
	cases := []struct {
		name       string
		capability string
		refused    bool
	}{
		{name: "a colon-bearing served capability is refused at publication", capability: "helm:1", refused: true},
		{name: "control: a colon-free served capability merges", capability: "helm"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entity, err := entitysynth.MergeForPublish(descriptor(tc.capability), "", nil, overlay, "acme/renderer")
			if !tc.refused {
				if err != nil {
					t.Fatalf("a colon-free served capability was refused at publication: %v", err)
				}
				if got := entity.DeclaredCapabilities(); len(got) != 1 || got[0] != tc.capability {
					t.Fatalf("published entity serves %v; want [%s]", got, tc.capability)
				}
				return
			}
			if !errors.Is(err, wasm.ErrWasmManifestRejected()) {
				t.Fatalf("want ErrWasmManifestRejected for the served capability %q, got %v", tc.capability, err)
			}
		})
	}
}
