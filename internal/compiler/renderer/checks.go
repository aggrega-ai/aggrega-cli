// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
)

// This file holds this package's rules expressed as constraint declarations:
// each is a value naming the sentinel it enforces, what a violation does to the
// build, the facts it reads, and what it is invoked once per. Declared that
// way, a rule's point in the compilation follows from the facts it reads rather
// than from where a call happens to sit, and the provenance its diagnostic
// carries is supplied to it rather than assembled in its body.
//
// The rules are declared here and the hand-written functions beside this file
// still run. That is what a reader comparing the two should expect rather than
// read as duplication left behind: nothing yet implements a view over a real
// compilation product, so a declaration has nothing to be invoked with. The
// declaration is what makes the placement computable; the invocation arrives
// with the change that supplies a view, and the hand-written form is removed by
// the change that no longer needs it.
//
// What a declaration here does not carry is the traversal its hand-written
// counterpart performs. A hand-written scan over assembled output walks every
// dispatch group and every file within it, and sorts both so the file it names
// is the same one across runs of identical input. A check is invoked once per
// subject instead, and the framework's enumeration order is what makes that
// sequence deterministic — so a declaration reads one file's bytes, holds no
// loop, and sorts nothing. One that kept the walk would have re-implemented the
// traversal inside its own predicate, which is the arrangement the framework
// replaces; one that enumerated the path set and then read each path's bytes
// would have done the same thing by a longer route.
//
// A detail carries fact values and never a rendering of them: turning an
// objection into a message is the framework's job, and a rule that formatted one
// would be doing it twice. A subject's coordinates are legitimate detail where
// the locator does not already name them. A boundary is authored, so the locator
// names it; a provider is selected and an emitted path is produced rather than
// written, so neither reaches the locator and a rule that has to say which file
// it objects about names them here.

// CrossBoundaryLinkPlaceholderResolved reports each assembled file that still
// carries a cross-boundary link placeholder.
//
// A placeholder is a build-internal marker the host stamps into a consuming
// node's injected input for a uses edge whose ends assemble into different
// dispatch groups, and the consuming provider's link resolution replaces it with
// provider-native cross-reference wiring. One standing in assembled output means
// resolution never reached it — the injected value landed at a position the
// resolver could not address, or an edge was stamped with no matching resolve. A
// build-internal marker is never valid shipped output, so a violation halts the
// build rather than advising and letting it finish.
//
// It reads one fact, the bytes standing at one output path, and reads them for
// the single path it was invoked for. The whole of its predicate is a substring
// test over those bytes, which is why the bytes are all it needs: what makes a
// placeholder detectable is a prefix the host chose, so the rule never has to
// establish which edge stamped it or which provider was meant to resolve it.
// That the rule finds the same file across runs of identical input follows from
// the order it is invoked in, not from anything it does.
//
// Its detail names the provider and the emitted path, because a path is unique
// only within a boundary and a provider together — so naming both is what makes
// the objection point at one file, and the boundary the locator already carries
// completes the coordinate.
var CrossBoundaryLinkPlaceholderResolved = constraint.Check[
	constraint.RenderedView,
	constraint.BoundaryProviderPathSubject,
]{
	Sentinel: ErrOrphanCrossBoundaryLinkSentinel(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.RenderedView,
		s constraint.BoundaryProviderPathSubject,
	) []constraint.Objection {
		content := v.AssembledFileContent(s.BoundaryName(), s.ProviderName(), s.EmittedPath())
		if !bytes.Contains(content, []byte(rendercore.CrossBoundaryLinkSentinelPrefix)) {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrOrphanCrossBoundaryLinkSentinel(), s.ProviderName(), s.EmittedPath(),
		)}
	},
}

// SelfInjectKeyNotDeliveredByAnEdge reports each delivery key a component
// declares for itself that one of its own dependency edges also delivers under
// the same mechanism.
//
// Two authoring surfaces can deliver a value under one key: the component's own
// injection block, and the injection block on an entry in its dependency block.
// A key written on both is ambiguous about which value arrives, so the build
// halts and the author resolves it by removing one of the two. Presence in both
// is the whole signal: the rule compares no values and does not treat two
// sources agreeing as acceptable, because the agreement is a property of one
// input rather than of the authoring.
//
// It is invoked once per key the component declares for itself, which is the
// side the objection is attributed to, and reads one fact — the sockets whose
// edges deliver that same key under that same mechanism. A non-empty answer is
// the violation, and each socket in it is its own objection, so an author
// wiring the same key through two dependencies is told about both rather than
// about whichever sorted first.
//
// Its detail names the socket alone. The locator already names the mechanism
// and the key, because the subject stands for the entry the author wrote at
// that position; the socket belongs to a different entry the locator does not
// reach, so naming it is what makes the objection point at the second
// declaration as well as the first.
var SelfInjectKeyNotDeliveredByAnEdge = constraint.Check[
	constraint.GraphView,
	constraint.NodeMechanismDeliveryKeySubject,
]{
	Sentinel: ErrSelfInjectKeyCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.GraphView,
		s constraint.NodeMechanismDeliveryKeySubject,
	) []constraint.Objection {
		sockets := v.UsesInjectDeliverySockets(s.NodeID(), s.MechanismName(), s.DeliveryKey())
		if len(sockets) == 0 {
			return nil
		}
		out := make([]constraint.Objection, 0, len(sockets))
		for _, socket := range sockets {
			out = append(out, s.Raise(ErrSelfInjectKeyCollision(), socket))
		}
		return out
	},
}

// ProvidesTemplateSelfContained reports each provides entry whose template
// reaches outside the self-contained data a provides template is evaluated
// against.
//
// A provides entry is evaluated against node-local data alone — the instance
// identifier, the configuration pillars, and the node's own resolved locals —
// so a reference to anything else would render empty rather than fail. The rule
// refuses the reference instead, which is what turns a silently empty value
// into a halt naming the expression to fix. The admitted set is not restated
// here: the rule evaluates the same declared scope the provides evaluation
// itself uses, so a field added to that surface becomes reachable from a
// template with no edit to this rule.
//
// It reads one fact, the template text as authored, and that is what lets the
// rule run before anything is evaluated. The resolved value cannot serve as the
// input, because a template this rule refuses is exactly one whose value is
// never produced. Reading the text also means the rule derives its own
// references rather than receiving a reference set collected in the direction a
// consumption question needs — attributing a reference wrongly here would
// refuse authoring the template language permits.
//
// A template that does not parse is not this rule's finding. Evaluation reports
// the parse error against the same entry, so refusing it here as well would
// report one authoring fault twice.
//
// Its detail names the offending expression. The locator names the provides
// entry, which is the position the author edits, and the expression is what
// inside that entry has to change.
var ProvidesTemplateSelfContained = constraint.Check[
	constraint.DescribedView,
	constraint.NodeProvidesKeySubject,
]{
	Sentinel: ErrProvidesSelfContainment(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeProvidesKeySubject,
	) []constraint.Objection {
		text := v.AuthoredNodeProvides(s.NodeID(), s.ProvidesKey())
		if text == "" {
			return nil
		}
		tree, err := gotmpl.ParseTree(text, "{{", "}}")
		if err != nil {
			return nil
		}
		violation := providesScope.check(tree)
		if violation == nil {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrProvidesSelfContainment(), violation.Ref.Path,
		)}
	},
}
