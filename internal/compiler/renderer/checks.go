// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"bytes"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/libraryimport"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
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
// is selected, so neither is a key the Release document holds — which leaves
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
// is selected, so neither is a key the Release document holds.
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

// BoundaryAttachmentFragmentsReachAnAssembler reports each attachment a boundary
// declares on itself whose output includes pre-assembly fragments and joined a
// dispatch group that is not handed to an assembler.
//
// A provider that folds its group's output into one document writes a fragment
// only through the assembler that folds it, and a group's assembler is the engine
// elected from the components that joined it. An attachment a boundary declares
// renders once for the boundary and joins the group the boundary's own path
// declaration evaluates to for it — so where no component evaluates to the same
// group, nothing is elected, the group is written flat, and the attachment's
// fragments reach no file while the build looks as though it succeeded. That is a
// Loud Guardrail case rather than a warning one: the output is content the author
// declared, and nothing downstream records that it is missing.
//
// It is the twin of the rule over a group whose elected provider claims an
// assembly its plugin cannot perform. That rule reads the elected engine's claim,
// and a group nothing was elected for has none, so a group holding only a
// boundary-level attachment's fragments is invisible to it; this rule reads the
// attachment instead, which is why it is keyed by the attachment rather than the
// group.
//
// It reads one fact and needs every half of it. Fragments alone cannot say whether
// they reach a file, and the group's assembly alone cannot say whether this
// attachment had anything for it to fold: an attachment whose output was placed as
// files of its own is written whether or not its group assembles, and is not
// refused. Whether the group assembles is the answer the group's finalization acts
// on, recorded when the contributions were sorted, so the rule and the
// finalization cannot disagree about a group's branch. Its coverage bound is that
// answer: a group that IS handed to an assembler, which then folds none of the
// attachment's fragments, is outside it.
//
// It is invoked once per attachment the render routed output for, positioned at
// the attachment's own entry in the Release — the one place an operator moves it
// or changes the directory it shares. Its detail captions every value: the
// boundary, the Feature instance, the dispatch path its output was routed to and
// the provider, because the path is evaluated output that the locator cannot name.
var BoundaryAttachmentFragmentsReachAnAssembler = constraint.Check[
	constraint.DispatchedView,
	constraint.BoundaryAttachmentSubject,
]{
	Sentinel: ErrBoundaryContributionNotAssembled(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DispatchedView,
		s constraint.BoundaryAttachmentSubject,
	) []constraint.Objection {
		routing, routed := v.BoundaryAttachmentOutputRouting(s.BoundaryPath(), s.AttachmentName())
		if !routed || !routing.Fragments || routing.GroupAssembles {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrBoundaryContributionNotAssembled(), boundaryContributionAdvice,
			constraint.Captioned("boundary", s.BoundaryPath()),
			constraint.Captioned("feature", s.AttachmentName()),
			constraint.Captioned("dispatch path", routing.AssemblyPath),
			constraint.Captioned("provider", routing.Provider),
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
// value it finds, and a rule reading the names alone would refuse a Release
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
// selected, so neither is a key the Release document holds.
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
// to go: a component's name is written among the components an Aggregate
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

// derivedNameScopeCaption is the rule's word for the further coordinate that
// completes a contributor's identity, spelled for the kind of contributor it
// belongs to.
//
// The coordinate exists because the kind's own name does not always identify one
// contributor: a component carries one attachment per key its author wrote, and a
// feature instance name may be written at more than one boundary. Presenting it
// bare would leave a reader with two quoted strings and no way to tell which is
// which, so the caption says what the second one is in the words the author's own
// document uses for it.
//
// The kind arrives as a fact and is not decided here, exactly as in the caption
// above: the render classifies a contributor once, at the site that names one.
func derivedNameScopeCaption(contributorKind string) string {
	if contributorKind == producerKindFeature {
		return "attached at boundary"
	}
	return "carrying attachment"
}

// AssemblyGroupDerivesEachNameOnce reports each contributor to a dispatch group
// that derived a name another contributor to the same group also derived, or that
// carried one name twice inside a single one of its own declarations.
//
// A contributor derives the names its output addresses resources by — the
// identifier a target uses for one resource, the name one output is published
// under for another to reference — and puts them inside the file bytes the host
// writes verbatim. Where two contributors' names land in one document, one such
// name can hold only one thing, so two contributors arriving at one name means
// one of them would be published and the other silently dropped: a resource the
// author declared would leave the output entirely, and anything referring to it
// would resolve to the survivor instead. That is a Loud Guardrail case rather
// than a warning one — nothing in the merged output records what was dropped.
//
// # Which contributors are compared, and why the answer is not "all of them"
//
// A provider that folds its dispatch group's per-node output into ONE document
// is handed some of the group's output and not the rest: a contributor whose
// output the render placed as discrete files at its own position stands beside
// that document rather than inside it, so its names meet nobody's. Comparing it
// against the folded contributors refuses a build over a collision the output
// does not have — and there is no edit that answers such a refusal, because the
// names genuinely do not meet, so nothing renaming one of them changes anything.
// 🔴 A halt an author cannot act on is worse than a missed one: a missed one
// produces output someone eventually reads, while a halt with no available remedy
// reads as a guardrail and behaves as a wall. Accepting the broad comparison as
// harmless conservatism was considered and refused, and it must not be restored
// on those grounds.
//
// 🔴 The narrowing is bounded to a group that HAS one such document, and dropping
// that bound turns it into a removal. A provider that folds nothing — one that
// nests each node under its own position, one with flat output, one declaring no
// assembly at all — places every contributor's output as discrete files, so a
// predicate reading the placement alone would find nothing compared anywhere. Two
// files are exactly where a name can still collide there: a resource identity
// scoped to a cluster rather than to a document is unique across the files as a
// set, which is why the comparison over such a group stays group-wide.
//
// The host reads its own routing and never learns what a key space's uniqueness
// means. A guest whose names must be unique across something wider than one
// document expresses that scope in the space name it declares, which is the
// mechanism the accumulation's own contract names and needs no host change.
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
// It reads two facts: every declaration the group's contributors made, and what
// the group's elected provider declared it does with the group's output. The
// predicate is the whole of the comparison: which of the contributors that
// arrived at one name in one space can reach each other at all, how many of them
// there are, and whether any of them repeated the name inside one declaration.
// None of the three is a question either fact answers — a fact that named the
// colliding names would be this rule rather than its input.
//
// The two facts are read together rather than combined upstream, and the reason
// is the one that keeps a fact a fact: each answers only what it observed. Where
// a contributor's output went is a fact about that contributor; whether the group
// folds anything into one document is a fact about the group's provider; and what
// follows from the pair is the decision, which is this rule's.
//
// It raises one objection per contributor involved in each colliding name, rather
// than one per name, for the reason the collision rule at this stage does: a
// contributor is what an author can go and change and a name is not. Every
// contributor whose name met another's is named and not only some of them,
// because nothing in a collision makes one of two derivations the intended one,
// so naming a subset of the ones that met would assert a choice the compiler has
// not made. A contributor that derived the same name where it could meet nobody
// is not among them, and is not named: an author reading it would have no edit to
// make. A contributor whose own declaration carried the name twice is named on
// that count alone, whoever else derived it.
//
// The space bounds the comparison. Two contributors deriving one name into two
// different spaces have not collided — neither output displaces the other — and a
// rule comparing across spaces would refuse authoring that renders correctly.
//
// It does not interpret what a name means. A space is an opaque string its declarer
// chose, a name is an opaque string its declarer derived, and this compares them
// for equality and does nothing else — so one rule covers every space any
// contributor declares, and a second provider needs no second rule.
//
// Its detail names the derived name bare and captions the rest. The registered
// wording is about a name derived more than once, so the name is the value it is
// read against; the space, the contributor, the provider and the path are values
// the wording does not name, and they are in the detail because the subject
// carries no locator — an assembly path is evaluated output and a provider is
// selected, so neither is a key the Release document holds.
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
		// Whether this group has one document for two names to meet inside. Only
		// where it has does a contributor's own placement narrow anything: a group
		// that folds nothing places every contributor's output as discrete files,
		// and reading that placement as a reason not to compare would leave nothing
		// compared at all.
		folds := boundaryAssemblyFolds(
			v.AssemblyGroupProviderAssembly(s.AssemblyPath(), s.ProviderName()))
		// meets reports whether one declaration's contributor can reach another
		// contributor's name in this group. It is the whole of the narrowing, in one
		// place, so the run count below and the per-entry decision cannot come apart.
		meets := func(d constraint.DerivedNameDeclaration) bool {
			return !folds || !d.OutputPlacedStandalone
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
			// How many of the run's contributors can reach each other's names.
			// Where the group folds nothing this is the whole run, so the
			// comparison is the run's own length exactly as it is for a group with
			// no placement to read.
			meeting := 0
			for _, d := range run {
				if meets(d) {
					meeting++
				}
			}
			// Fewer than two contributors meeting, and none of them repeating the
			// name inside one of its own declarations, is the ordinary answer: a
			// name derived once wherever it can be read.
			if meeting < 2 && !repeated {
				continue
			}
			for _, d := range run {
				// A contributor is named for one of two reasons, and the second
				// stands wherever its output landed: its name met another
				// contributor's, or one of its own declarations carried the name
				// twice, which is a fault inside that one contributor and is
				// answerable in a document nobody else writes into.
				if !(meeting >= 2 && meets(d)) && !d.RepeatedWithinOneDeclaration {
					continue
				}
				details := []constraint.DetailValue{
					constraint.Uncaptioned(d.DerivedName),
					constraint.Captioned("key space", d.KeySpace),
					constraint.Captioned(
						derivedNameCaption(d.ContributorKind, d.RepeatedWithinOneDeclaration),
						d.Contributor,
					),
				}
				// The further coordinate is presented only where there is one. A
				// contributor its kind's own name identifies has none, and an empty
				// captioned value would put a caption in front of nothing — which
				// reads as a coordinate the author failed to supply rather than one
				// that does not apply.
				if d.ContributorScope != "" {
					details = append(details, constraint.Captioned(
						derivedNameScopeCaption(d.ContributorKind), d.ContributorScope))
				}
				details = append(details,
					constraint.Captioned("provider", s.ProviderName()),
					constraint.Captioned("dispatch path", s.AssemblyPath()),
				)
				out = append(out, s.RaiseWith(ErrDerivedKeyCollision(), details...))
			}
		}
		return out
	},
}

// AssemblyPathNamesADirectoryBeneathTheOutputRoot reports each boundary path
// declaration that sent a contribution to a dispatch group whose evaluated path
// does not name a directory beneath the output root: one climbing out of it,
// naming an absolute location, or cleaning to the output root itself.
//
// The group's path is the directory every file its contributors render is written
// under. A path climbing out would put output outside the directory the build was
// asked to write, and one naming the root itself would put the boundary's output at
// the top of the output directory, beside every other boundary's — so the build
// halts here, before any group assembles and before anything is written, rather
// than at the write. That is a Loud Guardrail case: the author wrote the value and
// is the one who can change it.
//
// It reads the group's path from its subject, because the path IS the group's
// coordinate, and one fact: what each contribution's declaration evaluated to. The
// path is the predicate; the declarations are what make the finding actionable,
// since the field an operator edits is written on a boundary and the path names
// none.
//
// It raises one objection per distinct boundary, field and value rather than one
// per contribution, because a boundary's components usually evaluate its
// declaration alike and an author edits the field once. The field named is the one
// the fault comes from — `directory:` where no sub-directory is composed or where
// the directory alone already leaves the root, `subDirectory:` otherwise — with the
// value that field evaluated to, because the value is often a template whose
// evaluation the author has not seen. A group always received at least one
// declaration, since a group comes into existence when its first contribution is
// sorted into it; a group reporting none is still refused, naming its path, so a
// record that came out empty cannot pass a path the rule refuses.
//
// Its detail captions every value. The Release, the boundary, the field and the
// path are four values of the same shape, and the caption is what tells them apart.
var AssemblyPathNamesADirectoryBeneathTheOutputRoot = constraint.Check[
	constraint.DispatchedView,
	constraint.AssemblyPathProviderSubject,
]{
	Sentinel: ErrBoundaryDirectoryNotBeneathOutputRoot(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DispatchedView,
		s constraint.AssemblyPathProviderSubject,
	) []constraint.Objection {
		if s.AssemblyPath() != "." && pathscope.IsLocalSlash(s.AssemblyPath()) {
			return nil
		}
		declared := v.AssemblyGroupBoundaryPathDeclarations(s.AssemblyPath(), s.ProviderName())
		if len(declared) == 0 {
			return []constraint.Objection{s.RaiseWithAdvice(
				ErrBoundaryDirectoryNotBeneathOutputRoot(), boundaryDirectoryAdvice,
				constraint.Captioned("output path", s.AssemblyPath()),
			)}
		}
		var named [][3]string
		out := make([]constraint.Objection, 0, len(declared))
		for _, d := range declared {
			field, value := boundaryPathFieldAtFault(d.Directory, d.SubDirectory)
			key := [3]string{d.Boundary, field, value}
			if slices.Contains(named, key) {
				continue
			}
			named = append(named, key)
			out = append(out, s.RaiseWithAdvice(
				ErrBoundaryDirectoryNotBeneathOutputRoot(), boundaryDirectoryAdvice,
				constraint.Captioned("Release", d.Release),
				constraint.Captioned("boundary", d.Boundary),
				constraint.Captioned(field, value),
				constraint.Captioned("output path", s.AssemblyPath()),
			))
		}
		return out
	},
}

// boundaryPathFieldAtFault is the field a refused dispatch path came from, and the
// value that field evaluated to: `directory:` where no sub-directory is composed or
// where the directory alone already leaves the output root, `subDirectory:`
// otherwise.
func boundaryPathFieldAtFault(evalDir, evalSub string) (field, value string) {
	if subDirectoryDisabled(evalSub) || (evalDir != "" && !pathscope.IsLocalSlash(path.Clean(evalDir))) {
		return "directory:", evalDir
	}
	return "subDirectory:", evalSub
}

// AssemblyGroupPlacesEachContributorInADirectoryOfItsOwn reports each contributor
// a dispatch group would place under a directory whose name is `.` or `..`.
//
// A group gives a contributor a directory of its own where its provider nests each
// contributor under one, and, for a component, where the group is written flat and
// the component's artifacts block placed files, which are named by a key carrying
// nothing of the component. That directory is named for the contributor alone, and
// the one contributor that can arrive at `.` or `..` is a Feature attached at a
// boundary under such an instance name, which the Release admits. As a directory the first
// names the position holding every contributor and the second the one above it, so
// the Feature's files would land outside the position the group gave it — and in
// the first case among every other contributor's files. The build halts here,
// before any group assembles and before anything is written. That is a Loud
// Guardrail case: the instance name is the operator's to change.
//
// It reads one fact: the contributors the group places under a directory of their
// own, each with the segment the group would write. A contributor the group places
// under no directory of its own is not in that fact, so a Feature named `.` in a
// group that folds its output, or in one written flat — where a boundary-level
// Feature's files are named with a prefix beside the boundary's own rather than
// placed in a directory — is not refused: its name never becomes a directory.
//
// Its detail captions every value, and names a Feature by the boundary it is
// attached at and its instance name, which is the key the operator wrote. Any other
// contributor is named by its kind and its own coordinates, for the case the
// schema already keeps out of reach.
var AssemblyGroupPlacesEachContributorInADirectoryOfItsOwn = constraint.Check[
	constraint.DispatchedView,
	constraint.AssemblyPathProviderSubject,
]{
	Sentinel: ErrBoundaryFeatureNameNotADirectory(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DispatchedView,
		s constraint.AssemblyPathProviderSubject,
	) []constraint.Objection {
		var out []constraint.Objection
		for _, d := range v.AssemblyGroupContributorDirectories(s.AssemblyPath(), s.ProviderName()) {
			if d.Segment != "." && d.Segment != ".." {
				continue
			}
			details := []constraint.DetailValue{constraint.Captioned("Release", d.Release)}
			if d.ContributorKind == producerKindFeature {
				details = append(details,
					constraint.Captioned("boundary", d.ContributorScope),
					constraint.Captioned("feature", d.Contributor))
			} else {
				details = append(details, constraint.Captioned(d.ContributorKind, d.Contributor))
				if d.ContributorScope != "" {
					details = append(details, constraint.Captioned(
						derivedNameScopeCaption(d.ContributorKind), d.ContributorScope))
				}
			}
			out = append(out, s.RaiseWithAdvice(
				ErrBoundaryFeatureNameNotADirectory(), boundaryFeatureNameAdvice, details...))
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
// It reads two facts: the output paths the group it was invoked for wrote, which
// is what there is to scan, and the bytes standing at one of them. The whole of
// its predicate is a substring test over those bytes, which is why the bytes are
// all it needs: what makes a placeholder detectable is a prefix the host chose,
// so the rule never has to establish which edge stamped it or which provider was
// meant to resolve it. That the rule finds the same files in the same order
// across runs of identical input follows from the order the path fact answers in,
// not from anything it does.
//
// It is keyed by the group for the reason the collision rule beside it is: an
// assembled file belongs to the group that assembled it, so a per-boundary keying
// would report one offending file once for every boundary that fed the group and
// position one of those reports at a boundary that contributed nothing to it.
// That matters more here than it looks, because this fault is the translation's
// rather than any document's — so a boundary position would name an editable site
// for a fault no edit to it can fix.
//
// Its detail names the provider and the emitted path, because a path is unique
// only within a group — so naming the provider is what makes the objection point
// at one file, and the assembly path the subject carries completes the coordinate.
var CrossBoundaryLinkPlaceholderResolved = constraint.Check[
	constraint.RenderedView,
	constraint.AssemblyPathProviderSubject,
]{
	Sentinel: ErrOrphanCrossBoundaryLinkSentinel(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.RenderedView,
		s constraint.AssemblyPathProviderSubject,
	) []constraint.Objection {
		var out []constraint.Objection
		for _, path := range v.EmittedFilePathSet(s.AssemblyPath(), s.ProviderName()) {
			content := v.AssembledFileContent(s.AssemblyPath(), s.ProviderName(), path)
			if !bytes.Contains(content, []byte(rendercore.CrossBoundaryLinkSentinelPrefix)) {
				continue
			}
			out = append(out, s.Raise(
				ErrOrphanCrossBoundaryLinkSentinel(), s.ProviderName(), path,
			))
		}
		return out
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
// ordinary mixed build, where one provider links and the other does not,
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

// takenInjectClaim is one claim an attachment made on its component's delivery
// pipe under a key an earlier delivery already held against it, together with
// the attachment that delivered first — empty where the component's own block or
// one of its connections did.
type takenInjectClaim struct {
	constraint.InjectDelivery

	// HeldBy is the attachment whose earlier delivery holds the key against this
	// claim, empty where the component's own block or one of its connections
	// delivered it.
	HeldBy string
}

// takenInjectClaims walks one node's deliveries in delivery order and reports
// every claim the named attachment made under a key an earlier delivery holds
// against it, by whichever route the attachment delivered.
//
// It is the ONE walk every rule over the record shares, so they cannot come to
// disagree about which claim holds a key. Three things decide a claim, and each
// holds on every route:
//
//   - The first claim under a key is the one that holds it, so the walk
//     accumulates what has been delivered as it goes and asks of each claim
//     whether the key was already held. A search over the whole answer would find
//     an attachment's claim colliding with a LATER claim and refuse a pair that no
//     build has a problem with, because the later claim is the one that took
//     nothing. That is the direction a rule about a shared keyed space is easiest
//     to get wrong in, since both readings agree on every input where every claim
//     resolves.
//   - Presence alone is the signal. No value is compared, and two claims agreeing
//     on a value still collide, because the agreement is a property of one input
//     rather than of the authoring.
//   - A claim that delivered nothing is passed over rather than counted: it left
//     the key free, so the next claim under it is the first to hold it. On the
//     inject-block route that is a reference resolving to nothing, or a key
//     another claim already held. On the mapping route it is a key the rendered
//     output does not carry — and such a key makes no claim at all, because the
//     merge sets every key the output carries, an empty value included. A mapping
//     writing an empty value under a key has therefore delivered under it: the
//     empty value replaced whatever stood there.
//
// What holds a key differs between an attachment's own inject block and the route
// that rewrites the pipe after every block has delivered — its FeatureRecipe's
// mapping — and that difference is the whole of what separates them. Against a
// claim an attachment's own inject block makes, any earlier delivery holds the
// key — the component's own block, one of its connections, or another
// attachment's block. Against a claim a
// rewrite makes, only an earlier delivery by a DIFFERENT attachment holds it,
// by any of that attachment's routes. A rewrite of what the component's own
// block or one of its connections delivered is interception, which is what a
// mapping is for and what the compose bridge's mapping does to a consumer's host
// variable; and a rewrite of what its own attachment delivered is that
// attachment transforming its own delivery, which displaces nothing anyone else
// wrote.
//
// The attachment that delivered first is reported with the claim because a
// rewrite's claim is always held by an attachment, and naming it is what lets the
// author find the second of the two entries. A key several attachments delivered
// before the claim is held by the first of them, which is the one that stood
// there before any displacement began.
func takenInjectClaims(deliveries []constraint.InjectDelivery, attachment string) []takenInjectClaim {
	var out []takenInjectClaim
	held := map[string]bool{}
	heldBy := map[string][]string{}
	for _, delivery := range deliveries {
		slot := delivery.Mechanism + "\x00" + delivery.DeliveryKey
		if delivery.Attachment == attachment {
			if rewritesThePipe(delivery) {
				if holder := firstOtherThan(heldBy[slot], attachment); holder != "" {
					out = append(out, takenInjectClaim{InjectDelivery: delivery, HeldBy: holder})
				}
			} else if held[slot] {
				out = append(out, takenInjectClaim{InjectDelivery: delivery, HeldBy: firstOtherThan(heldBy[slot], "")})
			}
		}
		if delivery.Delivered {
			held[slot] = true
			if delivery.Attachment != "" {
				heldBy[slot] = append(heldBy[slot], delivery.Attachment)
			}
		}
	}
	return out
}

// firstOtherThan returns the first attachment in delivery order that is not the
// excluded one, and the empty string where there is none.
func firstOtherThan(attachments []string, excluded string) string {
	for _, a := range attachments {
		if a != excluded {
			return a
		}
	}
	return ""
}

// rewritesThePipe reports whether a delivery was made by the route that writes
// over the pipe once every inject block has delivered — an attachment's
// FeatureRecipe mapping — rather than by an inject block.
//
// It is the one place the walk above decides which holding rule a claim takes,
// so a route added later is classified here or nowhere, and the rule reading the
// rewrite route cannot come to classify a delivery differently from the walk
// that decided it.
func rewritesThePipe(delivery constraint.InjectDelivery) bool {
	return delivery.ThroughMapping
}

// AttachmentInjectKeyNotDeliveredByAnotherSource reports each delivery key one
// attachment's own inject block asks the component's injection pipe for that
// another source has already delivered under.
//
// Three authoring surfaces deliver into one pipe through their blocks: the
// component's own injection block, the blocks on the entries of its dependency
// block, and the block on each attachment attached to it. The pipe is one keyed
// space shared between them, so a key two of them deliver under is ambiguous
// about which value arrives, and the build halts rather than letting one surface
// silently displace another. The author resolves it by removing one of the two
// claims.
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
// Which claims collide is decided by [takenInjectClaims], the walk this rule
// shares with the rule over an attachment's mapping; this rule reports the claims
// the attachment's own block made. It is asked before any mapping renders, which
// is all a block's claim needs: every block delivers before any mapping does, so
// nothing a mapping delivers can come ahead of a block's claim.
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
		for _, claim := range takenInjectClaims(v.NodeInjectDeliveries(s.NodeID()), s.AttachmentName()) {
			if rewritesThePipe(claim.InjectDelivery) {
				continue
			}
			out = append(out, s.RaiseWith(
				ErrAttachmentInjectKeyCollision(),
				constraint.Uncaptioned(claim.DeliveryKey),
				constraint.Captioned("mechanism", claim.Mechanism),
			))
		}
		return out
	},
}

// AttachmentInjectFromNamesAPublishedKey reports each entry one attachment's own
// inject block writes whose `from:` names a key the attachment's FeatureRecipe does
// not publish, where that FeatureRecipe does not read the attachment's entries
// itself.
//
// An attachment's entry delivers into the component's pipe the value its
// FeatureRecipe publishes under the key `from:` names. A key the FeatureRecipe does
// not publish puts no value there, and where nothing else receives the entry either
// it is dropped without a word — a misspelt key builds and the component starts
// without the variable it was written to receive. The build halts instead, naming
// the key, the entry, the FeatureRecipe and the keys it does publish, which are the
// alternatives the author picks from.
//
// A FeatureRecipe reading the attachment's entries at .inject — in a template it has
// evaluated against the attachment's render context, the one context carrying them:
// its values mapping, workload, artifacts, omits or a pipeline step's input or gate,
// never its injectMapping — is the one exception, because there the entry is not
// dropped: the FeatureRecipe receives every entry as written and delivers it its own
// way, under a key that is its own business rather than one it publishes. Whether
// it reads them is the read census's answer, recorded on the claim where the claim
// was made, and that census sees a read only where the template text decides the
// path — a FeatureRecipe handing the whole of its context on, or reaching the
// entries through a function such as index over the context, is answered as not
// reading them, and its entries naming unpublished keys are refused. A FeatureRecipe
// whose templates the census cannot read is answered as reading them, because its
// render halts on that template with the fault the author has to fix first.
//
// It is the condition a `from:` naming no published key raises on every surface
// that writes one — a component's own block, a connection's — raised here from the
// attachment's own block. It reads the same record the rule beside it reads, and a
// claim whose key was already held as well as naming an unpublished key is reported
// by each of the two rules, because each reason has its own remedy. An attachment
// whose FeatureRecipe's provides were not evaluated for the component — one
// rendering once for its boundary, where no boundary holding the component made
// that evaluation — has no claim marked, so it is never reported here.
//
// It is invoked once per attachment, beside the rule over a held key and over the
// same record, and each offending entry is its own objection.
//
// # What the detail names
//
// The key `from:` names stands bare, because the registered wording says a
// from-key is what the finding is about. The entry is named by its `in:` and its
// mechanism, captioned, then the FeatureRecipe, then each key it publishes, sorted;
// a FeatureRecipe publishing nothing ends the message after its name. The node and
// the attachment are carried by the locator, which positions the finding at the
// attachment's own entry in the document that wrote it, on the reading the rule
// beside it states.
var AttachmentInjectFromNamesAPublishedKey = constraint.Check[
	constraint.HydratedView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: resolver.ErrInvalidInjectKey(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeAttachmentSubject,
	) []constraint.Objection {
		var out []constraint.Objection
		for _, claim := range v.NodeInjectDeliveries(s.NodeID()) {
			if claim.Attachment != s.AttachmentName() || rewritesThePipe(claim) ||
				!claim.Unresolved || claim.DirectiveRead {
				continue
			}
			detail := []constraint.DetailValue{
				constraint.Uncaptioned(claim.From),
				constraint.Captioned("in", claim.DeliveryKey),
				constraint.Captioned("mechanism", claim.Mechanism),
				constraint.Captioned("FeatureRecipe", claim.FeatureRecipe),
			}
			for _, key := range claim.Published {
				detail = append(detail, constraint.Captioned("publishes", key))
			}
			out = append(out, s.RaiseWith(resolver.ErrInvalidInjectKey(), detail...))
		}
		return out
	},
}

// AttachmentInjectMappingKeyNotDeliveredByAnotherAttachment reports each
// delivery key one attachment's FeatureRecipe injectMapping writes that a
// different attachment attached to the same component already delivered.
//
// An attachment delivers into its component's pipe by its own inject block and by
// its FeatureRecipe's mapping, whose delivery key is a template rendered against
// the attachment's own values and infra. Two attachments arriving at one key by
// any pairing of those routes leave the component reading
// whichever was merged last, and the merge is last-wins by construction — so a
// credential one attachment delivered would be presented under another's with
// nothing in the build saying so. The build halts instead, naming both
// attachments, and the author resolves it by giving one of them a key of its own
// or by removing one of them.
//
// A mapping rewriting a key the component's own block or one of its connections
// delivered is not reported. That is interception — a routing Feature qualifying
// a host, a guard redirecting an upstream — and it is what the mapping mechanism
// exists for. The rule therefore reads no value and no meaning: what separates a
// collision from an interception is which surface made the earlier delivery,
// which the record attributes and the rendered pipe does not.
//
// It is invoked once per attachment, the side the finding is positioned at,
// immediately after every attachment's mapping has rendered and before anything
// reads the rewritten pipe. The rendered keys exist only from that point, so it
// is the earliest the question is answerable. Which claims collide is decided by
// [takenInjectClaims], the one walk this rule shares with the rule over an
// attachment's own block; this rule reports the claims the attachment's mapping
// made. Each colliding key is its own objection.
//
// # What the detail names
//
// The delivery key stands bare, because the registered wording already says a
// duplicated delivery key is what the finding is about, and the mechanism is
// captioned for the reason the sibling rule gives. Both attachments are named,
// captioned, each by the name the build gives it on the component — an
// attachment a connection carries is named by its alias and its slot. The one the
// finding is positioned at is named as well as located, because the locator
// names where it was written and the other is named by what it is called, and a
// reader comparing the two needs them in one spelling. The other is named because
// it is written somewhere the locator does not reach, and finding it is half of
// the remedy.
//
// # What the advice says
//
// The other half is [attachmentMappingKeyAdvice], supplied with every raise. It is
// stated in what the author of either document writes — attachments, their values
// and infra, the Aggregate and the Release — because which key a mapping writes is
// the FeatureRecipe's business: the rule reads no value and knows no Feature, so
// it cannot say which value chooses the key, only that a mapping renders from the
// attachment's own values and infra, and that the other attachment may have
// delivered through its own inject block instead. It does not say which document
// wrote the other attachment either, because the record carries that attachment's
// name and not its site.
var AttachmentInjectMappingKeyNotDeliveredByAnotherAttachment = constraint.Check[
	constraint.HydratedView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrAttachmentInjectMappingKeyCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeAttachmentSubject,
	) []constraint.Objection {
		var out []constraint.Objection
		for _, claim := range takenInjectClaims(v.NodeInjectDeliveries(s.NodeID()), s.AttachmentName()) {
			if !claim.ThroughMapping {
				continue
			}
			out = append(out, s.RaiseWithAdvice(
				ErrAttachmentInjectMappingKeyCollision(),
				attachmentMappingKeyAdvice,
				constraint.Uncaptioned(claim.DeliveryKey),
				constraint.Captioned("mechanism", claim.Mechanism),
				constraint.Captioned("attachment", s.AttachmentName()),
				constraint.Captioned("delivered first by attachment", claim.HeldBy),
			))
		}
		return out
	},
}

// attachmentMappingKeyAdvice is what the duplicate-delivery-key refusal tells the
// author to write instead.
//
// Each clause is a property of the build rather than of a catalog. The attachment
// the refusal is positioned at always delivered through its mapping, whose key is
// rendered from that attachment's own values and infra. The attachment that
// delivered first did so through its mapping or through its own inject block, and
// the claim this rule reads names that attachment and not its route, so the advice
// names both. The Release's schema
// admits infra on an attachment and refuses values on one, so a value choosing a
// key is written where the Aggregate attaches the attachment, while the Release
// goes on supplying its infra.
const attachmentMappingKeyAdvice = "Give one of the two attachments a key of its own, or attach one fewer: " +
	"the key this attachment delivers under is chosen by its FeatureRecipe, which can read the attachment's " +
	"own values and infra, and the other's is chosen the same way or by its own inject block where it " +
	"delivered through one. A Release sets an attachment's infra and never its values, so where a key is " +
	"chosen by a value, attach that attachment in the Aggregate with the value, and keep giving the rest " +
	"from the Release."

// AttachmentInjectMappingCoversEveryAuthoredMechanism reports each custom
// delivery mechanism the host component's own injection block declares — one
// beyond env, file and arg — that one attachment's rendered injectMapping output
// carries no top-level key for.
//
// A FeatureRecipe's injectMapping rewrites the pipe the host component delivers
// into, and it names the mechanisms it writes as the top-level keys of its own
// rendered output. A component declaring a custom mechanism the output does not
// name has therefore asked for a delivery this attachment drops on the floor: the merge
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

// AttachmentInjectMappingWritesOnlyDeliveredKeys reports each top-level key one
// attachment's rendered injectMapping output wrote deliveries under that no
// delivery mechanism of the host reads.
//
// The merge into the host's pipe applies env, file and arg, and every other
// top-level key as a custom mechanism under its own name. It rebuilds the all view
// from those afterwards rather than merging it, so deliveries an output writes
// there reach nothing while the output still parses. The build halts rather than
// rendering a consumer that silently lacks them, and the author resolves it by
// moving the deliveries under a mechanism that delivers them, which is what the
// detail names.
//
// It is invoked once per attachment and reads one fact, the keys that
// attachment's output wrote and the merge did not apply. The attachment is the
// positioned side for the reason the mechanism pairing's rule gives: the mapping
// is authored in a FeatureRecipe the Aggregate does not contain, and the
// attachment's entry is the position in the author's own file that binds it. The
// FeatureRecipe is captioned in the detail, since it is the document the remedy is
// applied to and the locator cannot reach it.
//
// Each key is its own objection. The node and the attachment are not in the
// detail, because the locator carries both.
var AttachmentInjectMappingWritesOnlyDeliveredKeys = constraint.Check[
	constraint.HydratedView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrInjectMappingOutputKeyNotDelivered(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeAttachmentSubject,
	) []constraint.Objection {
		var out []constraint.Objection
		for _, written := range v.AttachmentInjectMappingUndeliveredKeys(
			s.NodeID(), s.AttachmentName(),
		) {
			out = append(out, s.RaiseWith(
				ErrInjectMappingOutputKeyNotDelivered(),
				constraint.Uncaptioned(written.Key),
				constraint.Captioned("recipe", written.Recipe),
				constraint.Captioned("an output may carry", injectMappingDeliveredKeysPhrase),
			))
		}
		return out
	},
}

// injectMappingDeliveredKeysPhrase names, in the author's terms, the top-level
// keys the merge into the host's pipe applies.
const injectMappingDeliveredKeysPhrase = "env, file, arg, or a custom mechanism the host's Recipe reads under its own name"

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

// ProvidesTemplateRenderedNameAvailable reports each provides entry that reads
// the name its target gives the resources it renders for the node, on a node
// whose provider reports no such name.
//
// The refusal is what keeps the value from being worse than its absence. The name
// reaches a template as a plain string, so a node with none renders the reference
// away and the entry publishes an address one segment short — a name no emitted
// resource carries — at exit zero and with nothing to read. Refusing before
// anything is evaluated reports it against the entry the author wrote.
//
// It is a fact and not a heuristic, which is why it halts rather than advising:
// whether the node's provider reports such a name is settled by the Recipe
// binding, ahead of every evaluation, so nothing here is guessed.
//
// It reads four facts and derives its own references from the authored text, for
// the reason the self-containment rule beside it does: a template this rule
// refuses is exactly one whose value is never produced, so the resolved value
// cannot serve as the input.
//
// The predicate itself is shared with the rule over the rest of the surfaces this
// value lands on, which is what keeps one reference from being answered two ways.
// This value's own part is which facts to read: they are keyed by the provides
// entry, and the entry's own position names it, so no further detail is added.
//
// A reference the self-containment rule already refuses is not this rule's
// finding: a reference the surface does not admit at all is that rule's subject
// rather than this one's.
var ProvidesTemplateRenderedNameAvailable = constraint.Check[
	constraint.DescribedView,
	constraint.NodeProvidesKeySubject,
]{
	Sentinel: ErrRenderedNameUnavailable(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeProvidesKeySubject,
	) []constraint.Objection {
		return renderedNameObjections(
			s,
			v.NodeRenderedName(s.NodeID()),
			v.AuthoredNodeProvides(s.NodeID(), s.ProvidesKey()),
			"{{", "}}",
			v.NodeDeclaresAuthoredSource(s.NodeID()),
			v.NodeRenderingProvider(s.NodeID()),
			nil,
		)
	},
}

// TemplateFieldRenderedNameAvailable reports each inline template field a node's
// own Recipe writes that reads the name its target gives the resources it renders
// for the node, on a node whose provider reports no such name.
//
// It is the same refusal the provides rule carries, over the rest of the surfaces
// the value lands on: the values mapping, the workload body, each artifacts entry,
// and each pipeline step's input and its gate all render against the node's own
// context, so a reference to that name resolves away on every one of them and
// emits a value one segment short. One predicate serves them all, which is what
// keeps an author from being told two different things about one reference.
//
// It reads five facts, and the delimiter pair is the one that makes the reference
// set correct rather than approximate. A provider whose own target syntax uses the
// usual pair declares an alternate one for its artifacts, so an artifacts body has
// to be parsed with the pair that provider declared and every other field with the
// usual one. Which of the two applies follows from the field, and the reading that
// answers it sits beside the enumeration that built the label.
//
// Its detail names the offending expression, the provider, and the authored field.
// The field is captioned as well as positioned because the position cannot always
// name it: an artifacts entry's key is an output filename, so it carries the
// character the locator separates its segments with and the position stops at the
// block holding the entry. Naming the field is what makes such a finding
// actionable in a Recipe declaring many.
var TemplateFieldRenderedNameAvailable = constraint.Check[
	constraint.DescribedView,
	constraint.NodeTemplateFieldSubject,
]{
	Sentinel: ErrRenderedNameUnavailable(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeTemplateFieldSubject,
	) []constraint.Objection {
		left, right := "{{", "}}"
		if RecipeTemplateFieldReadsWithArtifactDelimiters(s.TemplateField()) {
			delims := v.ProviderArtifactTemplateDelimiters(s.NodeID())
			left, right = delims.Left, delims.Right
		}
		return renderedNameObjections(
			s,
			v.NodeRenderedName(s.NodeID()),
			v.AuthoredNodeTemplateText(s.NodeID(), s.TemplateField()),
			left, right,
			v.NodeDeclaresAuthoredSource(s.NodeID()),
			v.NodeRenderingProvider(s.NodeID()),
			[]constraint.DetailValue{constraint.Captioned("field", s.TemplateField())},
		)
	},
}

// AttachmentTemplateReadsNoHostRenderedName reports each inline template field a
// per-component attachment's FeatureRecipe writes that reads the name a target
// gives the resources it renders for a node, on a host whose provider reported no
// such name.
//
// # What the value is on an attachment, and so when it is refused
//
// The attachment's render context is derived from its host node's, so the value
// standing at the key is the HOST's answer — the name the host's own bound provider
// reports for the host's resources — in the same way .context.instance_id there is
// the host's identity. That is the value an attachment needs: it names its own
// resources after the component hosting it, and the host's reported name is a
// spelling of that component the host's target accepts where the identity as
// written may not be. So the reference is admitted wherever the host's provider
// reported a name, and refused where it reported none, because an empty value there
// would name a resource nothing emits.
//
// The value is never the attachment's OWN provider's answer, and the rule does not
// read the attachment's declared capability: an attachment rendering through a
// different target from its host still reads its host's name, which is what naming
// a resource after the host means.
//
// # Why it is this refusal rather than the one the node's surfaces raise
//
// It raises the reference-not-admitted condition every template surface shares,
// which is what the attachment's locals, source and provides surfaces raise for this
// same key through their own scope on the same hosts. One condition across every
// surface of one attachment is what lets an author fix it once: on such a host the
// surface does not define the key, and the detail lists what it does define — the
// host's identity among it.
//
// # Why a boundary-scope attachment ends the rule
//
// Such an attachment renders once for a boundary rather than once per component,
// against a context whose identity pillar is excluded whole — so the reference is
// already refused there by the rules declared over that surface, and reporting it
// here as well would give one authoring fault two positions.
//
// Its detail carries the offending reference bare, the field it was written in
// captioned, and the set this surface defines captioned. The last two are derived
// from the admission itself rather than restated, so what the build refuses and what
// the author is told cannot disagree. The field is named as well as positioned
// because the position cannot always name it: an artifacts entry's key is an output
// filename, so it carries the character the locator separates its segments with and
// the position stops at the block holding the entry.
var AttachmentTemplateReadsNoHostRenderedName = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentTemplateFieldSubject,
]{
	Sentinel: ErrTemplateUnknownRoot(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentTemplateFieldSubject,
	) []constraint.Objection {
		if v.AttachmentExecutionScope(s.NodeID(), s.AttachmentName()) == executionScopeBoundary {
			return nil
		}
		left, right := "{{", "}}"
		if RecipeTemplateFieldReadsWithArtifactDelimiters(s.TemplateField()) {
			delims := v.AttachmentProviderArtifactTemplateDelimiters(s.NodeID(), s.AttachmentName())
			left, right = delims.Left, delims.Right
		}
		violation := classifyAttachmentTemplateRenderedName(
			v.AttachmentAuthoredTemplateText(s.NodeID(), s.AttachmentName(), s.TemplateField()),
			left, right,
			v.NodeRenderedName(s.NodeID()),
		)
		if violation == nil {
			return nil
		}
		caption, phrase := templateAdmittedSetPhrase(violation)
		return []constraint.Objection{s.RaiseWith(
			ErrTemplateUnknownRoot(),
			constraint.Uncaptioned(violation.Ref.Path),
			constraint.Captioned("written in", s.TemplateField()),
			constraint.Captioned(caption, phrase),
		)}
	},
}

// AttachmentTemplateReadsCallerRenderFactsDeclared reports each inline template
// field producing the attachment's own resources, in the FeatureRecipe a
// per-component attachment binds, that reads the render facts of the components
// connecting to its host, where that FeatureRecipe does not declare it reads them.
//
// # Why the declaration is what the rule reads
//
// The declaration decides when the attachment renders. One that declares it
// renders after every component has rendered, so each caller has reported what it
// emitted; one that does not renders in its host's turn, before a caller later in
// the walk has rendered at all. So a template reading the facts is refused where
// the declaration is missing and admitted where it is present, and nothing about
// the facts themselves is read.
//
// # What counts as a reading, and the direction it errs in
//
// The references are the over-collecting census, which is the direction a rule
// refusing a reference needs: it records a field read inside a range or a with
// body as well as at the root. That is also its limit here. The callers are a
// list, so their facts are read off an element — inside `range .context.used_by`,
// or off `index .context.used_by 0` — and the census records such a read relative
// to the element, as `.render_facts`, with nothing tying it to the list it came
// from. So a field counts as reading the facts where it reads the connection list
// at all and also reads `render_facts` relative to some element, or names
// `render_facts` beneath the list directly.
//
// That reading can refuse a template that reads an element field of that name off
// some other list beside the connection list, or that reads the host's own facts
// through a dot rebound to the context — `{{ with .context }}{{ .render_facts }}` —
// beside the connection list, where the census records the same relative read; the
// host's own facts written out in full, `.context.render_facts`, are never taken
// for the callers'. An author it refuses that way sets the declaration, which costs
// nothing but when the attachment renders. It
// cannot see a read made through a variable a range declares, nor one a template
// builds at evaluation, nor one written in content imported from a file or a
// library — none of which the authored text spells. The declaration remains the
// contract either way: an attachment that does not set it renders in its host's
// turn whatever its templates read.
//
// # Why a pipeline step's input and gate are not this rule's
//
// A step's templates are evaluated without the connections made to the host,
// whether or not the FeatureRecipe declares it reads its callers' facts — so
// declaring, the remedy this rule names, would not make a step's read work.
// Such a field is [AttachmentPipelineStepReadsNoCallerRenderFacts]'s, which names
// the remedy that does; asking both would give one authoring fault two
// conditions with opposite advice.
//
// # Why a boundary-scope attachment ends the rule
//
// Such an attachment renders once for a boundary against a context that carries
// no per-component pillar at all, so a reference to the connection list there is
// already refused by the rules declared over that surface, and reporting it here
// as well would give one authoring fault two positions.
//
// Its detail carries the offending reference bare and the field it was written in
// captioned, so an author whose position stops at the block holding an artifacts
// entry is still told which entry.
var AttachmentTemplateReadsCallerRenderFactsDeclared = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentTemplateFieldSubject,
]{
	Sentinel: ErrCallerRenderFactsNotDeclared(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentTemplateFieldSubject,
	) []constraint.Objection {
		if v.AttachmentExecutionScope(s.NodeID(), s.AttachmentName()) == executionScopeBoundary {
			return nil
		}
		if RecipeTemplateFieldIsPipelineStep(s.TemplateField()) {
			return nil
		}
		if v.AttachmentReadsCallerRenderFacts(s.NodeID(), s.AttachmentName()) {
			return nil
		}
		left, right := "{{", "}}"
		if RecipeTemplateFieldReadsWithArtifactDelimiters(s.TemplateField()) {
			delims := v.AttachmentProviderArtifactTemplateDelimiters(s.NodeID(), s.AttachmentName())
			left, right = delims.Left, delims.Right
		}
		ref, reads := attachmentTemplateCallerRenderFactsReference(
			v.AttachmentAuthoredTemplateText(s.NodeID(), s.AttachmentName(), s.TemplateField()),
			left, right,
		)
		if !reads {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrCallerRenderFactsNotDeclared(),
			constraint.Uncaptioned(ref),
			constraint.Captioned("written in", s.TemplateField()),
		)}
	},
}

// AttachmentPipelineStepReadsNoCallerRenderFacts reports each pipeline step input
// or gate of the FeatureRecipe a per-component attachment binds that reads the
// render facts of the components connecting to its host — whatever that
// FeatureRecipe declares.
//
// # Why the declaration is not read
//
// A step's templates are evaluated without the connections made to the host, for
// every FeatureRecipe. A step may rewrite the host's own documents, so one that
// could read who connects would make the host's rendered output depend on its
// callers, and a component's own output stays the same whoever uses it. The
// declaration decides when the attachment renders and nothing about what a step
// sees, so a step reading the callers' facts reads nothing either way — and the
// read is refused either way rather than rendered as an empty value. The remedy
// the refusal names is the same in both cases: a step reads the host's own facts
// in full, at `.context.render_facts`, and the callers' facts are read by the
// templates that produce the attachment's own resources.
//
// # What counts as a reading
//
// The reading [AttachmentTemplateReadsCallerRenderFactsDeclared] makes, through
// the same matcher, with the same direction of error and the same bound: a
// field-relative `render_facts` read beside a read of the connection list counts,
// and a read the authored text does not spell is not seen.
//
// # Why every other field ends the rule
//
// The templates producing the attachment's own resources DO see the connections
// on a FeatureRecipe that declares it, so a reading there is the other rule's,
// whose remedy is the declaration.
//
// # Why a boundary-scope attachment ends the rule
//
// For the reason it ends the other rule: that attachment's surface already
// refuses any reference to the connection list.
//
// Its detail carries the offending reference bare and the field it was written in
// captioned, which names the step by its id.
var AttachmentPipelineStepReadsNoCallerRenderFacts = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentTemplateFieldSubject,
]{
	Sentinel: ErrPipelineStepReadsCallerRenderFacts(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentTemplateFieldSubject,
	) []constraint.Objection {
		if v.AttachmentExecutionScope(s.NodeID(), s.AttachmentName()) == executionScopeBoundary {
			return nil
		}
		if !RecipeTemplateFieldIsPipelineStep(s.TemplateField()) {
			return nil
		}
		left, right := "{{", "}}"
		if RecipeTemplateFieldReadsWithArtifactDelimiters(s.TemplateField()) {
			delims := v.AttachmentProviderArtifactTemplateDelimiters(s.NodeID(), s.AttachmentName())
			left, right = delims.Left, delims.Right
		}
		ref, reads := attachmentTemplateCallerRenderFactsReference(
			v.AttachmentAuthoredTemplateText(s.NodeID(), s.AttachmentName(), s.TemplateField()),
			left, right,
		)
		if !reads {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrPipelineStepReadsCallerRenderFacts(),
			constraint.Uncaptioned(ref),
			constraint.Captioned("written in", s.TemplateField()),
		)}
	},
}

// AttachmentTemplateReadsTargetRenderFactsDeclared reports each inline template
// field producing the attachment's own resources, in the FeatureRecipe a
// per-component attachment binds, that reads the render facts of the components its
// host is wired to, where that FeatureRecipe does not declare it reads them.
//
// It is the twin of [AttachmentTemplateReadsCallerRenderFactsDeclared] for the
// other end of a connection, and reads as that one does: the same fields, the same
// census in the same direction of error, the same bounds — a read through a
// variable a range declares, one built at evaluation, or one imported from a file
// or a library is not seen — and the same two exits, for a pipeline step's input
// or gate, which is [AttachmentPipelineStepReadsNoTargetRenderFacts]'s, and for a
// boundary-scope attachment, whose own surface refuses any reference to the
// connections.
//
// # Why the declaration is what the rule reads
//
// It decides when the attachment renders — one that declares it renders after every
// component has rendered, so a target later in the walk than the host has reported
// what it emitted — and whether the connections on its context carry their targets'
// facts at all. So a template reading the facts is refused where the declaration is
// missing and admitted where it is present, and nothing about the facts themselves
// is read.
//
// Its detail carries the offending reference bare and the field it was written in
// captioned.
var AttachmentTemplateReadsTargetRenderFactsDeclared = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentTemplateFieldSubject,
]{
	Sentinel: ErrTargetRenderFactsNotDeclared(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentTemplateFieldSubject,
	) []constraint.Objection {
		if v.AttachmentExecutionScope(s.NodeID(), s.AttachmentName()) == executionScopeBoundary {
			return nil
		}
		if RecipeTemplateFieldIsPipelineStep(s.TemplateField()) {
			return nil
		}
		if v.AttachmentReadsTargetRenderFacts(s.NodeID(), s.AttachmentName()) {
			return nil
		}
		left, right := "{{", "}}"
		if RecipeTemplateFieldReadsWithArtifactDelimiters(s.TemplateField()) {
			delims := v.AttachmentProviderArtifactTemplateDelimiters(s.NodeID(), s.AttachmentName())
			left, right = delims.Left, delims.Right
		}
		ref, reads := attachmentTemplateTargetRenderFactsReference(
			v.AttachmentAuthoredTemplateText(s.NodeID(), s.AttachmentName(), s.TemplateField()),
			left, right,
		)
		if !reads {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrTargetRenderFactsNotDeclared(),
			constraint.Uncaptioned(ref),
			constraint.Captioned("written in", s.TemplateField()),
		)}
	},
}

// AttachmentPipelineStepReadsNoTargetRenderFacts reports each pipeline step input
// or gate of the FeatureRecipe a per-component attachment binds that reads the
// render facts of the components its host is wired to — whatever that
// FeatureRecipe declares.
//
// # Why the declaration is not read
//
// A step's templates are evaluated with the connections the host makes and never
// with their targets' render facts, for every FeatureRecipe: a step may rewrite the
// host's own documents, and the host's own render reads what its targets publish,
// never what they emitted. So a step reading a target's facts reads nothing either
// way, and the read is refused either way rather than rendered as an empty value.
// The remedy the refusal names is the same in both cases: a step reads the host's
// own facts in full, at `.context.render_facts`, and the targets' facts are read by
// the templates that produce the attachment's own resources.
//
// It is the twin of [AttachmentPipelineStepReadsNoCallerRenderFacts], and reads
// through the one matcher [AttachmentTemplateReadsTargetRenderFactsDeclared] reads
// through, so the two cannot disagree about what a reading is and differ only in
// which fields they own.
//
// Its detail carries the offending reference bare and the field it was written in
// captioned, which names the step by its id.
var AttachmentPipelineStepReadsNoTargetRenderFacts = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentTemplateFieldSubject,
]{
	Sentinel: ErrPipelineStepReadsTargetRenderFacts(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentTemplateFieldSubject,
	) []constraint.Objection {
		if v.AttachmentExecutionScope(s.NodeID(), s.AttachmentName()) == executionScopeBoundary {
			return nil
		}
		if !RecipeTemplateFieldIsPipelineStep(s.TemplateField()) {
			return nil
		}
		left, right := "{{", "}}"
		if RecipeTemplateFieldReadsWithArtifactDelimiters(s.TemplateField()) {
			delims := v.AttachmentProviderArtifactTemplateDelimiters(s.NodeID(), s.AttachmentName())
			left, right = delims.Left, delims.Right
		}
		ref, reads := attachmentTemplateTargetRenderFactsReference(
			v.AttachmentAuthoredTemplateText(s.NodeID(), s.AttachmentName(), s.TemplateField()),
			left, right,
		)
		if !reads {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrPipelineStepReadsTargetRenderFacts(),
			constraint.Uncaptioned(ref),
			constraint.Captioned("written in", s.TemplateField()),
		)}
	},
}

// attachmentTemplateCallerRenderFactsReference reads one attachment template
// field's text as written, parsed with the delimiter pair that field is evaluated
// with, and reports whether it reads its callers' render facts, returning the
// first reference that does.
//
// It is the one reading both rules over that reference make, so the two cannot
// disagree about what a reading is and differ only in which fields they own and
// which condition they raise. It takes the text and the pair as values rather than
// the presentation they come from, so every fact either rule reads is read in that
// rule's own body. A template that does not parse is left to the evaluation that
// reports it.
func attachmentTemplateCallerRenderFactsReference(text, left, right string) (string, bool) {
	refs, err := gotmpl.CollectTemplateFieldAccessesWithDelims(text, left, right)
	if err != nil {
		return "", false
	}
	return callerRenderFactsReference(refs)
}

// The connection list a per-component attachment reads its callers from, the
// connection map it reads its targets from, and the field a connection's render
// facts sit under on an entry of either. Declared once and read only by
// [callerRenderFactsReference] and [targetRenderFactsReference], so a renamed field
// cannot leave a stale spelling matching nothing — which would turn a rule quiet
// rather than failing. The field is one constant for both ends because it is one
// field: a caller's entry and a target's entry carry their facts under one name, in
// one shape.
const (
	callerConnectionsRef     = ".context.used_by"
	targetConnectionsRef     = ".context.uses"
	connectionRenderFactsRef = ".render_facts"
)

// callerRenderFactsReference reports whether a template's collected references
// read its callers' render facts, and returns the first reference that does, in
// the census's own order.
//
// The census records a field read off an element of the connection list relative
// to the element, so a reading is recognised as the pair: the list read at all,
// and `render_facts` read relative to an element — or `render_facts` named beneath
// the list directly. Both halves go through [pillarRefMatches], the one statement
// of what reaching a root or a key beneath it means.
func callerRenderFactsReference(refs []string) (string, bool) {
	readsCallers := false
	for _, ref := range refs {
		if pillarRefMatches(ref, callerConnectionsRef) {
			readsCallers = true
			break
		}
	}
	if !readsCallers {
		return "", false
	}
	for _, ref := range refs {
		if pillarRefMatches(ref, callerConnectionsRef+connectionRenderFactsRef) ||
			pillarRefMatches(ref, connectionRenderFactsRef) {
			return ref, true
		}
	}
	return "", false
}

// attachmentTemplateTargetRenderFactsReference reads one attachment template
// field's text as written, parsed with the delimiter pair that field is evaluated
// with, and reports whether it reads the render facts of the components its host is
// wired to, returning the first reference that does.
//
// It is the one reading both rules over that reference make, for the reason
// [attachmentTemplateCallerRenderFactsReference] is the one reading of its pair.
func attachmentTemplateTargetRenderFactsReference(text, left, right string) (string, bool) {
	refs, err := gotmpl.CollectTemplateFieldAccessesWithDelims(text, left, right)
	if err != nil {
		return "", false
	}
	return targetRenderFactsReference(refs)
}

// targetRenderFactsReference reports whether a template's collected references read
// the render facts of the components its host is wired to, and returns the first
// reference that does, in the census's own order.
//
// The connections are a map keyed by alias, so a target's facts are read either
// beneath one alias written out — `.context.uses.db.render_facts` — or off an
// element: inside `range .context.uses`, inside `with .context.uses.db`, or off
// `index .context.uses "db"`, which the census records relative to the element as
// `.render_facts` with nothing tying it to the map it came from. So a field counts
// as reading the facts where it names `render_facts` directly beneath an alias of
// the map, or where it reads the map at all and also reads `render_facts` relative
// to some element. That second half errs in the direction
// [callerRenderFactsReference] does and for the same reason, with one more case in
// it: a template reading the connections and also a caller's facts off
// `.context.used_by` is taken as reading its targets' as well.
//
// `render_facts` written one level beneath the map names a connection whose alias
// is that word, and is not taken for a reading. Every half goes through
// [pillarRefMatches].
func targetRenderFactsReference(refs []string) (string, bool) {
	readsTargets := false
	for _, ref := range refs {
		if pillarRefMatches(ref, targetConnectionsRef) {
			readsTargets = true
			break
		}
	}
	if !readsTargets {
		return "", false
	}
	for _, ref := range refs {
		if beneathAlias, ok := strings.CutPrefix(ref, targetConnectionsRef+"."); ok {
			if _, field, hasField := strings.Cut(beneathAlias, "."); hasField &&
				pillarRefMatches("."+field, connectionRenderFactsRef) {
				return ref, true
			}
			continue
		}
		if pillarRefMatches(ref, connectionRenderFactsRef) {
			return ref, true
		}
	}
	return "", false
}

// renderedNameSubject is the surface the rendered-name predicate needs of a
// subject: the node whose provider is asked about, and the raising every subject
// carries.
//
// It is an interface over the kinds rather than one kind, because the key lands on
// several authored surfaces and each is enumerated along its own axis. What the
// predicate does with a subject is identical on all of them.
type renderedNameSubject interface {
	constraint.Subject
	NodeID() string
}

// renderedNameObjections is the ONE body of the rendered-name predicate: it
// refuses one authored template that reads the name its target gives the
// resources it renders for the node, on a node whose provider reports no such name.
//
// The facts arrive as arguments rather than being read here, because reading them
// is precisely the part that differs between the values declared over this
// predicate: each keys its facts by its own coordinates, and a predicate reaching
// for a view would have to know which. The delimiter pair is among them, and it is
// what makes the reference set correct rather than approximate on a surface whose
// text is parsed with an alternate pair.
//
// surfaceDetail is what the calling value adds after the reference — the authored
// field, where the position cannot name it. It is a positional argument rather
// than a variadic so a value cannot omit it silently; the provides entry supplies
// none, its own position naming the entry.
//
// A template that does not parse is not this predicate's finding: evaluation
// reports the parse error against the same text, so refusing it here as well would
// report one authoring fault twice.
func renderedNameObjections[S renderedNameSubject](
	s S,
	reportedName string,
	templateText string,
	leftDelim, rightDelim string,
	declaresAuthoredSource bool,
	provider string,
	surfaceDetail []constraint.DetailValue,
) []constraint.Objection {
	if reportedName != "" || templateText == "" {
		return nil
	}
	tree, err := gotmpl.ParseTree(templateText, leftDelim, rightDelim)
	if err != nil {
		return nil
	}
	ref, reads := renderedNameReference(tree)
	if !reads {
		return nil
	}
	detail := []constraint.DetailValue{
		constraint.Uncaptioned(ref),
		constraint.Captioned("provider", provider),
	}
	return []constraint.Objection{s.RaiseWithAdvice(
		ErrRenderedNameUnavailable(),
		renderedNameRemedy(declaresAuthoredSource),
		append(detail, surfaceDetail...)...,
	)}
}

// renderedNameRemedy is what to write instead of a name the node's provider does
// not report, and it differs by whether the node supplies source of its own.
//
// The remedy is a property of the occurrence rather than of the rule, which is
// what makes stating it worth the branch. A node that supplies no source, on a
// target that reports no name for it, has its output named after its own
// identity, so there IS a correct value and naming it closes the question; a node
// that does supply source and whose target still reports no name has no
// substitute, and offering the identity there would send its reader to a value
// that names none of that node's resources. Stating the
// wrong remedy is worse than stating none, which is why the two are separate
// sentences rather than one that covers both loosely.
// Neither sentence says WHY a target reports no name, and the omission is
// deliberate rather than terseness. The reasons differ between targets — one
// reads the resource names a Recipe wrote, another derives its name from where its
// files were placed and does not know it while a node renders — and the compiler
// holds no knowledge of either: the name crosses the boundary already composed, so
// a sentence naming a reason would have the core assert something about a target it
// deliberately understands nothing about, and it would be false for every target it
// was not written for.
//
// Both sentences reach every surface the value lands on, so neither says
// "publish": a values mapping, a workload body and an artifacts entry render a
// value rather than publishing one, and a remedy phrased for one surface would be
// wrong wherever the same refusal reaches another.
func renderedNameRemedy(declaresAuthoredSource bool) string {
	if declaresAuthoredSource {
		return "write a value that does not depend on a name this target derives: it reports none " +
			"for the output it renders from supplied source, so there is nothing of that kind to read here"
	}
	return "read {{ .context.instance_id }} instead: this node supplies no source of its own, " +
		"so its resources are named after the node's identity"
}

// localsReferenceFault classifies the reference one locals entry makes, given the
// block's declared key order, the entry's own key, and the text standing at it.
//
// It is the shared body of the two rules below, and sharing it is what makes them
// one rule reported under two identities rather than two rules that can come to
// disagree. The decision itself is a third thing again — a pure classification in
// the template package — so the surface that evaluates a locals block and the
// surface that refuses one from declarations alone reach the same verdict without
// either reaching into the other.
//
// It takes the two facts' VALUES rather than the presentation they came from, and
// that is not merely tidier: a helper handed the presentation would put the facts a
// rule reads outside the rule's own body, where the narrowing read over the shipped
// checks cannot see them — so the rule would be neither cleared nor accused of
// declaring a wider view than it reads.
//
// # The author-order prefix, and why it is a slice of the declared order
//
// The entries above this one in author order are exactly the locals this one may
// read. That is not an approximation of what the evaluation does: the evaluation
// refuses before it evaluates and any refusal aborts the block, so the readable set
// at the entry for key k is the declared order's prefix above k, always. So the
// prefix is taken from the key-order fact rather than from any record of what
// resolved, which is what lets the rule run before anything resolves.
//
// An entry the declared order does not name yields no fault. Both facts are
// answered from the one ordered mapping the Recipe holds, so a key the axis
// presented is a key that order contains; a presentation answering the two from two
// sources would be the second producer the design forbids, and this reads as
// admitted rather than inventing a prefix for an entry whose position is unknown.
func localsReferenceFault(declared []string, key, text string) gotmpl.RootKeyReference {
	at := slices.Index(declared, key)
	if at < 0 {
		return gotmpl.RootKeyReference{}
	}
	return gotmpl.ClassifyRootKeyReference(
		text, "{{", "}}", localsContextRoot, declared[:at], declared,
	)
}

// localsResolvedAt is the locals readable at one entry: the declared order's prefix
// above it, empty for an entry that order does not name.
//
// It is separate from the classification above because the two are asked at
// different moments — the classification decides whether there is a fault, and this
// spells the readable set into the finding — and one function returning both would
// hand every caller a value it did not ask for.
func localsResolvedAt(declared []string, key string) []string {
	at := slices.Index(declared, key)
	if at < 0 {
		return nil
	}
	return declared[:at]
}

// boundThroughComponent is the part of a subject the caption below reads, and
// declaring it as an interface rather than taking each kind in turn is what makes
// one caption serve every rule that can carry it.
//
// A kind carrying no component cannot be passed here at all, so a rule that must
// not caption a component is excluded by the compiler rather than by a convention
// somebody has to keep. That exclusion has one member and it is deliberate: the
// two rules over a locals block a boundary-scope attachment declares are keyed by
// the boundary, because such a block is written once and reaches every component
// the boundary holds. One authored block is asked about once there, so those two
// have no repeated finding to tell apart and no single component to name — and a
// caption naming one would be false for every other component the boundary holds.
type boundThroughComponent interface{ NodeID() string }

// componentReachedThrough is the captioned detail naming the component a finding
// was reached through.
//
// # Why one function rather than the caption written at each rule
//
// The rules that carry it compose their details at their own raise, so writing
// the caption at each one would be several copies agreeing on the day they were
// written, with nothing comparing the wording afterwards. Composed here, the
// words and the decision to say them at all have one home, and a reword reaches
// every rule that calls it or none of them.
//
// # Why the finding needs it
//
// One document can be bound by many components, so one authored mistake in it is
// reported once per component that binds it — and every one of those findings
// carries the same position, because they are all about the same authored entry
// in the same document. Without this the reader gets that message repeated with
// nothing to tell the copies apart, and cannot say which of their components are
// affected. The position answers where the mistake is; this answers which
// component each finding is about.
//
// The value is the component's own identifier, which is what the author writes
// in the Aggregate and what a Release addresses it by.
func componentReachedThrough(s boundThroughComponent) constraint.DetailValue {
	return constraint.Captioned("reached through the component", s.NodeID())
}

// LocalsEntryReadsNoLaterSibling reports each local whose template reads a sibling
// the same block declares BELOW it.
//
// A locals block resolves in author order and an entry reads only a sibling
// defined above it. The block is never sorted, reordered or topologically arranged
// to make a later definition reachable, because emitted output would then follow an
// inference rather than what the author wrote — so the reference is refused instead
// of resolved. Without the refusal it resolves through the missing-key policy to an
// empty string, and a derived hostname or a secret name silently loses the segment
// it was built from.
//
// # Why an ordering question is answerable from declarations
//
// The rule reads two facts and neither is a value: the keys the block declares, in
// author order, and the text standing at this entry. The prefix above this entry is
// what it may read, so the whole question is decided before anything is evaluated —
// which is where it has to be decided, since the value a refused template would
// produce is never produced.
//
// # Its detail, and why the remedy travels with the occurrence
//
// The offending expression stands bare, because the registered wording already says
// a sibling defined later is what the finding is about. The key it names and the
// locals readable at this point are captioned, since a reference path and a key are
// both dotted words and nothing else in the message tells them apart.
//
// The remedy is supplied per objection rather than registered with the condition,
// and this rule is why that channel exists: a SELF-reference is the same condition
// — the key is declared and unresolved — and no reordering fixes it, so the remedy
// that applies to every other occurrence is wrong for exactly that one. A registered
// sentence would state the wrong remedy at whichever class it did not describe.
//
// The key is not in the detail, because the locator carries it: the finding is
// positioned at the Recipe's own locals entry, which is what an author edits. The
// component is not in the locator and is captioned instead, which is what the
// caption below exists for.
//
// # One authored mistake, one finding per component, and why they are not merged
//
// The rule is invoked once per component that binds the block, so one mistake in a
// Recipe several components share is reported once for each of them. That is not a
// duplicate to be filtered out: each finding stands for a different component, and
// merging them would tell the reader that something is wrong without saying which
// of their components it reaches. The findings share a position because they share
// an authored entry; the caption is what tells them apart. Anything added here to
// collapse them would take that back.
var LocalsEntryReadsNoLaterSibling = constraint.Check[
	constraint.DescribedView,
	constraint.NodeLocalsKeySubject,
]{
	Sentinel: ErrLocalsForwardReference(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeLocalsKeySubject,
	) []constraint.Objection {
		declared := v.DeclaredLocalsKeys(s.NodeID())
		ref := localsReferenceFault(declared, s.LocalsKey(), v.AuthoredNodeLocals(s.NodeID(), s.LocalsKey()))
		if ref.Fault != gotmpl.RootKeyUnresolved {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrLocalsForwardReference(),
			gotmpl.LocalsForwardRemedy(ref.Key, s.LocalsKey()),
			constraint.Uncaptioned(ref.Path),
			// The caption says which key the expression names and stops there. It
			// deliberately does not say the key is declared LATER: a self-reference
			// is this same condition and its key is declared at the entry itself,
			// so a caption asserting a position would be false for that class —
			// which is the class the remedy below exists to describe.
			constraint.Captioned("the key it names is", ref.Key),
			constraint.Captioned("resolved at this point",
				gotmpl.LocalsReadablePhrase(localsResolvedAt(declared, s.LocalsKey()))),
			componentReachedThrough(s),
		)}
	},
}

// LocalsEntryReadsOnlyDeclaredSiblings reports each local whose template reads a
// key the block does not declare at all.
//
// The locals pillar is an open keyed map, so an unknown key under it passes the
// root check and resolves through the missing-key policy to an empty string; the
// refusal is what stops that empty value from reaching an emitted artifact.
//
// It is a separate identity from the ordering rule above because the two mistakes
// have separate fixes: this one is a NAMING fault the author corrects by fixing the
// key, and no reordering makes it readable. One condition covering both would tell
// an author who mistyped a key to reorder a block that is already in the right
// order.
//
// The two rules share their whole body and differ in which fault they report, which
// is what keeps them one rule under two identities. It also means an entry carries
// at most one of them: the classification names the first offending reference and
// the fault it has, so no input is reported twice.
//
// Its detail names the offending expression bare, and captions the key that does
// not exist and the keys the block does declare — so the correction is readable
// from the finding, which is the whole of what a naming fault needs. It captions
// the component last, for the reason the ordering rule above states: one mistake
// in a shared block is reported once per component that binds it, and nothing
// else in the message separates those findings.
var LocalsEntryReadsOnlyDeclaredSiblings = constraint.Check[
	constraint.DescribedView,
	constraint.NodeLocalsKeySubject,
]{
	Sentinel: ErrLocalsUnknownReference(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeLocalsKeySubject,
	) []constraint.Objection {
		declared := v.DeclaredLocalsKeys(s.NodeID())
		ref := localsReferenceFault(declared, s.LocalsKey(), v.AuthoredNodeLocals(s.NodeID(), s.LocalsKey()))
		if ref.Fault != gotmpl.RootKeyUndeclared {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrLocalsUnknownReference(),
			gotmpl.LocalsUnknownRemedy,
			constraint.Uncaptioned(ref.Path),
			constraint.Captioned("the missing key is", ref.Key),
			constraint.Captioned("the block declares", gotmpl.LocalsDeclaredList(declared)),
			componentReachedThrough(s),
		)}
	},
}

// AttachmentLocalsEntryReadsNoLaterSibling reports each local of the FeatureRecipe
// an attachment binds on a node whose template reads a sibling the same block
// declares BELOW it.
//
// It is the node-carried attachment's form of the rule the node's own Recipe block
// carries, and the contract it enforces is the same one: the block resolves in
// author order, an entry reads only a sibling defined above it, and the block is
// never reordered to make a later definition reachable. What differs is the
// document the finding is positioned at and the coordinate the facts are keyed by,
// which is exactly why it is a separate declaration rather than the same rule
// reached over a wider axis: the block is authored on the FeatureRecipe, and two
// attachments on one node write theirs independently.
//
// # The prefix is the attachment's OWN block, and that bound is the substance
//
// A base Recipe's resolved locals are merged over this block AFTER it has
// resolved, so they are not readable from inside it. The readable set is therefore
// the prefix of THIS block and stops there, and an attachment local naming a
// base-Recipe key is reading a key that does not exist at the point it runs — which
// the naming rule beside this one reports.
//
// Its detail carries what the node's own form carries and for the same reasons: the
// offending expression bare, the key it names captioned, the locals readable at
// this point captioned, and the component the attachment sits on captioned last —
// one FeatureRecipe is bound by attachments on many components, so the repetition
// this last one separates is the same repetition, one document further out. The
// remedy travels with the occurrence because a self-reference is this same
// condition and no reordering fixes that one.
var AttachmentLocalsEntryReadsNoLaterSibling = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentLocalsKeySubject,
]{
	Sentinel: ErrLocalsForwardReference(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentLocalsKeySubject,
	) []constraint.Objection {
		declared := v.AttachmentDeclaredLocalsKeys(s.NodeID(), s.AttachmentName())
		ref := localsReferenceFault(declared, s.LocalsKey(),
			v.AttachmentAuthoredLocals(s.NodeID(), s.AttachmentName(), s.LocalsKey()))
		if ref.Fault != gotmpl.RootKeyUnresolved {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrLocalsForwardReference(),
			gotmpl.LocalsForwardRemedy(ref.Key, s.LocalsKey()),
			constraint.Uncaptioned(ref.Path),
			constraint.Captioned("the key it names is", ref.Key),
			constraint.Captioned("resolved at this point",
				gotmpl.LocalsReadablePhrase(localsResolvedAt(declared, s.LocalsKey()))),
			componentReachedThrough(s),
		)}
	},
}

// AttachmentLocalsEntryReadsOnlyDeclaredSiblings reports each local of the
// FeatureRecipe an attachment binds on a node whose template reads a key the block
// does not declare at all.
//
// It is a separate identity from the ordering rule above for the reason the node's
// own pair are separate: this is a NAMING fault the author corrects by fixing the
// key, and no reordering makes it readable. The two share their whole body and
// differ in which fault they report, so an entry carries at most one of them.
//
// The class it reaches that the node's own form cannot is an attachment local
// naming a base-Recipe key: the merge happens after this block resolves, so such a
// key is undeclared here and the finding says so rather than suggesting a reorder.
var AttachmentLocalsEntryReadsOnlyDeclaredSiblings = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentLocalsKeySubject,
]{
	Sentinel: ErrLocalsUnknownReference(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentLocalsKeySubject,
	) []constraint.Objection {
		declared := v.AttachmentDeclaredLocalsKeys(s.NodeID(), s.AttachmentName())
		ref := localsReferenceFault(declared, s.LocalsKey(),
			v.AttachmentAuthoredLocals(s.NodeID(), s.AttachmentName(), s.LocalsKey()))
		if ref.Fault != gotmpl.RootKeyUndeclared {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrLocalsUnknownReference(),
			gotmpl.LocalsUnknownRemedy,
			constraint.Uncaptioned(ref.Path),
			constraint.Captioned("the missing key is", ref.Key),
			constraint.Captioned("the block declares", gotmpl.LocalsDeclaredList(declared)),
			componentReachedThrough(s),
		)}
	},
}

// BoundaryAttachmentLocalsEntryReadsNoLaterSibling reports each local of the
// FeatureRecipe a boundary-scope attachment binds whose template reads a sibling
// the same block declares BELOW it.
//
// The contract is the one every locals block follows; what is its own here is
// where the block is reached from. Such an attachment is declared at a boundary and
// stands against no single component, so the rule is invoked over the boundary set
// rather than the node set — one authored block asked about once, whatever the
// boundary holds, including a boundary holding no component at all.
//
// There is no base block to merge over this one, because there is no component
// whose Recipe could contribute one. The readable set is the prefix of this block
// and that is the whole of it.
var BoundaryAttachmentLocalsEntryReadsNoLaterSibling = constraint.Check[
	constraint.DescribedView,
	constraint.BoundaryAttachmentLocalsKeySubject,
]{
	Sentinel: ErrLocalsForwardReference(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.BoundaryAttachmentLocalsKeySubject,
	) []constraint.Objection {
		declared := v.BoundaryAttachmentDeclaredLocalsKeys(s.BoundaryPath(), s.AttachmentName())
		ref := localsReferenceFault(declared, s.LocalsKey(),
			v.BoundaryAttachmentAuthoredLocals(s.BoundaryPath(), s.AttachmentName(), s.LocalsKey()))
		if ref.Fault != gotmpl.RootKeyUnresolved {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrLocalsForwardReference(),
			gotmpl.LocalsForwardRemedy(ref.Key, s.LocalsKey()),
			constraint.Uncaptioned(ref.Path),
			constraint.Captioned("the key it names is", ref.Key),
			constraint.Captioned("resolved at this point",
				gotmpl.LocalsReadablePhrase(localsResolvedAt(declared, s.LocalsKey()))),
		)}
	},
}

// BoundaryAttachmentLocalsEntryReadsOnlyDeclaredSiblings reports each local of the
// FeatureRecipe a boundary-scope attachment binds whose template reads a key the
// block does not declare at all.
//
// It is a separate identity from the ordering rule above for the reason every form
// of this pair is separate: a naming fault is corrected by fixing the key, and
// telling such an author to reorder a block already in the right order would
// describe their mistake wrongly.
var BoundaryAttachmentLocalsEntryReadsOnlyDeclaredSiblings = constraint.Check[
	constraint.DescribedView,
	constraint.BoundaryAttachmentLocalsKeySubject,
]{
	Sentinel: ErrLocalsUnknownReference(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.BoundaryAttachmentLocalsKeySubject,
	) []constraint.Objection {
		declared := v.BoundaryAttachmentDeclaredLocalsKeys(s.BoundaryPath(), s.AttachmentName())
		ref := localsReferenceFault(declared, s.LocalsKey(),
			v.BoundaryAttachmentAuthoredLocals(s.BoundaryPath(), s.AttachmentName(), s.LocalsKey()))
		if ref.Fault != gotmpl.RootKeyUndeclared {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrLocalsUnknownReference(),
			gotmpl.LocalsUnknownRemedy,
			constraint.Uncaptioned(ref.Path),
			constraint.Captioned("the missing key is", ref.Key),
			constraint.Captioned("the block declares", gotmpl.LocalsDeclaredList(declared)),
		)}
	},
}

// BoundaryAttachmentTemplateReadsNoPerComponentPillar reports each inline
// template field of a boundary-scope attachment's FeatureRecipe that names a
// pillar the per-component path carries and this surface does not.
//
// # One rule over a whole family of authored fields
//
// It is invoked once per template field the FeatureRecipe writes, with the
// authored field label as the innermost coordinate, so one declaration covers
// every one of them and a field the FeatureRecipe gains later is covered with
// nothing here moving. The alternative — a rule per template-bearing field — puts
// the field identity in structure, and a field whose declaration somebody forgot
// is then silently ungated with nothing able to report the omission.
//
// One rule serves them all even though two reference sets do: every field the
// recipe evaluates once for its boundary is checked against the boundary scope,
// and its injectMapping — which runs once per consuming component — against the
// scope that evaluation reads. The rule does not hold either set; it hands the
// field and its authored text to the shared classification, which selects the set
// the render itself applies to that field. Only the boundary scope narrows away
// per-component pillars, so only a field checked against it can commit this
// rule's finding.
//
// # Why the execution scope is read and not assumed
//
// Declaring an attachment ON a boundary and declaring a FeatureRecipe that runs
// ONCE PER boundary are two separate decisions. An attachment written at a
// boundary whose FeatureRecipe states no scope reaches every component that
// boundary holds and is evaluated against the full per-component context, where
// the pillars this rule refuses are exactly the ones its templates are supposed
// to read. So the rule ends on such an attachment before reading anything
// further; refusing it would reject legal authoring on the strength of where the
// operator happened to attach it.
//
// Its detail carries the offending reference bare, the field it was written in
// captioned, and the set this surface defines captioned. The last two are derived
// from the admission itself rather than restated, so what the build refuses and
// what the author is told cannot disagree.
//
// # Why the field is named in the detail as well as positioned
//
// The locator spells the authored keys the dialect can address and stops at one
// it cannot — a keyed block whose key is a filename carries the dialect's
// separator inside a single authored key, so an artifact entry positions at the
// block and no further. The block says where to look and only the detail can say
// which field inside it. It is captioned, because a field label standing bare
// beside another quoted value is not something a reader can tell apart from it.
var BoundaryAttachmentTemplateReadsNoPerComponentPillar = constraint.Check[
	constraint.DescribedView,
	constraint.BoundaryAttachmentTemplateFieldSubject,
]{
	Sentinel: ErrFRTemplateInvalidField(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.BoundaryAttachmentTemplateFieldSubject,
	) []constraint.Objection {
		if v.BoundaryAttachmentExecutionScope(s.BoundaryPath(), s.AttachmentName()) != executionScopeBoundary {
			return nil
		}
		ref := classifyBoundaryScopeFRTemplate(s.TemplateField(), v.BoundaryAttachmentAuthoredTemplateText(
			s.BoundaryPath(), s.AttachmentName(), s.TemplateField(),
		))
		if ref.Fault != boundaryScopeFRTemplatePerComponentPillar {
			return nil
		}
		caption, phrase := templateAdmittedSetPhrase(ref.Violation)
		return []constraint.Objection{s.RaiseWith(
			ErrFRTemplateInvalidField(),
			constraint.Uncaptioned(ref.Violation.Ref.Path),
			constraint.Captioned("written in", s.TemplateField()),
			constraint.Captioned(caption, phrase),
		)}
	},
}

// BoundaryAttachmentTemplateReadsOnlyDefinedReferences reports each inline
// template field of a boundary-scope attachment's FeatureRecipe that names a
// reference the scope that field is evaluated in does not define.
//
// It is a separate identity from the rule above for the reason the locals pair
// are separate: the two describe different mistakes. That one names a field that
// EXISTS and carries nothing here, so the author rewrites the reference against
// the pillars this surface populates; this one names a reference its field's
// scope does not define — a stale root, a mistyped key beneath a closed pillar,
// or a read of .boundary in the injectMapping, which runs per consuming component
// where nothing sets it — which no rewriting against the boundary pillars will
// fix and which would otherwise render as an empty value or fail while the
// template runs. The two share their whole body and differ in which fault they
// report, so a field carries at most one of them.
//
// It reads the execution scope and ends on an attachment that is not boundary
// scope, for the reason stated on the rule above.
//
// Its detail names the offending reference bare, the field it was written in
// captioned for the reason the rule above states, and the set that was consulted
// captioned — which for a refused key is that key's own root rather than the
// context, the distinction the author needs since the two refusals read
// identically otherwise.
var BoundaryAttachmentTemplateReadsOnlyDefinedReferences = constraint.Check[
	constraint.DescribedView,
	constraint.BoundaryAttachmentTemplateFieldSubject,
]{
	Sentinel: ErrTemplateUnknownRoot(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.BoundaryAttachmentTemplateFieldSubject,
	) []constraint.Objection {
		if v.BoundaryAttachmentExecutionScope(s.BoundaryPath(), s.AttachmentName()) != executionScopeBoundary {
			return nil
		}
		ref := classifyBoundaryScopeFRTemplate(s.TemplateField(), v.BoundaryAttachmentAuthoredTemplateText(
			s.BoundaryPath(), s.AttachmentName(), s.TemplateField(),
		))
		if ref.Fault != boundaryScopeFRTemplateUnknownRoot {
			return nil
		}
		caption, phrase := templateAdmittedSetPhrase(ref.Violation)
		return []constraint.Objection{s.RaiseWith(
			ErrTemplateUnknownRoot(),
			constraint.Uncaptioned(ref.Violation.Ref.Path),
			constraint.Captioned("written in", s.TemplateField()),
			constraint.Captioned(caption, phrase),
		)}
	},
}

// BoundaryPathTemplateReadsOnlyAdmittedFields reports each path template a
// boundary declares that names a variable this surface does not offer.
//
// # One rule over a whole family of authored fields
//
// It is invoked once per path template the boundary writes, with the authored
// field as the innermost coordinate, so one declaration covers every one of them
// and a field the Release surface gains later is covered with nothing here
// moving. The alternative — a rule per path-bearing field — puts the field
// identity in structure, and a field whose declaration somebody forgot is then
// silently ungated with nothing able to report the omission.
//
// # One rule serves both fields even though their reference sets differ
//
// This owner is the family's degenerate case: the two fields are checked
// against two different admitted sets, because a whole-boundary template is
// evaluated once for the scope while a per-component segment is evaluated once
// for each component placed in it. That does not fork the rule, because the rule
// does not hold a set — it hands the authored text and the field to the shared
// classification, which selects the set the render itself would apply. What the
// family buys on this owner is therefore enumerable coverage rather than a
// reference set shared across fields, and saying so is what keeps the shape from
// being reached for where it pays less than it looks.
//
// Its detail carries the offending reference bare, the field it was written in
// captioned, and the set this surface defines captioned. The last two are derived
// from the admission itself rather than restated, so what the build refuses and
// what the author is told cannot disagree. The field is named in the detail as
// well as positioned because a boundary writes more than one, and a locator
// naming the scope alone would not say which.
var BoundaryPathTemplateReadsOnlyAdmittedFields = constraint.Check[
	constraint.DescribedView,
	constraint.BoundaryPathTemplateFieldSubject,
]{
	Sentinel: ErrPathTemplateInvalidField(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.BoundaryPathTemplateFieldSubject,
	) []constraint.Objection {
		ref := classifyPathTemplate(s.PathTemplateField(), v.BoundaryAuthoredPathTemplateText(
			s.BoundaryPath(), s.PathTemplateField(),
		))
		if ref.Fault != pathTemplateInvalidField {
			return nil
		}
		caption, phrase := templateAdmittedSetPhrase(ref.Violation)
		return []constraint.Objection{s.RaiseWith(
			ErrPathTemplateInvalidField(),
			constraint.Uncaptioned(ref.Violation.Ref.Path),
			constraint.Captioned("written in", s.PathTemplateField()),
			constraint.Captioned(caption, phrase),
		)}
	},
}

// BoundaryPathTemplateReadsOnlyDefinedKeys reports each path template a boundary
// declares that names a key the variable it reads through does not define.
//
// It is a separate identity from the rule above for the reason the locals pair
// are separate: the two describe different mistakes. That one names a VARIABLE
// this surface does not offer — one the context does not carry at all, or the
// component's own id in a template evaluated once for the whole scope, where a
// single component's id would make the emitted path depend on which component was
// placed first. This one names a key beneath a variable that IS offered, which is
// a mistyped key rather than a wrong variable, and which would otherwise render as
// an empty path segment an operator only discovers in the emitted layout. The two
// share their whole body and differ in which fault they report, so a field carries
// at most one of them.
//
// The identity it raises is the unknown-reference halt every template surface
// reports for a mistyped key, which is what the evaluation beneath it already
// raises for the same text — so the declaration adds a position and a breadth,
// never a second name for one mistake.
//
// Its detail names the offending reference bare, the field it was written in
// captioned for the reason the rule above states, and the set that was consulted
// captioned — which for a refused key is that key's own variable rather than the
// context, the distinction the author needs since the two refusals read
// identically otherwise.
var BoundaryPathTemplateReadsOnlyDefinedKeys = constraint.Check[
	constraint.DescribedView,
	constraint.BoundaryPathTemplateFieldSubject,
]{
	Sentinel: ErrTemplateUnknownRoot(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.BoundaryPathTemplateFieldSubject,
	) []constraint.Objection {
		ref := classifyPathTemplate(s.PathTemplateField(), v.BoundaryAuthoredPathTemplateText(
			s.BoundaryPath(), s.PathTemplateField(),
		))
		if ref.Fault != pathTemplateUnknownKey {
			return nil
		}
		caption, phrase := templateAdmittedSetPhrase(ref.Violation)
		return []constraint.Objection{s.RaiseWith(
			ErrTemplateUnknownRoot(),
			constraint.Uncaptioned(ref.Violation.Ref.Path),
			constraint.Captioned("written in", s.PathTemplateField()),
			constraint.Captioned(caption, phrase),
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
// # It reads presence from the pipe, and only presence
//
// The fact it reads is the value the render resolved for the key, whose second
// result is whether the key was resolved at all, and only that result is read.
// Every entry a Recipe's provides block writes is resolved, one whose template
// renders to nothing included — its value is the empty string — so such an
// entry fulfils the contract: refusing it would be inspecting the value. One
// fact answers the whole predicate, since the fact's absence half IS the violation.
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
// rule — the three declared values below, one per owner the artifacts walk is
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
// one home for the emptiness condition among the render's readers, which reach it
// directly where what they hold is the value, and through recipeSourceDeclared
// where what they hold is the entity.
//
// Its scope is the render rather than the compiler, and saying so is the point: the
// descent that resolves a Release's dependencies classifies a source by presence
// alone and singles the empty string out as a reference it cannot place, so it never
// reaches this helper. The two sides therefore disagree about one input — an entry
// written and left empty, which the render reads as no source and the descent reads
// as a source it cannot resolve. Neither reading is reached for an authored Recipe:
// loading the Recipe refuses that entry first, under its own condition.
//
// A source that evaluated to an empty string leaves the entity with none just as
// an absent source does, and an author cannot tell the two apart from the
// outcome — so this is one condition rather than two, which is what lets a rule
// about a template reading the source pillar name a single cause.
//
// One helper for the same reason the pillar roots above have one home: this is a
// single decision with more than one reader, and a copy per reader would agree
// today and would be exactly what diverges the next time the emptiness condition
// changes. The rule itself lives in sourceref, beside the locality rule that
// reads the same value, because readers outside this package ask it too.
func sourceValueDeclared(src any) bool {
	return sourceref.Declared(src)
}

// recipeSourceDeclared reports whether a Recipe carries a source, reading the
// value the author WROTE rather than the resolved one.
//
// That is what makes it usable as the guard resolution is gated on: it is asked
// before a source has been resolved, and a reader wanting the resolved value
// passes that value to sourceValueDeclared directly instead.
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

// recipeWorkloadDeclared reports whether a Recipe declares a workload for its
// provider to synthesize, reading the block the author WROTE.
//
// It is one helper because two readers ask the one question and must not part:
// the render, which synthesizes a workload for exactly the nodes this answers
// true for, and the question put to a provider ahead of the render about the name
// it will give that node's resources. A provider told a node is synthesized when
// the render then synthesizes nothing for it — or the reverse — would report a
// name for resources that are not emitted, or withhold one for resources that
// are. A nil Recipe declares no workload.
func recipeWorkloadDeclared(recipe *types.Recipe) bool {
	return recipe != nil && !recipe.Spec.Workload.IsEmpty()
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
// ownerDetail is what the calling value adds to the objection after the
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
	ownerDetail []constraint.DetailValue,
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
			ErrArtifactPillarNotDeclared(), append(detail, ownerDetail...)...,
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

// The artifact-content-pin rule, declared once per artifacts block the rule
// governs.
//
// One rule, three values, on the reading the artifact-pillar family above states:
// three separately authored artifacts blocks are three subject kinds, an entry's
// output name is meaningful only together with the block it was written in, and the
// pins are recorded in three different documents. A value reading node-keyed facts
// about a FeatureRecipe's entries would measure that FeatureRecipe's content
// against whatever the host component's Recipe happened to record.
//
// The predicate has ONE body, [artifactContentPinObjections], so the digest
// function, the comparison and the reporting exist once. A second copy would agree
// on the day it was written and be free to drift the moment either the digest or
// the wording moved — and a drift here means one owner refusing content another
// admits.

// artifactContentPinObjections is the one body of the artifact-content-pin
// predicate: it re-digests the text standing at one artifact and reports a
// disagreement with the digest that entry's document recorded for it.
//
// pinned is what says a comparison is owed, and it is read rather than inferred
// from an empty pin. An entry carries a recorded pin only where its resolved form
// is content supplied by an imported file: an inline entry is part of the entity
// and is pinned by whatever pins that, and a bundle-supplied one is pinned by the
// version the bundle resolved at. That gate lives in the fact's production, at the
// one site that knows which form the walk resolved, so a reader cannot pair a pin
// with the wrong form.
//
// It re-digests the text rather than reading a digest computed elsewhere, because
// the text is the fact and the digest is a function of it: a second recorded digest
// beside the text would be a second answer to one question, free to disagree with
// the bytes the text fact carries. The digest is the shared content digest the
// pin step itself uses, so the value compared and the value recorded come from one
// implementation.
//
// importedPath is the file the entry was supplied from, which the calling value
// reads from its own owner because the path and the pin are recorded in
// different documents and one fact may name only one of them. It is a bare string
// with no presence flag: an entry carrying a recorded pin was resolved from an
// imported file by the same branch that recorded the path, so a pinned entry
// always has one.
//
// ownerDetail is what the calling value adds after the digests — the attachment,
// where the entry belongs to one — and is positional rather than variadic so a
// value cannot omit it silently.
//
// # What the detail carries, and why the path is in it
//
// Its detail leaves the artifact bare, because the registered wording already says
// imported artifact content is what the finding is about, and captions the two
// digests, which are otherwise two hex strings a reader cannot tell apart.
//
// The locator names the artifacts block in the document the entry was written in,
// which is where the `$import` naming the drifted file is authored — so an author
// reading the finding is put one line from the reference. That is not the same as
// being told which file drifted, and the difference is a hop the author pays on
// every occurrence: the artifact's output name is chosen independently of the file
// it was supplied from, so nothing else the finding carries names the file. The
// path is therefore reported beside the name, CAPTIONED, because an absolute path
// standing bare beside a quoted output name is two quoted strings a reader cannot
// tell apart — the bare slot is already taken by the artifact.
//
// It is spelled once, in [artifactContentPinDetail], rather than at each calling
// value. Three copies would agree the day they were written, and the agreement is
// what would hide the next change to the caption or the order.
func artifactContentPinObjections[S artifactPillarSubject](
	s S,
	templateText string,
	pin string,
	pinned bool,
	importedPath string,
	ownerDetail []constraint.DetailValue,
) []constraint.Objection {
	if !pinned {
		return nil
	}
	got := digest.ContentDigest([]byte(templateText))
	if got == pin {
		return nil
	}
	detail := artifactContentPinDetail(s.ArtifactName(), importedPath, got, pin)
	return []constraint.Objection{s.RaiseWithAdvice(
		ErrArtifactContentMismatch(), artifactContentPinAdvice, append(detail, ownerDetail...)...,
	)}
}

// artifactContentPinDetail is the one spelling of what an imported-file pin
// disagreement names: the artifact, bare, because the registered sentence already
// says the finding is about imported artifact content; then the imported file and
// the two digests, each captioned, because they are otherwise strings a reader
// cannot tell apart. Every composer of the finding builds its detail here — the
// build's checks and [ArtifactContentPinMismatch] alike — so the finding reads the
// same whichever command reports it.
func artifactContentPinDetail(artifact, importedPath, current, recorded string) []constraint.DetailValue {
	return []constraint.DetailValue{
		constraint.Uncaptioned(artifact),
		constraint.Captioned("imported file", importedPath),
		constraint.Captioned("its content digests to", current),
		constraint.Captioned("the recorded pin is", recorded),
	}
}

// artifactContentPinAdvice is what an imported-file pin disagreement tells its
// reader to do. It is advice rather than part of the registered sentence because
// advice belongs to the raise path, and it is one constant because every path that
// raises this finding offers the same two remedies: the file was changed on
// purpose, so the pin is re-recorded, or it was not, so the file is restored.
const artifactContentPinAdvice = "If the file was changed on purpose, run `aggrega pin` on the entity that " +
	"imports it to record its new digest; otherwise restore the file"

// ArtifactContentMatchesItsRecordedPin reports each of a node's artifacts whose
// imported content no longer digests to the pin recorded for it in the lock beside
// its Recipe.
//
// A pin recorded for an entry is the digest of the bytes that entry's imported file
// held when a pull resolved the Recipe. The build re-digests the file it reads and
// refuses the disagreement, so a file edited after the pull halts loudly rather
// than rendering content nothing verified. A Recipe no pull has resolved has no pin
// recorded beside it and nothing to compare, which is why the fact reports presence
// rather than answering with an empty digest.
//
// It reads two facts and holds no traversal over subjects, because it is invoked
// once per artifact the node's walk resolved. Both are keyed by the node and the
// entry's output name: the text, which is the entry's content after the walk
// resolved whichever form it took, and the recorded pin, which is present only for
// the one form that carries one.
//
// # Why the text and not the projected bytes
//
// The comparison is against the entry's content, not against what evaluating it
// produced. An artifact template is a template: evaluating it substitutes values
// that differ per node, so a digest over the output would disagree with the
// recorded pin for every node whose values differ — while the content the pin was
// taken from is the same text for every node that binds the Recipe.
var ArtifactContentMatchesItsRecordedPin = constraint.Check[
	constraint.HydratedView,
	constraint.NodeArtifactSubject,
]{
	Sentinel: ErrArtifactContentMismatch(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeArtifactSubject,
	) []constraint.Objection {
		pin, pinned := v.ArtifactRecordedContentPin(s.NodeID(), s.ArtifactName())
		return artifactContentPinObjections(
			s,
			v.ArtifactTemplateText(s.NodeID(), s.ArtifactName()),
			pin, pinned,
			v.ArtifactImportedPath(s.NodeID(), s.ArtifactName()),
			nil,
		)
	},
}

// AttachmentArtifactContentMatchesItsRecordedPin reports each artifact of the
// FeatureRecipe one attachment binds on a node whose imported content no longer
// digests to the pin recorded for it in the lock beside that FeatureRecipe.
//
// Every reading the value above sets out applies here over a different document.
// Each of its two facts is keyed by the attachment as well as by the node, and that
// is what makes the verdict the attachment's rather than the host's: the render of
// an attachment walks the same producer under the HOST component's node identity,
// so facts keyed by the node alone would measure this FeatureRecipe's content
// against the pin recorded for the host Recipe — admitting drifted content wherever
// the host has no pin, and refusing intact content wherever the host has one under
// the same output name.
//
// Its detail names the attachment, which is what says which FeatureRecipe: the
// locator names the artifacts block inside that document, and a node may carry
// several attachments whose blocks are separate documents.
var AttachmentArtifactContentMatchesItsRecordedPin = constraint.Check[
	constraint.HydratedView,
	constraint.NodeAttachmentArtifactSubject,
]{
	Sentinel: ErrArtifactContentMismatch(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeAttachmentArtifactSubject,
	) []constraint.Objection {
		pin, pinned := v.AttachmentArtifactRecordedContentPin(
			s.NodeID(), s.AttachmentName(), s.ArtifactName(),
		)
		return artifactContentPinObjections(
			s,
			v.AttachmentArtifactTemplateText(s.NodeID(), s.AttachmentName(), s.ArtifactName()),
			pin, pinned,
			v.AttachmentArtifactImportedPath(s.NodeID(), s.AttachmentName(), s.ArtifactName()),
			[]constraint.DetailValue{constraint.Captioned("attachment", s.AttachmentName())},
		)
	},
}

// BoundaryAttachmentArtifactContentMatchesItsRecordedPin reports each artifact of
// the FeatureRecipe one boundary-scope attachment binds whose imported content no
// longer digests to the pin recorded for it in the lock beside that FeatureRecipe.
//
// It is the same question the two values above ask, rooted where such an attachment
// is declared. A boundary-scope attachment is written once on a boundary and reaches
// every component the boundary holds, so its facts are keyed by the boundary rather
// than by any component: keyed by a node, one authored entry would be measured once
// per component the boundary holds, and not at all for a boundary holding none.
//
// Its detail names the attachment, on the reading the node-carried form follows.
var BoundaryAttachmentArtifactContentMatchesItsRecordedPin = constraint.Check[
	constraint.HydratedView,
	constraint.BoundaryAttachmentArtifactSubject,
]{
	Sentinel: ErrArtifactContentMismatch(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.BoundaryAttachmentArtifactSubject,
	) []constraint.Objection {
		pin, pinned := v.BoundaryAttachmentArtifactRecordedContentPin(
			s.BoundaryPath(), s.AttachmentName(), s.ArtifactName(),
		)
		return artifactContentPinObjections(
			s,
			v.BoundaryAttachmentArtifactTemplateText(
				s.BoundaryPath(), s.AttachmentName(), s.ArtifactName(),
			),
			pin, pinned,
			v.BoundaryAttachmentArtifactImportedPath(
				s.BoundaryPath(), s.AttachmentName(), s.ArtifactName(),
			),
			[]constraint.DetailValue{constraint.Captioned("attachment", s.AttachmentName())},
		)
	},
}

// The four rules over a node's own authored artifacts entries, declared once per
// condition over the stage that holds the bound Recipe.
//
// # They are keyed by the authored entry and never by the artifact
//
// Every one of them reports the failure of a resolution, and the artifact
// coordinate the shipped artifact rules are keyed by is enumerated from that
// resolution's SUCCESS: an entry importing a library bundle becomes one artifact
// per file the bundle holds, so an entry whose import fails becomes none. A rule
// keyed there would be invoked zero times for exactly the inputs it exists to
// refuse, and a rule invoked zero times reports what a rule that ran and approved
// reports. The authored key is there from the moment the Recipe is bound.
//
// # Each reads the step of the resolution cascade it is about, and no other
//
// The resolution is a cascade — the reference splits, the library is found, the
// bundle is found, the bundle's own files are read — and each step presupposes
// the one above. So each rule below declines an entry whose earlier step already
// failed, and that is what keeps one authoring mistake reported once: an entry
// naming a library nothing declares has no bundle either, and a reader told both
// would be told the second thing as though it were a separate fault.
//
// # What they widen, stated because it is a real change and not a refinement
//
// The resolver they stand in front of returns ONE error, so an entry carrying
// two faults at different steps reported whichever the cascade reached first.
// These are invoked once per entry and each reports every occurrence it finds,
// so a Recipe with three offending entries now reports three findings and a
// bundle whose files carry two distinct faults reports both. Each finding carries
// its own position, which is why they are not folded into one message.
//
// # What each of them owes its detail, and why
//
// An authored artifacts key is the output's filename, so it carries the locator
// dialect's separator as one key that dialect cannot address: a finding is
// positioned at the artifacts block in the Recipe's own file and stops there. So
// every rule below names the entry in its detail, CAPTIONED — the block says
// where to look and only the detail can say which entry inside it, and a filename
// standing bare beside another quoted value is not something a reader can tell
// apart from it.
//
// The node is a coordinate the subject carries and the locator does not, and it
// is deliberately absent from every detail below: the fault is in the Recipe's
// text rather than in any one node that binds it, so naming a node would point an
// author at an instance when the edit is in the entry every instance shares.

// artifactsEntryLibraryReference splits one entry's authored library reference
// into the library and bundle it names, and reports whether the entry carries a
// library reference at all.
//
// It is the split the consumption site resolves the reference with rather than a
// second reading of the same grammar, so a library a rule names in a finding is
// the library the resolution looked for.
//
// An entry the fact answers empty for carries no library reference — it is
// written inline, supplied by a file of the workspace, or refused elsewhere as
// host-bearing — and reports false, which is what leaves every rule below
// declining it rather than judging it.
func artifactsEntryLibraryReference(ref string) (library, bundle string, held bool) {
	if ref == "" {
		return "", "", false
	}
	library, bundle, ok := libraryimport.SplitRef(ref, "/artifacts/")
	if !ok {
		return "", "", false
	}
	return library, bundle, true
}

// The rules over the values-mapping import a node's own Recipe writes are keyed by
// the Recipe's own spec-root field, which the authoring surface fixes and which
// carries no locator separator — so a finding is positioned exactly, at
// .spec.valuesMapping in the Recipe's own file, and none of them owes a captioned
// stand-in for a position it could not reach.
//
// The node is a coordinate the subject carries and the locator does not, and it is
// deliberately absent from every detail below: the fault is in the Recipe's text
// rather than in any one node that binds it, so naming a node would point an author
// at an instance when the edit is in the entry every instance shares.

// valuesMappingLibraryReference splits a node's authored values-mapping import
// reference into the library and entry it names, and reports whether the field
// carries a library reference at all.
//
// It is the split the consumption site resolves the reference with rather than a
// second reading of the same grammar, so a library a rule names in a finding is the
// library the resolution looked for.
//
// A field the fact answers empty for carries no library reference — the values
// mapping is written inline, supplied by a file of the workspace, or refused
// elsewhere as host-bearing — and reports false, which is what leaves every rule
// below declining it rather than judging it. A coordinate naming a field other than
// the values mapping answers empty for the same reason and declines the same way.
func valuesMappingLibraryReference(ref string) (library, entry string, held bool) {
	if ref == "" {
		return "", "", false
	}
	library, entry, ok := libraryimport.SplitRef(ref, "/mappings/")
	if !ok {
		return "", "", false
	}
	return library, entry, true
}

// ValuesMappingImportIsWellShaped reports a values-mapping import whose reference
// does not name an entry the compiler can look for.
//
// The reference reaches this rule already read as a library import — it carries the
// `#/mappings/` fragment and no registry host — so what is left to judge is whether
// the two halves that fragment separates are both there. A reference naming an
// empty entry is the production shape of that fault: the fragment is present, the
// library is named, and there is nothing after the prefix for the library to be
// asked about.
//
// # What it deliberately does not judge
//
// A reference whose library half carries the retired `@` version separator splits
// cleanly and is not reported here. The resolution beneath refuses it with a steer
// to the colon form, and that refusal is reached on a path this rule does not stand
// in front of — the reference is resolved as a dependency before any render begins,
// under the condition that names an unusable library reference. Declaring a
// predicate here for a reference this stage is never asked about would put a rule
// in the enforced set that nothing can invoke, which reads exactly like a rule that
// ran and approved.
//
// The reference is the value the registered sentence is about, so it stands bare.
// The advice names the shape the reference has to take, because the remedy is a
// single edit and stating it is cheaper for the reader than describing it.
var ValuesMappingImportIsWellShaped = constraint.Check[
	constraint.DescribedView,
	constraint.NodeRecipeImportFieldSubject,
]{
	Sentinel: ErrMappingImportShape(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeRecipeImportFieldSubject,
	) []constraint.Objection {
		ref := v.RecipeValuesMappingImportReference(s.NodeID(), s.RecipeImportField())
		if ref == "" {
			return nil
		}
		if _, _, held := valuesMappingLibraryReference(ref); held {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrMappingImportShape(),
			"name the library and the entry either side of the fragment — write "+
				"<Library>#/mappings/<entry>",
			constraint.Uncaptioned(ref),
		)}
	},
}

// ValuesMappingImportNamesADeclaredEntry reports a values-mapping import naming an
// entry the MappingLibrary it resolved to does not declare.
//
// It declines a field whose library was not found at all, because a library nothing
// declares declares no entry either — and the resolution that refuses an absent
// library is reached before any render begins, so a build meeting that fault never
// reaches this rule to be told about the entry instead.
//
// The entry is the value the registered sentence is about, so it stands bare; the
// library is captioned beside it because an entry name means nothing without the
// document it was looked for in, and the two are written by different authors often
// enough that the reader may have to open a library they did not publish.
var ValuesMappingImportNamesADeclaredEntry = constraint.Check[
	constraint.DescribedView,
	constraint.NodeRecipeImportFieldSubject,
]{
	Sentinel: ErrMappingEntryNotFound(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeRecipeImportFieldSubject,
	) []constraint.Objection {
		ref := v.RecipeValuesMappingImportReference(s.NodeID(), s.RecipeImportField())
		library, entry, held := valuesMappingLibraryReference(ref)
		if !held || !v.RecipeValuesMappingLibraryDeclared(s.NodeID(), s.RecipeImportField()) {
			return nil
		}
		if v.RecipeValuesMappingEntryDeclared(s.NodeID(), s.RecipeImportField()) {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrMappingEntryNotFound(),
			constraint.Uncaptioned(entry),
			constraint.Captioned("MappingLibrary", library),
		)}
	},
}

// The rules over the workload import a node's own Recipe writes are keyed by the
// same Recipe spec-root coordinate the values-mapping rules above are, and carry the
// same two properties: the key is fixed by the authoring surface and carries no
// locator separator, so a finding is positioned exactly at .spec.workload in the
// Recipe's own file and owes no captioned stand-in; and the node is a coordinate the
// subject carries and the locator does not, deliberately absent from every detail
// below, because the fault is in the Recipe's text rather than in any one node that
// binds it.

// workloadLibraryReference splits a node's authored workload import reference into
// the library and entry it names, and reports whether the field carries a library
// reference at all.
//
// It is the split the consumption site resolves the reference with rather than a
// second reading of the same grammar, so a library a rule names in a finding is the
// library the resolution looked for.
//
// A field the fact answers empty for carries no library reference — the workload is
// written inline, supplied by a file of the workspace, or refused elsewhere as
// host-bearing — and reports false, which is what leaves every rule below declining
// it rather than judging it. A coordinate naming a field other than the workload
// answers empty for the same reason and declines the same way.
func workloadLibraryReference(ref string) (library, entry string, held bool) {
	if ref == "" {
		return "", "", false
	}
	library, entry, ok := libraryimport.SplitRef(ref, "/workloads/")
	if !ok {
		return "", "", false
	}
	return library, entry, true
}

// WorkloadImportIsWellShaped reports a workload import whose reference does not name
// an entry the compiler can look for.
//
// The reference reaches this rule already read as a library import — it carries the
// `#/workloads/` fragment and no registry host — so what is left to judge is whether
// the two halves that fragment separates are both there. A reference naming an empty
// entry is the production shape of that fault: the fragment is present, the library
// is named, and there is nothing after the prefix for the library to be asked about.
//
// # What it deliberately does not judge
//
// A reference whose library half carries the retired `@` version separator splits
// cleanly and is not reported here. The resolution beneath refuses it with a steer to
// the colon form, and that refusal is reached on a path this rule does not stand in
// front of — the reference is resolved as a dependency before any render begins,
// under the condition that names an unusable library reference. Declaring a predicate
// here for a reference this stage is never asked about would put a rule in the
// enforced set that nothing can invoke, which reads exactly like a rule that ran and
// approved.
//
// The reference is the value the registered sentence is about, so it stands bare. The
// advice names the shape the reference has to take, because the remedy is a single
// edit and stating it is cheaper for the reader than describing it.
var WorkloadImportIsWellShaped = constraint.Check[
	constraint.DescribedView,
	constraint.NodeRecipeImportFieldSubject,
]{
	Sentinel: ErrWorkloadImportShape(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeRecipeImportFieldSubject,
	) []constraint.Objection {
		ref := v.RecipeWorkloadImportReference(s.NodeID(), s.RecipeImportField())
		if ref == "" {
			return nil
		}
		if _, _, held := workloadLibraryReference(ref); held {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrWorkloadImportShape(),
			"name the library and the entry either side of the fragment — write "+
				"<Library>#/workloads/<entry>",
			constraint.Uncaptioned(ref),
		)}
	},
}

// WorkloadImportNamesADeclaredEntry reports a workload import naming an entry the
// WorkloadLibrary it resolved to does not declare.
//
// It declines a field whose library was not found at all, because a library nothing
// declares declares no entry either — and the resolution that refuses an absent
// library is reached before any render begins, so a build meeting that fault never
// reaches this rule to be told about the entry instead.
//
// The entry is the value the registered sentence is about, so it stands bare; the
// library is captioned beside it because an entry name means nothing without the
// document it was looked for in, and the two are written by different authors often
// enough that the reader may have to open a library they did not publish.
var WorkloadImportNamesADeclaredEntry = constraint.Check[
	constraint.DescribedView,
	constraint.NodeRecipeImportFieldSubject,
]{
	Sentinel: ErrWorkloadEntryNotFound(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeRecipeImportFieldSubject,
	) []constraint.Objection {
		ref := v.RecipeWorkloadImportReference(s.NodeID(), s.RecipeImportField())
		library, entry, held := workloadLibraryReference(ref)
		if !held || !v.RecipeWorkloadLibraryDeclared(s.NodeID(), s.RecipeImportField()) {
			return nil
		}
		if v.RecipeWorkloadEntryDeclared(s.NodeID(), s.RecipeImportField()) {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrWorkloadEntryNotFound(),
			constraint.Uncaptioned(entry),
			constraint.Captioned("WorkloadLibrary", library),
		)}
	},
}

// ArtifactsEntryImportIsWellShaped reports an artifacts entry whose library
// import the compiler will not resolve for a reason in the reference or in the
// bundle's own bodies.
//
// It carries the two production violation classes of one condition rather than
// splitting them into two identities, because they are one rule: the reference an
// author wrote does not name a bundle this compiler can expand. Their remedies
// differ, which is why each occurrence carries its own advice rather than the
// condition carrying one registered sentence for both.
//
// # The retired version separator
//
// A library reference pins a version with a colon. A reference writing the
// separator the registry address form uses instead resolves to a library name
// nothing declares, so without this arm the build would report a missing library
// and send the author looking for a publication rather than at the character they
// typed. The corrected reference is spelled out in the advice, because the remedy
// is a single edit and stating it is cheaper for the reader than describing it.
//
// # A bundle file body that is itself a field-root import
//
// A bundle file body is terminal content: one file slot cannot expand into
// another bundle, since a bundle holds many files and a slot names one output. So
// a body that is itself a field-root import is refused rather than mis-resolved,
// and composition across libraries is done at the consumption site, where one key
// consumes one bundle.
//
// Every offending file is reported rather than the first, because an author
// republishing a bundle is better told about all of them than about whichever the
// walk reached first — and the fault is in the library's own document, which the
// consuming Recipe's author may not be the one to edit. The bundle and the file
// are both captioned for that reason: the finding spans two documents, and which
// of them each quoted name belongs to is not something a reader can recover from
// the strings.
var ArtifactsEntryImportIsWellShaped = constraint.Check[
	constraint.DescribedView,
	constraint.NodeArtifactsEntrySubject,
]{
	Sentinel: ErrArtifactImportShape(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeArtifactsEntrySubject,
	) []constraint.Objection {
		ref := v.ArtifactsEntryImportReference(s.NodeID(), s.ArtifactsEntryKey())
		library, bundle, held := artifactsEntryLibraryReference(ref)
		if !held {
			return nil
		}
		if strings.ContainsRune(library, '@') {
			return []constraint.Objection{s.RaiseWithAdvice(
				ErrArtifactImportShape(),
				fmt.Sprintf("pin the version with ':' rather than '@' — write %q",
					strings.Replace(ref, "@", ":", 1)),
				constraint.Uncaptioned(ref),
				constraint.Captioned("artifacts entry", s.ArtifactsEntryKey()),
			)}
		}
		var out []constraint.Objection
		for _, file := range v.ArtifactsEntryBundleNestedImportFiles(
			s.NodeID(), s.ArtifactsEntryKey(),
		) {
			out = append(out, s.RaiseWithAdvice(
				ErrArtifactImportShape(),
				"a bundle file body cannot import another bundle — compose the two at the "+
					"artifacts entry that consumes them",
				constraint.Uncaptioned(ref),
				constraint.Captioned("bundle", bundle),
				constraint.Captioned("bundle file", file),
				constraint.Captioned("artifacts entry", s.ArtifactsEntryKey()),
			))
		}
		return out
	},
}

// ArtifactsEntryNamesADeclaredLibrary reports an artifacts entry whose library
// import names an ArtifactLibrary the load did not resolve — neither in the
// local catalog nor among the libraries a lockfile pinned and the load bridged
// in.
//
// It declines an entry whose reference carries the retired version separator,
// because such a reference names a library nobody wrote: reporting it as a
// missing library would send the author looking for a publication when the rule
// beside this one already names the character to correct.
//
// The library the reference names is the value the registered sentence is about,
// so it stands bare. The bundle is captioned beside it because an author reading
// only the library has to go back to the document to see which of its entries
// asked for it, and the entry is captioned for the reason every rule over this
// kind captions it.
var ArtifactsEntryNamesADeclaredLibrary = constraint.Check[
	constraint.DescribedView,
	constraint.NodeArtifactsEntrySubject,
]{
	Sentinel: ErrArtifactLibraryNotFound(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeArtifactsEntrySubject,
	) []constraint.Objection {
		ref := v.ArtifactsEntryImportReference(s.NodeID(), s.ArtifactsEntryKey())
		library, bundle, held := artifactsEntryLibraryReference(ref)
		if !held || strings.ContainsRune(library, '@') {
			return nil
		}
		if v.ArtifactsEntryLibraryDeclared(s.NodeID(), s.ArtifactsEntryKey()) {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrArtifactLibraryNotFound(),
			constraint.Uncaptioned(library),
			constraint.Captioned("bundle", bundle),
			constraint.Captioned("artifacts entry", s.ArtifactsEntryKey()),
		)}
	},
}

// ArtifactsEntryNamesADeclaredBundle reports an artifacts entry whose library
// import names a bundle the ArtifactLibrary it resolved to does not declare.
//
// It declines an entry whose library was not found at all, because a library
// nothing declares declares no bundle either — the rule above names the library,
// which is the outer fault and the one to correct first.
//
// The bundle is the value the registered sentence is about, so it stands bare;
// the library is captioned beside it because a bundle name means nothing without
// the document it was looked for in, and the two are written by different authors
// often enough that the reader may have to open a library they did not publish.
var ArtifactsEntryNamesADeclaredBundle = constraint.Check[
	constraint.DescribedView,
	constraint.NodeArtifactsEntrySubject,
]{
	Sentinel: ErrArtifactLibraryEntryNotFound(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeArtifactsEntrySubject,
	) []constraint.Objection {
		ref := v.ArtifactsEntryImportReference(s.NodeID(), s.ArtifactsEntryKey())
		library, bundle, held := artifactsEntryLibraryReference(ref)
		if !held || !v.ArtifactsEntryLibraryDeclared(s.NodeID(), s.ArtifactsEntryKey()) {
			return nil
		}
		if v.ArtifactsEntryBundleDeclared(s.NodeID(), s.ArtifactsEntryKey()) {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrArtifactLibraryEntryNotFound(),
			constraint.Uncaptioned(bundle),
			constraint.Captioned("ArtifactLibrary", library),
			constraint.Captioned("artifacts entry", s.ArtifactsEntryKey()),
		)}
	},
}

// ArtifactsEntryBundleFilesStayUnderTheirKey reports a file of an imported bundle
// whose own name does not name a file of its own beneath the artifacts key its
// output is nested under.
//
// Each file of a bundle becomes an output named by the consuming key with the
// bundle's own filename beneath it, and the filename comes from a document the
// consuming Recipe did not write. So the name is judged before it is composed
// rather than after: once the two halves are joined, a climbing segment reads as
// an ordinary interior one and the composed name resolves somewhere the consuming
// Recipe never named. A name that stays under the key and still names no file of
// its own is reported too — "." or "x/.." names the key itself, and "a//b" or
// "a/b/" is a second spelling of "a/b" that no collision guard comparing names as
// written can see. The question is asked of the shared predicate the render walk
// asks, because an output name is a logical name rather than a path in the host's
// own spelling, and that predicate gives it the same verdict on every platform.
//
// A file written plainly beneath the key's directory is untouched, including one
// naming a subdirectory of its own: a bundle is free to carry a nested layout, and
// nesting that layout under the consumption key is exactly what the key is for.
//
// Every offending file is reported rather than the first, for the reason the
// shape rule reports every offending body: the remedy is an edit to the library's
// own document, and an author republishing it is better told about all of them at
// once.
//
// The file name is the value the registered sentence is about and stands bare.
// The entry is captioned, and so is the library: the finding spans two documents,
// the consuming Recipe and the published library, and which of them each quoted
// name belongs to is not recoverable from the strings.
var ArtifactsEntryBundleFilesStayUnderTheirKey = constraint.Check[
	constraint.DescribedView,
	constraint.NodeArtifactsEntrySubject,
]{
	Sentinel: ErrArtifactLibraryFileEscapesKey(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeArtifactsEntrySubject,
	) []constraint.Objection {
		ref := v.ArtifactsEntryImportReference(s.NodeID(), s.ArtifactsEntryKey())
		library, _, held := artifactsEntryLibraryReference(ref)
		if !held || !v.ArtifactsEntryBundleDeclared(s.NodeID(), s.ArtifactsEntryKey()) {
			return nil
		}
		var out []constraint.Objection
		for _, file := range v.ArtifactsEntryBundleFileNames(s.NodeID(), s.ArtifactsEntryKey()) {
			if pathscope.NamesFileBeneath(file) {
				continue
			}
			out = append(out, s.RaiseWithAdvice(
				ErrArtifactLibraryFileEscapesKey(),
				artifactLibraryFileNameAdvice,
				constraint.Uncaptioned(file),
				constraint.Captioned("ArtifactLibrary", library),
				constraint.Captioned("artifacts entry", s.ArtifactsEntryKey()),
			))
		}
		return out
	},
}

// emittedNameCollisionObjections is the predicate all three emitted-name collision
// values share: an output name more than one of a render's blocks produced is
// refused, and a name one block produced is not.
//
// It is one function because it is one rule. The three values differ only in which
// owner's fact they read and which document they name, so a second copy of the
// predicate would agree on the day it was written and be free to drift the moment
// the condition gains a term — and a drift here would let one owner refuse an
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

// AssembledPathWrittenByOneContributor reports each output path that more than one
// contributor to an assembly placed a file at.
//
// The components a dispatch group holds, the features attached to a boundary it
// serves, and the assembly's own merged output each choose the names they write
// under independently of one another, and nothing declares those names distinct. Two
// of them meeting on one path means one of the two files is not in the output the
// author expected, and which one survives is a property of the order the contributing
// maps happen to iterate in rather than of anything the author wrote — so the build
// halts and names both contributors instead of shipping whichever won.
//
// It is a sibling of the rule refusing two blocks of one document meeting on one
// name, not a widening of it, and the two ask about different things. That one is
// about a single document's own emission, under the names that document chose,
// before anything is grouped; this one is about several independently authored
// contributors meeting once a group has placed their output — under paths an
// assembly may have nested, so two contributors emitting one filename that an
// assembly keeps apart are not reported here.
//
// # It is keyed by the GROUP, and that is what makes one collision one finding
//
// A collision at an output path is a property of the group that wrote it. Keyed
// per boundary instead — which reads as the friendlier arrangement, since a
// boundary is a place an author can open — the rule is invoked once for every
// boundary that fed the group, and the facts answer each of those invocations
// identically because the paths and the contributors belong to the group and not
// to any boundary of it. One collision then reports as several findings that say
// the same thing, and the count depends on how many boundaries happened to
// collapse into one group rather than on anything about the fault.
//
// The sharper half is the POSITION. Several boundaries reach one group exactly
// when their own path declarations evaluate alike, and nothing says the
// contributors that met sat in all of them — so a per-boundary keying positions a
// finding at every boundary of the group, including boundaries whose components
// contributed nothing to the colliding path. Such a finding sends its reader to a
// document that is not the problem, which is worse than a finding with no position
// at all. Keyed here the group has no single authored site to name, so the
// Release document is the position and the boundaries that fed the group are
// named in the detail: every site an operator could act on, and no claim that one
// of them is at fault.
//
// 🔴 The key is a REPORTING decision and not a detection one. What collides is
// unchanged: the same pairs of contributors on the same output paths are refused,
// and nothing about the group keying widens or narrows the set of inputs that
// halt. The predicate below is the same comparison it always was — more than one
// contributor at one path — so a reader who takes the per-boundary reporting as
// evidence that the check was wrong will change what the build detects while
// trying to change what it says.
//
// # What it holds and what it reads
//
// It holds a walk over the group's own output paths, because the subject names the
// group rather than one path — the same shape the derived-name rule at the stage
// before this has, and for the same reason. It reads two facts: the paths the
// group wrote, which is what there is to ask about, and the contributors at one of
// them, which is the predicate. The boundaries are a third fact, read only to be
// reported.
//
// Its detail names the path, the contributors that met — joined into one value in
// the order the fact answered, which is producer order — and the boundaries that
// fed the group. All three are a function of the build rather than of the order
// the placements arrived in.
var AssembledPathWrittenByOneContributor = constraint.Check[
	constraint.RenderedView,
	constraint.AssemblyPathProviderSubject,
]{
	Sentinel: ErrAssembledOutputCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.RenderedView,
		s constraint.AssemblyPathProviderSubject,
	) []constraint.Objection {
		var out []constraint.Objection
		for _, path := range v.EmittedFilePathSet(s.AssemblyPath(), s.ProviderName()) {
			producers := v.AssembledFileProducerSet(s.AssemblyPath(), s.ProviderName(), path)
			if len(producers) < 2 {
				continue
			}
			out = append(out, s.RaiseWith(
				ErrAssembledOutputCollision(),
				constraint.Captioned("output path", path),
				constraint.Captioned("written by", joinWithAnd(producers)),
				constraint.Captioned("assembled within", joinWithAnd(
					v.AssemblyGroupBoundarySet(s.AssemblyPath(), s.ProviderName()))),
			))
		}
		return out
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
// renders no host documents, so a host-addressed step there is not reported
// here. No declared rule owns that site's missing document set; the condition
// stays with the pipeline walk, which halts when a step addresses host documents
// the site stands against none of.
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
// the documents instead. Whether the attachment's own output joins that merge
// is a separate question with its own answer — it joins where the attachment
// rendered for the same target the group assembles for — and this condition
// does not turn on it: the deferral is keyed on the host's provider alone, so
// an attachment on a merging host has its pipeline held back either way. A
// step addressing the attachment's own documents therefore has
// nothing to address while the component renders, which is the only point an
// attachment's own scope is ever offered: the deferral to the post-assembly
// phase carries the host scope alone, because that is the scope whose documents
// the deferred replay is given.
//
// So the finding is a property of the component's provider paired with the
// step's own scope, and the attachment's provider does not enter it. That is
// what makes the condition readable here: both halves are declared facts at the
// point every plugin and provider has described itself.
//
// The remedies are the ones the condition admits: put the component on a
// provider that does not merge, address the component's documents instead, or
// drop the step. Changing the attachment's own provider is not among them, and
// naming it would send an author to edit the one declaration that decides
// nothing here.
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

// AttachmentBoundaryScopeHasADeclaringBoundary reports each attachment a node
// carries whose FeatureRecipe generates once for a boundary, where no boundary
// holding that node declares an attachment of the same name.
//
// # The fault it names, and the one it replaces
//
// A FeatureRecipe declaring the boundary scope generates once for the boundary
// that holds its attachment, however many components that boundary holds. So the
// boundary is the site the attachment has to be written at: an attachment of
// that name written only against a component names no scope for the recipe to
// generate for, and nothing decides which boundary it would run at, which values
// it would read, or where its output would be written. The build already refuses
// such a configuration, but it refuses it at the point the render reaches for a
// boundary attachment and finds none — which names a missing projection rather
// than the authoring mistake that produced it.
//
// # Why it walks the ancestors instead of reading one path
//
// An attachment written at a boundary reaches every component that boundary
// holds, including the components of the scopes nested inside it, and it appears
// in every one of their merged attachment sets — which is what this rule is
// invoked over. So a component carrying the attachment is the ORDINARY shape,
// and a rule refusing that would reject the very configuration boundary
// attachments exist for. What separates the two is whether the attachment is
// declared at the component's own scope or at one enclosing it, so the rule
// walks from the node's placement toward the root and ends the moment a scope
// declares the name.
//
// A component may also restate at itself an attachment its enclosing scope
// already declares. That renders exactly as the scope-only form, so the walk
// admits it for the same reason: the scope's declaration is what the build
// generates from either way.
//
// The walk is the one the render's own dispatch uses to find the scope a
// boundary-scope recipe generates for, so a coordinate this rule admits is a
// coordinate that dispatch can resolve. Two walks would be free to differ, and
// the difference would read as approval for a build that then halts.
//
// # Emptiness
//
// A component the Release placed in no scope has no ancestor to walk, so a
// boundary-scope attachment on it is reported here. That is the honest reading:
// there is no scope such a recipe could generate for, which is the same fault
// with the walk finding nothing rather than finding scopes that declare nothing.
//
// Its detail names the attachment, which the rule's registered sentence refers
// to, and the scope the component sits in, captioned — the site an author writes
// the declaration at. A component in no scope contributes no such value, so the
// detail carries the attachment alone rather than an empty path.
var AttachmentBoundaryScopeHasADeclaringBoundary = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrBoundaryScopeAttachmentNotDeclaredAtABoundary(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentSubject,
	) []constraint.Objection {
		if v.AttachmentExecutionScope(s.NodeID(), s.AttachmentName()) != executionScopeBoundary {
			return nil
		}
		placement, _ := v.NodeBoundaryPlacement(s.NodeID())
		if _, declared := declaringAncestorBoundary(placement, func(candidate string) bool {
			_, written := v.BoundaryAttachmentDeclaration(candidate, s.AttachmentName())
			return written
		}); declared {
			return nil
		}
		detail := []constraint.DetailValue{constraint.Uncaptioned(s.AttachmentName())}
		if placement != "" {
			detail = append(detail, constraint.Captioned("on a component in boundary", placement))
		}
		return []constraint.Objection{s.RaiseWith(
			ErrBoundaryScopeAttachmentNotDeclaredAtABoundary(), detail...,
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
// # Why the execution scope is read and not assumed
//
// The axis this walks is every attachment a boundary DECLARES, which is a
// separate question from what scope the FeatureRecipe behind one generates at.
// An attachment written at a boundary whose FeatureRecipe states no scope
// generates once per component the boundary holds, and it appears in every one
// of those components' merged attachment sets — so the node-rooted rule beside
// this one already asks this same question of that authored step, once for each
// of them. Reading the scope is what keeps one authored step from being reported
// once per component AND again here.
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
		if v.BoundaryAttachmentExecutionScope(
			s.BoundaryPath(), s.AttachmentName(),
		) != executionScopeBoundary {
			return nil
		}
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
// addresses nothing. That follows from the scope the FeatureRecipe declares,
// which is settled long before any node is walked.
//
// # Why the execution scope is read and not assumed
//
// The reasoning above holds for a FeatureRecipe that generates once for the
// boundary, and for no other. Declaring an attachment ON a boundary and
// declaring a FeatureRecipe that runs ONCE PER boundary are two separate
// decisions: an attachment written at a boundary whose FeatureRecipe states no
// scope generates once for each component the boundary holds, and each of those
// generations stands against that component — which is the host its pipeline
// addresses. Such an attachment has a host set at every site it runs at, so the
// condition below does not hold for it and the rule ends before reading
// anything further. Refusing it would reject the boundary block's own purpose,
// which is to reach every component nested inside the boundary, on the strength
// of where the operator happened to write the attachment.
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
		if v.BoundaryAttachmentExecutionScope(
			s.BoundaryPath(), s.AttachmentName(),
		) != executionScopeBoundary {
			return nil
		}
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

// unsupportedOmitsAdvice is what a reader of the omits refusal can do about it,
// whichever document declared the entries. It is the raise's advice rather than
// the condition's sentence because the sentence states the condition, and the
// three rules below raising it share the remedy.
const unsupportedOmitsAdvice = "this provider's contract does not declare that it leaves rendered documents out; " +
	"remove the omits key, or render the source through a provider whose contract declares it"

// OmitsProviderHonoursTheEntries reports a node whose Recipe declares omit entries
// while the provider rendering it does not declare that it honours them.
//
// Leaving a rendered document out is something only the provider can do: it alone
// knows what its source renders and in which terms a document is named, so the
// entries are handed to it and it does the leaving out. A provider that does not
// would render every document the author asked to leave out and report success, so
// the pairing is refused here, before anything renders. Which providers honour the
// entries is each provider's own declared claim, read as a fact; nothing here holds
// a list of providers, so a provider that comes to honour them stops being refused
// by declaring so.
//
// # What it reads, and in which order
//
// Whether the Recipe declares entries comes first, and the claim is read only for a
// Recipe that does — a node declaring none never has its provider asked. Any entry
// counts, whatever its gate would evaluate to: whether the provider can honour the
// list does not depend on which entries one build keeps.
//
// The claim's second result is read before its first. A binding that resolves to no
// provider has no claim at all, and that failure is reported where the render
// reaches for the provider, naming the binding; refusing it here would report a
// provider declining the key in place of a provider that could not be found.
//
// Its detail leads with the provider, which the registered wording is about, and
// names the node, captioned.
var OmitsProviderHonoursTheEntries = constraint.Check[
	constraint.DescribedView,
	constraint.NodeSubject,
]{
	Sentinel: ErrUnsupportedOmits(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeSubject,
	) []constraint.Objection {
		if !v.NodeDeclaresOmits(s.NodeID()) {
			return nil
		}
		honours, resolved := v.ProviderHonoursOmits(s.NodeID())
		if !resolved || honours {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrUnsupportedOmits(), unsupportedOmitsAdvice,
			constraint.Uncaptioned(v.NodeRenderingProvider(s.NodeID())),
			constraint.Captioned("node", s.NodeID()),
		)}
	},
}

// AttachmentOmitsProviderHonoursTheEntries reports an attachment on a node whose
// FeatureRecipe declares omit entries while the provider rendering that
// FeatureRecipe does not declare that it honours them.
//
// It is the node rule's question asked of the attachment's own document: the
// FeatureRecipe declares its own capability and renders its own source, so its
// entries name documents only its own provider renders, and that provider's claim
// is the one that decides. It reads in the order the node rule does, for the same
// reasons.
//
// # The attachments this rule is not about
//
// An attachment whose FeatureRecipe generates once for a boundary renders at the
// boundary that declares it, not once per component carrying it, so the rule over
// a boundary's attachments asks this question of it there — once, where it renders.
// Asking it here as well would report one authored list once per component the
// boundary holds and again at the boundary. Every other attachment renders once per
// component carrying it, which is exactly the set this rule is invoked over.
//
// Its detail leads with the provider and names the node and the attachment,
// captioned.
var AttachmentOmitsProviderHonoursTheEntries = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrUnsupportedOmits(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentSubject,
	) []constraint.Objection {
		if v.AttachmentExecutionScope(s.NodeID(), s.AttachmentName()) == executionScopeBoundary {
			return nil
		}
		if !v.AttachmentDeclaresOmits(s.NodeID(), s.AttachmentName()) {
			return nil
		}
		honours, resolved := v.AttachmentProviderHonoursOmits(s.NodeID(), s.AttachmentName())
		if !resolved || honours {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrUnsupportedOmits(), unsupportedOmitsAdvice,
			constraint.Uncaptioned(v.AttachmentRenderingProvider(s.NodeID(), s.AttachmentName())),
			constraint.Captioned("node", s.NodeID()),
			constraint.Captioned("attachment", s.AttachmentName()),
		)}
	},
}

// BoundaryAttachmentOmitsProviderHonoursTheEntries reports an attachment declared
// on a boundary whose FeatureRecipe generates once for that boundary and declares
// omit entries, while the provider rendering it does not declare that it honours
// them.
//
// It is the node rule's question asked of the document that renders once for the
// boundary, read in the same order and for the same reasons. An attachment written
// at a boundary whose FeatureRecipe generates per component is not asked here: it
// renders once for each component the boundary holds, and the rule over a node's
// attachments asks it there.
//
// Its detail leads with the provider and names the boundary and the attachment,
// captioned. It names no node, because such an attachment renders against none.
var BoundaryAttachmentOmitsProviderHonoursTheEntries = constraint.Check[
	constraint.DescribedView,
	constraint.BoundaryAttachmentSubject,
]{
	Sentinel: ErrUnsupportedOmits(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.BoundaryAttachmentSubject,
	) []constraint.Objection {
		if v.BoundaryAttachmentExecutionScope(s.BoundaryPath(), s.AttachmentName()) != executionScopeBoundary {
			return nil
		}
		if !v.BoundaryAttachmentDeclaresOmits(s.BoundaryPath(), s.AttachmentName()) {
			return nil
		}
		honours, resolved := v.BoundaryAttachmentProviderHonoursOmits(s.BoundaryPath(), s.AttachmentName())
		if !resolved || honours {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrUnsupportedOmits(), unsupportedOmitsAdvice,
			constraint.Uncaptioned(v.BoundaryAttachmentRenderingProvider(s.BoundaryPath(), s.AttachmentName())),
			constraint.Captioned("boundary", s.BoundaryPath()),
			constraint.Captioned("attachment", s.AttachmentName()),
		)}
	},
}

// BoundaryArtifactsProviderAcceptsTheFiles reports a node whose Recipe declares
// files for its boundary's shared output while the provider rendering it does not
// declare that it accepts them.
//
// The host writes such a file itself, once, in the directory holding the dispatch
// group's assembled output, and hands the provider none of them — so whether the
// file means anything is a property of the provider's target, which only the
// provider can state. A provider whose target does not read a file placed there
// would ship it and ignore it, and the author would learn only once the Release was
// applied that the configuration never took effect, so the pairing is refused here,
// before anything renders. Which providers accept the files is each provider's own
// declared claim, read as a fact; nothing here holds a list of providers.
//
// # What it reads, and in which order
//
// Whether the Recipe declares the block comes first, and the claim is read only
// for a Recipe that does. Any entry counts, whatever it would evaluate to: whether
// the provider accepts the block does not depend on what one build evaluates it to.
// The claim's second result is read before its first, for the reason the omits
// rules beside it give: a binding that resolves to no provider is reported where
// the render reaches for the provider, naming the binding.
//
// It is positioned at the block in the Recipe, where the edit is, and its detail
// leads with the provider, which the registered wording is about, and names the
// node, captioned, so a Recipe bound by several components says which one met a
// provider declining the files.
var BoundaryArtifactsProviderAcceptsTheFiles = constraint.Check[
	constraint.DescribedView,
	constraint.NodeRecipeBoundaryBlockSubject,
]{
	Sentinel: ErrUnsupportedBoundaryArtifacts(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeRecipeBoundaryBlockSubject,
	) []constraint.Objection {
		if !v.RecipeBoundaryArtifactsDeclared(s.NodeID(), s.RecipeBoundaryBlock()) {
			return nil
		}
		accepts, resolved := v.ProviderAcceptsBoundaryArtifacts(s.NodeID())
		if !resolved || accepts {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrUnsupportedBoundaryArtifacts(),
			"this provider's contract does not declare that it accepts files written to a boundary's shared "+
				"output; remove the boundary block, or render the Recipe through a provider whose contract declares it",
			constraint.Uncaptioned(v.NodeRenderingProvider(s.NodeID())),
			constraint.Captioned("node", s.NodeID()),
		)}
	},
}

// The rule that one container of a Workload names each environment variable once,
// declared once per document that authors a Workload.
//
// A container receives its environment from two lists its author writes: the
// plain `variables`, and the `attachments` entries delivered `as: env`, whose key
// is the name such an entry sets. A provider synthesizing the Workload writes the
// two kinds in an order of its own — the helm and compose providers write the
// attachment-read entries first and the plain variables after them — so one name
// written in both lists would reach the process with whichever value that order
// lets win. That outcome follows from where the two kinds land rather than from
// anything the author wrote, so the pair is refused before any provider is handed
// the Workload: once, on the host, whichever provider renders it.
//
// One rule, three values, on the reading the artifact-pillar family states: a
// node's own Recipe, the FeatureRecipe an attachment binds on a node and the
// FeatureRecipe a boundary-scope attachment binds are three documents, each
// authoring its own Workload, and each value reads the facts keyed by its own
// owner. The predicate has ONE body, [workloadEnvironmentObjections], so the
// comparison and the wording exist once.
//
// # What it reads, and what it cannot see
//
// It reads names and never a value: each container, its plain variable names, and
// the names of its attachments entries delivered as env, from the Workload as
// parsed, after the entries naming a connection were folded onto the slot that
// connection's attachment carries — a fold that never moves a key. An attachments
// entry delivered as a file is mounted rather than set in the environment, so it
// never forms a pair. Two entries written under complementary template conditions
// never reach it either, because it reads the rendered Workload, in which only one
// of the two exists.
//
// Two sources of environment entries are outside what it reads, and both are
// bounds rather than oversights. A container's `envFrom` entries set every key of
// the ConfigMap or Secret they name, and those keys are not known when the
// Workload is compiled, so no `envFrom` key is compared. And an entry a provider
// or a pipeline step writes into a rendered document after the Workload is
// synthesized is not part of the Workload this rule reads.
//
// # Where a finding is positioned
//
// Each value is invoked over its owner's workload template field alone, so a
// finding is positioned at the Workload key of the document that declares it —
// the position every other refusal about a Workload carries — whichever form the
// Workload took there, written in place or imported. The rendered Workload is not
// a text an author wrote line for line, so the container and the variable are
// named in the detail rather than located.

// workloadEnvironmentAdvice is what a reader of the refusal can do about it. It is
// the raise's advice rather than part of the registered sentence because the
// sentence states the condition, and the three values raising it share the remedy.
const workloadEnvironmentAdvice = "Rename one of the two, or write them under complementary " +
	"template conditions so that only one of them renders"

// workloadEnvironmentObjections is the one body of the rule: for each container,
// in the order the fact answers them, each attachments entry delivered as env
// whose name is also one of that container's plain variables raises one
// objection, in the order the attachments entries are authored.
//
// The two name lists are compared within one container and never across two:
// every container is an environment of its own, so one name in two containers is
// not a pair.
//
// Its detail leaves the variable bare, because the registered sentence already
// says an environment variable is what the finding is about, and captions the
// container, which the position cannot name. ownerDetail is what the calling value
// adds after the container — the attachment, where the Workload belongs to one —
// and is positional rather than variadic so a value cannot omit it silently.
func workloadEnvironmentObjections(
	s constraint.Subject,
	containers []constraint.ContainerEnvironmentNames,
	ownerDetail []constraint.DetailValue,
) []constraint.Objection {
	var out []constraint.Objection
	for _, c := range containers {
		for _, name := range c.EnvAttachments {
			if !slices.Contains(c.Variables, name) {
				continue
			}
			detail := []constraint.DetailValue{
				constraint.Uncaptioned(name),
				workloadContainerDetail(c),
			}
			out = append(out, s.RaiseWithAdvice(
				validator.ErrDuplicateContainerEnvironmentVariable(), workloadEnvironmentAdvice,
				append(detail, ownerDetail...)...,
			))
		}
	}
	return out
}

// workloadContainerDetail names one container the way the rule's detail does: a
// main container by the key it is written under, and an init container by its
// place in the list, spelled the way that list is indexed.
func workloadContainerDetail(c constraint.ContainerEnvironmentNames) constraint.DetailValue {
	if c.Init {
		return constraint.Captioned("init container", fmt.Sprintf("initContainers[%d]", c.Position))
	}
	return constraint.Captioned("container", c.Name)
}

// WorkloadEnvironmentVariableNamedOnce reports each environment variable one
// container of the Workload a node's own Recipe renders names twice — as a plain
// variable and as an attachments entry delivered as env.
//
// It acts on the Recipe's workload field and on no other: invoked over any other
// template field the Recipe writes, it raises nothing, so the rule is asked about
// the Workload once whatever axis it is handed.
//
// Its facts are keyed by the node alone, which is the owner of the Workload a
// node's own render parses; a FeatureRecipe's Workload is answered by the two
// values below, keyed by their own owner.
var WorkloadEnvironmentVariableNamedOnce = constraint.Check[
	constraint.HydratedView,
	constraint.NodeTemplateFieldSubject,
]{
	Sentinel: validator.ErrDuplicateContainerEnvironmentVariable(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeTemplateFieldSubject,
	) []constraint.Objection {
		if s.TemplateField() != recipeFieldWorkload {
			return nil
		}
		return workloadEnvironmentObjections(s, v.ParsedNodeWorkloadEnvironmentNames(s.NodeID()), nil)
	},
}

// AttachmentWorkloadEnvironmentVariableNamedOnce reports each environment
// variable one container of the Workload the FeatureRecipe one attachment binds
// on a node renders names twice.
//
// It is the node rule's question asked of the attachment's own document. Its facts
// are keyed by the attachment as well as by the node, because the attachment's
// render runs under the host component's node identity over a Workload its
// FeatureRecipe declares: a node-keyed answer would be the host's containers.
//
// Its detail names the attachment after the container, captioned, because the
// position names the Workload key inside the FeatureRecipe, and a node may carry
// several attachments whose FeatureRecipes are separate documents.
var AttachmentWorkloadEnvironmentVariableNamedOnce = constraint.Check[
	constraint.HydratedView,
	constraint.NodeAttachmentTemplateFieldSubject,
]{
	Sentinel: validator.ErrDuplicateContainerEnvironmentVariable(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeAttachmentTemplateFieldSubject,
	) []constraint.Objection {
		if s.TemplateField() != recipeFieldWorkload {
			return nil
		}
		return workloadEnvironmentObjections(s,
			v.AttachmentParsedWorkloadEnvironmentNames(s.NodeID(), s.AttachmentName()),
			[]constraint.DetailValue{constraint.Captioned("attachment", s.AttachmentName())},
		)
	},
}

// BoundaryAttachmentWorkloadEnvironmentVariableNamedOnce reports each environment
// variable one container of the Workload the FeatureRecipe one boundary-scope
// attachment binds renders names twice.
//
// It is the same question the two values above ask, rooted where such an
// attachment is declared: it is written once on a boundary and renders once for
// it, so its facts are keyed by the boundary rather than by any component. Its
// detail names the attachment, on the reading the node-carried form follows.
var BoundaryAttachmentWorkloadEnvironmentVariableNamedOnce = constraint.Check[
	constraint.HydratedView,
	constraint.BoundaryAttachmentTemplateFieldSubject,
]{
	Sentinel: validator.ErrDuplicateContainerEnvironmentVariable(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.BoundaryAttachmentTemplateFieldSubject,
	) []constraint.Objection {
		if s.TemplateField() != recipeFieldWorkload {
			return nil
		}
		return workloadEnvironmentObjections(s,
			v.BoundaryAttachmentParsedWorkloadEnvironmentNames(s.BoundaryPath(), s.AttachmentName()),
			[]constraint.DetailValue{constraint.Captioned("attachment", s.AttachmentName())},
		)
	},
}
