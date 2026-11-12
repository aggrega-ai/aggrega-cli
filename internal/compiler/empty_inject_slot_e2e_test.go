// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — one verdict for an attachment's inject block on a slot
// whose allowlist is empty, wherever the attachment is authored.
//
// A component type can declare an attachment slot with an empty inject
// allowlist: whatever fills it is consumed by the Recipe, and no delivery may be
// dictated for it. Three authoring sites reach such a slot, and each is measured
// by its own instrument. A component the root Aggregate declares answers to the
// delivery-mechanism allowlist rule at resolution. A component inside a module
// the root Aggregate imports answers to the slot shape the Component's slot
// table projects, once the module is flattened. An attachment declared at a
// subsystem and cascading onto the component answers to that same projection,
// against the slot it lands on.
//
// Composing a module must not make a surface stricter than the one it came from,
// so the three sites have to agree. This file pins the agreement as a grid: two
// shapes of inject block — the empty block, which dictates no delivery, and a
// block naming a mechanism, which dictates one — across the three sites. The
// empty block builds on every site; the mechanism-carrying block halts on every
// site with the slot-shape class.

package compiler_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestEmptyInjectBlockOnBlackBoxSlot_OneVerdictOnEverySurface drives each cell
// of the grid through the real pipeline.
//
// The admitted rows assert more than a green build: the fill must reach the
// component bound to the declared slot, because a build that stopped binding the
// slot — or dropped the attachment — would also build, and would leave the row
// certifying nothing about the slot it exists to cover. The refused rows assert
// the slot-shape class specifically, so a different gate rejecting the document
// first cannot stand in for the rule.
func TestEmptyInjectBlockOnBlackBoxSlot_OneVerdictOnEverySurface(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		release string
		// node is the IR node the fill must reach bound to its slot. Empty on a
		// refused row.
		node string
		// refused is whether the build must halt with the slot-shape class.
		refused bool
	}{
		{
			name:    "an empty block on a component the root Aggregate declares builds",
			release: "release-empty-inject-root.yaml",
			node:    "consumer",
		},
		{
			name:    "an empty block on a component inside an imported module builds",
			release: "release-empty-inject-module.yaml",
			node:    "data.consumer",
		},
		{
			name:    "an empty block contributed from a subsystem builds",
			release: "release-empty-inject-contributed.yaml",
			node:    "consumer",
		},
		{
			name:    "a delivery block on a component the root Aggregate declares halts",
			release: "release-instance-slot.yaml",
			refused: true,
		},
		{
			name:    "a delivery block on a component inside an imported module halts",
			release: "release-bad-slot-shape.yaml",
			refused: true,
		},
		{
			name:    "a delivery block contributed from a subsystem halts",
			release: "release-cascade-slot.yaml",
			refused: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := pipelinetest.BuildE(t, subaggCoverageInput(tc.release))
			if tc.refused {
				if !errors.Is(err, resolver.ErrAttachmentSlotShape()) {
					t.Fatalf("want ErrAttachmentSlotShape for a delivery block on a slot permitting none; got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("an empty inject block dictates no delivery and must build on every surface; got %v", err)
			}
			attachments, ok := result.IRGraph.Spec.FeatureValuesMapping.Get(tc.node)
			if !ok {
				t.Fatalf("want attachments on %s; the attachment map carries %v",
					tc.node, result.IRGraph.Spec.FeatureValuesMapping.Keys())
			}
			entry, ok := attachments.Get("root_password")
			if !ok {
				t.Fatalf("want the root_password fill on %s; got %v", tc.node, attachments.Keys())
			}
			if entry.Binding != "root_password" {
				t.Errorf("want the fill bound to the declared slot, so the row covers the slot rather than "+
					"a free attachment; got binding %q", entry.Binding)
			}
			if entry.Inject.Len() != 0 {
				t.Errorf("an empty inject block dictates no delivery; got mechanisms %v", entry.Inject.Keys())
			}
		})
	}
}
