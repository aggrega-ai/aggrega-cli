// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// maxInheritanceDepth bounds how far an extends chain may run. It is a guard on
// authoring rather than a termination safeguard: the walk below terminates on
// its own, because every step records the entity it reached and stops the moment
// it reaches one twice.
//
// It is read by the rules over a chain's length rather than by the walk, which
// is what keeps one bound governing every authored kind that declares extends.
const maxInheritanceDepth = 10

// ExtendsChainBound returns the most entities an extends chain may reach,
// counting the entity it starts from: a longer chain is refused. It is the bound
// the chain rules enforce, read by a pass that follows chains for another purpose
// so that it stops where they stop, with one value governing both.
func ExtendsChainBound() int { return maxInheritanceDepth }

// chainFaultKind names which of the two chain-structural failures a walk stops
// on. Each carries its own condition and its own remedy, so they are
// distinguished at the point of detection rather than at the point of
// reporting.
//
// The chain's LENGTH is not among them, and the absence is the design rather
// than an omission: a chain running past the bound is judged by a declared rule
// over the shape this walk produces, so the walk measures the length and stops
// judging it. What remains here are the two shapes that stop the walk from
// producing a shape at all — one of which is itself a declared rule, and is
// carried out of the walk as a fact rather than as a diagnostic.
type chainFaultKind int

const (
	// chainFaultCycle: following the chain reached an entity already in it.
	chainFaultCycle chainFaultKind = iota
	// chainFaultMissingParent: a link named an entity the lookup cannot serve.
	chainFaultMissingParent
)

// chainFault is what a stopped chain walk reports: which failure, and the
// coordinates a reader needs to act on it.
//
// from and link are the two halves of one edge — the entity that declares the
// offending extends, and the reference it declares. Naming the edge rather than
// one endpoint is what makes a cycle actionable: the entity a resolution was
// requested for is rarely the one carrying the link that closes the loop, so a
// diagnostic naming only the former tells an author to look in the wrong file.
type chainFault struct {
	kind chainFaultKind
	// from is the entity whose extends declaration is at fault.
	from string
	// link is the extends reference declared on from.
	link string
}

// ExtendsChainSpec carries everything an extends chain walk needs that differs
// between the kinds that declare extends. The walk itself — the cycle rule, the
// depth bound, and the order the two are decided in — is the same for all of
// them and lives in [ResolveExtendsChain].
//
// Key and Name are separate because they answer different questions, and for
// the kinds an author addresses by name the two answers coincide. Key decides
// what makes two links the SAME link; Name decides what a diagnostic CALLS a
// link. A Manifest is the case that separates them: two Manifest files may
// legally declare one metadata.name, so a chain that folded them into one link
// would report a loop that is not there — while a diagnostic naming the file
// would hand the author a path the machine composed rather than the name they
// wrote.
type ExtendsChainSpec[T any] struct {
	// Key reports what makes two links the same link. Reaching a key already
	// in the chain is what a cycle is.
	Key func(T) string
	// Name reports what a diagnostic calls a link. Optional: nil means Key
	// answers for both, which is right wherever a link's identity is the name
	// its author wrote.
	Name func(T) string
	// ParentLink reports the reference a link extends. An empty reference ends
	// the chain.
	ParentLink func(T) string
	// Lookup resolves a reference to the link it names, reporting false when
	// nothing answers to it. A lookup that knows WHY a reference did not
	// resolve records that itself and reads it back in MissingParent, which is
	// where the reason reaches the reader.
	Lookup func(string) (T, bool)
	// MissingParent supplies the two halves of the unresolvable-parent
	// diagnostic that depend on where this kind's references resolve: what the
	// reference failed to name, and where the author should look instead.
	// Optional: nil means the catalog wording, which is right for every kind
	// whose extends addresses a catalog entity.
	MissingParent func(from, link string) (constraint, suggestion string)
}

// name reports what a diagnostic calls v, falling back to the chain-membership
// key where the spec declares no separate name.
func (s ExtendsChainSpec[T]) name(v T) string {
	if s.Name != nil {
		return s.Name(v)
	}
	return s.Key(v)
}

// ResolveExtendsChain follows an extends chain from start and returns it in
// walk order — start first, then its parent, then its parent's parent — or the
// failure for the one structural fault that stopped it, reported against start.
//
// It is the single home for the extends walk and for the three conditions that
// walk can produce. Every path that follows an extends chain calls it, so the
// cycle rule, the depth bound, the identity the visited set is keyed on and the
// words each failure is reported in are decided once rather than per kind.
//
// # Two of the three conditions are declared rules, and one is not
//
// A chain that returns to an entity already in it, and one that ends cleanly
// past the depth bound, are each a declared rule over a declared fact — see
// componentExtendsChainReachesNoEntityTwice and
// componentExtendsChainStaysWithinTheDepthBound, together with the document kinds each
// other authored kind that declares extends supplies. What this function does for
// those two is present the shape
// its own walk produced and render whatever came back, exactly as every other
// path in this package that invokes a rule does.
//
// A link naming an entity the lookup cannot serve stays a raise here, because
// there is no shape to present: the walk cannot reach the entity, so nothing it
// could hand a rule would describe the chain the author wrote.
//
// kind selects which document kind's declarations are asked, and a kind none of them
// covers is refused rather than passed. Silently passing would leave a whole
// authored kind's chains ungoverned by two rules that every other kind's are
// held to, with nothing in the build saying so.
//
// # Why the visited set is keyed on the RESOLVED link
//
// A parent link and a link's own key are written in two different vocabularies:
// an author may pin a version in the link (`core/base:1.0.0`) while the entity
// names itself without one (`core/base`), and a Manifest's reference may be a
// relative path to a file the Manifest names differently again. Keying the
// visited set on the reference text would therefore compare values that never
// match, and the walk would take a full extra lap around a cycle before
// recognising it — which both delays detection past the depth bound and names
// the wrong edge when it does fire. So the walk resolves each reference first
// and asks whether the link it reached is already in the chain, which is one
// question in one vocabulary.
//
// # Why the depth bound is applied AFTER the walk
//
// The two rules answer different questions and must not preempt each other.
// Testing the bound inside the loop lets a long cycle trip it before it closes,
// and the author is then told to shorten a chain whose length is not the problem
// — advice that cannot fix their files. Because the visited set bounds the walk
// on its own, the length can be judged once the shape is known: a chain that
// closes on itself is a cycle at any length, and only a chain that ends cleanly
// can be too deep.
//
// That ordering is now carried by the facts rather than by this sequence. A
// chain closing on itself reports no length at all, so the rule over the bound
// has no subject on exactly the input the cycle rule raises for — and the two
// may be asked in either order without the ordering being something a later
// reader has to know to preserve.
func ResolveExtendsChain[T any](kind envelope.Kind, start T, spec ExtendsChainSpec[T]) ([]T, *ValidationError) {
	chain, fault := walkExtendsChain(start, spec)
	if fault != nil && fault.kind == chainFaultMissingParent {
		return nil, unresolvableParentError(kind, spec.name(start), fault, spec.MissingParent)
	}

	shape := extendsChainShape{reached: len(chain)}
	if fault != nil {
		shape = extendsChainShape{
			cycle: constraint.ExtendsChainCycleEdge{
				Declaring: fault.from,
				Reference: fault.link,
			},
			closes: true,
		}
	}
	if failure := extendsChainShapeFailure(kind, spec.name(start), shape); failure != nil {
		return nil, failure
	}
	return chain, nil
}

// extendsChainShapeFailure runs the two rules over a chain's shape and renders
// whatever they raised into the failure shape this package's callers read.
//
// It picks the declarations for the kind the chain was walked for, because a
// fact is filed under exactly one authored document kind: a rule reading a
// Component's chain facts off a Manifest's presentation would be refused at the
// point it read them, so the kind chooses the pair rather than the presentation
// being made to answer for all of them.
//
// Both rules are invoked rather than the first raising one short-circuiting the
// second, and nothing about the order is load-bearing: a chain closing on itself
// has no length to report, so the depth rule has no subject on exactly the input
// the cycle rule raises for. That exclusion lives in the fact, which is what lets
// this read as two rules asked about one shape instead of a sequence a later
// reader has to preserve.
//
// It returns the first failure rather than the set, because the walk returns one
// and because at most one can be raised: the two rules' subjects do not overlap.
//
// It panics on a kind that declares no chain facts, for the reason the failure
// constructors in this package panic on an unmappable kind: the kind reaches here
// as a constant chosen at the call site, never from an authored document, so an
// unrecognised one means a caller began walking a new kind's chains without
// declaring that kind's facts — and answering nil would leave those chains
// ungoverned in silence.
func extendsChainShapeFailure(
	kind envelope.Kind,
	name string,
	shape extendsChainShape,
) *ValidationError {
	var objections []constraint.Objection
	switch kind {
	case envelope.KindComponent:
		// The presentation is held as the view the rule declared rather than as
		// its own concrete type, because the entry takes the check and the view
		// together and infers one type from both. Handing over the concrete type
		// would infer that type and then refuse the rule, which declares the view.
		presented := newComponentExtendsChainShapeView(shape)
		var view constraint.DocumentView = presented
		objections = append(objections,
			constraint.RunOnDocument("", view, componentExtendsChainReachesNoEntityTwice)...)
		objections = append(objections,
			constraint.RunOnDocument("", view, componentExtendsChainStaysWithinTheDepthBound)...)
	case envelope.KindFeature:
		presented := newFeatureExtendsChainShapeView(shape)
		var view constraint.DocumentView = presented
		objections = append(objections,
			constraint.RunOnDocument("", view, featureExtendsChainReachesNoEntityTwice)...)
		objections = append(objections,
			constraint.RunOnDocument("", view, featureExtendsChainStaysWithinTheDepthBound)...)
	case envelope.KindManifest:
		presented := newManifestChainDocumentView(shape)
		var view constraint.DocumentView = presented
		objections = append(objections,
			constraint.RunOnDocument("", view, manifestExtendsChainReachesNoEntityTwice)...)
		objections = append(objections,
			constraint.RunOnDocument("", view, manifestExtendsChainStaysWithinTheDepthBound)...)
	case envelope.KindRelease:
		presented := newReleaseChainDocumentView(shape)
		var view constraint.DocumentView = presented
		objections = append(objections,
			constraint.RunOnDocument("", view, releaseExtendsChainReachesNoEntityTwice)...)
		objections = append(objections,
			constraint.RunOnDocument("", view, releaseExtendsChainStaysWithinTheDepthBound)...)
	default:
		panic(fmt.Sprintf(
			"validator: entity kind %q declares no extends-chain facts, so a chain walked for it "+
				"cannot be judged; declare that kind's chain facts, or walk the chain for a kind "+
				"that carries them", kind))
	}

	failures := shapeRuleErrorsFrom(objections, ruleSubject{
		Kind: kind,
		Name: name,
		Rule: "extends",
	})
	if len(failures) == 0 {
		return nil
	}
	return failures[0]
}

// walkExtendsChain is the walk itself, kept separate from the diagnostic so the
// two halves can be read independently. It is unexported, and
// ResolveExtendsChain is the only way to reach it, so no caller can walk a
// chain and then report its fault in words of its own.
//
// It measures the chain and judges nothing about its length. The bound is a
// declared rule over the shape this produces, so a chain past it comes back here
// as an ordinary chain and is refused one step out — which is also why the walk
// needs no length guard to terminate: the visited set bounds it, because every
// step records the entity it reached and stops the moment it reaches one twice.
func walkExtendsChain[T any](start T, spec ExtendsChainSpec[T]) ([]T, *chainFault) {
	chain := []T{start}
	visited := map[string]bool{spec.Key(start): true}
	current := start

	for {
		link := spec.ParentLink(current)
		if link == "" {
			break
		}
		parent, ok := spec.Lookup(link)
		if !ok {
			return nil, &chainFault{kind: chainFaultMissingParent, from: spec.name(current), link: link}
		}
		key := spec.Key(parent)
		if visited[key] {
			return nil, &chainFault{kind: chainFaultCycle, from: spec.name(current), link: link}
		}
		visited[key] = true
		chain = append(chain, parent)
		current = parent
	}

	return chain, nil
}

// unresolvableParentError renders the one chain fault that is not a declared
// rule as the validation failure reported against the entity the resolution was
// requested for.
//
// It is the single home for that diagnostic, so every path that walks an extends
// chain reports the same words for it. The message supplies only what the
// condition's own registered sentence cannot carry: the edge at fault.
//
// It is the only fault still rendered here, and the reason is the input rather
// than the wording. The other two are declared rules over the shape the walk
// produced; this one is raised where the walk could not produce a shape at all,
// because the entity the reference names is not there to be reached.
//
// missingParent is the one part of the wording that cannot be shared, because
// an extends reference does not resolve against the same thing for every kind:
// a catalog entity's names another catalog entity, while a Manifest's names a
// file beside the Release. Sending an author to the wrong one is advice that
// cannot fix their files, so the two sentences that depend on it are supplied
// by the caller and everything else about the failure — the condition, the
// field path, the entity it is reported against — stays here. A nil
// missingParent takes the catalog wording, and with it the one question only a
// catalog reference raises: whether the link's version pin is malformed, which
// is reported as that malformation rather than as an absent entity.
//
// It panics on any other fault kind rather than returning a failure carrying no
// condition, because a kind reaching here without a diagnostic is a defect in the
// compiler that would otherwise surface as an unattributed halt.
func unresolvableParentError(
	kind envelope.Kind,
	name string,
	f *chainFault,
	missingParent func(from, link string) (string, string),
) *ValidationError {
	if f.kind != chainFaultMissingParent {
		panic(fmt.Sprintf("validator: extends-chain fault kind %d carries no diagnostic here", f.kind))
	}
	if missingParent == nil {
		if failure := malformedParentPinError(kind, name, f); failure != nil {
			return failure
		}
	}
	required := fmt.Sprintf("%s extends %s, which no catalog entity provides", f.from, f.link)
	suggestion := catalogMissingParentSuggestion
	if missingParent != nil {
		required, suggestion = missingParent(f.from, f.link)
	}
	return NewValidationError(kind, name, ValidationDetail{
		FieldPath:  ".spec.extends",
		Constraint: required,
		Suggestion: suggestion,
		Err:        ErrInheritanceMerge(),
	})
}

// catalogMissingParentSuggestion is the advice a catalog entity's unresolvable
// extends link carries wherever it is reported.
const catalogMissingParentSuggestion = "Verify the extends reference names an entity present in the catalog"

// pullRemedy is the advice WithPublishedBaseRemedy adds to it.
const pullRemedy = "a base published to a registry is fetched by `aggrega pull --release <release>`"

// WithPublishedBaseRemedy returns errs with each catalog entity's unresolvable extends link
// carrying one more piece of advice: that a base published to a registry is
// fetched by a pull. Every other failure is returned as it is.
//
// The advice is true only where the resolution that missed reads what a pull
// leaves behind — a content-cache slot a lockfile pin vouches for — so it is added
// by such a resolution and never by one that reads catalog directories alone,
// where a pull that succeeds changes nothing the resolution reads and the advice
// would send its reader round the same loop. Both sentences live here, beside the
// missing-parent failure they complete, so the wording has one home.
func WithPublishedBaseRemedy(errs []*ValidationError) []*ValidationError {
	if len(errs) == 0 {
		return errs
	}
	out := make([]*ValidationError, len(errs))
	for i, e := range errs {
		out[i] = e
		if e == nil || e.detail.Suggestion != catalogMissingParentSuggestion || !errors.Is(e.detail.Err, ErrInheritanceMerge()) {
			continue
		}
		detail := e.detail
		detail.Suggestion += "; " + pullRemedy
		out[i] = &ValidationError{kind: e.kind, name: e.name, detail: detail}
	}
	return out
}

// malformedParentPinError reports an unresolvable extends link whose version pin
// is malformed as that malformation, and returns nil when the pin is well formed
// or absent.
//
// It is asked only for a catalog entity's extends, where the link is a catalog
// reference that may carry a pin; the kinds whose extends names a file beside
// the document supply their own missing-parent wording and are never asked,
// because a file path carries no pin. It runs after the lookup missed, so it
// changes no chain that resolves, only the explanation of one that did not: a
// malformed pin names no version, so "which no catalog entity provides" would
// send the author looking for an entity when the fault is the pin's text.
func malformedParentPinError(kind envelope.Kind, name string, f *chainFault) *ValidationError {
	pe, malformed := types.ParseVersionPin(f.link).Malformed()
	if !malformed {
		return nil
	}
	return NewValidationError(kind, name, ValidationDetail{
		FieldPath:  ".spec.extends",
		Constraint: fmt.Sprintf("%s extends %s", f.from, strings.Join(pe.Values(), ", ")),
		Suggestion: types.VersionPinForms,
		Err:        types.ErrMalformedVersionPin(),
	})
}
