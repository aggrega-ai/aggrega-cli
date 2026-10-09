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

// VerifiedFactRows returns a mutable view of every lockfile row that records the
// verified facts a pull discovers — the catalog entities, the four library
// sections, and the Wasm plugins.
//
// It is the single enumeration of those sections. Four passes need it — the
// signature fold, the marker fold, the carry-forward of both facts across a
// warm-cache re-pull, and the build-time report — and each one naming the sections
// itself is how a section came to be covered by three of the four: the pass that
// missed it looked no different from the passes that did not. Consuming this
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
			URI:            lf.Entities[i].URI,
			Digest:         lf.Entities[i].Digest,
			ManifestDigest: lf.Entities[i].ManifestDigest,
			deprecation:    &lf.Entities[i].Deprecation,
			signature:      &lf.Entities[i].Signature,
		})
	}
	for i := range lf.SchemaLibraries {
		rows = append(rows, VerifiedFactRow{
			URI:            lf.SchemaLibraries[i].URI,
			Digest:         lf.SchemaLibraries[i].Digest,
			ManifestDigest: lf.SchemaLibraries[i].ManifestDigest,
			deprecation:    &lf.SchemaLibraries[i].Deprecation,
			signature:      &lf.SchemaLibraries[i].Signature,
		})
	}
	for i := range lf.MappingLibraries {
		rows = append(rows, VerifiedFactRow{
			URI:            lf.MappingLibraries[i].URI,
			Digest:         lf.MappingLibraries[i].Digest,
			ManifestDigest: lf.MappingLibraries[i].ManifestDigest,
			deprecation:    &lf.MappingLibraries[i].Deprecation,
			signature:      &lf.MappingLibraries[i].Signature,
		})
	}
	for i := range lf.WorkloadLibraries {
		rows = append(rows, VerifiedFactRow{
			URI:            lf.WorkloadLibraries[i].URI,
			Digest:         lf.WorkloadLibraries[i].Digest,
			ManifestDigest: lf.WorkloadLibraries[i].ManifestDigest,
			deprecation:    &lf.WorkloadLibraries[i].Deprecation,
			signature:      &lf.WorkloadLibraries[i].Signature,
		})
	}
	for i := range lf.ArtifactLibraries {
		rows = append(rows, VerifiedFactRow{
			URI:            lf.ArtifactLibraries[i].URI,
			Digest:         lf.ArtifactLibraries[i].Digest,
			ManifestDigest: lf.ArtifactLibraries[i].ManifestDigest,
			deprecation:    &lf.ArtifactLibraries[i].Deprecation,
			signature:      &lf.ArtifactLibraries[i].Signature,
		})
	}
	for i := range lf.Plugins {
		rows = append(rows, VerifiedFactRow{
			URI:            lf.Plugins[i].URI,
			Digest:         lf.Plugins[i].Digest,
			ManifestDigest: lf.Plugins[i].ManifestDigest,
			deprecation:    &lf.Plugins[i].Deprecation,
			signature:      &lf.Plugins[i].Signature,
		})
	}
	return rows
}
