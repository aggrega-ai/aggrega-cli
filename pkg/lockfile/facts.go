// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

// VerifiedFactRow is a mutable view of one pinned lockfile row's verified facts:
// its identity and content pin, plus read and write access to the two slots a pull
// records into — the verified signature and the verified retroactive deprecation
// marker.
//
// The two slots are reached through methods rather than exposed as fields because
// the view addresses the row in place: recording a fact through it writes the
// lockfile row itself, and a copied-out field would silently record nothing.
type VerifiedFactRow struct {
	// Section is the lockfile section the row sits in, named exactly as the
	// Lockfile field is declared — "Entities", "SchemaLibraries", "Plugins" — which
	// is also the name the closure's own enumeration of its verifiable axes gives
	// the section the row's reference was resolved on (closure.VerifiableAxis.Axis).
	// A pass holding a closure reference finds its row by the pair, so an entity row
	// and a plugin row recording the same reference are never read as one.
	Section string
	// URI is the row's canonical identity — the first key a pull's discovered facts
	// are joined onto the row by; a row's ManifestDigest is the second.
	URI string
	// Digest is the row's pinned content digest. A caller carrying a fact forward
	// across pulls compares it, because a changed pin is a different artifact whose
	// status must be re-discovered rather than inherited.
	Digest string
	// ManifestDigest is the row's recorded OCI manifest digest, empty where the row
	// records none. Both facts a pull discovers are read off that manifest — a
	// signature and a deprecation marker are referrers of it — so a caller carrying
	// a fact forward compares it as well.
	ManifestDigest string

	deprecation **DeprecationRecord
	signature   **VerifiedSignature
}

// Deprecation returns the row's recorded deprecation marker, or nil when the row
// carries none.
func (r VerifiedFactRow) Deprecation() *DeprecationRecord {
	return *r.deprecation
}

// RecordDeprecation records a verified deprecation marker on the row.
func (r VerifiedFactRow) RecordDeprecation(rec *DeprecationRecord) {
	*r.deprecation = rec
}

// Signature returns the row's recorded signature fact, or nil when the row
// carries none.
func (r VerifiedFactRow) Signature() *VerifiedSignature {
	return *r.signature
}

// RecordSignature records a verified signature fact on the row.
func (r VerifiedFactRow) RecordSignature(sig *VerifiedSignature) {
	*r.signature = sig
}

// RowKey names one fact-bearing row by the section it sits in and the reference it
// records — the pair a closure reference finds its own row by, so an entity row and
// a plugin row recording the same reference are two keys.
type RowKey struct {
	// Section is the row's VerifiedFactRow.Section.
	Section string
	// URI is the row's reference.
	URI string
}

// RecordedSignatures returns the signature fact every fact-bearing row records,
// keyed by the row's section and reference, with nil for a row that records none
// and no key for a reference no row records. A nil lockfile records nothing.
//
// It is the one reading of which rows record a signature. The build's check of
// recorded signatures reads a row's fact through it to decide whether to re-hash a
// bundle or hold the row to the signing requirement, and a pull reads it after
// carrying the prior lockfile's facts forward to decide which rows the content
// cache served it must ask a registry about. Reading the rows two ways could leave
// a row the pull does not ask about that the build then refuses.
func (lf *Lockfile) RecordedSignatures() map[RowKey]*VerifiedSignature {
	if lf == nil {
		return map[RowKey]*VerifiedSignature{}
	}
	rows := lf.VerifiedFactRows()
	recorded := make(map[RowKey]*VerifiedSignature, len(rows))
	for _, row := range rows {
		recorded[RowKey{Section: row.Section, URI: row.URI}] = row.Signature()
	}
	return recorded
}

// VerifiedFactRows returns a mutable view of every lockfile row that records the
// verified facts a pull discovers — the catalog entities, the four library
// sections, and the Wasm plugins.
//
// It is the single enumeration of those sections. Every pass over those facts needs
// it — the signature fold, the marker fold, the carry-forward of both facts across a
// warm-cache re-pull, the build-time report, and the reading of which rows record a
// signature (RecordedSignatures) — and each one naming the sections itself is how a
// section came to be covered by some passes and not others: the pass that missed it
// looked no different from the passes that did not. Consuming this
// instead means a section is either in this enumeration or in none of the passes,
// which is a difference a reader can see in one place.
//
// The returned views address rows in the receiver, so recording a fact through one
// writes the lockfile. They are invalidated by anything that reallocates a section
// slice; a caller that appends to a section takes the views again afterwards.
func (lf *Lockfile) VerifiedFactRows() []VerifiedFactRow {
	total := len(lf.Entities) + len(lf.SchemaLibraries) + len(lf.MappingLibraries) +
		len(lf.WorkloadLibraries) + len(lf.ArtifactLibraries) + len(lf.Plugins)
	rows := make([]VerifiedFactRow, 0, total)

	for i := range lf.Entities {
		rows = append(rows, VerifiedFactRow{
			Section:        "Entities",
			URI:            lf.Entities[i].URI,
			Digest:         lf.Entities[i].Digest,
			ManifestDigest: lf.Entities[i].ManifestDigest,
			deprecation:    &lf.Entities[i].Deprecation,
			signature:      &lf.Entities[i].Signature,
		})
	}
	for i := range lf.SchemaLibraries {
		rows = append(rows, VerifiedFactRow{
			Section:        "SchemaLibraries",
			URI:            lf.SchemaLibraries[i].URI,
			Digest:         lf.SchemaLibraries[i].Digest,
			ManifestDigest: lf.SchemaLibraries[i].ManifestDigest,
			deprecation:    &lf.SchemaLibraries[i].Deprecation,
			signature:      &lf.SchemaLibraries[i].Signature,
		})
	}
	for i := range lf.MappingLibraries {
		rows = append(rows, VerifiedFactRow{
			Section:        "MappingLibraries",
			URI:            lf.MappingLibraries[i].URI,
			Digest:         lf.MappingLibraries[i].Digest,
			ManifestDigest: lf.MappingLibraries[i].ManifestDigest,
			deprecation:    &lf.MappingLibraries[i].Deprecation,
			signature:      &lf.MappingLibraries[i].Signature,
		})
	}
	for i := range lf.WorkloadLibraries {
		rows = append(rows, VerifiedFactRow{
			Section:        "WorkloadLibraries",
			URI:            lf.WorkloadLibraries[i].URI,
			Digest:         lf.WorkloadLibraries[i].Digest,
			ManifestDigest: lf.WorkloadLibraries[i].ManifestDigest,
			deprecation:    &lf.WorkloadLibraries[i].Deprecation,
			signature:      &lf.WorkloadLibraries[i].Signature,
		})
	}
	for i := range lf.ArtifactLibraries {
		rows = append(rows, VerifiedFactRow{
			Section:        "ArtifactLibraries",
			URI:            lf.ArtifactLibraries[i].URI,
			Digest:         lf.ArtifactLibraries[i].Digest,
			ManifestDigest: lf.ArtifactLibraries[i].ManifestDigest,
			deprecation:    &lf.ArtifactLibraries[i].Deprecation,
			signature:      &lf.ArtifactLibraries[i].Signature,
		})
	}
	for i := range lf.Plugins {
		rows = append(rows, VerifiedFactRow{
			Section:        "Plugins",
			URI:            lf.Plugins[i].URI,
			Digest:         lf.Plugins[i].Digest,
			ManifestDigest: lf.Plugins[i].ManifestDigest,
			deprecation:    &lf.Plugins[i].Deprecation,
			signature:      &lf.Plugins[i].Signature,
		})
	}
	return rows
}
