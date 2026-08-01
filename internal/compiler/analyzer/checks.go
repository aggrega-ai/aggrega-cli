// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"

// This file holds this package's rules expressed as constraint declarations:
// each is a value naming the sentinel it enforces, what a violation does to the
// build, the facts it reads, the host powers it needs, and what it is invoked
// once per. Declared that way, a rule's point in the compilation follows from
// the facts it reads rather than from where a call happens to sit, and the
// provenance its diagnostic carries is supplied to it rather than assembled in
// its body.
//
// A rule reaches this shape when every input its predicate reads is a declared
// fact. The other rules in this package are not there yet, and the missing
// pieces are inputs rather than mechanism: a socket's or a slot's declared
// selector, a Feature entity's applicability surfaces and its own labels, a
// node's finalised label set, an edge's resolved target and originating
// attachment, and the node's merged attachment set after the whole attachment
// cascade. Each of those is a fact declaration, and declaring a fact fixes a
// placement — so they arrive with the change that declares them rather than
// being approximated here from a carrier that answers a nearby question. Until
// then those rules keep their hand-written form in the files beside this one,
// which is where they still run from.

// OverriddenAnnotationAdvisory reports each leaf of an attachment's
// configuration where an explicitly authored value took precedence over one an
// annotation supplied. The author may be unaware of the suppression, because the
// annotation's contribution no longer appears in the rendered output. The
// emitted output is not wrong, so the rule advises and the build finishes.
//
// It reads one fact — the per-leaf provenance of an attachment's configuration —
// and keys it entirely by the coordinates of the subject it was invoked for. That
// fact completes only once the whole attachment cascade and annotation-driven
// expansion have been folded, which is what places the rule where it runs: the
// per-leaf provenance record is the only evidence that a suppression happened,
// and it does not exist earlier.
//
// It fires wherever an explicit value displaced an annotation-supplied one,
// whether or not the two would have produced different output. The per-leaf
// merge overwrites the suppressed value, so no comparison is available to make
// and the provenance record is the whole of the actionable signal.
var OverriddenAnnotationAdvisory = constraint.Check[
	constraint.GraphView,
	constraint.NoHostPowers,
	constraint.NodeAttachmentPillarLeafSubject,
]{
	Sentinel: ErrOverriddenAnnotation,
	Severity: constraint.SeverityWarning,
	Run: func(
		v constraint.GraphView,
		s constraint.NodeAttachmentPillarLeafSubject,
	) []constraint.Objection {
		leaf := v.PerLeafAttachmentProvenance(
			s.NodeID(), s.AttachmentName(), s.PillarName(), s.LeafName(),
		)
		if !leaf.DisplacesAnnotation {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrOverriddenAnnotation,
			leaf.DisplacedAnnotationKey,
			leaf.DisplacedAnnotationSource,
		)}
	},
}
