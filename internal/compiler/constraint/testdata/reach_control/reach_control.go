// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package reachcontrol is the positive control for the document-reach gate:
// the one program beside it that MUST compile. It establishes the three things
// the failing programs' verdicts depend on, so a failure there is the reach
// being refused rather than something unrelated.
//
// First, that the views are nameable from outside and their accessors still
// exist — a moved import path or a renamed accessor would otherwise make every
// case beside it "fail to compile" for a reason that has nothing to do with
// reaching a document.
//
// Second, and this is what the coverage claim rests on, that the views form one
// containment chain and that the one the programs beside this one probe contains
// every other. Each assignment below compiles only if that view's method set
// contains the assigned view's, so the containment is asserted here rather than
// described in a comment. That is what makes it enough to probe one view: a
// contained view exposes a subset of the containing one's methods, so a document
// unreachable from the container is unreachable from all of them.
//
// The residual is worth naming where the assertions are, because it is not
// visible from them. A chain that stopped being one line — a branch — breaks
// these assignments and stops the gate here. A chain EXTENDED past the probed
// view does not: the assignments below still hold, and the new stage's own
// accessors would go unprobed. Adding a stage therefore owes a line here.
//
// Third, that a subject still carries its provenance, which is the whole of what
// a subject hands a rule beyond the ability to raise.
//
// It is a separate control from the one the forgery gate calibrates against.
// That one establishes that a rule can raise through the framework at all; this
// one establishes the shape of the view chain. Two gates resting on two
// different properties calibrate against the property each rests on.
package reachcontrol

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The containment chain, asserted rather than asserted about. Each line compiles
// only while the deepest view still contains the named one.
var (
	_ constraint.View           = (constraint.RenderedView)(nil)
	_ constraint.DocumentView   = (constraint.RenderedView)(nil)
	_ constraint.NormalizedView = (constraint.RenderedView)(nil)
	_ constraint.TopologyView   = (constraint.RenderedView)(nil)
	_ constraint.GraphView      = (constraint.RenderedView)(nil)
	_ constraint.DescribedView  = (constraint.RenderedView)(nil)
	_ constraint.HydratedView   = (constraint.RenderedView)(nil)
	_ constraint.DispatchedView = (constraint.RenderedView)(nil)
	_ constraint.RenderedView   = (constraint.RenderedView)(nil)
)

// ReadAFact reads one fact off the deepest view through the accessor the chain's
// root declares, so the control breaks loudly if the accessor is renamed.
func ReadAFact(v constraint.RenderedView, document string) []byte {
	return v.RawAggregateText(document)
}

// ReadProvenance reads what a subject carries, which is where a rule's finding
// gets its file and its locator from.
func ReadProvenance(s constraint.Subject) (string, string) {
	return s.SourceFile(), s.Locator()
}
