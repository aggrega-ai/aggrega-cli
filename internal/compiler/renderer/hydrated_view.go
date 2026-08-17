// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// hydratedView presents, as named facts, the state each node reached before any
// of its artifact bytes existed: the local names its Recipe declares, the value
// its Recipe resolved for each provides key, the reference set of its inline
// values mapping, whether a source survived resolution, whether a workload was
// parsed, and the template text standing at each of its artifacts before
// anything evaluates it.
//
// It satisfies [constraint.HydratedView], and the compiler holds it to that
// contract at the bottom of this file rather than a comment claiming it.
//
// # The earlier stage is embedded, not re-projected
//
// A stage's view contains the stage it extends, so every fact complete before
// this state was reached is readable here. Those facts are answered by the
// presentation of what each plugin reports about itself, embedded and delegated
// to, rather than re-derived: one fact has one producer, and two producers of a
// fact are free to disagree with nothing comparing them. That presentation in
// turn embeds the one below it, so a rule reading a graph fact through this
// value reaches the same answer the graph's own presentation gives.
//
// The delegation is load-bearing for the rules placed here rather than
// incidental. A rule over an artifact entry reads the entry's text — a fact of
// this stage — together with the delimiter pair its provider declares, which is
// a fact of the stage below; asking the embedded presentation for the pair is
// what lets one rule read both without either stage producing the other's fact.
//
// # Every fact is read from the retention the render already holds
//
// The state each of these facts describes is a loop-local: it is built for one
// node, used to render it, and dropped when that node's turn ends. What survives
// is a projection of that state, one value per fact declared here, recorded as
// each node completes. The accessors below read that retention and nothing else,
// so presenting this stage re-reads no document and re-derives no value — a
// second derivation beside the recording would be free to disagree with it about
// a node neither of them reads twice.
//
// The resolved provides are the exception in placement rather than in principle:
// they are evaluated once for the whole render before any node is rendered, so
// the retention holds that one bag by reference instead of copying it under every
// node. It is not written to after its evaluation, which is what lets a reader
// take it without a lock.
//
// # A node the render did not reach answers as one that declared nothing
//
// A fact accessor returns a value and never an error, so a node absent from the
// retention is answered with the zero value of the fact asked for. That is
// indistinguishable from a node whose Recipe declared nothing, and deliberately
// so at this layer: a node is recorded unconditionally as its state completes, so
// the only nodes missing from the retention are those a render never reached —
// and a render that stopped short reports why it stopped, which is not this
// stage's finding.
//
// # Values are handed over uncopied
//
// The contract every view shares makes a fact's value immutable to a check, and
// this value keeps that contract. What an accessor returns is the retained value
// itself rather than a copy of it, so a check that wrote through one would change
// what every later reader sees; no check does, and the contract is where that is
// declared rather than here.
type hydratedView struct {
	constraint.DescribedView

	// projections is the render's retention of per-node mid-render state,
	// together with the render-wide resolved-provides bag. It is held by pointer
	// because the retention is filled as the node walk proceeds: this
	// presentation is built once, before any node has rendered, and answers from
	// whatever the walk has recorded by the time a reader asks.
	projections *hydratedProjections
}

// newHydratedView presents the state each node reached before its artifact bytes
// existed, with the facts complete earlier answered by the presentation of what
// every plugin reports about itself.
//
// It is unexported because the render is the one thing that builds it: the
// retention it reads is the render's own working state, so a caller outside this
// package has nothing to construct it from and the bypass does not compile.
//
// The products are taken rather than reached for, because a view projects facts
// already produced and never produces one of its own. The retention is taken by
// pointer rather than by value, so this presentation is built once at the point
// the retention is installed and answers from what the node walk records into it
// afterwards.
//
// A build constructs exactly one of these and publishes it on the render's
// result. Constructing a second one over a different retention would give this
// stage's facts a second producer, free to disagree with the first about a node
// neither of them records twice.
//
// It returns nil when the earlier presentation or the retention is absent. A
// presentation missing either would answer a whole class of facts from nothing,
// and a presentation that answers emptily is worse than an absent one, because a
// rule reading it cannot tell an empty answer from a real one.
func newHydratedView(
	earlier constraint.DescribedView,
	projections *hydratedProjections,
) constraint.HydratedView {
	if earlier == nil || projections == nil {
		return nil
	}
	return &hydratedView{DescribedView: earlier, projections: projections}
}

// DeclaredLocalsKeys returns the local names a node's Recipe declares, in author
// order.
//
// Author order is the fact rather than an incidental property of it: locals are
// evaluated in the order they are declared, so a rule about what a local may
// reference is a rule about this order. The order is the one the Recipe's own
// ordered mapping holds, so it is the order the author wrote.
func (v *hydratedView) DeclaredLocalsKeys(node string) []string {
	p, ok := v.projections.projection(node)
	if !ok {
		return nil
	}
	return p.DeclaredLocalsKeys
}

// ResolvedNodeProvides returns the value a node's Recipe resolved for one
// provides key, and whether the key was resolved at all.
//
// It reads the render's own resolved bag, which is evaluated once for the whole
// render and not written to afterwards — so this takes no lock and answers the
// same value whenever it is asked. A node that resolved no provides at all, and
// a key that node did not resolve, both report false.
func (v *hydratedView) ResolvedNodeProvides(node string, key string) (string, bool) {
	bag, ok := v.projections.ResolvedNodeProvides[node]
	if !ok {
		return "", false
	}
	return bag.Get(key)
}

// ValuesMappingTemplateRefs returns the context paths a node's inline
// values-mapping template reads, and whether the node's mapping is authored
// inline at all.
//
// The second result is load-bearing. A mapping authored as an import carries no
// inline text, so its reference set is absent rather than empty, and a rule that
// could not tell the two apart would report every value unread for exactly those
// nodes whose mapping it never read. A node the render did not reach reports the
// same absence, for the same reason it answers every other fact with its zero
// value.
//
// The paths come back sorted, as the retention recorded them: a template has no
// single reading order to preserve, so sorting is what makes the result the same
// on every run.
func (v *hydratedView) ValuesMappingTemplateRefs(node string) ([]string, bool) {
	p, ok := v.projections.projection(node)
	if !ok {
		return nil, false
	}
	return p.ValuesMappingTemplateRefs.Paths, p.ValuesMappingTemplateRefs.Inline
}

// ResolvedNodeSourceDeclared reports whether a node's Recipe carries a source
// once source resolution has run.
//
// The value is the RESOLVED source rather than the text the author wrote: source
// resolution replaces the Recipe with a copy whose source holds the evaluated
// result, and the retention records that copy — so a source template evaluating
// to an empty string reports the same as an absent source. That is one condition
// rather than two, which is what lets a rule about a template reading the source
// pillar name a single cause instead of distinguishing two states an author
// cannot tell apart from the outcome.
func (v *hydratedView) ResolvedNodeSourceDeclared(node string) bool {
	p, _ := v.projections.projection(node)
	return p.ResolvedNodeSourceDeclared
}

// ParsedNodeWorkloadPresent reports whether a node's render state carries a
// workload.
//
// A Recipe declaring no workload body reaches artifact evaluation with none,
// because workload synthesis had nothing to parse. So this is what separates a
// node whose templates may read the workload pillar from one whose reference to
// it has nothing to resolve against.
func (v *hydratedView) ParsedNodeWorkloadPresent(node string) bool {
	p, _ := v.projections.projection(node)
	return p.ParsedNodeWorkloadPresent
}

// ArtifactTemplateText returns the template text standing at one of a node's
// artifacts, as it is before anything evaluates it.
//
// The artifact is named by the output name the entry resolves to, which is the
// name the traversal invoking a rule keys each subject by. The text is the
// entry's content whichever form the entry took — written inline, supplied by an
// imported file, or supplied by an entry of a library bundle — because the
// retention records it where that content is resolved rather than where the entry
// is authored.
//
// An artifact the retention does not hold answers empty, which is what a node the
// render did not reach and an artifact of an attachment rather than of the node's
// own Recipe both give.
func (v *hydratedView) ArtifactTemplateText(node string, artifact string) string {
	p, ok := v.projections.projection(node)
	if !ok {
		return ""
	}
	text, _ := p.ArtifactTemplateText.Get(artifact)
	return text
}

var _ constraint.HydratedView = (*hydratedView)(nil)
