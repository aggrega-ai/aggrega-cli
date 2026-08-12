// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — what a Feature attachment contributed at a subsystem
// scope currently does when it lands on a component whose attachment slot of the
// same name reserves delivery to the Recipe.
//
// 🔴 THIS FILE PINS PRESENT BEHAVIOUR, AND PRESENT BEHAVIOUR IS A KNOWN GAP. It
// is not a specification to preserve. Whoever makes the compiler reject the
// pairing described below must UPDATE these tests rather than read them as the
// contract they were built to protect.
//
// What is authored: a subsystem declares one attachment, and two components sit
// beneath it. A subsystem-scope attachment cascades onto every component in the
// scope, so both receive it. One of them, `cov/consumer`, declares an attachment
// slot under that same name whose inject allowlist is empty — an empty allowlist
// reserves delivery to the Recipe, so no authored delivery mechanism is in shape
// for it. The other, `cov/store`, declares no attachment slots at all, so the
// same name is a free attachment there and the same delivery is entirely legal.
//
// What the compiler does with it today: it builds, silently. A contributed fill
// is checked against no slot, because the rule that checks a fill against its
// slot runs over a component instance's own authored block, and a fill arriving
// through the cascade was authored somewhere else. The pairing reaches the
// finalized graph with its delivery retained and no diagnostic is produced.
//
// What would have to change: the subject the rule runs over. The same delivery is
// legal on one of these two components and out of shape on the other, so what is
// wrong is neither the authored block — which is well-formed, and which the
// contrast row below shows is caught the moment it sits on the component instance
// directly — nor the slot, but the pair of the two. A rule keyed to the authored
// block cannot express that pair, which is why nothing catches it here.
//
// The two rows are read together on purpose. A row asserting only that the
// cascade case builds would keep passing if the slot-shape rule disappeared
// entirely, at which point it would no longer be about the cascade at all. The
// contrast row fixes that: the same attachment, moved one scope down onto the
// component that receives it, halts. The two fixtures declare the same
// components, the same Component types and the same slot; where the attachment
// sits is what differs.

package compiler_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestCascadeAttachmentSlot_ContributedFillIsUnchecked pins the accepted case: a
// subsystem-scope attachment whose delivery is out of shape for a slot it lands
// on builds green, and the fill reaches the node anyway.
//
// The assertions are deliberately not "it built". A build that stopped
// exercising the cascade — because the attachment stopped reaching the nodes, or
// stopped binding the slot, or had its delivery quietly dropped on the node
// where it is illegal — would also build green, and this test would then pass
// while pinning nothing. So it reads the finalized graph and asserts the fill's
// per-node shape, which is the fact a change to this behaviour would move.
func TestCascadeAttachmentSlot_ContributedFillIsUnchecked(t *testing.T) {
	t.Parallel()
	result, err := pipelinetest.BuildE(t, subaggCoverageInput("release-cascade-slot.yaml"))
	if err != nil {
		t.Fatalf("present behaviour is that this builds with no diagnostic; got %v", err)
	}

	// Both components under the subsystem, and only those two. A subsystem-scope
	// attachment is declared for every component beneath it, so a fill reaching
	// a subset would be a partial delivery rather than the behaviour pinned here.
	const wantNodes = 2
	mapping := result.IRGraph.Spec.FeatureValuesMapping
	if got := mapping.Len(); got != wantNodes {
		t.Fatalf("want the attachment map to carry %d nodes, got %d: %v", wantNodes, got, mapping.Keys())
	}

	for _, tc := range []struct {
		node string
		// binding is the attachment slot the fill lands on, or the free-attachment
		// marker when the node's Component declares no slot of that name.
		binding string
		// why records what makes this node's half of the discrimination interesting.
		why string
	}{
		{
			node:    "consumer",
			binding: "root_password",
			why:     "the slot's empty inject allowlist reserves delivery to the Recipe, so the retained delivery is out of shape for it",
		},
		{
			node:    "store",
			binding: types.AttachmentBindingFree,
			why:     "the Component declares no attachment slots, so the same name is a free attachment and the same delivery is legal",
		},
	} {
		t.Run(tc.node, func(t *testing.T) {
			attachments, ok := mapping.Get(tc.node)
			if !ok {
				t.Fatalf("the cascade must reach %s; the attachment map carries %v", tc.node, mapping.Keys())
			}
			entry, ok := attachments.Get("root_password")
			if !ok {
				t.Fatalf("want the contributed attachment on %s; got %v", tc.node, attachments.Keys())
			}
			if entry.Binding != tc.binding {
				t.Errorf("want binding %q on %s (%s); got %q", tc.binding, tc.node, tc.why, entry.Binding)
			}
			// The delivery survives onto both nodes. This is the sharp half: the
			// author declared it once, for every component in the scope, and it is
			// neither dropped on the node where it is illegal nor reported there.
			delivery, ok := entry.Inject.Get("env")
			if !ok {
				t.Fatalf("want the contributed delivery retained on %s; got mechanisms %v", tc.node, entry.Inject.Keys())
			}
			if got, ok := delivery.Get("PW"); !ok || got != "value" {
				t.Errorf("want the delivery to carry PW from value on %s; got %q (present=%v)", tc.node, got, ok)
			}
		})
	}
}

// TestCascadeAttachmentSlot_AuthoredFillHalts is the contrast the row above is
// read against: the same attachment moved one scope down, onto the component
// instance that receives it. It halts — so the green above is the contributed
// fill escaping the rule, rather than the rule being absent from the tree.
//
// It asserts the slot-shape class specifically. Asserting only that the build
// failed could not tell this rule from any other gate rejecting the document
// first.
func TestCascadeAttachmentSlot_AuthoredFillHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, subaggCoverageInput("release-instance-slot.yaml"))
	if !errors.Is(err, resolver.ErrAttachmentSlotShape()) {
		t.Fatalf("want ErrAttachmentSlotShape for the same delivery authored on the component instance; got %v", err)
	}
	// The locator names the authoring site, which is what makes the contrast
	// legible: it points at the component instance, whereas the row above
	// authors the same attachment one scope up and gets no locator at all.
	const wantLocator = `.spec.subsystems.svc.components["consumer"].features.root_password.inject`
	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("want a MultiResolverError carrying the per-violation slice; got %T", err)
	}
	var found bool
	for _, v := range multi.Violations {
		if v.Field == wantLocator {
			found = true
		}
	}
	if !found {
		t.Errorf("no violation located at %s; got %v", wantLocator, multi.Violations)
	}
}
