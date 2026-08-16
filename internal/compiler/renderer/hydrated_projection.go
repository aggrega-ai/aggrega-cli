// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"sync"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The render's per-node working state is a loop-local: it is built, used to
// render one node, and dropped when that node's turn ends. Anything asking a
// question about the state a node reached before its artifact bytes existed has
// nothing left to read by the time the render finishes.
//
// What survives here is a PROJECTION of that state and never the state itself.
// The distinction is what bounds the cost: the whole per-node state grows with
// everything a node accumulates, while a projection carries one field per
// question the framework declares at this point in the compilation — a fixed and
// small set, so the retention grows with the declared questions rather than with
// the build.
//
// The field names below are the correspondence, and they are checked rather than
// trusted: each names the accessor that reads it, and a guard beside this file
// compares the two sets in both directions. A question declared here later
// therefore cannot leave the projection behind silently — the guard names the
// field nobody added.

// valuesMappingRefs is the reference set a node's inline values-mapping template
// reads, together with whether that node's mapping is authored inline at all.
//
// The second half is load-bearing and is why this is one value rather than a
// bare slice. A mapping authored as an import carries no inline text, so its
// reference set is ABSENT rather than empty, and a reader unable to tell the two
// apart would report every value unread for exactly those nodes whose mapping it
// never read.
type valuesMappingRefs struct {
	// Paths are the context paths the inline template reads, sorted. A template
	// has no single reading order to preserve, so sorting is what makes the value
	// the same on every run.
	Paths []string
	// Inline reports whether the node's mapping is authored inline.
	Inline bool
}

// hydratedProjection is what one node's mid-render state is reduced to once that
// state is complete: per-node resolved locals and provides, the resolved source,
// the parsed workload, and each artifact entry's template text before anything
// evaluates it.
//
// Every field is a value already read out of the state. None of them is a
// carrier the state can be walked back out of, which is the property that keeps
// this a projection: a reader holding one of these can answer the declared
// questions and can reach nothing else.
type hydratedProjection struct {
	// DeclaredLocalsKeys are the local names the node's Recipe declares, in
	// author order.
	DeclaredLocalsKeys []string

	// ValuesMappingTemplateRefs is the node's inline values-mapping reference
	// set, and whether the mapping is authored inline.
	ValuesMappingTemplateRefs valuesMappingRefs

	// ResolvedNodeSourceDeclared reports whether the node's Recipe carries a
	// source once source resolution has run.
	//
	// The value recorded is the RESOLVED source rather than the text the author
	// wrote: source resolution replaces the node's Recipe with a local copy whose
	// source holds the evaluated result, and that copy is what everything
	// downstream reads. So a source template evaluating to an empty string is
	// recorded the same as an absent source, which is one condition rather than
	// two.
	ResolvedNodeSourceDeclared bool

	// ParsedNodeWorkloadPresent reports whether the node's render state carries a
	// workload. A Recipe declaring no workload body reaches artifact evaluation
	// with none, because workload synthesis had nothing to parse.
	ParsedNodeWorkloadPresent bool

	// ArtifactTemplateText is the template text standing at each of the node's
	// artifacts, keyed by the output name the entry resolves to and held in the
	// order the entries were walked.
	//
	// The text is the entry's content whichever form the entry took — written
	// inline, supplied by an imported file, or supplied by an entry of a library
	// bundle — because it is recorded where that content is resolved rather than
	// where the entry is authored. Recording it at the authoring site would hand
	// back an import directive for two of the three forms, and resolving it a
	// second time somewhere else would read the file twice and could answer
	// differently each time.
	ArtifactTemplateText types.OrderedMap[string]
}

// hydratedProjections is one render's retained set of those projections, keyed
// by node.
//
// It is installed once per render and lives for the whole of it. The per-node
// projections are written as each node's state completes; the resolved provides
// are not written at all, because the render already holds them.
type hydratedProjections struct {
	// ResolvedNodeProvides is the render's own resolved-provides bag, held here
	// by reference rather than copied per node.
	//
	// The bag is evaluated once for the whole render before any node is rendered
	// and is not written to afterwards, so a second copy would carry no
	// information the original does not and would grow with the node count. What
	// is retained here is the one map.
	ResolvedNodeProvides NodeProvides

	// mu serialises writes. The node walk is sequential today, so the lock is
	// never contended; it is here because a map written from two goroutines is a
	// crash rather than a wrong answer, and the walk's sequence is not a property
	// this value can assert about its callers.
	mu     sync.Mutex
	byNode map[string]*hydratedProjection
}

// newHydratedProjections returns an empty set for one render, holding the
// render's resolved-provides bag by reference.
func newHydratedProjections(provides NodeProvides) *hydratedProjections {
	return &hydratedProjections{
		ResolvedNodeProvides: provides,
		byNode:               map[string]*hydratedProjection{},
	}
}

// recordNode reduces one node's completed mid-render state to its projection and
// retains that.
//
// It READS the state and retains no reference to it: every field it takes is
// either a value or a slice built here, so the state itself is free to be
// dropped when the node's turn ends.
//
// It is called at the one point where each field's source holds the value its
// own accessor names — after source resolution and workload parsing, and before
// anything evaluates an artifact template. A nil set records nothing, which is
// the state of a render whose caller assembled its own input.
func (h *hydratedProjections) recordNode(state *PhaseBState) {
	if h == nil || state == nil {
		return
	}
	p := &hydratedProjection{
		ParsedNodeWorkloadPresent: state.ParsedWorkload != nil,
	}
	if recipe := state.Recipe; recipe != nil {
		p.DeclaredLocalsKeys = recipe.Spec.Locals.Keys()
		p.ResolvedNodeSourceDeclared = recipeSourceDeclared(recipe)
		p.ValuesMappingTemplateRefs = collectValuesMappingRefs(recipe.Spec.ValuesMapping)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.byNode[state.NodeID]; ok {
		// The artifact walk may have reached this node first; keep what it wrote.
		p.ArtifactTemplateText = existing.ArtifactTemplateText
	}
	h.byNode[state.NodeID] = p
}

// recordArtifactTemplate retains the template text standing at one of a node's
// artifacts, under the output name that entry resolves to.
//
// It is called from the artifacts walk rather than beside recordNode, because
// that walk is where an imported entry's and a bundle entry's content is
// resolved — the content the recorded fact names for all three authoring forms.
// A nil set records nothing, which is what keeps the attachment-carried artifacts
// that flow through the same walk out of a fact declared over a node's own
// Recipe.
func (h *hydratedProjections) recordArtifactTemplate(nodeID, outputName, text string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.byNode[nodeID]
	if !ok {
		p = &hydratedProjection{}
		h.byNode[nodeID] = p
	}
	p.ArtifactTemplateText.Set(outputName, text)
}

// projection returns one node's retained projection, and reports whether the
// node was recorded.
func (h *hydratedProjections) projection(nodeID string) (hydratedProjection, bool) {
	if h == nil {
		return hydratedProjection{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.byNode[nodeID]
	if !ok {
		return hydratedProjection{}, false
	}
	return *p, true
}

// recordedNodes reports how many nodes the set holds.
func (h *hydratedProjections) recordedNodes() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byNode)
}

// collectValuesMappingRefs derives the reference set of a node's values mapping.
//
// The set is derived from the INLINE text alone, because that is the authoring
// form the question is about: a mapping authored as an import carries no inline
// text, so its reference set is absent rather than empty.
//
// A text that does not parse still reports the authoring form it took, and
// carries no paths. That combination is unreachable in a completed render — the
// same text is evaluated through the same parser earlier in the node's turn, and
// a fault there halts the build before this is recorded — so the branch states
// what is true about the authoring rather than inventing a third condition.
func collectValuesMappingRefs(mapping types.ValuesMapping) valuesMappingRefs {
	if mapping.Inline == "" {
		return valuesMappingRefs{}
	}
	paths, err := gotmpl.CollectTemplateFieldAccesses(mapping.Inline)
	if err != nil {
		return valuesMappingRefs{Inline: true}
	}
	return valuesMappingRefs{Paths: paths, Inline: true}
}
