// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// This file holds this package's rules expressed as constraint declarations:
// each is a value naming the sentinel it enforces, what a violation does to the
// build, the facts it reads, the host powers it needs, and what it is invoked
// once per. Declared that way, a rule's point in the compilation follows from
// the facts it reads rather than from where a call happens to sit, and the
// provenance its diagnostic carries is supplied to it rather than assembled in
// its body.
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
	constraint.NoHostPowers,
	constraint.BoundaryProviderPathSubject,
]{
	Sentinel: ErrOrphanCrossBoundaryLinkSentinel,
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
			ErrOrphanCrossBoundaryLinkSentinel, s.ProviderName(), s.EmittedPath(),
		)}
	},
}
