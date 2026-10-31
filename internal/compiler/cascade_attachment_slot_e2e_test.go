// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — what a Feature attachment contributed at a subsystem
// scope does when it lands on a component whose attachment slot of the same name
// reserves delivery to the Recipe.
//
// What is authored: a subsystem declares one attachment, and two components sit
// beneath it. A subsystem-scope attachment cascades onto every component in the
// scope, so both receive it. One of them, `cov/consumer`, declares an attachment
// slot under that same name whose inject allowlist is empty — an empty allowlist
// reserves delivery to the Recipe, so no authored delivery mechanism is in shape
// for it. The other, `cov/store`, declares no attachment slots at all, so the
// same name is a free attachment there and the same delivery is entirely legal.
//
// What the compiler does with it: it halts, naming the component whose slot the
// delivery is out of shape for, and locating the attachment at the scope it was
// authored at rather than at the node it reached.
//
// Why the subject is the pair. The same delivery is legal on one of these two
// components and out of shape on the other, so what is wrong is neither the
// authored block — which is well-formed, and which the contrast row below shows
// is caught the moment it sits on the component instance directly — nor the
// slot, but the pair of the two. The rule is therefore asked of a node's
// attachment set after the whole cascade has folded, which is the earliest point
// the slot is known, and it carries a locator back to the subsystem the author
// wrote so the diagnostic names an edit that exists.
//
// The two rows are read together on purpose. Both halt, and what separates them
// is WHERE the diagnostic points: the contributed row is located at the
// subsystem that declared the attachment, the authored row at the component
// instance. So the pair asserts that one rule reaches both routes while still
// naming the site each was written at — and a regression that collapsed the
// contributed route onto a node-shaped locator would fail here rather than pass
// quietly. The two fixtures declare the same components, the same Component
// types and the same slot; where the attachment sits is what differs.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// collectResolverErrors flattens an error tree into the resolver violations it
// carries, walking both the single-unwrap and the joined multi-unwrap shapes.
//
// The build's halt for this class is a join of per-violation errors, so reading
// the violations rather than substring-matching the rendered string is what lets
// a row assert the COUNT of violations and each one's locator separately. A
// substring match over the joined text cannot tell one violation from two.
func collectResolverErrors(err error) []*resolver.ResolverError {
	var out []*resolver.ResolverError
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		if re, ok := e.(*resolver.ResolverError); ok {
			out = append(out, re)
			return
		}
		switch x := e.(type) {
		case interface{ Unwrap() []error }:
			for _, sub := range x.Unwrap() {
				walk(sub)
			}
		case interface{ Unwrap() error }:
			walk(x.Unwrap())
		}
	}
	walk(err)
	return out
}

// TestCascadeAttachmentSlot_ContributedFillHalts asserts the closed case: a
// subsystem-scope attachment whose delivery is out of shape for a slot it lands
// on halts the build, with a diagnostic that names the component and slot it is
// wrong for and locates the attachment where the author wrote it.
//
// It asserts the slot-shape class specifically rather than "the build failed". A
// remedy that reported an ADVISORY instead of halting, or a different gate
// rejecting the document first, would both leave a bare failure assertion green
// while not implementing the rule — and this class is defined by producing no
// signal, so the assertion has to be the signal itself.
func TestCascadeAttachmentSlot_ContributedFillHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, subaggCoverageInput("release-cascade-slot.yaml"))
	if !errors.Is(err, resolver.ErrAttachmentSlotShape()) {
		t.Fatalf("want ErrAttachmentSlotShape for a subsystem-contributed delivery landing on a "+
			"Black-Box slot; got %v", err)
	}

	violations := collectResolverErrors(err)
	// Exactly one. This is where the per-node asymmetry is asserted: the
	// attachment reaches both components in the scope, and only the one whose
	// Component declares the slot is a violation. Two violations would mean the
	// rule fired on `store`, where the same delivery is a legal free attachment;
	// zero would mean the halt came from somewhere else entirely.
	if len(violations) != 1 {
		t.Fatalf("want exactly one violation — the rule must fire on the component declaring the slot "+
			"and not on the one where the same delivery is a legal free attachment; got %d: %v",
			len(violations), violations)
	}
	v := violations[0]

	// The locator names the AUTHORED site: the subsystem whose features block
	// declared the attachment. Not the node it reached, and not the delivery key
	// inside it — a derived subject that cannot point back at an authored edit
	// sends the reader nowhere. It is spelled in the one dialect the editor's
	// position recovery walks, so it resolves to that key in the document.
	const wantLocator = `.spec.subsystems.svc.features.root_password`
	if v.Field != wantLocator {
		t.Errorf("want the violation located at the authoring scope %s; got %s", wantLocator, v.Field)
	}

	// The message names the pair the rule is about — the component reached and
	// the slot of that name — and suggests the edit.
	for _, want := range []string{"consumer", "root_password", "move"} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("want the diagnostic to carry %q so it names the pair and the fix; got %q", want, v.Message)
		}
	}

	// It must NOT send the author at the delivery block. The block is legal on
	// its own terms — the contrast row's fixture proves the identical block is
	// accepted wherever no slot closes it — so naming it would point at correct
	// YAML and invite the author to damage it. What is wrong is the scope the
	// attachment sits at relative to the slot it reached.
	if strings.Contains(v.Error(), "inject") {
		t.Errorf("the diagnostic must not name the delivery block, only the (component, slot) pair "+
			"and the scope to move the attachment to; got %q", v.Error())
	}
}

// TestCascadeAttachmentSlot_ContributedFillWithinAllowlistBuilds is the positive
// control for the row above, and it is the assertion that makes that row mean
// what it claims.
//
// The rule is gated on two conditions — the fill reaches a DECLARED slot, and
// the slot does not permit the delivery — so the two rows above establish only
// the margins: one fill on a slot that forbids its delivery, one on no slot at
// all. Neither can tell "the rule reads the slot's allowlist" from "the rule
// rejects every contributed fill that reaches a declared slot", because both
// readings produce the same result on both rows. This row is the interior cell
// that separates them: a contributed fill reaching a declared slot whose
// allowlist names its mechanism, which must build.
func TestCascadeAttachmentSlot_ContributedFillWithinAllowlistBuilds(t *testing.T) {
	t.Parallel()
	result, err := pipelinetest.BuildE(t, subaggCoverageInput("release-cascade-slot-permitted.yaml"))
	if err != nil {
		t.Fatalf("a contributed fill whose delivery the slot's allowlist names must build; got %v", err)
	}

	// Asserted rather than left at "it built": a build that stopped delivering
	// the fill, or stopped binding the slot, would also build green and would
	// leave this row certifying nothing about the pair it exists to cover.
	attachments, ok := result.IRGraph.Spec.FeatureValuesMapping.Get("consumer")
	if !ok {
		t.Fatalf("the cascade must reach consumer; the attachment map carries %v",
			result.IRGraph.Spec.FeatureValuesMapping.Keys())
	}
	entry, ok := attachments.Get("api_token")
	if !ok {
		t.Fatalf("want the contributed attachment on consumer; got %v", attachments.Keys())
	}
	if entry.Binding != "api_token" {
		t.Errorf("want the fill bound to the declared slot it reached, so this row covers the "+
			"declared-slot cell rather than the free-attachment one; got binding %q", entry.Binding)
	}
	delivery, ok := entry.Inject.Get("env")
	if !ok {
		t.Fatalf("want the permitted delivery retained on consumer; got mechanisms %v", entry.Inject.Keys())
	}
	if got, ok := delivery.Get("TOKEN"); !ok || got != "value" {
		t.Errorf("want the delivery to carry TOKEN from value; got %q (present=%v)", got, ok)
	}
}

// TestCascadeAttachmentSlot_AuthoredFillHalts is the contrast the row above is
// read against: the same attachment moved one scope down, onto the component
// instance that receives it. It halts too, and the LOCATOR is what differs —
// this row points at the component instance, the row above at the subsystem.
//
// Keeping it is what makes the row above attributable. Were the authored route
// to stop halting, the row above could still pass on a rule that had quietly
// become cascade-only; and were both to collapse onto one locator shape, one of
// the two would be naming a site its author did not write. The pair pins that one
// rule serves both routes while each diagnostic names its own authoring site.
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
	//
	// The offending delivery mechanism is named in the message rather than in the
	// locator, and the rule reports once per mechanism the fill dictates — so the
	// mechanism is asserted beside the site, or a halt about a different mechanism
	// at the same attachment would pass.
	const wantLocator = `.spec.subsystems.svc.components.consumer.features.root_password`
	const wantMechanism = "env"
	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("want a MultiResolverError carrying the per-violation slice; got %T", err)
	}
	var found bool
	for _, v := range multi.Violations {
		if v.Field == wantLocator && strings.Contains(v.Message, wantMechanism) {
			found = true
		}
	}
	if !found {
		t.Errorf("no violation located at %s naming mechanism %q; got %v",
			wantLocator, wantMechanism, multi.Violations)
	}
}
