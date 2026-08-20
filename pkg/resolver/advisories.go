// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// RunAdvisories evaluates the ADVISORY rules that can be asked of the two
// authoring documents before the composition is flattened, and returns one
// finding per objection.
//
// It is the sibling of [RunGuards] and it is a separate entry point rather than a
// second return value from that one, which is the whole point of the split. A
// resolver finding carries no severity: the shape a finding takes is decided at
// the boundary it crosses, and the boundary decides advisory-versus-halting by
// which collection it appends to. [RunGuards] returns the collection a build
// halts on, so a rule asked there halts however its declaration reads. Nothing
// this function returns can halt a build, because it has no such collection to
// return — which is what makes an advisory's consequence a property of where it
// is asked rather than of whoever wired it.
//
// The rules asked here are the ones whose subject is legal authoring that is
// probably not what the author meant. Each names the sentinel it enforces, what a
// violation does to the build, the facts it reads and what it is invoked once
// per; the declarations themselves are the statement of how many there are.
//
// A near-miss reading cannot halt, and that is a property of the rule rather than
// a caution about it. Pairing two names is a heuristic, and an author may
// deliberately name a socket or an attachment that happens to sit one edit from a
// declared name — so a reading that refused would refuse legal input on a guess.
// The mitigation is to name both spellings and let the author see the pair.
//
// Findings come back rule by rule, and within a rule in the order its traversal
// invoked it, so identical input yields a byte-identical sequence.
//
// cancelled is read once per outermost coordinate of each traversal, and a walk
// that sees it turn hands back nothing rather than a truncated set — which is why
// this returns an error alongside the findings. A caller with no cancellation
// concern passes nil.
//
// Returns no findings when the application document is absent.
func RunAdvisories(
	cancelled constraint.Cancellation,
	na types.NormalizedAggregate,
	release *types.Release,
	catalog CatalogLookup,
	parameters map[string]any,
) ([]*ResolverError, error) {
	agg := na.Aggregate()
	if agg == nil {
		return nil, nil
	}
	// The presentation is built here rather than shared with the halting suite,
	// and the duplication is deliberate: sharing it would mean one function
	// holding both collections, which is exactly the arrangement that lets an
	// advisory be appended to the halting one. The cost is that the derived
	// contracts resolve once more per build. Do not "unify" the two
	// constructions without first moving the severity onto what a check raises,
	// or the separation that keeps an advisory advisory goes with it.
	contracts := newNormalizedView(na, release, catalog, parameters)
	return runDeclaredAdvisories(cancelled, contracts,
		fmt.Sprintf("Aggregate %s", agg.Metadata.Name))
}

// runDeclaredAdvisories asks every advisory rule declared over the two
// pre-flatten documents.
//
// Every rule asked here reads what the APPLICATION wrote, so every finding names
// the application document. That is the rules' subject matter and not a
// simplification: a reading of what the deployment wrote is not attributable to
// the application, and a rule of that kind arriving here would need the second
// document named the way the halting suite names it.
//
// 🔴 Each traversal call NAMES its rule as a literal argument, and that is a
// constraint of the framework rather than a style. Which rules a caller invokes
// is derived by reading this module's source for a traversal entry called with a
// declared rule named at the call site; a helper taking a rule and calling the
// traversal itself would name a parameter there instead, and the derivation would
// report an invocation it can attribute to no rule — leaving every rule reached
// that way indistinguishable from one nothing invokes at all.
func runDeclaredAdvisories(
	cancelled constraint.Cancellation,
	view *normalizedView,
	developerDocument string,
) ([]*ResolverError, error) {
	coordinates := newCoordinateSet(view)
	facts := constraint.NormalizedView(view)

	var out []*ResolverError
	// A traversal that stopped on the cancellation signal ends the whole suite: a
	// partial set would report a document as satisfying readings that were never
	// asked of it.
	collect := func(objections []constraint.Objection, err error) error {
		if err != nil {
			return err
		}
		out = append(out, FindingsFromObjections(objections, developerDocument)...)
		return nil
	}

	// Once per component. The socket a near-miss reading is about is by
	// definition not one the component's type declares, so a per-socket
	// invocation would never be presented with its own subject; the set the
	// component wrote is read as a fact instead.
	if err := collect(constraint.RunOnNodes(
		cancelled, coordinates, facts, authoredSocketNameNearADeclaredOne)); err != nil {
		return nil, err
	}

	// Once per attachment a component carries.
	if err := collect(constraint.RunOnNodeAttachments(
		cancelled, coordinates, coordinates, facts, attachmentNameNearADeclaredSlot)); err != nil {
		return nil, err
	}

	return out, nil
}
