// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"sort"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// A render's emitted file set is a loop-local, and it is a shorter-lived one than
// the state the stage before this retains. It is accumulated across the render's
// passes, split by the intermediate flag as its output joins a dispatch group, and
// gone when that render's turn ends. By the time the build returns, the names each
// block emitted under and the blocks themselves are unreachable: what survives is the
// assembled output, where an assembly is free to have renamed, nested or merged the
// render's own names away.
//
// So this file retains a PROJECTION of that set and never the set itself. The
// projection carries the output names and, per name, which of the render's blocks
// produced it. It carries no bytes, which is what bounds the cost: the retention
// grows with the number of files a build emits rather than with their size, and a
// build emitting large artifacts retains no more than one emitting small ones.
//
// # Three owners, because one rule runs over three renders
//
// The emission this stage describes happens in three separately authored documents.
// A component node's own Recipe emits under that node's identity. The FeatureRecipe
// one attachment binds on a node emits under the HOST component's node identity, over
// blocks the attachment declares. The FeatureRecipe one boundary-scope attachment
// binds emits under a composite dispatch marker, against no single component.
//
// A single node-keyed retention would fold the second into the first, so a rule
// reading it would answer about the host's own emission while the blocks in question
// were the attachment's — admitting a collision the attachment never wrote wherever
// the host has one, and refusing none of the attachment's own. Both directions are
// wrong and neither is visible from the outcome. And no widening of a node-and-name
// enumeration produces the attachment coordinate, so the answer is a fact per owner
// rather than a wider axis over one fact.
//
// So this file holds three retentions under three key types, three accessors, and
// three coordinate axes — one per owner — sharing the one projection helper that
// reduces an accumulation to its per-name origin sets. The shape is the one the stage
// before this already carries for the same three owners.

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

// attachmentEmittedProjection is what the completed emission of the render one
// attachment carries on a node is reduced to.
//
// It is the owner-keyed form of [emittedProjection] and holds the same shape for
// the same reasons. It is a separate type rather than the same one under a second
// key because the accessor that reads it is a separate declared fact: the field name
// is the correspondence with that accessor, and one type carrying both names would
// claim to answer either fact from either key.
type attachmentEmittedProjection struct {
	// AttachmentEmittedFileOriginSet are the authored input blocks that produced
	// each file the attachment's own render emitted, keyed by the output name that
	// render chose and held in name order, with each entry's blocks deduplicated
	// and in the order the render produced them.
	//
	// The name is the render's own, before the caller composes the attachment's
	// output prefix onto it: two of this render's blocks meeting on a name have
	// already collided whatever prefix is later added, and prefixing first would
	// make the retention answer under a name no block chose.
	AttachmentEmittedFileOriginSet types.OrderedMap[[]string]

	// sourceFile is the FeatureRecipe this render's blocks were authored in, which
	// is the document an author edits to rename one of two that met.
	//
	// It is unexported because it is a locator input rather than a declared fact:
	// the guard beside this file compares the retention's EXPORTED fields against
	// what the stage declares, so an exported field here would be reported as a
	// retention carrying something no accessor answers.
	//
	// It is retained rather than recovered because it cannot be recovered: the
	// render is reached under the host component's node identity, so nothing
	// downstream of it names the FeatureRecipe.
	sourceFile string
}

// boundaryAttachmentEmittedProjection is what the completed emission of the render
// one boundary-scope attachment carries is reduced to.
//
// It is rooted at the boundary because that is where such an attachment is
// declared, which is also why its fact is keyed there.
type boundaryAttachmentEmittedProjection struct {
	// BoundaryAttachmentEmittedFileOriginSet are the authored input blocks that
	// produced each file that render emitted, keyed by the output name the render
	// chose and held in name order.
	//
	// The name is the render's own, before the caller routes the output by the flag
	// saying whether an assembly consumes it.
	BoundaryAttachmentEmittedFileOriginSet types.OrderedMap[[]string]

	// sourceFile is the FeatureRecipe this render's blocks were authored in,
	// retained and unexported for the reasons its node-carried sibling states.
	sourceFile string
}

// emittedProjections is one render's retained set of those projections, keyed by
// node and, for what an attachment's render emitted, by that attachment's owner.
//
// It is installed once per render and lives for the whole of it, each entry written
// as its own render's emission completes.
//
// The three owners are held in three maps under three key types rather than in one
// map under an owner discriminator, on the reading the sibling retention at the
// stage before this states: a boundary-rooted identity is then unable to be written
// into a node-rooted key at all, which is a property of the types rather than of a
// discriminator being set correctly at every write.
type emittedProjections struct {
	// mu serialises writes, for the reason the sibling retention gives: the node
	// walk is sequential today, so the lock is never contended, and it is here
	// because a map written from two goroutines is a crash rather than a wrong
	// answer — which is not a property this value can assert about its callers.
	mu                   sync.Mutex
	byNode               map[string]*emittedProjection
	byNodeAttachment     map[nodeAttachmentKey]*attachmentEmittedProjection
	byBoundaryAttachment map[boundaryAttachmentKey]*boundaryAttachmentEmittedProjection
}

// newEmittedProjections returns an empty set for one render.
func newEmittedProjections() *emittedProjections {
	return &emittedProjections{
		byNode:               map[string]*emittedProjection{},
		byNodeAttachment:     map[nodeAttachmentKey]*attachmentEmittedProjection{},
		byBoundaryAttachment: map[boundaryAttachmentKey]*boundaryAttachmentEmittedProjection{},
	}
}

// originsByEmittedName reduces one render's accumulated emission to the origin set
// each output name carries: the blocks that produced a file under that name,
// deduplicated, in production order, with the names themselves in name order.
//
// It is one function because it is one mechanic, and all three owners' recorders
// call it. A second copy per owner would agree today and would be free to diverge
// on the deduplication rule or the ordering — and the divergence would be invisible,
// because each owner's own reading would look correct in isolation.
//
// The names are collected and sorted before the map is filled, so the key order is a
// function of the input rather than of the order the render's passes happened to
// place files in. The order WITHIN an entry is production order, which IS a property
// of the input: a render's blocks are read in a fixed sequence, so the first block
// listed is the one that reached the name first.
func originsByEmittedName(files []EmittedFile) types.OrderedMap[[]string] {
	originsByName := map[string][]string{}
	names := make([]string, 0, len(files))
	for _, f := range files {
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

	var out types.OrderedMap[[]string]
	for _, name := range names {
		out.Set(name, originsByName[name])
	}
	return out
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

	p := &emittedProjection{EmittedFileOriginSet: originsByEmittedName(state.EmittedFiles)}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.byNode[state.NodeID] = p
}

// recordNodeAttachment reduces the completed emission of the render one attachment
// carries on a node to its projection and retains that.
//
// It is called where that render's own emission is complete — after every pass of it
// that can contribute a file has run — and before the caller composes the
// attachment's output prefix onto the names or merges them into any dispatch map.
// That is the span in which the set is this attachment's own: one statement later the
// names are prefixed and the files join what other renders produced.
//
// The owner is supplied rather than derived, because it is not derivable at that
// render: the node identity such a render carries is the HOST component's, so a set
// filed under it would answer about the host while the blocks being read are the
// attachment's.
//
// It READS the state and retains no reference to it, on the reading the node's own
// recorder states: the map it builds is its own and holds only names and block tags.
func (e *emittedProjections) recordNodeAttachment(key nodeAttachmentKey, state *PhaseBState) {
	if e == nil || state == nil {
		return
	}
	projected := originsByEmittedName(state.EmittedFiles)

	e.mu.Lock()
	defer e.mu.Unlock()
	e.byNodeAttachment[key] = &attachmentEmittedProjection{
		AttachmentEmittedFileOriginSet: projected,
		sourceFile:                     artifactFindingFile(state),
	}
}

// recordBoundaryAttachment reduces the completed emission of the render one
// boundary-scope attachment carries to its projection and retains that.
//
// It is called at the same point of that render its node-carried sibling is called
// at, and before the caller routes the output by the flag saying whether an assembly
// consumes it.
//
// The key is rooted at the boundary because that is where the attachment is
// declared; the render itself carries a composite dispatch marker rather than any
// component's node identity, so no node-rooted key could name it.
func (e *emittedProjections) recordBoundaryAttachment(
	key boundaryAttachmentKey, state *PhaseBState,
) {
	if e == nil || state == nil {
		return
	}
	projected := originsByEmittedName(state.EmittedFiles)

	e.mu.Lock()
	defer e.mu.Unlock()
	e.byBoundaryAttachment[key] = &boundaryAttachmentEmittedProjection{
		BoundaryAttachmentEmittedFileOriginSet: projected,
		sourceFile:                             artifactFindingFile(state),
	}
}

// coordinates presents this retention as the three emitted coordinate axes.
//
// It is the one construction seam for those axes, so every site that invokes a rule
// over an emission reaches the same reader. A caller building its own would be a
// second implementation of the name axis, free to differ on the deduplication the
// retention already performed.
func (e *emittedProjections) coordinates() *emittedStageCoordinates {
	return newEmittedStageCoordinates(e)
}

// nodeAttachmentProjection returns one attachment owner's retained emission, and
// reports whether the render reached that owner at all.
//
// The report separates a render that emitted nothing from one the walk never
// reached, on the division the node's own reader draws: the presentation collapses
// the two because a fact accessor returns a value and never a report, while the
// enumeration does not, because an axis over an owner nothing rendered would present
// rules coordinates nothing produced.
func (e *emittedProjections) nodeAttachmentProjection(
	key nodeAttachmentKey,
) (*attachmentEmittedProjection, bool) {
	if e == nil {
		return nil, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.byNodeAttachment[key]
	return p, ok
}

// boundaryAttachmentProjection returns one boundary-scope attachment owner's
// retained emission, and reports whether the render reached that owner at all.
func (e *emittedProjections) boundaryAttachmentProjection(
	key boundaryAttachmentKey,
) (*boundaryAttachmentEmittedProjection, bool) {
	if e == nil {
		return nil, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.byBoundaryAttachment[key]
	return p, ok
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

// AttachmentEmittedFileOriginSet returns the authored input blocks that produced the
// file standing at one output name within the render one attachment carries on a
// node.
//
// It answers from the owner-keyed retention rather than from the node's own, which
// is what keeps this render's emission and the host component's separate: the two
// renders emit under the same node identity, and only the attachment coordinate tells
// them apart.
//
// An owner the retention does not hold, and a name that render emitted nothing
// under, both answer with no blocks — the same answer for the same reason the
// presentation's own contract gives: an accessor returns a value and never a report.
func (v *emittedView) AttachmentEmittedFileOriginSet(
	node string, attachment string, path string,
) []string {
	p, ok := v.emissions.nodeAttachmentProjection(
		nodeAttachmentKey{node: node, attachment: attachment},
	)
	if !ok {
		return nil
	}
	origins, _ := p.AttachmentEmittedFileOriginSet.Get(path)
	return origins
}

// BoundaryAttachmentEmittedFileOriginSet returns the authored input blocks that
// produced the file standing at one output name within the render one boundary-scope
// attachment carries.
//
// It is keyed at the boundary because that is where the attachment is declared, so
// one authored document has one entry however many components the boundary holds.
func (v *emittedView) BoundaryAttachmentEmittedFileOriginSet(
	boundary string, attachment string, path string,
) []string {
	p, ok := v.emissions.boundaryAttachmentProjection(
		boundaryAttachmentKey{boundary: boundary, attachment: attachment},
	)
	if !ok {
		return nil
	}
	origins, _ := p.BoundaryAttachmentEmittedFileOriginSet.Get(path)
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

// NodeAttachmentEmittedPaths returns the output names the render one attachment
// carries on a node emitted, deduplicated, in name order.
//
// It reads the same owner-keyed retention the fact is answered from, for the reason
// the node's own axis reads its own: one reader means the names a rule is invoked
// over and the names its facts are keyed by cannot disagree.
//
// An owner the retention does not hold answers with nothing, which is what a render
// that emitted nothing gives: neither has a name for a rule to be asked about.
// Each name carries the FeatureRecipe the render's blocks were authored in, which
// the retention holds because nothing downstream of that render names it.
func (c *emittedStageCoordinates) NodeAttachmentEmittedPaths(
	node string, attachment string,
) []constraint.AuthoredCoordinate {
	p, ok := c.emissions.nodeAttachmentProjection(
		nodeAttachmentKey{node: node, attachment: attachment},
	)
	if !ok {
		return nil
	}
	return emittedNameCoordinates(p.AttachmentEmittedFileOriginSet.Keys(), p.sourceFile)
}

// BoundaryAttachmentEmittedPaths returns the output names the render one
// boundary-scope attachment carries emitted, deduplicated, in name order, each
// carrying the FeatureRecipe those blocks were authored in.
func (c *emittedStageCoordinates) BoundaryAttachmentEmittedPaths(
	boundary string, attachment string,
) []constraint.AuthoredCoordinate {
	p, ok := c.emissions.boundaryAttachmentProjection(
		boundaryAttachmentKey{boundary: boundary, attachment: attachment},
	)
	if !ok {
		return nil
	}
	return emittedNameCoordinates(p.BoundaryAttachmentEmittedFileOriginSet.Keys(), p.sourceFile)
}

// emittedNameCoordinates pairs each output name with the document its blocks were
// authored in.
//
// It is one function because both attachment axes need the identical pairing, and a
// second copy would be free to differ on whether a name contributes a locator
// segment — which it does not: a name is output, and no document holds it as a key.
func emittedNameCoordinates(names []string, sourceFile string) []constraint.AuthoredCoordinate {
	out := make([]constraint.AuthoredCoordinate, 0, len(names))
	for _, name := range names {
		out = append(out, constraint.AuthoredCoordinate{Value: name, SourceFile: sourceFile})
	}
	return out
}

// The enumeration is held to the three axes the rules over a render's own emissions
// are keyed by, so an axis that stops being satisfied fails to compile here rather
// than at the invocation that needed it.
var (
	_ constraint.NodeEmittedPathEnumerator               = (*emittedStageCoordinates)(nil)
	_ constraint.NodeAttachmentEmittedPathEnumerator     = (*emittedStageCoordinates)(nil)
	_ constraint.BoundaryAttachmentEmittedPathEnumerator = (*emittedStageCoordinates)(nil)
)
