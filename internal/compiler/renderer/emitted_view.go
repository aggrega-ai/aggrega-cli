// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"sort"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// A node's emitted file set is a loop-local, and it is a shorter-lived one than
// the state the stage before this retains. It is accumulated across the render's
// passes, read by the per-node collision detection, split by the intermediate
// flag as the node joins its dispatch group, and gone when that node's turn ends.
// By the time the render returns, the names each block emitted under and the
// blocks themselves are unreachable: what survives is the assembled output, where
// an assembly is free to have renamed, nested or merged the node's own names away.
//
// So this file retains a PROJECTION of that set and never the set itself. The
// projection carries the output names and, per name, which of the node's blocks
// produced it. It carries no bytes, which is what bounds the cost: the retention
// grows with the number of files a build emits rather than with their size, and a
// build emitting large artifacts retains no more than one emitting small ones.
//
// The retention and the presentation are one file because the retention is one
// field. The stage before this holds four structures at three scopes and earns a
// file of its own for them; a second file here would be a second place to look for
// one map.

// emittedProjection is what one node's completed emission is reduced to: the
// output names its own render placed files under, and the blocks that produced
// each.
//
// The field name is the correspondence with the accessor that reads it, on the
// reading the stage before this follows, and a guard beside this file compares the
// two sets in both directions — so a question declared at this stage later cannot
// leave the projection behind silently.
type emittedProjection struct {
	// EmittedFileOriginSet are the authored input blocks that produced each file
	// the node emitted, keyed by the output name and held in name order, with each
	// entry's blocks deduplicated and in the order the render produced them.
	//
	// The keys are held in name order rather than in production order, because the
	// key order is what the name axis of this stage's coordinates answers with and
	// the order files are placed into the accumulation is not a property of the
	// input. The order WITHIN an entry is the production order, which is a property
	// of the input: a node's blocks are read in a fixed sequence, so the first
	// block listed is the one that reached the name first.
	EmittedFileOriginSet types.OrderedMap[[]string]
}

// emittedProjections is one render's retained set of those projections, keyed by
// node.
//
// It is installed once per render and lives for the whole of it, each entry
// written as its node's own emission completes.
type emittedProjections struct {
	// mu serialises writes, for the reason the sibling retention gives: the node
	// walk is sequential today, so the lock is never contended, and it is here
	// because a map written from two goroutines is a crash rather than a wrong
	// answer — which is not a property this value can assert about its callers.
	mu     sync.Mutex
	byNode map[string]*emittedProjection
}

// newEmittedProjections returns an empty set for one render.
func newEmittedProjections() *emittedProjections {
	return &emittedProjections{byNode: map[string]*emittedProjection{}}
}

// recordNode reduces one node's completed emission to its projection and retains
// that.
//
// It READS the state and retains no reference to it: the map it builds is its
// own and holds only names and block tags, so the emitted bytes are free to be
// dropped when the node's turn ends.
//
// It is called at the one point where the node's OWN emission is complete: after
// every render pass that can contribute a file has run, and after an own-scope
// pipeline has appended whatever it emitted. It is called before the node joins
// its dispatch group, which is what keeps this a fact about one node rather than
// about a group.
//
// A pipeline deferred whole to post-assembly contributes nothing here, and that is
// the truthful answer rather than a gap: such a pipeline emits into the merged
// documents a group assembled, which is not this node's own output and is not
// keyed by a name this node chose.
//
// A nil set records nothing, which is the state of a render whose caller assembled
// its own input.
func (e *emittedProjections) recordNode(state *PhaseBState) {
	if e == nil || state == nil {
		return
	}

	// Names first, sorted, so the map's key order is name order. Ranging the
	// accumulation once to collect names and once to fill them is what makes the
	// key order a function of the input rather than of the order the passes
	// happened to place files in.
	originsByName := map[string][]string{}
	names := make([]string, 0, len(state.EmittedFiles))
	for _, f := range state.EmittedFiles {
		origin := f.Origin.String()
		existing, seen := originsByName[f.Name]
		if !seen {
			names = append(names, f.Name)
			originsByName[f.Name] = []string{origin}
			continue
		}
		// A block emitting several files under one name is one contributor, not
		// several: what this fact reports is which blocks met on a name, so a
		// repeat from a block already listed adds nothing to answer.
		duplicate := false
		for _, already := range existing {
			if already == origin {
				duplicate = true
				break
			}
		}
		if !duplicate {
			originsByName[f.Name] = append(existing, origin)
		}
	}
	sort.Strings(names)

	p := &emittedProjection{}
	for _, name := range names {
		p.EmittedFileOriginSet.Set(name, originsByName[name])
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.byNode[state.NodeID] = p
}

// recordedNodes reports how many nodes the retention holds.
//
// It exists so an assertion over what the retention answers can establish that it
// holds anything at all first. A reading taken over an empty retention reports
// exactly what a reading over a correct one reports — nothing wrong — so the
// count is measured rather than assumed.
func (e *emittedProjections) recordedNodes() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.byNode)
}

// projection returns one node's retained emission, and reports whether the
// render reached that node at all.
//
// The report is what separates a node that emitted nothing from a node the render
// never reached. The presentation collapses the two, because a fact accessor
// returns a value and never a report; the enumeration does not, because a
// coordinate axis over a node the render never reached would present rules
// coordinates nothing produced.
func (e *emittedProjections) projection(node string) (*emittedProjection, bool) {
	if e == nil {
		return nil, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.byNode[node]
	return p, ok
}

// emittedView presents, as named facts, what each node's own render emitted
// before anything was grouped for assembly: which of the node's blocks produced
// the file standing at each output name.
//
// It satisfies [constraint.EmittedView], and the compiler holds it to that
// contract at the bottom of this file rather than a comment claiming it.
//
// # The earlier stage is embedded, not re-projected
//
// A stage's view contains the stage it extends, so every fact complete before any
// file was emitted is readable here. Those facts are answered by the presentation
// of the state each node reached before its artifact bytes existed, embedded and
// delegated to, rather than derived again: one fact has one producer, and two
// producers of a fact are free to disagree with nothing comparing them.
//
// # Why this stage exists at all rather than being folded into either neighbour
//
// It cannot be folded into the stage before it, which holds the state a node
// reached BEFORE any artifact bytes: no file has been emitted there, so a name
// two blocks met on does not exist yet and a rule about it would be asked over an
// empty set.
//
// It cannot be folded into the stage after it either, and that is the less
// obvious half. The stage after this holds every node's output already sorted
// into the group it will be merged in, and sorting is what splits a node's files
// by the intermediate flag and hands part of them to an assembler. A rule about
// two of one node's own blocks meeting on a name is answerable before any of
// that, and its remedy is an edit to one of those blocks — so placing it later
// would run it after the compiler had done work the answer was always going to
// discard, and would ask it over a set that no longer has the shape the node
// emitted.
//
// # A node the render did not reach answers with a zero value
//
// A fact accessor returns a value and never an error, so a node absent from the
// retention is answered with no blocks. For a node that emitted nothing that is
// also the truthful answer, and deliberately so at this layer: every node the
// walk completes is recorded unconditionally, so a node with no answer is one
// that emitted nothing or one a render never reached — and a render that stopped
// short reports why it stopped, which is not this stage's finding.
//
// # Values are handed over uncopied
//
// The contract every view shares makes a fact's value immutable to a check, and
// this value keeps that contract. What the accessor returns is the retained slice
// itself rather than a copy of it, so a check that wrote through one would change
// what every later reader sees; no check does, and the contract is where that is
// declared rather than here.
type emittedView struct {
	constraint.HydratedView

	// emissions is the render's retention of what each node's own render emitted.
	// It is held by pointer because the retention is filled as the node walk
	// proceeds.
	emissions *emittedProjections
}

// newEmittedView presents what each node's own render emitted, with the facts
// complete earlier answered by the presentation of the state each node reached
// before its artifact bytes existed.
//
// It is unexported because the render is the one thing that builds it: the
// emission it reads is the render's own working state, so a caller outside this
// package has nothing to construct it from and the bypass does not compile.
//
// It returns nil when the earlier presentation or the retention is absent. A
// presentation missing either would answer a whole class of facts from nothing,
// and a presentation that answers emptily is worse than an absent one, because a
// rule reading it cannot tell an empty answer from a real one.
func newEmittedView(
	earlier constraint.HydratedView,
	emissions *emittedProjections,
) constraint.EmittedView {
	if earlier == nil || emissions == nil {
		return nil
	}
	return &emittedView{HydratedView: earlier, emissions: emissions}
}

// EmittedFileOriginSet returns the authored input blocks that produced the file
// standing at one output name within one node.
//
// The name is the one this stage's own coordinate axis enumerates, so a rule
// invoked over that axis reads this fact for exactly the names the enumeration
// presented it.
//
// A node the retention does not hold, and a name that node emitted nothing
// under, both answer with no blocks — which is the same answer for the same
// reason the presentation's own contract gives: an accessor returns a value and
// never a report.
func (v *emittedView) EmittedFileOriginSet(node string, path string) []string {
	p, ok := v.emissions.projection(node)
	if !ok {
		return nil
	}
	origins, _ := p.EmittedFileOriginSet.Get(path)
	return origins
}

var _ constraint.EmittedView = (*emittedView)(nil)

// emittedStageCoordinates is the node and output-name enumeration one render's
// own emissions carry.
//
// It reads the same retention the presentation answers the fact from, and derives
// nothing else. One reader means the names a rule is invoked over and the names
// its facts are keyed by cannot disagree — and they could disagree, because the
// name axis is a deduplication over an accumulation that may hold one name
// several times, which a second implementation would be free to get wrong in a
// way nothing compares.
//
// # Order
//
// Both axes return a deterministic order and neither ranges a plain Go map. The
// node axis is the node enumeration the caller supplies, which carries its own
// order; the name axis is the retention's key order, which is name order because
// the retention is filled in name order.
type emittedStageCoordinates struct {
	// emissions is the render's retention of what each node emitted. It is held
	// rather than copied because each entry is written once, as its node's
	// emission completes, and is not written to afterwards.
	emissions *emittedProjections
}

// newEmittedStageCoordinates enumerates the output names each node's own render
// emitted.
//
// It returns nil when the retention is absent. An enumeration over no retention
// would present every rule an empty subject set, and a rule invoked zero times
// reports exactly what a rule invoked and satisfied reports — so a caller checks
// for nil rather than being handed a value that answers emptily.
func newEmittedStageCoordinates(emissions *emittedProjections) *emittedStageCoordinates {
	if emissions == nil {
		return nil
	}
	return &emittedStageCoordinates{emissions: emissions}
}

// NodeEmittedPaths returns the output names one node's own render emitted,
// deduplicated, in name order.
//
// A node the retention does not hold answers with nothing, which is what a node
// that emitted nothing gives: neither has a name for a rule to be asked about.
func (c *emittedStageCoordinates) NodeEmittedPaths(node string) []string {
	p, ok := c.emissions.projection(node)
	if !ok {
		return nil
	}
	return p.EmittedFileOriginSet.Keys()
}

// The enumeration is held to the axis the rules over a render's own emissions are
// keyed by, so an axis that stops being satisfied fails to compile here rather
// than at the invocation that needed it.
var _ constraint.NodeEmittedPathEnumerator = (*emittedStageCoordinates)(nil)
