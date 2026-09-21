// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// extendsChainCycleAdvice is what an author does about a chain that returns to
// an entity already in it: which edge closes the loop, and the two edits that
// open it.
//
// It is the occurrence's advice rather than part of the condition's registered
// sentence because the edge is a property of the occurrence, and a registered
// sentence is one static string per condition — it can name neither the entity
// whose declaration closes the loop nor the reference that declaration makes.
//
// Naming the edge is the whole of what makes the remedy actionable: the entity a
// resolution was requested for is rarely the one carrying the link that closes
// the loop, so an author handed the loop without the edge has to walk the chain
// themselves to find which file to open. Naming it twice — once as what was found
// and once as what to change — is a cost rather than an oversight, and it is the
// price of one sentence carrying both halves: an author told only that a loop
// exists has not been told where to cut it.
func extendsChainCycleAdvice(edge constraint.ExtendsChainCycleEdge) string {
	return fmt.Sprintf(
		"%s extends %s, which is already in the chain; remove the extends on %s, or point it at "+
			"an entity outside the chain",
		edge.Declaring, edge.Reference, edge.Declaring)
}

// extendsChainDepthAdvice is what an author does about a chain that ends cleanly
// and runs past the bound: the bound, the length reached, and the two edits that
// bring it under.
//
// The bound and the length are the occurrence's values for the reason the edge is
// above: the registered sentence is one static string and can carry neither, and
// an author told only that a chain is too deep has not been told by how much.
func extendsChainDepthAdvice(length int) string {
	return fmt.Sprintf(
		"the bound is %d and this chain reaches %d; shorten the chain, or extend a base that "+
			"sits closer to its root",
		maxInheritanceDepth, length)
}

// extendsChainCycleObjections is the rule itself: a chain an entity extends
// reaches each entity at most once, or the entity is refused.
//
// A chain that returns to an entity already in it describes no inheritance at
// all — every entity in the loop would have to be resolved before itself — so
// there is nothing to compose and no answer to give. Refusing costs the author
// one edit on the edge the advice names.
//
// It is one function over every authored kind that declares extends, and the
// declarations below hand it the answer each was invoked for. The rule does not
// vary by kind: the chain is followed the same way on each, the loop means the
// same thing on each, and the remedy is the same edit. What varies is which
// document the chain was read from, and that is the document-kind axis the three
// declarations express.
func extendsChainCycleObjections(
	s constraint.DocumentSubject,
	edge constraint.ExtendsChainCycleEdge,
	closes bool,
) []constraint.Objection {
	if !closes {
		return nil
	}
	return []constraint.Objection{
		s.RaiseWithAdvice(ErrCyclicExtension(), extendsChainCycleAdvice(edge)),
	}
}

// extendsChainDepthObjections is the rule itself: a chain that ends cleanly
// reaches no more entities than the bound admits, or the entity is refused.
//
// The bound is a guard on authoring rather than a termination safeguard — the
// walk terminates on its own, because it stops the moment it reaches an entity
// twice — so what this rule protects is a reader of the chain rather than the
// compiler that follows it.
//
// It raises nothing for a chain that does not end cleanly, and that is read from
// the fact rather than decided here: a chain closing on itself has no length to
// report, so the second result the fact carries is false and this rule has no
// subject. That is what lets the two rules over a chain's shape be invoked in
// either order — a loop is a loop at any length, and an author told to shorten
// one has been handed advice that cannot remove it.
//
// It is one function over every authored kind that declares extends, for the
// reason the cycle rule's own states.
func extendsChainDepthObjections(
	s constraint.DocumentSubject,
	length int,
	endsCleanly bool,
) []constraint.Objection {
	if !endsCleanly || length <= maxInheritanceDepth {
		return nil
	}
	return []constraint.Objection{
		s.RaiseWithAdvice(ErrExtendsChainTooDeep(), extendsChainDepthAdvice(length)),
	}
}

// componentExtendsChainReachesNoEntityTwice refuses a Component whose extends
// chain returns to an entity already in it.
//
// It is one of several declarations of one rule — one per authored kind that
// declares extends — and the plurality is forced by the check type rather than
// chosen: a check reads one presentation, and a fact is filed under exactly one
// authored document kind, so a single declaration would govern one kind and read
// as though it governed the rule, which is worse than an absent check because
// nothing would ever prompt a second look at the kinds it silently passed. Every
// one of them raises the same condition, so the diagnostic an author reads does
// not turn on which kind they wrote.
//
// It is invoked once per document rather than once per link, because the loop is
// a property of the chain: an author handed one finding has one edit to make, and
// a finding per link would report the same loop as many times as it has members.
var componentExtendsChainReachesNoEntityTwice = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrCyclicExtension(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		edge, closes := v.ComponentExtendsChainCycleEdge(s.SourceFile())
		return extendsChainCycleObjections(s, edge, closes)
	},
}

// componentExtendsChainStaysWithinTheDepthBound refuses a Component whose extends
// chain ends cleanly and reaches more entities than the bound admits.
//
// It is one of several declarations of one rule, for the reason
// componentExtendsChainReachesNoEntityTwice states.
var componentExtendsChainStaysWithinTheDepthBound = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrExtendsChainTooDeep(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		length, endsCleanly := v.ComponentExtendsChainLength(s.SourceFile())
		return extendsChainDepthObjections(s, length, endsCleanly)
	},
}

// featureExtendsChainReachesNoEntityTwice is the Feature document's declaration of
// the rule componentExtendsChainReachesNoEntityTwice declares for a Component.
// Why there are several, and why every one of them raises one condition, is
// stated there; it is not repeated here, because a rule's reasoning has one home.
var featureExtendsChainReachesNoEntityTwice = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrCyclicExtension(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		edge, closes := v.FeatureExtendsChainCycleEdge(s.SourceFile())
		return extendsChainCycleObjections(s, edge, closes)
	},
}

// featureExtendsChainStaysWithinTheDepthBound is the Feature document's
// declaration of the rule componentExtendsChainStaysWithinTheDepthBound declares
// for a Component.
var featureExtendsChainStaysWithinTheDepthBound = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrExtendsChainTooDeep(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		length, endsCleanly := v.FeatureExtendsChainLength(s.SourceFile())
		return extendsChainDepthObjections(s, length, endsCleanly)
	},
}

// manifestExtendsChainReachesNoEntityTwice is the Manifest document's declaration
// of the rule componentExtendsChainReachesNoEntityTwice declares for a Component.
//
// What a Manifest contributes of its own is what only a Manifest knows: a link is
// a file, so two Manifest files declaring one name are two links and a chain
// passing through both has not met itself. That is a property of the fact this
// reads rather than of the predicate, which is why the rule below is the shared
// one and not a Manifest-shaped variant of it.
var manifestExtendsChainReachesNoEntityTwice = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrCyclicExtension(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		edge, closes := v.ManifestExtendsChainCycleEdge(s.SourceFile())
		return extendsChainCycleObjections(s, edge, closes)
	},
}

// manifestExtendsChainStaysWithinTheDepthBound is the Manifest document's
// declaration of the rule componentExtendsChainStaysWithinTheDepthBound declares
// for a Component.
var manifestExtendsChainStaysWithinTheDepthBound = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrExtendsChainTooDeep(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		length, endsCleanly := v.ManifestExtendsChainLength(s.SourceFile())
		return extendsChainDepthObjections(s, length, endsCleanly)
	},
}

// releaseExtendsChainReachesNoEntityTwice is the Release document's declaration of
// the rule componentExtendsChainReachesNoEntityTwice declares for a Component.
//
// What a Release contributes of its own is what the Manifest's declaration
// contributes of its: a link is a file, so two Release files declaring one name
// are two links and a chain passing through both has not met itself. That is a
// property of the fact this reads rather than of the predicate, which is why the
// rule below is the shared one and not a Release-shaped variant of it.
var releaseExtendsChainReachesNoEntityTwice = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrCyclicExtension(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		edge, closes := v.ReleaseExtendsChainCycleEdge(s.SourceFile())
		return extendsChainCycleObjections(s, edge, closes)
	},
}

// releaseExtendsChainStaysWithinTheDepthBound is the Release document's
// declaration of the rule componentExtendsChainStaysWithinTheDepthBound declares
// for a Component.
var releaseExtendsChainStaysWithinTheDepthBound = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrExtendsChainTooDeep(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		length, endsCleanly := v.ReleaseExtendsChainLength(s.SourceFile())
		return extendsChainDepthObjections(s, length, endsCleanly)
	},
}
