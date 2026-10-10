// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import "github.com/aggrega-ai/aggrega-cli/pkg/closure"

// entityOrigin is where the lazy view read one indexed entity from: a document in a
// catalog directory, or a lockfile-pinned cache slot. It is the one record of that
// fact, written once per identity by the fold that places the entity in the index —
// the only step that learns which of the two it read, because it is handed the slot
// or no slot — and every provenance question asked of the entity is answered from it
// rather than from an index of its own:
//
//   - whether an author owns a document for it (authoredDocument), which is what
//     the pin step writes beside and a report names as the place it was declared;
//   - what its content paths are anchored at (anchorDocument);
//   - which tier served it (resolutionPath), which is what a pull-time pin decision
//     keys on;
//   - the manifest a pinned slot's bytes were held to when the fold read them
//     (heldManifestDigest), which a closure walk reports as the manifest the
//     reference was read under;
//   - and the source a flattened node records for it (source).
//
// Recording the fact once is what keeps those answers from disagreeing about one
// entity: they are projections of one value rather than separate writes that happen
// to be made in the same callback.
//
// The record is positive. An entity is reported as read from a cache slot because
// the fold saw it arrive from one, never inferred from the absence of a document
// path, so an identity the record holds nothing for keeps the answer its catalog
// gives without a record.
type entityOrigin struct {
	// source is what the fold received as the entity's source: the document path for
	// an entity authored in a catalog directory, and the label naming the lockfile
	// pin for one read from a cache slot, which is not a path anybody could open.
	source string
	// slot is the cache slot a pinned entity was read from, and empty for an entity
	// authored in a catalog directory.
	slot string
	// layoutDocument is the entity's document in the layout of its slot's verified
	// artifact — where a pinned Recipe's or FeatureRecipe's parse was anchored. It is
	// empty for every other kind, and for an authored entity, whose parse is anchored
	// at its own document.
	layoutDocument string
	// manifestDigest is the digest of the manifest persisted beside a pinned slot's
	// bytes, which the fold's verifying read held those bytes to before it placed
	// the entity. It is derived from the manifest the read held, never carried in
	// from the pin, so it is the manifest the reference was actually read under. It
	// is empty for an authored entity, which comes with no manifest.
	manifestDigest string
}

// authoredOrigin is the record of an entity read from a document in a catalog
// directory.
func authoredOrigin(document string) entityOrigin {
	return entityOrigin{source: document}
}

// pinnedOrigin is the record of an entity read from a lockfile-pinned cache slot.
// layoutDocument is the document its parse was anchored at inside the slot's layout,
// and empty for a kind that names no content by a path relative to itself.
func pinnedOrigin(label, slot, layoutDocument string) entityOrigin {
	return entityOrigin{source: label, slot: slot, layoutDocument: layoutDocument}
}

// foldedOrigin is the record the fold writes for an entity it placed: authored at
// src when it was handed no slot, and read from slot, under the label src, when it
// was. layoutDocument and manifestDigest — the digest of the manifest the fold's
// read held the slot's bytes to — are kept only for the second.
func foldedOrigin(src, slot, layoutDocument, manifestDigest string) entityOrigin {
	if slot == "" {
		return authoredOrigin(src)
	}
	origin := pinnedOrigin(src, slot, layoutDocument)
	origin.manifestDigest = manifestDigest
	return origin
}

// heldManifestDigest returns the digest of the manifest a pinned slot's bytes were
// held to when the fold read them, and empty for an entity authored in a catalog
// directory, which no manifest accompanies. A closure walk reports it as the
// manifest the reference was read under, as every other read of the content cache
// reports the manifest it held (cacheHitResolution), so the entry a fold placed is
// described the way the cache tier would have described it.
func (o entityOrigin) heldManifestDigest() string {
	if !o.fromCacheSlot() {
		return ""
	}
	return o.manifestDigest
}

// fromCacheSlot reports whether the entity was read from a lockfile-pinned cache
// slot.
func (o entityOrigin) fromCacheSlot() bool {
	return o.slot != ""
}

// authoredDocument returns the catalog-directory document the entity was authored
// in, and whether it has one. An entity read from a cache slot has none: the slot
// holds what a fetch placed there, which is no document its author edits or commits.
func (o entityOrigin) authoredDocument() (string, bool) {
	if o.fromCacheSlot() || o.source == "" {
		return "", false
	}
	return o.source, true
}

// anchorDocument returns the document the entity's content paths are relative to,
// and whether one is recorded: its authored document, or its document in the layout
// of its cache slot. The second is never an answer to where the entity was declared;
// it is read only to anchor what a source written as a template produces at render.
func (o entityOrigin) anchorDocument() (string, bool) {
	if document, ok := o.authoredDocument(); ok {
		return document, true
	}
	if o.layoutDocument != "" {
		return o.layoutDocument, true
	}
	return "", false
}

// resolutionPath returns the tier a closure walk reports for the entity: the content
// cache for one read from a slot, and a catalog directory otherwise. It is the answer
// the pull's own lookup gives for the same entity, which reaches a pinned one through
// its cache tier, so a pin decision read on either side keys on the same value.
func (o entityOrigin) resolutionPath() string {
	if o.fromCacheSlot() {
		return closure.ResolutionPathCache
	}
	return closure.ResolutionPathCatalog
}
