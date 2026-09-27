// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package recipepin

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Pins are the content pins in force for one entity: the digests a build
// compares the bytes it acquired against.
//
// The lists are the ways an entity depends on bytes it does not itself carry — an
// acquired remote source, a file an artifacts entry imports from the workspace,
// and the files of a local source — and they are separate lists because they are
// keyed differently: a source by its resolved identity, an imported file by the
// artifacts key that consumes it, a local source's file by its path beside the
// entity. They are the same rows [Lock] carries, so a pin does not read one way
// where it is recorded and another where it is used.
//
// Ignore rides beside the three lists without being a fourth: it is the rule
// list, recorded when the entity was pinned, that decides which files under a
// local source directory the local-source list covers. Every reading of that
// directory applies it — a publication, a comparison, a render — and no reading
// compares it with anything; only the pin step writes a new one.
//
// The zero value records nothing, which is what an entity that has never been
// resolved reports. A reader holding it has nothing to compare and concludes
// nothing, which is the ordinary answer rather than an approval.
type Pins struct {
	SourceDigests      []types.SourceDigest
	ArtifactDigests    []types.ArtifactDigest
	LocalSourceDigests []types.LocalSourceDigest
	Ignore             *types.IgnoreRules
}

// Empty reports whether these pins record nothing at all — no pin of any class,
// and no ignore rules.
func (p Pins) Empty() bool {
	return len(p.SourceDigests) == 0 && len(p.ArtifactDigests) == 0 && len(p.LocalSourceDigests) == 0 &&
		p.Ignore == nil
}

// IgnoreRulesOf returns the composed rules a recorded ignore section holds, and
// none for a lock that records no section — the one reading of "which rules does
// this lock apply", so a lock recording no section and one recording an empty
// list read alike.
func IgnoreRulesOf(ignore *types.IgnoreRules) []types.IgnoreRule {
	if ignore == nil {
		return nil
	}
	return ignore.Rules
}

// Lookup answers the pins in force for the entity declared in a named document,
// keyed by that document's path.
//
// The key is the document rather than the entity's identity, and the reason is
// that a lock is defined by the directory it sits in. Two catalog directories
// may declare one identity — the load resolves that by taking the first and
// reports the second as shadowed — so an identity-keyed answer could describe
// the lock beside a document this build never read. That is worse than no
// answer: a comparison against the wrong record is indistinguishable from
// inputs that had not moved.
//
// An entity with no document on disk has no key here. One resolved from a
// registry lives in a cache slot rather than in a file anybody
// authored, so it records nothing — the same answer every other path-keyed
// reading of such an entity gives.
type Lookup map[string]Pins

// Record files the pins in force for the entity declared at documentPath.
//
// A nil lookup and an empty document path both record nothing, so a producer
// that has no lookup to fill, or an entity that was read from no file, needs no
// guard of its own. Pins that record nothing are not filed either: an absent
// entry and an entry carrying two empty lists say the same thing, and keeping
// only one of those spellings is what lets a reader treat a miss as the answer.
func (l Lookup) Record(documentPath string, pins Pins) {
	if l == nil || documentPath == "" || pins.Empty() {
		return
	}
	l[documentPath] = pins
}

// For returns the pins recorded for the entity declared at documentPath, and
// the zero Pins where none are.
//
// A nil lookup and an empty path both answer the zero value, so a caller that
// was handed no lookup, and one holding an entity that was read from no file,
// read the same "nothing recorded" a caller with both reads for an entity that
// was never resolved.
func (l Lookup) For(documentPath string) Pins {
	if l == nil || documentPath == "" {
		return Pins{}
	}
	return l[documentPath]
}
