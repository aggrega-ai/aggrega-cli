// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// This file holds this package's rules expressed as constraint declarations:
// each is a value naming the sentinel it enforces, what a violation does to the
// build, the facts it reads, and what it is invoked once per. Declared that
// way, a rule's point in the compilation follows from the facts it reads rather
// than from where a call happens to sit, and the provenance its diagnostic
// carries is supplied to it rather than assembled in its body.
//
// A rule here is reached where something presents a view over the compilation
// products its stage names, and each stage's invocation lives beside that
// presentation rather than here. Which of these rules a build reaches, and from
// where, is derived from the source rather than stated in prose — so this file
// deliberately makes no claim about it, and a reader asking whether a particular
// rule is enforced reads the derived answer instead of a comment that could go
// stale against the code beside it.
//
// A value here may be declared and reached from nowhere, which is a real state
// rather than an oversight: declaring a rule makes its placement computable and
// does not make it run. Naming which values are in that state is left to the
// derivation, because a list here would be a second copy of what the derivation
// computes and the copy is what goes stale.
//
// What a declaration does not carry is the traversal a hand-written enforcer
// performs. Such a form walks its own subject set — every dispatch group and every
// file within it, say — and sorts as it goes so the position it names is the same
// one across runs of identical input. A check is invoked once per subject instead,
// and the framework's enumeration order is what makes that sequence deterministic
// — so a declaration reads one file's bytes, holds no loop, and sorts nothing. One
// that kept the walk would have re-implemented the traversal inside its own
// predicate, which is the arrangement the framework replaces; one that enumerated
// the path set and then read each path's bytes would have done the same thing by a
// longer route.
//
// The other consequence of being invoked once per subject is what the build
// reports. A walk that returns on its first offender names one position however
// many are wrong; a rule invoked per subject raises about each, so an author with
// three faults is told about three of them from one build.
//
// A detail carries fact values and never a rendering of them: turning an
// objection into a message is the framework's job, and a rule that formatted one
// would be doing it twice. A subject's coordinates are legitimate detail where
// the locator does not already name them. A boundary is authored, so the locator
// names it; a provider is selected and an emitted path is produced rather than
// written, so neither reaches the locator and a rule that has to say which file
// it objects about names them here.

// AssemblyPathServesOneProvider reports each boundary whose output was sent to a
// directory another provider also writes into.
//
// A boundary declares the directory its output lands in, and the default
// declaration gives each provider a sub-path of its own, so two providers reach
// one directory only where an operator has written declarations that evaluate
// alike. Both then assemble there, and each writes files the other knows nothing
// about — so one silently displaces the other on disk. That is a Loud Guardrail
// case rather than a warning one: the displaced output is gone, and the build
// that produced it looked as though it succeeded.
//
// It reads two facts, both about the group it was invoked for: the providers that
// assembled at the same directory, and the boundaries that fed this group. The
// first is the predicate — more than one provider is the violation, and one is
// the ordinary state — and the second is what makes the finding actionable, since
// the directory declaration an operator edits is written on a boundary and the
// group's own coordinates name none.
//
// It raises one objection per contributing boundary rather than one per group,
// because a boundary is what an author can go and change and a group is not. A
// group always has at least one, since a group comes into existence when a
// boundary's first contributor joins it, so the per-boundary shape has no silent
// empty case.
//
// Its detail names the directory bare and captions the other two. The registered
// wording names the directory and nothing else, so that is the value it is read
// against; a boundary name and a provider name are both bare words, and nothing
// else in the message would tell them apart. All three are in the detail because
// the subject carries no locator — a directory is evaluated output and a provider
// is selected, so neither is a key the deployment document holds — which leaves
// the detail as the only place the offending group's coordinates are stated.
var AssemblyPathServesOneProvider = constraint.Check[
	constraint.DispatchedView,
	constraint.AssemblyPathProviderSubject,
]{
	Sentinel: ErrPathCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DispatchedView,
		s constraint.AssemblyPathProviderSubject,
	) []constraint.Objection {
		if len(v.AssemblyGroupProviderSet(s.AssemblyPath())) < 2 {
			return nil
		}
		boundaries := v.AssemblyGroupBoundarySet(s.AssemblyPath(), s.ProviderName())
		out := make([]constraint.Objection, 0, len(boundaries))
		for _, boundary := range boundaries {
			out = append(out, s.RaiseWith(
				ErrPathCollision(),
				constraint.Uncaptioned(s.AssemblyPath()),
				constraint.Captioned("boundary", boundary),
				constraint.Captioned("provider", s.ProviderName()),
			))
		}
		return out
	},
}

// AssemblyGroupProviderHasItsDeclaredAssembler reports each dispatch group whose
// provider says it combines the group's per-node output while the plugin serving
// that provider offers no such assembly.
//
// A provider states what becomes of a group's output — folded into one document,
// or each node nested under its own position — and the plugin serving it either
// offers that assembly or offers per-node rendering alone. Where the two disagree
// the group can be rendered and cannot be combined, so the assembly would simply
// not happen: the build would write unassembled per-node fragments at a path an
// operator is expecting one artifact at, and would look as though it succeeded.
// That is a Loud Guardrail case, not a warning one — nothing downstream can tell
// the fragments from an intended flat output.
//
// It reads two facts about the group it was invoked for, and it needs both. The
// claim alone cannot distinguish a group that owes an assembly from one that does
// not, and the offer alone cannot distinguish a plugin that omits an assembly
// because none was ever claimed — which is the ordinary flat-output case and the
// majority of groups — from one that omits the assembly its provider promised.
//
// A group at the empty assembly path is passed over. Nothing is assembled there:
// output written at no dispatch path is per-node output by construction, so a
// claim about combining it has nothing to govern and a group there cannot lose an
// artifact it was never going to produce.
//
// Its detail names the provider bare and captions the path. The registered wording
// names the provider, so that is the value it is read against; the path is a
// second value the wording does not name, and it is in the detail because the
// subject carries no locator — an assembly path is evaluated output and a provider
// is selected, so neither is a key the deployment document holds.
var AssemblyGroupProviderHasItsDeclaredAssembler = constraint.Check[
	constraint.DispatchedView,
	constraint.AssemblyPathProviderSubject,
]{
	Sentinel: ErrBoundaryAssemblerMissing(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DispatchedView,
		s constraint.AssemblyPathProviderSubject,
	) []constraint.Objection {
		if s.AssemblyPath() == "" {
			return nil
		}
		if !boundaryAssemblyCombines(
			v.AssemblyGroupProviderAssembly(s.AssemblyPath(), s.ProviderName()),
		) {
			return nil
		}
		if v.AssemblyGroupAssemblerProvided(s.AssemblyPath(), s.ProviderName()) {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrBoundaryAssemblerMissing(),
			constraint.Uncaptioned(s.ProviderName()),
			constraint.Captioned("dispatch path", s.AssemblyPath()),
		)}
	},
}

// AssemblyGroupBoundariesAgreeOnAssemblyName reports each boundary feeding a
// dispatch group whose provider names the group's assembly from a boundary
// annotation, requires that name to be one value, and whose boundaries resolved it
// to more than one.
//
// Several boundaries reach one group when their own directory declarations
// evaluate alike, and the provider then folds their output into a single named
// artifact. A merged artifact has exactly one name, so where the boundaries
// disagree about it the compiler has no basis for choosing: one boundary's name
// would be published and the others' silently discarded, and the deployment would
// look as though every boundary's declaration had been honoured. That is a Loud
// Guardrail case rather than a warning one — the discarded names leave no trace in
// the output, and a name is what an operator addresses the artifact by afterwards.
//
// It reads two facts about the group it was invoked for, and it needs both. The
// resolved names alone cannot tell a disagreement that matters from one the
// provider is indifferent to: a provider may name its assembly from the first
// value it finds, and a rule reading the names alone would refuse a deployment
// that provider renders correctly. The declaration alone says nothing about
// whether this group's boundaries agree.
//
// A group with fewer than two names is passed over, and the two ways of reaching
// that are one state here. A group fed by a single boundary has nothing to
// disagree with, and a group whose provider names its assembly from no annotation
// resolved nothing per boundary — in both, the group's name is settled by
// something other than a comparison across boundaries.
//
// It raises one objection per boundary feeding the group rather than one per
// group, for the reason the collision rule at this stage does: the annotation an
// author edits is written on a boundary, and a group is not a place anyone can go
// and change. Every contributing boundary is named and not only the ones holding a
// minority value — nothing in the disagreement makes one of several values the
// intended one, so naming a subset would assert a choice the compiler has not
// made.
//
// Its detail names the resolved value bare and captions the rest. The registered
// wording names the conflicting values and nothing else, so that is what it is
// read against; the annotation, the boundary, the provider and the path are values
// the wording does not name, and they are in the detail because the subject
// carries no locator — an assembly path is evaluated output and a provider is
// selected, so neither is a key the deployment document holds.
var AssemblyGroupBoundariesAgreeOnAssemblyName = constraint.Check[
	constraint.DispatchedView,
	constraint.AssemblyPathProviderSubject,
]{
	Sentinel: ErrChartNameMismatch(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DispatchedView,
		s constraint.AssemblyPathProviderSubject,
	) []constraint.Objection {
		annotation, mustAgree := v.AssemblyGroupAssemblyNameAnnotation(
			s.AssemblyPath(), s.ProviderName(),
		)
		if annotation == "" || !mustAgree {
			return nil
		}
		names := v.AssemblyGroupBoundaryAssemblyNames(
			s.AssemblyPath(), s.ProviderName(),
		)
		if names.Len() < 2 {
			return nil
		}
		agreed := ""
		first := true
		disagree := false
		for _, name := range names.All() {
			if first {
				agreed, first = name, false
				continue
			}
			if name != agreed {
				disagree = true
			}
		}
		if !disagree {
			return nil
		}
		out := make([]constraint.Objection, 0, names.Len())
		for boundary, name := range names.All() {
			out = append(out, s.RaiseWith(
				ErrChartNameMismatch(),
				constraint.Uncaptioned(name),
				constraint.Captioned("annotation", annotation),
				constraint.Captioned("boundary", boundary),
				constraint.Captioned("provider", s.ProviderName()),
				constraint.Captioned("dispatch path", s.AssemblyPath()),
			))
		}
		return out
	},
}

// derivedNameCaption is the rule's word for a contributor that derived a
// colliding name, spelled for the kind of contributor it is and for whether that
// contributor's own declaration carried the name more than once.
//
// The kind is a word rather than a value because it is what tells an author where
// to go: a component's name is written among the components a deployment
// declares, a boundary-scope feature's among that boundary's features. The value
// the caption precedes stays the contributor's own name, quoted by the framework,
// so nothing here renders a value.
//
// The kind arrives as a fact and is not decided here. The render classifies a
// contributor once, at the site that names one, and this composes a caption from
// that classification rather than testing for a kind a second time.
func derivedNameCaption(contributorKind string, repeated bool) string {
	if repeated {
		return "derived more than once by " + contributorKind
	}
	return "derived by " + contributorKind
}

// AssemblyGroupDerivesEachNameOnce reports each contributor to a dispatch group
// that derived a name another contributor to the same group also derived, or that
// carried one name twice inside a single one of its own declarations.
//
// A contributor derives the names its output addresses resources by — the
// identifier a target uses for one resource, the name one output is published
// under for another to reference — and puts them inside the file bytes the host
// writes verbatim. Where the group's output merges, one such name can hold only
// one thing, so two contributors arriving at one name means one of them would be
// published and the other silently dropped: a resource the author declared would
// leave the output entirely, and anything referring to it would resolve to the
// survivor instead. That is a Loud Guardrail case rather than a warning one —
// nothing in the merged output records what was dropped.
//
// A name fails uniqueness two ways and both are the same defect. Two contributors
// can arrive at one name, which the declarations show directly. Or one
// contributor's single declaration can carry the name twice, which the
// accumulated declarations cannot show: a contributor legitimately restates a
// name across its several render crossings — a source render, a workload
// synthesis, an artifacts placement — describing one resource in each report it
// belongs to, and that restatement leaves the same pair of entries a duplicate
// would. So the distinction is drawn where a single declaration is still whole
// and each declaration carries the answer, which is why the fact this reads
// carries it per entry rather than leaving it to be counted.
//
// It reads one fact, which is every declaration the group's contributors made.
// The predicate is the whole of the comparison: how many contributors arrived at
// one name in one space, and whether any of them repeated it inside one
// declaration. Neither is a question the fact answers — a fact that named the
// colliding names would be this rule rather than its input.
//
// It raises one objection per contributor involved in each colliding name, rather
// than one per name, for the reason the collision rule at this stage does: a
// contributor is what an author can go and change and a name is not. Every
// contributor that derived the name is named and not only some of them, because
// nothing in a collision makes one of two derivations the intended one, so naming
// a subset would assert a choice the compiler has not made.
//
// The space bounds the comparison. Two contributors deriving one name into two
// different spaces have not collided — neither output displaces the other — and a
// rule comparing across spaces would refuse authoring that renders correctly.
//
// It is blind to what a name means. A space is an opaque string its declarer
// chose, a name is an opaque string its declarer derived, and this compares them
// for equality and does nothing else — so one rule covers every space any
// contributor declares, and a second provider needs no second rule.
//
// Its detail names the derived name bare and captions the rest. The registered
// wording is about a name derived more than once, so the name is the value it is
// read against; the space, the contributor, the provider and the path are values
// the wording does not name, and they are in the detail because the subject
// carries no locator — an assembly path is evaluated output and a provider is
// selected, so neither is a key the deployment document holds.
var AssemblyGroupDerivesEachNameOnce = constraint.Check[
	constraint.DispatchedView,
	constraint.AssemblyPathProviderSubject,
]{
	Sentinel: ErrDerivedKeyCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DispatchedView,
		s constraint.AssemblyPathProviderSubject,
	) []constraint.Objection {
		declared := v.AssemblyGroupDerivedNames(s.AssemblyPath(), s.ProviderName())
		if len(declared) == 0 {
			return nil
		}
		var out []constraint.Objection
		// The declarations arrive in space then name then contributor order, so
		// every entry for one (space, name) pair is consecutive and the run is the
		// whole set of contributors that arrived at that name. Walking runs rather
		// than building a map is what keeps the objections in the order the fact
		// states, which is the order that makes the report a function of the input.
		for start := 0; start < len(declared); {
			end := start + 1
			for end < len(declared) &&
				declared[end].KeySpace == declared[start].KeySpace &&
				declared[end].DerivedName == declared[start].DerivedName {
				end++
			}
			run := declared[start:end]
			start = end

			repeated := false
			for _, d := range run {
				if d.RepeatedWithinOneDeclaration {
					repeated = true
					break
				}
			}
			// One contributor that did not repeat the name is the ordinary answer:
			// a name derived once in its space by the one thing that derives it.
			if len(run) < 2 && !repeated {
				continue
			}
			for _, d := range run {
				out = append(out, s.RaiseWith(
					ErrDerivedKeyCollision(),
					constraint.Uncaptioned(d.DerivedName),
					constraint.Captioned("key space", d.KeySpace),
					constraint.Captioned(
						derivedNameCaption(d.ContributorKind, d.RepeatedWithinOneDeclaration),
						d.Contributor,
					),
					constraint.Captioned("provider", s.ProviderName()),
					constraint.Captioned("dispatch path", s.AssemblyPath()),
				))
			}
		}
		return out
	},
}

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
		content := v.AssembledFileContent(s.BoundaryPath(), s.ProviderName(), s.EmittedPath())
		if !bytes.Contains(content, []byte(rendercore.CrossBoundaryLinkSentinelPrefix)) {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrOrphanCrossBoundaryLinkSentinel(), s.ProviderName(), s.EmittedPath(),
		)}
	},
}

// CrossBoundaryLinkEndsShareOneProvider reports each connection whose two ends
// render through different providers where at least one of them advertises
// cross-boundary linking.
//
// A provider's cross-reference wiring is written in that provider's own artifact
// vocabulary, and a provider cannot resolve a reference another one published.
// So a connection spanning two providers is wired by neither, and the build
// carries the scalar value the injection contract already pipes across instead.
// That value is a working connection rather than a broken one, which is why this
// advises and does not halt: the author is told the native wiring did not
// happen, and the output stands.
//
// # Two ends on different providers are always in different dispatch groups
//
// A dispatch group is keyed by an assembly path AND a provider, so two ends
// whose providers differ cannot share one group. The rule therefore needs no
// reading of where either end was placed: the placement cannot make two
// differing providers into one group, and it cannot separate two matching ones
// into a case this rule is about. What it reads is the two providers and the two
// capability claims, which is the whole of the condition.
//
// # What it reads, and why the membership read is not incidental
//
// Four facts. The node at the far end of the connection; whether that node is in
// the flattened set at all; each end's rendering provider; and each end's
// cross-boundary-linking claim.
//
// The membership read is what keeps the rule silent about a connection whose far
// end is not a component. Such a connection is already refused before any render
// — a dangling target is a Loud Guardrail at the topology stage — so this is not
// a case the rule is expected to meet; reading membership is what makes the
// silence explicit rather than an accident of a provider that reads empty. A
// node in the set with no bound Recipe is a different case and is NOT silenced:
// it renders through no provider, which differs from any provider its peer
// binds, and a linking peer with nothing to link to is exactly what the author
// wants told.
//
// # Why at least one claim rather than both
//
// One end advertising linking is enough for the wiring to have been attempted
// and to be impossible, and the author who attached that provider is the one
// who expected native wiring. Requiring both claims would say nothing for the
// ordinary mixed deployment, where one provider links and the other does not,
// which is the case this rule exists for.
//
// Its detail leads with the far end, which the registered wording is about and
// so carries no caption, and follows with the socket and each end's provider,
// captioned — the wording names none of them, and a reader handed four bare
// quoted names has no way to tell a socket from a provider from a node.
var CrossBoundaryLinkEndsShareOneProvider = constraint.Check[
	constraint.DescribedView,
	constraint.EdgeSubject,
]{
	Sentinel: ErrCrossProviderLinkUnsupported(),
	Severity: constraint.SeverityWarning,
	Run: func(
		v constraint.DescribedView,
		s constraint.EdgeSubject,
	) []constraint.Objection {
		target := v.PerEdgeResolvedTarget(s.NodeID(), s.SocketName(), s.AliasName())
		if _, inSet := v.FlattenedNodeComponentType(target); !inSet {
			return nil
		}
		consumerProvider := v.NodeRenderingProvider(s.NodeID())
		targetProvider := v.NodeRenderingProvider(target)
		if consumerProvider == targetProvider {
			return nil
		}
		if !v.ProviderCrossBoundaryLinking(s.NodeID()) &&
			!v.ProviderCrossBoundaryLinking(target) {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrCrossProviderLinkUnsupported(),
			constraint.Uncaptioned(target),
			constraint.Captioned("socket", s.SocketName()),
			constraint.Captioned("consumer provider", consumerProvider),
			constraint.Captioned("target provider", targetProvider),
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
// Its detail names the delivery key and the socket. The key stands bare, because
// the registered wording already says a duplicated delivery key is what the
// finding is about; the socket is captioned, since a socket name and a delivery
// key are both bare words and nothing else in the message tells them apart.
//
// Both are needed, and for different reasons. The socket belongs to a different
// entry from the one the objection is attributed to, so naming it is what makes
// the finding point at the second declaration as well as the first. The key is
// there because the locator cannot always carry it: what a key spells is the
// mechanism's own business, and a mechanism delivering into a file names a mount
// path, which carries the separator the locator dialect divides its segments
// with. A locator stops at the deepest segment it can address, so for such a key
// it names the mechanism's block and the detail is the only thing that says which
// key inside it. A key that happens to be one bare word does reach the locator,
// and the detail naming it as well costs nothing: a reader is told the same thing
// twice rather than left to find out which case they are in.
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
			out = append(out, s.RaiseWith(
				ErrSelfInjectKeyCollision(),
				constraint.Uncaptioned(s.DeliveryKey()),
				constraint.Captioned("socket", socket),
			))
		}
		return out
	},
}

// AttachmentInjectKeyNotDeliveredByAnotherSource reports each delivery key one
// attachment attached to a component asks the component's injection pipe for
// that another source has already delivered under.
//
// Three authoring surfaces deliver into one pipe: the component's own injection
// block, the blocks on the entries of its dependency block, and the block on each
// attachment attached to it. The pipe is one keyed space shared between them, so a
// key two of them deliver under is ambiguous about which value arrives, and the
// build halts rather than letting one surface silently displace another. The
// author resolves it by removing one of the two claims. Presence in both is the
// whole signal: the rule compares no values and does not treat two sources
// agreeing as acceptable, because the agreement is a property of one input rather
// than of the authoring.
//
// It is invoked once per attachment, which is the side the objection is
// attributed to, and reads one fact — the attributed deliveries the node's pipe
// carries. The attachment is the attributed side because the other surface may be
// the component's own block or one of its connections, neither of which is a
// subject a rule can be invoked over here, and because the attachment's block is
// a position an author can open. Each colliding key is its own objection, so an
// author whose attachment duplicates two keys is told about both rather than
// about whichever came first.
//
// # Why the answer is walked in order rather than searched
//
// The fact comes back in delivery order and the first claim under a key is the one
// that holds it, so the rule accumulates what has been delivered as it walks and
// asks of its own attachment's claims whether the key was already held. A search
// over the whole answer would read differently: it would find its own attachment's
// claim colliding with a LATER claim by another attachment, and refuse a pair that
// no build has a problem with — the later claim delivers nothing and takes no key,
// so nothing was displaced. That is the direction a rule about a shared keyed
// space is easiest to get wrong in, because both readings agree on every input
// where every claim resolves.
//
// A claim that delivered nothing is passed over rather than counted, for the same
// reason: it left the key free, so the next claim under it is the first to hold
// it. The fact carries that distinction precisely because it cannot be recovered
// from the pipe afterwards.
//
// # What the detail names
//
// The delivery key stands bare, because the registered wording already says a
// duplicated delivery key is what the finding is about. The mechanism is
// captioned, since a mechanism name and a delivery key are both bare words and
// nothing else in the message tells them apart — and a key is only duplicated
// within one mechanism, so the mechanism is what says which keyed space the
// finding is in.
//
// The node and the attachment are not in the detail, because the locator carries
// both: a finding is positioned at the attachment's own entry inside the
// component's block, which is the position the author edits. The key is in the
// detail as well as in the fact for the reason the sibling rule over a
// self-declared key gives: what a key spells is the mechanism's own business, so
// the locator naming the attachment's block cannot always name the key inside it.
var AttachmentInjectKeyNotDeliveredByAnotherSource = constraint.Check[
	constraint.HydratedView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrAttachmentInjectKeyCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeAttachmentSubject,
	) []constraint.Objection {
		var out []constraint.Objection
		held := map[string]bool{}
		for _, delivery := range v.NodeInjectDeliveries(s.NodeID()) {
			slot := delivery.Mechanism + "\x00" + delivery.DeliveryKey
			if delivery.Attachment == s.AttachmentName() && held[slot] {
				out = append(out, s.RaiseWith(
					ErrAttachmentInjectKeyCollision(),
					constraint.Uncaptioned(delivery.DeliveryKey),
					constraint.Captioned("mechanism", delivery.Mechanism),
				))
			}
			if delivery.Delivered {
				held[slot] = true
			}
		}
		return out
	},
}

// AttachmentInjectMappingCoversEveryAuthoredMechanism reports each delivery
// mechanism the host component's own injection block declares that one
// attachment's rendered injectMapping output carries no top-level key for.
//
// A FeatureRecipe's injectMapping rewrites the pipe the host component delivers
// into, and it names the mechanisms it writes as the top-level keys of its own
// rendered output. A component declaring a mechanism the output does not name has
// therefore asked for a delivery this attachment drops on the floor: the merge
// applies what the output carried and the declaration reaches nothing. The build
// halts rather than rendering against a pipe missing a mechanism the author
// dictated, and the author resolves it by writing the mechanism's key into the
// FeatureRecipe's mapping — which is what the registered wording says.
//
// It is invoked once per attachment, which is the side the finding is positioned
// at, and reads one fact — the pairing between what the component declared and
// what that attachment's output named. The attachment is the positioned side
// because the FeatureRecipe is reached through it: the mapping is authored in a
// catalog document the Aggregate does not contain, and the attachment's entry is
// the position in the author's own file that binds it. The FeatureRecipe is named
// in the detail for that reason rather than left to be looked up.
//
// Each unmapped mechanism is its own objection, so a component declaring two
// mechanisms an attachment maps neither of is told about both rather than about
// whichever the component declared first.
//
// # Why a mapping naming nothing is not a mapping naming none
//
// An output that parsed as a flat delivery dictionary makes no claim about
// mechanisms at all — it is the legitimate idiom for a FeatureRecipe producing
// env-only deliveries — and neither does an attachment binding a FeatureRecipe
// with no mapping to evaluate. The fact reports no pairing for either, so this
// rule is invoked over an empty answer and raises nothing. Reading an empty
// answer as "every declared mechanism went unmapped" would refuse every such
// FeatureRecipe, and the two states are told apart by the fact rather than here.
//
// # What the detail names
//
// The mechanism stands bare, because the registered wording already says an
// inject mechanism with no matching key is what the finding is about. The
// FeatureRecipe is captioned, since a mechanism name and a recipe name are both
// bare words and nothing else in the message tells them apart — and it is the
// document the remedy is applied to, which the locator cannot reach: the locator
// names the attachment's entry in the Aggregate, and the mapping is written
// somewhere else entirely.
//
// The node and the attachment are not in the detail, because the locator carries
// both.
var AttachmentInjectMappingCoversEveryAuthoredMechanism = constraint.Check[
	constraint.HydratedView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrUnmappedCustomMechanism(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeAttachmentSubject,
	) []constraint.Objection {
		var out []constraint.Objection
		for _, declared := range v.AttachmentInjectMappingMechanisms(
			s.NodeID(), s.AttachmentName(),
		) {
			if declared.Mapped {
				continue
			}
			out = append(out, s.RaiseWith(
				ErrUnmappedCustomMechanism(),
				constraint.Uncaptioned(declared.Mechanism),
				constraint.Captioned("recipe", declared.Recipe),
			))
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

// RecipePublishesEveryDeclaredProvidesKey reports each key a component type's
// output contract declares that the Recipe bound to the node resolved no value
// for.
//
// The contract is structural key-presence, subset-required and
// superset-permitted: every declared key has to arrive, while a Recipe
// publishing more than the contract names has still fulfilled it. Extra keys are
// therefore not this rule's finding at all — the reading that reports them is
// advisory and sits on the other side of the same contract, and it is invoked
// once per key the Recipe published rather than once per key the contract
// declares. The two readings share a contract and share neither an axis nor a
// severity.
//
// Values are never inspected. What arrives under a declared key is an opaque
// pointer string the runtime resolves, so its declared type stays a documentary
// contract between the producer and its consumers and there is nothing here to
// check it against. Presence is the whole of the question.
//
// # Why the axis is the DECLARED keys and not the published ones
//
// The keys this rule is invoked over are the ones the contract declares, which
// is what lets it see the condition at all: a key the Recipe never published is
// absent from the published set by construction, so a rule walking that set
// would be invoked zero times for exactly the inputs it exists to refuse. The
// enumeration the caller supplies is the declared contract, positioned at the
// Recipe — the document whose provides block an author adds the missing entry
// to.
//
// # It reads presence from the pipe rather than from the authored block
//
// The fact it reads is the value the render resolved for the key, whose second
// result is whether the key was resolved at all. That is the answer wanted: a
// key an author wrote and whose template resolved to nothing is a key the
// consumer reads as unset, and reading the authored block instead would report
// such an entry as fulfilled. It also means one fact answers the whole
// predicate, since the fact's absence half IS the violation.
//
// # Every shortfall is reported, not the first one
//
// The rule is invoked once per declared key, so a Recipe short of three of them
// raises three objections and an author corrects all three from one build. Each
// carries its own position — the Recipe, and the declared key inside its
// provides block — which is why they are not folded into one message: a position
// is something an author navigates to, and a single message naming three of them
// can be navigated to none.
//
// Its detail leads with the missing key, which is the value the registered
// wording is about and so carries no caption. The component type whose contract
// declares it and the instance the Recipe was bound to follow, each captioned,
// because the registered wording names neither and the locator names the
// Recipe's own provides entry rather than either of them. The instance is what
// tells two findings apart where one Recipe serves several instances of one type:
// the entry at fault is written once, so those findings share a locator and
// differ only here.
var RecipePublishesEveryDeclaredProvidesKey = constraint.Check[
	constraint.HydratedView,
	constraint.NodeProvidesKeySubject,
]{
	Sentinel: ErrProvidesContractUnfulfilled(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeProvidesKeySubject,
	) []constraint.Objection {
		if _, resolved := v.ResolvedNodeProvides(s.NodeID(), s.ProvidesKey()); resolved {
			return nil
		}
		componentType, _ := v.FlattenedNodeComponentType(s.NodeID())
		return []constraint.Objection{s.RaiseWith(ErrProvidesContractUnfulfilled(),
			constraint.Uncaptioned(s.ProvidesKey()),
			constraint.Captioned("declared by component type", componentType),
			constraint.Captioned("bound to component instance", s.NodeID()))}
	},
}

// The two pillar roots an artifact template may only read when the document
// carrying it supplies them. They are declared once and read by every form of the
// rule — the three declared values below, one per carrier the artifacts walk is
// reached by — because the roots ARE the rule's subject: a copy in each site would agree
// today and would be the thing that silently diverges if a pillar were ever
// renamed, since a stale spelling matches nothing and the check goes quiet rather
// than failing.
const (
	pillarRefWorkload = ".workload"
	pillarRefSource   = ".source"
)

// pillarRefMatches reports whether a template reference names a pillar root or
// reaches into it. One helper rather than the same two-part test written at each
// arm of each form: what counts as reading a pillar is one decision, and the
// exact-or-descendant shape of it is the part a re-implementation gets subtly
// wrong (a prefix test alone would match a sibling root whose name merely starts
// with the pillar's).
func pillarRefMatches(ref, pillar string) bool {
	return ref == pillar || strings.HasPrefix(ref, pillar+".")
}

// sourceValueDeclared reports whether a source VALUE carries a source. It is the
// one home for the emptiness condition itself, and every reader of that
// condition reaches it — directly where what it holds is the value, and through
// recipeSourceDeclared where what it holds is the entity.
//
// A source that evaluated to an empty string leaves the entity with none just as
// an absent source does, and an author cannot tell the two apart from the
// outcome — so this is one condition rather than two, which is what lets a rule
// about a template reading the source pillar name a single cause.
//
// One helper for the same reason the pillar roots above have one home: this is a
// single decision with more than one reader, and a copy per reader would agree
// today and would be exactly what diverges the next time the emptiness condition
// changes. Any shape other than nil and string carries a source: a structured
// source is a container, and a container that exists names something.
func sourceValueDeclared(src any) bool {
	switch s := src.(type) {
	case nil:
		return false
	case string:
		return s != ""
	default:
		return true
	}
}

// recipeSourceDeclared reports whether a Recipe carries a source, reading the
// RESOLVED value rather than the text the author wrote.
//
// The emptiness condition itself lives in sourceValueDeclared; this adds the one
// thing an entity has that a bare value does not, which is that the entity may
// be absent. A nil Recipe carries no source.
func recipeSourceDeclared(recipe *types.Recipe) bool {
	if recipe == nil {
		return false
	}
	return sourceValueDeclared(recipe.Spec.Source)
}

// The artifact-pillar rule, declared once per artifacts block the rule governs.
//
// One rule, three values, and the reason is the type rather than the rule: a
// check carries exactly one subject type parameter, and three separately
// authored artifacts blocks are three subject kinds — a component's own Recipe,
// the FeatureRecipe an attachment binds on a node, and the FeatureRecipe a
// boundary-scope attachment binds. An artifact's output name is chosen
// independently in each of those documents and is meaningful only together with
// the block it was written in, so one value over one kind would govern one of the
// three while reading as though it governed the rule.
//
// The predicate has ONE body, [artifactPillarObjections], and each value supplies
// its own four facts and its own subject to it. The three would otherwise carry
// the same reference collection, the same two conditions and the same reporting
// three times over — copies that agree today, where the agreement is what hides
// the next change to what counts as a reference or to how a finding reads.
//
// What differs between the three is only where the facts are keyed, and that
// difference is the whole point of there being three: a FeatureRecipe's entries
// flow through the same artifacts walk as a node's own, under the host
// component's node identity, so a value reading node-keyed facts about them would
// answer from the host's pillars while reading the attachment's template.

// artifactPillarSubject is what the predicate needs of the subject it was invoked
// over: the artifact, and the ability to raise a finding against it.
//
// It is a constraint over the three subject kinds rather than a concrete type
// because each check carries one subject type parameter, so the values differ in
// their subject even where their predicate does not.
type artifactPillarSubject interface {
	constraint.Subject
	ArtifactName() string
}

// artifactPillarObjections is the one body of the artifact-pillar predicate: it
// collects the references one artifact template makes, read with the pair that
// template is read with, and reports every reference reaching for a pillar the
// document carrying the entry does not supply.
//
// The four values arrive as arguments rather than being read here, because
// reading them is precisely the part that differs between the three values
// declared over this predicate: each keys its facts by its own coordinates, and a
// predicate reaching for a view would have to know which.
//
// carrierDetail is what the calling value adds to the objection after the
// artifact — the attachment, where the entry belongs to one. It is a positional
// argument rather than a variadic so a value cannot omit it silently; a node's
// own artifacts block supplies none.
//
// A template that does not parse is not this predicate's finding: evaluation
// reports the parse error against the same entry, so refusing it here as well
// would report one authoring fault twice.
func artifactPillarObjections[S artifactPillarSubject](
	s S,
	delims constraint.ArtifactTemplateDelimiters,
	templateText string,
	workloadDeclared, sourceDeclared bool,
	carrierDetail []constraint.DetailValue,
) []constraint.Objection {
	refs, err := gotmpl.CollectTemplateFieldAccessesWithDelims(
		templateText, delims.Left, delims.Right,
	)
	if err != nil {
		return nil
	}
	var out []constraint.Objection
	for _, ref := range refs {
		switch {
		case !workloadDeclared && pillarRefMatches(ref, pillarRefWorkload):
		case !sourceDeclared && pillarRefMatches(ref, pillarRefSource):
		default:
			continue
		}
		detail := []constraint.DetailValue{
			constraint.Uncaptioned(ref),
			constraint.Captioned("artifact", s.ArtifactName()),
		}
		out = append(out, s.RaiseWith(
			ErrArtifactPillarNotDeclared(), append(detail, carrierDetail...)...,
		))
	}
	return out
}

// ArtifactPillarDeclared reports each reference an artifact template makes to a
// configuration pillar the node's Recipe does not carry.
//
// Two pillars are readable from an artifact template only when the Recipe
// supplies them. A Recipe declaring no workload body reaches artifact evaluation
// with no workload to read, and one carrying no source reaches it with no source
// to read.
//
// What happens then depends on how deep the reference goes, and the rule refuses
// both cases with one message because neither is acceptable. A reference of one
// segment is admitted, because the pillar is a root the context defines: it
// renders the literal text "<no value>" and the build writes the file. A
// reference that descends through the pillar fails instead, with wording that
// names the first hop rather than the expression the author wrote. So the rule
// stands in front of a shipped placeholder on one side and an unactionable halt
// on the other, and replaces both with a halt naming the expression to fix.
//
// The source arm reads whether a source survived resolution rather than whether
// one was authored, and the two differ: a source template evaluating to an empty
// string leaves the Recipe with no source just as an absent source does. One
// condition covers both, so an author is told the same thing whichever way the
// source went missing.
//
// It reads four facts and holds no traversal over subjects, because it is
// invoked once per artifact a node projects. The delimiter pair is one of the
// four and is what makes the reference set correct rather than approximate: a
// provider whose own target syntax uses the usual pair declares an alternate
// one, and read with the wrong pair a template's downstream syntax would surface
// as references it does not make. The pair completes before the text does, which
// is why the rule sits at the stage that holds the text and reads the pair
// through the stage containment.
//
// Two properties are worth stating because a reader may expect the shape the
// hand-written arms this value replaced had. This value reports EVERY offending
// reference in one template rather than the first, because an author naming two
// absent pillars is better told about both than about whichever the parser reached
// first. And a template that does not parse is not this rule's finding: evaluation
// reports the parse error against the same entry, so refusing it here as well
// would report one authoring fault twice — the parse pre-check standing beside the
// invocation reports it instead, which is the one condition that pre-check kept.
//
// Its detail names the offending expression and the artifact that holds it. The
// expression stands bare, because the registered wording already says a pillar
// reference is what the finding is about; the artifact is captioned, because an
// output name is a filename and a filename beside a dotted reference is two
// quoted strings a reader cannot tell apart.
//
// Naming the artifact is what makes the finding actionable, and the locator is
// why. An artifact's output name is a filename, so it carries the separator the
// locator dialect divides its segments with, and a locator stops at the deepest
// segment it can address rather than spelling a path that resolves nowhere: it
// names the artifacts block in the Recipe's own file. That is the block the author
// edits, and the detail is the only thing that can say which artifact inside it —
// which matters most in a Recipe that declares many.
//
// The node is a coordinate the subject carries and the locator does not, and it is
// deliberately left out of the detail: the fault is in the Recipe's text rather
// than in any one node that binds it, so naming a node would point an author at
// an instance when the edit is in the entry every instance shares.
var ArtifactPillarDeclared = constraint.Check[
	constraint.HydratedView,
	constraint.NodeArtifactSubject,
]{
	Sentinel: ErrArtifactPillarNotDeclared(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeArtifactSubject,
	) []constraint.Objection {
		return artifactPillarObjections(
			s,
			v.ProviderArtifactTemplateDelimiters(s.NodeID()),
			v.ArtifactTemplateText(s.NodeID(), s.ArtifactName()),
			v.ParsedNodeWorkloadPresent(s.NodeID()),
			v.ResolvedNodeSourceDeclared(s.NodeID()),
			nil,
		)
	},
}

// AttachmentArtifactPillarDeclared reports each reference an artifact template
// the FeatureRecipe one attachment binds on a node makes to a configuration
// pillar that FeatureRecipe does not carry.
//
// Every reading the value above sets out applies here over a different document:
// the pillars are readable only where the FeatureRecipe supplies them, a
// reference of one segment renders the literal placeholder while a deeper one
// fails evaluation, and the source arm reads whether a source survived
// resolution rather than whether one was authored.
//
// Each of its four facts is keyed by the attachment as well as by the node, and
// that is what makes the verdict the attachment's rather than the host's. The
// render of an attachment walks the same producers under the HOST component's
// node identity, so a fact keyed by the node alone would answer about the host's
// pillars while the template being read is the attachment's — admitting a
// reference the FeatureRecipe never supplied wherever the host happens to supply
// it, and refusing a legitimate one wherever the host does not. The delimiter
// pair is keyed per attachment for the same reason: the attachment's
// FeatureRecipe declares its own capability, so the provider reading its entries
// is independently authored from the one reading the node's.
//
// Its detail names the offending expression, the artifact, and the attachment.
// The attachment is what the locator cannot carry — the locator names the
// artifacts block inside the FeatureRecipe, which is the document an author
// edits, and a node may carry several attachments whose blocks are separate
// documents — so the objection says which one.
var AttachmentArtifactPillarDeclared = constraint.Check[
	constraint.HydratedView,
	constraint.NodeAttachmentArtifactSubject,
]{
	Sentinel: ErrArtifactPillarNotDeclared(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeAttachmentArtifactSubject,
	) []constraint.Objection {
		return artifactPillarObjections(
			s,
			v.AttachmentProviderArtifactTemplateDelimiters(s.NodeID(), s.AttachmentName()),
			v.AttachmentArtifactTemplateText(s.NodeID(), s.AttachmentName(), s.ArtifactName()),
			v.AttachmentParsedWorkloadPresent(s.NodeID(), s.AttachmentName()),
			v.AttachmentResolvedSourceDeclared(s.NodeID(), s.AttachmentName()),
			[]constraint.DetailValue{constraint.Captioned("attachment", s.AttachmentName())},
		)
	},
}

// BoundaryAttachmentArtifactPillarDeclared reports each reference an artifact
// template the FeatureRecipe one boundary-scope attachment binds makes to a
// configuration pillar that FeatureRecipe does not carry.
//
// It is the same question the two values above ask, rooted where such an
// attachment is declared. A boundary-scope attachment is written once on a
// boundary and reaches every component the boundary holds, so its facts are keyed
// by the boundary rather than by any component: keyed by a node, one authored
// entry would be asked about once per component the boundary holds, and not at
// all for a boundary holding none.
//
// Its detail names the offending expression, the artifact, and the attachment, on
// the reading the node-carried form follows: the locator names the artifacts
// block inside the FeatureRecipe and carries neither the boundary nor the
// attachment.
var BoundaryAttachmentArtifactPillarDeclared = constraint.Check[
	constraint.HydratedView,
	constraint.BoundaryAttachmentArtifactSubject,
]{
	Sentinel: ErrArtifactPillarNotDeclared(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.BoundaryAttachmentArtifactSubject,
	) []constraint.Objection {
		return artifactPillarObjections(
			s,
			v.BoundaryAttachmentProviderArtifactTemplateDelimiters(s.BoundaryPath(), s.AttachmentName()),
			v.BoundaryAttachmentArtifactTemplateText(
				s.BoundaryPath(), s.AttachmentName(), s.ArtifactName(),
			),
			v.BoundaryAttachmentParsedWorkloadPresent(s.BoundaryPath(), s.AttachmentName()),
			v.BoundaryAttachmentResolvedSourceDeclared(s.BoundaryPath(), s.AttachmentName()),
			[]constraint.DetailValue{constraint.Captioned("attachment", s.AttachmentName())},
		)
	},
}

// emittedNameCollisionObjections is the predicate all three emitted-name collision
// values share: an output name more than one of a render's blocks produced is
// refused, and a name one block produced is not.
//
// It is one function because it is one rule. The three values differ only in which
// carrier's fact they read and which document they name, so a second copy of the
// predicate would agree on the day it was written and be free to drift the moment
// the condition gains a term — and a drift here would let one carrier refuse an
// input the others admit, which is a difference no reader could tell from a
// deliberate one.
//
// # Why the test is the size of the set and nothing finer
//
// The fact answers with the blocks that produced a file under one name,
// deduplicated. So two or more entries means two independently authored blocks met
// on a name, and one entry means a single block produced it — including a block that
// produced several files under one name, which the retention records as one
// contributor because that is one authoring decision rather than a collision between
// two.
//
// The bytes are deliberately unreachable here. A predicate that could compare
// content would let two blocks that happen to agree pass as though the name had been
// claimed once, which is the same output with the author's intent unrecorded.
//
// # What the detail carries, and why each value is spelled as it is
//
// The emitted name is CAPTIONED, because it is a filename and a filename standing
// bare beside another quoted value is not something a reader can tell apart from it.
// The blocks that met are captioned too and are joined into one value in the order
// the fact answered, which is production order — so the first block named is the one
// that reached the name first, which is what tells an author which of the two to
// rename. Whatever the caller adds, naming the attachment where the locator cannot,
// follows.
//
// # Every offending name is reported, one objection each
//
// The rule is invoked once per emitted name, so a render whose blocks met on two
// names raises twice and an author is told about both. The hand-written reading this
// replaced returned one failure carrying every collision it found; the set of
// authored faults reported is the same, and the shape is one positioned diagnostic
// per fault rather than one message listing several positions.
func emittedNameCollisionObjections(
	s constraint.Subject,
	emittedName string,
	origins []string,
	extra []constraint.DetailValue,
) []constraint.Objection {
	if len(origins) < 2 {
		return nil
	}
	detail := []constraint.DetailValue{
		constraint.Captioned("output name", emittedName),
		constraint.Captioned("produced by", strings.Join(origins, ", ")),
	}
	detail = append(detail, extra...)
	return []constraint.Objection{
		s.RaiseWith(validator.ErrRecipeFilenameCollisionAcrossInputKinds(), detail...),
	}
}

// EmittedNameProducedByOneBlock reports each output name that more than one block of
// a node's own Recipe produced a file under.
//
// A Recipe's source render, its workload synthesis, its artifacts entries and its
// pipeline steps each place files under names chosen independently of one another,
// and nothing declares those names distinct. Two of them meeting on one name means
// one of the two files is not in the output the author expected, and which one
// survives is a property of the order the blocks happen to run in rather than of
// anything the author wrote — so the build halts and names both contributors instead
// of shipping whichever won.
//
// It reads one fact and holds no traversal, because it is invoked once per name the
// node's own render emitted.
//
// Its detail names the output name and the blocks that met, and deliberately not the
// node: the fault is in the Recipe's text rather than in any one node that binds it,
// so naming a node would point an author at an instance when the edit is in the
// block every instance shares. The locator names the component instance, which is
// where a reader starts.
var EmittedNameProducedByOneBlock = constraint.Check[
	constraint.EmittedView,
	constraint.NodePathSubject,
]{
	Sentinel: validator.ErrRecipeFilenameCollisionAcrossInputKinds(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.EmittedView,
		s constraint.NodePathSubject,
	) []constraint.Objection {
		return emittedNameCollisionObjections(
			s,
			s.EmittedPath(),
			v.EmittedFileOriginSet(s.NodeID(), s.EmittedPath()),
			nil,
		)
	},
}

// AttachmentEmittedNameProducedByOneBlock reports each output name that more than
// one block of the FeatureRecipe one attachment binds on a node produced a file
// under.
//
// It is the same rule the value above states, asked of a different document. Its
// fact is keyed by the attachment as well as by the node, and that is what makes the
// verdict the attachment's rather than the host's: an attachment's render emits
// under the HOST component's node identity, so a fact keyed by the node alone would
// answer about the host's own emission while the blocks in question are the
// attachment's — refusing none of the attachment's own collisions and refusing the
// host's twice.
//
// Its detail names the attachment as well, because the locator cannot: the locator
// names the FeatureRecipe the author edits, and a node may carry several attachments
// whose FeatureRecipes are separate documents.
var AttachmentEmittedNameProducedByOneBlock = constraint.Check[
	constraint.EmittedView,
	constraint.NodeAttachmentPathSubject,
]{
	Sentinel: validator.ErrRecipeFilenameCollisionAcrossInputKinds(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.EmittedView,
		s constraint.NodeAttachmentPathSubject,
	) []constraint.Objection {
		return emittedNameCollisionObjections(
			s,
			s.EmittedPath(),
			v.AttachmentEmittedFileOriginSet(s.NodeID(), s.AttachmentName(), s.EmittedPath()),
			[]constraint.DetailValue{constraint.Captioned("attachment", s.AttachmentName())},
		)
	},
}

// BoundaryAttachmentEmittedNameProducedByOneBlock reports each output name that more
// than one block of the FeatureRecipe one boundary-scope attachment binds produced a
// file under.
//
// It is the same question the two values above ask, rooted where such an attachment
// is declared. A boundary-scope attachment is written once on a boundary and reaches
// every component the boundary holds, so its fact is keyed by the boundary rather
// than by any component: keyed by a node, one authored document would be asked about
// once per component the boundary holds, and not at all for a boundary holding none.
//
// Its detail names the output name, the blocks that met, and the attachment, on the
// reading the node-carried form follows.
var BoundaryAttachmentEmittedNameProducedByOneBlock = constraint.Check[
	constraint.EmittedView,
	constraint.BoundaryAttachmentPathSubject,
]{
	Sentinel: validator.ErrRecipeFilenameCollisionAcrossInputKinds(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.EmittedView,
		s constraint.BoundaryAttachmentPathSubject,
	) []constraint.Objection {
		return emittedNameCollisionObjections(
			s,
			s.EmittedPath(),
			v.BoundaryAttachmentEmittedFileOriginSet(
				s.BoundaryPath(), s.AttachmentName(), s.EmittedPath(),
			),
			[]constraint.DetailValue{constraint.Captioned("attachment", s.AttachmentName())},
		)
	},
}

// The provider-compatibility rule, declared once per pipeline the rule governs.
//
// One rule, three values, and the reason is the type rather than the rule: a
// check carries exactly one subject type parameter, and three separately
// authored pipelines are three subject kinds — a component's own Recipe
// pipeline, the pipeline an attachment's FeatureRecipe carries on a node, and the
// pipeline a boundary-scope attachment's FeatureRecipe carries. A step
// identifier is meaningful only together with the pipeline it was written in, so
// one value over one kind would govern one of the three and read as though it
// governed the rule.
//
// # What each predicate does, and what it deliberately does not do
//
// Each reads the scope the step's export declares, the capabilities the step's
// plugin declares, and the provider rendering the documents that scope names —
// then objects when the provider is absent from the declared set. Matching is
// exact-string against the enumerated declaration, with no family abstraction:
// an empty capability set declares compatibility with nothing and therefore
// matches no provider, so a step whose plugin declares none objects rather than
// passing as unconstrained.
//
// None of them holds a loop over steps, because each is invoked once per step.
// None reads a descriptor: the capabilities and the scope arrive as declared
// facts, so a predicate here crosses to no guest and holds no plugin
// self-report — which is what lets the rule be complete at the point every
// plugin has described itself rather than at the point documents exist.
//
// # The provider facts are keyed where the value varies, and never by the part it plays
//
// A provider does not vary across the steps of one pipeline: one entity declares
// one capability for the whole of it. So the provider facts are keyed by the node
// or the attachment rather than by the step, and there is one fact per authoring
// site rather than one per part a provider plays at a site.
//
// That distinction is load-bearing here and easy to lose. The provider rendering
// a node's own documents is the provider a step of that node's own pipeline is
// matched against, AND it is the provider a host-addressed step of an
// attachment's pipeline on that node is matched against — one value, read at two
// sites. Which part it plays is a property of the reading site, so it is stated
// in each predicate below and carried in no fact name.
//
// # Emptiness
//
// An absent provider for the documents a scope names means the site has no such
// documents, and the rule does not enforce there. The concrete case is a
// host-addressed step at a site that stands against no host component: a
// component's own pipeline and a boundary-scope attachment's pipeline both have
// one, and a step addressing host documents there is refused by the rule that
// owns the missing document set rather than reported here as a compatibility
// claim against an empty provider.
//
// The opposite case does not arise at any site a traversal reaches: the
// capability an entity declares is required by both the Recipe and FeatureRecipe
// contracts, so the provider rendering a site's own documents is always present
// wherever a step of that site is enumerated.

// PipelineStepProviderCompatible reports each step of a node's own Recipe
// pipeline whose plugin does not declare compatibility with the provider
// rendering the documents the step addresses.
//
// A node's own pipeline stands against no host component, so a step addressing
// host documents has no provider to be matched against and is left to the rule
// that owns the missing document set. Every other step addresses the node's own
// documents, which the node's own provider renders.
//
// Its detail names that provider, because the locator names the authored step
// and a provider is selected rather than written — so the value the author has
// to reconcile the declaration against appears nowhere else in the objection.
var PipelineStepProviderCompatible = constraint.Check[
	constraint.DescribedView,
	constraint.NodePipelineStepSubject,
]{
	Sentinel: ErrPluginProviderIncompatible(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodePipelineStepSubject,
	) []constraint.Objection {
		if v.StepExportScope(s.NodeID(), s.StepID()) == types.ScopeHost {
			return nil
		}
		provider := v.NodeRenderingProvider(s.NodeID())
		if providerCompatible(provider, v.StepPluginCompatibleCapabilities(s.NodeID(), s.StepID())) {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrPluginProviderIncompatible(), provider)}
	},
}

// PipelineStepScopeHasADocumentSet reports each step of a node's own Recipe
// pipeline whose export declares it operates on host documents, which that
// pipeline has none of.
//
// It is the rule the compatibility rule above defers a host-addressed step to.
// The two are separate findings with separate remedies: that one asks whether a
// step's plugin declares the provider rendering the documents its scope names,
// this one asks whether the documents that scope names exist at this site at all
// — and a site with no such documents has no provider either, so asking about
// compatibility there would report a claim against an empty value rather than
// the fault the author has to fix.
//
// # Why the answer is complete before anything runs
//
// A node's own Recipe pipeline stands against no host component. That is a
// property of the site rather than of a render: the host document set such a
// pipeline is handed is empty wherever the pipeline is reached, so a step
// declaring it addresses host documents addresses nothing, and no walk has to
// run for that to be settled. What the walk adds is only which operations the
// guest chose to return, and that cannot make a host document set appear.
//
// # Emptiness
//
// A plugin the host could not resolve, load or ask reports no scope, and no
// scope is not the host scope — so such a step is not reported here. It halts
// with the host's own failure to obtain the self-report, which is the thing to
// repair, and this rule stays silent rather than naming a scope the author never
// declared. An export declaring no scope at all is the same reading: the step
// addresses the site's own documents, which the site has.
//
// It raises no detail. The locator names the authored step inside the Recipe,
// and the scope the finding is about is the one this rule is for, so there is no
// selected value the author has to reconcile the declaration against.
var PipelineStepScopeHasADocumentSet = constraint.Check[
	constraint.DescribedView,
	constraint.NodePipelineStepSubject,
]{
	Sentinel: ErrPipelineHostScopeUnavailable(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodePipelineStepSubject,
	) []constraint.Objection {
		if v.StepExportScope(s.NodeID(), s.StepID()) != types.ScopeHost {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrPipelineHostScopeUnavailable())}
	},
}

// AttachmentPipelineStepProviderCompatible reports each step of the pipeline an
// attachment's FeatureRecipe carries on a node whose plugin does not declare
// compatibility with the provider rendering the documents the step addresses.
//
// This is the one site where both arms are live, and the two providers are
// different values from different authored entities: a step addressing the
// attachment's own documents is matched against the provider its FeatureRecipe
// declares, while a step addressing the host component's documents is matched
// against the provider the host node's own Recipe declares — the same value that
// site's own pipeline is matched against. A host component with no bound Recipe
// renders no host documents, so a host-addressed step there is left to the rule
// that owns the missing document set.
//
// Its detail names the attachment and the provider. The locator names the
// authored step inside the FeatureRecipe, which is the document to edit, and
// neither the attachment that bound it nor the selected provider appears there.
//
// # The attachments this rule is not about
//
// An attachment whose FeatureRecipe declares the boundary scope is evaluated
// once for the boundary that holds it, not once for each component carrying it —
// and it still appears in every one of those components' merged attachment sets,
// because that set is what a component carries rather than where the attachment
// was written. So the rule ends on such an attachment without reading anything
// further: the pipeline it names runs on the boundary's surface, where the rule
// rooted at a boundary asks this same question once. Reading the scope is what
// keeps one authored step from being reported once per component that inherited
// it.
var AttachmentPipelineStepProviderCompatible = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentPipelineStepSubject,
]{
	Sentinel: ErrPluginProviderIncompatible(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentPipelineStepSubject,
	) []constraint.Objection {
		if v.AttachmentExecutionScope(s.NodeID(), s.AttachmentName()) == executionScopeBoundary {
			return nil
		}
		provider := v.AttachmentRenderingProvider(s.NodeID(), s.AttachmentName())
		if v.AttachmentStepExportScope(s.NodeID(), s.AttachmentName(), s.StepID()) == types.ScopeHost {
			provider = v.NodeRenderingProvider(s.NodeID())
			if provider == "" {
				return nil
			}
		}
		declared := v.AttachmentStepPluginCompatibleCapabilities(
			s.NodeID(), s.AttachmentName(), s.StepID(),
		)
		if providerCompatible(provider, declared) {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrPluginProviderIncompatible(), s.AttachmentName(), provider,
		)}
	},
}

// AttachmentPipelineStepOwnScopeHasAMergedTarget reports each step of an
// attachment's pipeline that addresses the attachment's own documents on a node
// whose documents are merged with its dispatch group's, where the step has
// nothing to address by the time it runs.
//
// It is the second rule over an attachment's pipeline steps, and the two are
// separate because they are separate findings with separate conditions and
// separate remedies. The one beside it asks whether a step's plugin declares
// compatibility with the provider its scope names. This one asks whether the
// documents that scope names will exist as discrete documents at all — a
// question that has an answer before any provider is matched against anything,
// and a different answer to give the author.
//
// # Why the condition is about the host's provider and not the attachment's
//
// A provider that merges its dispatch group's per-node output renders that
// output as fragments: the discrete documents arrive only once the group is
// merged. An attachment's pipeline against such a component therefore cannot
// run while the component is rendered, and runs once the merge has produced
// the documents instead. The attachment's OWN output is never part of that
// merge — an attachment's documents are written under its own position, as
// discrete files, whichever provider the component uses — so a step addressing
// the attachment's own documents has no merged document waiting for it, and
// nothing to address when it finally runs.
//
// So the finding is a property of the component's provider paired with the
// step's own scope, and the attachment's provider does not enter it. That is
// what makes the condition readable here: both halves are declared facts at the
// point every plugin and provider has described itself.
//
// The remedies are the ones the condition admits: give the attachment's own
// output a provider that does not merge, address the component's documents
// instead, or drop the step.
//
// # The attachments this rule is not about
//
// An attachment whose FeatureRecipe declares the boundary scope is evaluated
// once for the boundary that holds it, and its own output is written under that
// boundary rather than as a contribution to any component's dispatch group. The
// component's provider therefore decides nothing about where such an
// attachment's own documents stand, and the rule ends on it — the condition
// below would otherwise refuse a step that has a target, on the strength of a
// component whose merge the attachment never joins.
var AttachmentPipelineStepOwnScopeHasAMergedTarget = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentPipelineStepSubject,
]{
	Sentinel: ErrPipelinePatchPreAssembly(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentPipelineStepSubject,
	) []constraint.Objection {
		if v.AttachmentExecutionScope(s.NodeID(), s.AttachmentName()) == executionScopeBoundary {
			return nil
		}
		if v.AttachmentStepExportScope(
			s.NodeID(), s.AttachmentName(), s.StepID(),
		) == types.ScopeHost {
			return nil
		}
		if v.ProviderBoundaryAssembly(s.NodeID()) != wasm.BoundaryAssemblyMerge {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrPipelinePatchPreAssembly(), s.AttachmentName(),
		)}
	},
}

// BoundaryAttachmentPipelineStepProviderCompatible reports each step of the
// pipeline a boundary-scope attachment's FeatureRecipe carries whose plugin does
// not declare compatibility with the provider rendering the documents the step
// addresses.
//
// A boundary-scope attachment stands against no single component, so it has no
// host documents and a host-addressed step is left to the rule that owns the
// missing document set — the same shape a node's own pipeline has, reached for a
// different reason. Every other step addresses the attachment's own documents,
// which the provider its FeatureRecipe declares renders.
//
// Its detail names the attachment and the provider, on the same reading the
// node-carried attachment pipeline follows: the locator names the authored step
// inside the FeatureRecipe and carries neither.
var BoundaryAttachmentPipelineStepProviderCompatible = constraint.Check[
	constraint.DescribedView,
	constraint.BoundaryAttachmentPipelineStepSubject,
]{
	Sentinel: ErrPluginProviderIncompatible(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.BoundaryAttachmentPipelineStepSubject,
	) []constraint.Objection {
		if v.BoundaryAttachmentStepExportScope(
			s.BoundaryPath(), s.AttachmentName(), s.StepID(),
		) == types.ScopeHost {
			return nil
		}
		provider := v.BoundaryAttachmentRenderingProvider(s.BoundaryPath(), s.AttachmentName())
		declared := v.BoundaryAttachmentStepPluginCompatibleCapabilities(
			s.BoundaryPath(), s.AttachmentName(), s.StepID(),
		)
		if providerCompatible(provider, declared) {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrPluginProviderIncompatible(), s.AttachmentName(), provider,
		)}
	},
}

// BoundaryAttachmentPipelineStepScopeHasADocumentSet reports each step of the
// pipeline a boundary-scope attachment's FeatureRecipe carries whose export
// declares it operates on host documents, which such a pipeline has none of.
//
// It is the rule the compatibility rule above defers a host-addressed step to,
// on the same division of labour the node's own pipeline has: that one asks
// whether a step's plugin declares the provider rendering the documents its
// scope names, this one whether the documents that scope names exist at this
// site at all.
//
// # Why the answer is complete before anything runs
//
// A boundary-scope attachment stands against no single component. Its pipeline
// runs on the boundary's own surface, where there is no one component's document
// set to be the host one — so the host set such a pipeline is handed is empty
// wherever it is reached, and a step declaring it addresses host documents
// addresses nothing. That follows from where the attachment was declared, which
// is settled long before any node is walked.
//
// # Emptiness
//
// A plugin the host could not resolve, load or ask reports no scope, and no
// scope is not the host scope, so such a step is not reported here — it halts
// with the host's own failure to obtain the self-report. An export declaring no
// scope addresses the attachment's own documents, which this site has.
//
// Its detail names the attachment. The locator names the authored step inside
// the FeatureRecipe, which is the document to edit, and the name the operator
// attached it under appears nowhere in it.
var BoundaryAttachmentPipelineStepScopeHasADocumentSet = constraint.Check[
	constraint.DescribedView,
	constraint.BoundaryAttachmentPipelineStepSubject,
]{
	Sentinel: ErrPipelineHostScopeUnavailable(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.BoundaryAttachmentPipelineStepSubject,
	) []constraint.Objection {
		if v.BoundaryAttachmentStepExportScope(
			s.BoundaryPath(), s.AttachmentName(), s.StepID(),
		) != types.ScopeHost {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrPipelineHostScopeUnavailable(), s.AttachmentName(),
		)}
	},
}
