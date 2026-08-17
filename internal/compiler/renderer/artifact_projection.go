// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "sync"

// The artifacts a node projects are produced inside the render's own artifacts
// walk: each entry is resolved to the content standing at it, evaluated against
// the node's render context, and handed to the provider that decides the file it
// becomes. Only the provider's returned files survive that walk, so anything
// asking what an artifact evaluated to has nothing left to read by the time the
// render finishes.
//
// What survives here is a PROJECTION of that walk and never the walk's own
// state. The distinction bounds the cost, and it bounds it harder here than for
// the state a node reaches before its bytes exist: this retention carries
// EVALUATED bytes per artifact, where that one carries the template text before
// anything evaluates it. Retaining the walk's state would grow with everything
// the walk accumulates — every entry's provenance key sets, its origin, the
// projection handed to the provider — while retaining one field per declared
// question grows with the questions, which are a fixed and small set.
//
// The field names below are the correspondence, and they are checked rather than
// trusted: each names the accessor that reads it, and a guard beside this file
// compares the retained set against the framework's own declarations in both
// directions. A question declared here later therefore cannot leave the
// retention behind silently — the guard names the field nobody added.

// artifactProjection is what one node's artifacts walk is reduced to: the
// artifact names that walk projected, and the bytes standing at each of them.
//
// Both fields are values read out of the walk. Neither is a carrier the walk can
// be reached back through, which is the property that keeps this a projection: a
// reader holding one of these can answer the declared questions and can reach
// nothing else.
type artifactProjection struct {
	// ArtifactProjectionNameSet are the artifact names the node projected, in
	// the order the walk reached them — which is the order the Recipe declares
	// its entries, and within an entry that consumed a library bundle, the order
	// that bundle declares its files.
	//
	// This slice carries the order and the membership both, and it is the only
	// thing that does. The content field below is keyed rather than ordered
	// precisely so the order has one home: a keyed structure read for its order
	// would make the answer depend on iteration instead of on the input.
	ArtifactProjectionNameSet []string

	// ArtifactProjectionContent is the bytes each projected name evaluated to,
	// keyed by that name.
	//
	// It is a plain keyed map rather than an ordered one because nothing reads it
	// in order: every read arrives with a name already in hand, supplied by the
	// name set above, so this map is never walked. That is what keeps the
	// byte-identical-output obligation on the ordered field, where the order
	// actually is.
	//
	// The bytes are the same ones the projection handed the provider rather than
	// a copy of them. A fact returning bytes hands back the buffer the
	// compilation is already holding, and a copy taken here would be the single
	// copy in a surface whose contract is that no read pays for one.
	ArtifactProjectionContent map[string][]byte
}

// artifactProjections is one render's retained set of those projections, keyed
// by node.
//
// It is installed once per render and lives for the whole of it. Each node's
// entries are written as the artifacts walk reaches them, so the set answers
// from whatever the walk has recorded by the time a reader asks.
type artifactProjections struct {
	// mu serialises writes. The node walk is sequential today, so the lock is
	// never contended; it is here because a map written from two goroutines is a
	// crash rather than a wrong answer, and the walk's sequence is not a property
	// this value can assert about its callers.
	mu     sync.Mutex
	byNode map[string]*artifactProjection
}

// newArtifactProjections returns an empty set for one render.
func newArtifactProjections() *artifactProjections {
	return &artifactProjections{byNode: map[string]*artifactProjection{}}
}

// record retains one projected artifact under the node that projected it.
//
// # The recording position is the whole of the correctness here
//
// It is called from the artifacts walk at the point an entry becomes part of the
// projection — after the walk has skipped an entry that evaluated to nothing,
// and after it has refused a name it had already emitted. So the retained key
// set is the RENDER's, which is not the set of entries the Recipe declared: the
// text standing at each entry is recorded earlier in the same walk, before both
// of those, so an entry evaluating to whitespace is in that earlier key set and
// absent from this one. A retention taken beside the authored entry would
// over-report exactly the entries the render dropped, and would do it silently,
// because an over-reported name answers with plausible bytes rather than with
// nothing.
//
// # Retention is unconditional across the subject set
//
// Every artifact the walk projects is recorded, with no condition deciding which
// are worth it — a wrong condition there would produce no answer rather than a
// wrong one, and a rule that never runs reads exactly like a rule that ran and
// approved. A node whose Recipe declares no artifacts contributes no artifact to
// the subject set and so is recorded as nothing, which is the same answer it
// would give if it were recorded holding an empty set.
//
// # A nil set records nothing, and that is the discriminator rather than an escape
//
// An attachment's render walks this same producer under the HOST node's
// identity, around a Recipe synthesized from the attachment's own FeatureRecipe.
// The declared facts are over a node's own Recipe, so recording that walk would
// answer one question with another's artifacts under a single node key. The
// attachment's render builds its own state and carries no retention, which is
// what keeps the two apart.
func (a *artifactProjections) record(nodeID, name string, content []byte) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.byNode[nodeID]
	if !ok {
		p = &artifactProjection{ArtifactProjectionContent: map[string][]byte{}}
		a.byNode[nodeID] = p
	}
	// The membership test is what makes the two fields one key set by
	// construction rather than by the caller's discipline. The walk's own
	// collision guard halts on a name one Recipe emits twice, so a repeat does
	// not arrive from there; keeping the test means the name set cannot acquire a
	// duplicate from any future caller either, which is the failure a reader of
	// the name set could not see.
	if _, held := p.ArtifactProjectionContent[name]; !held {
		p.ArtifactProjectionNameSet = append(p.ArtifactProjectionNameSet, name)
	}
	p.ArtifactProjectionContent[name] = content
}

// projection returns one node's retained projection, and reports whether the
// node projected anything at all.
func (a *artifactProjections) projection(nodeID string) (artifactProjection, bool) {
	if a == nil {
		return artifactProjection{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.byNode[nodeID]
	if !ok {
		return artifactProjection{}, false
	}
	return *p, true
}

// recordedNodes reports how many nodes the set holds.
func (a *artifactProjections) recordedNodes() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.byNode)
}
