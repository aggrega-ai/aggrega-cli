// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile

import (
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// The lockfile pins each library axis in its own section, under its own key and
// its own Go type, because the section names are part of the file's wire form and
// a reader of a lockfile sees them. That shape is fixed; what is not fixed is
// having every pass over those sections written four times.
//
// librarySections below is the one place the declared axis set is bound to those
// four typed sections. Every pass that treats the sections alike — drift
// reporting, the pin projection, canonical ordering — ranges over it, so the
// passes stay axis-agnostic while the wire form stays exactly as it is. The
// coverage assertion beside it requires the binding to name every declared axis,
// so an axis added to the set cannot end up pinned by a section that no pass
// reaches.

// libraryRowView exposes the parts of a library row that canonicalisation must
// reallocate. A row's outer struct copy clones its scalars but shares slice
// headers and pointers, so each of these is replaced rather than copied.
type libraryRowView struct {
	URI         string
	Consumers   *[]LibraryConsumer
	Signature   **VerifiedSignature
	Deprecation **DeprecationRecord
}

// librarySection binds one declared library axis to the lockfile section that
// pins it. Each field erases the section's concrete row type behind a closure, so
// the passes above can be written once over four distinct types.
type librarySection struct {
	axis closure.LibraryAxis
	// keys projects the section's rows onto the identity triple drift compares on.
	keys func(*Lockfile) []driftKey
	// canonicalise deep-copies and sorts the section from src into dst.
	canonicalise func(dst, src *Lockfile)
}

// librarySections binds every declared library axis to its lockfile section.
func librarySections() []librarySection {
	return []librarySection{
		{
			axis: closure.LibraryAxisNamed("SchemaLibraries"),
			keys: func(lf *Lockfile) []driftKey { return driftKeysOf(lf.SchemaLibraries, schemaLibraryKey) },
			canonicalise: func(dst, src *Lockfile) {
				dst.SchemaLibraries = canonicaliseLibrarySection(src.SchemaLibraries, schemaLibraryView)
			},
		},
		{
			axis: closure.LibraryAxisNamed("MappingLibraries"),
			keys: func(lf *Lockfile) []driftKey { return driftKeysOf(lf.MappingLibraries, mappingLibraryKey) },
			canonicalise: func(dst, src *Lockfile) {
				dst.MappingLibraries = canonicaliseLibrarySection(src.MappingLibraries, mappingLibraryView)
			},
		},
		{
			axis: closure.LibraryAxisNamed("WorkloadLibraries"),
			keys: func(lf *Lockfile) []driftKey { return driftKeysOf(lf.WorkloadLibraries, workloadLibraryKey) },
			canonicalise: func(dst, src *Lockfile) {
				dst.WorkloadLibraries = canonicaliseLibrarySection(src.WorkloadLibraries, workloadLibraryView)
			},
		},
		{
			axis: closure.LibraryAxisNamed("ArtifactLibraries"),
			keys: func(lf *Lockfile) []driftKey { return driftKeysOf(lf.ArtifactLibraries, artifactLibraryKey) },
			canonicalise: func(dst, src *Lockfile) {
				dst.ArtifactLibraries = canonicaliseLibrarySection(src.ArtifactLibraries, artifactLibraryView)
			},
		},
	}
}

// LibraryPin is one row of a library section as a reader of its pins sees it: the
// reference, the version it resolved to, and the content digest it is pinned at.
type LibraryPin struct {
	URI             string
	ResolvedVersion string
	Digest          string
}

// LibraryPins returns the rows lf pins on axis, read through the one binding of the
// declared library axes to the lockfile's sections, and false for an axis no
// section binds. A reader ranging over closure.LibraryAxes reads every section
// through it, so it names no section itself and cannot leave one out.
func LibraryPins(lf *Lockfile, axis closure.LibraryAxis) ([]LibraryPin, bool) {
	for _, sec := range librarySections() {
		if sec.axis.Axis != axis.Axis {
			continue
		}
		keys := sec.keys(lf)
		out := make([]LibraryPin, 0, len(keys))
		for _, k := range keys {
			out = append(out, LibraryPin{URI: k.URI, ResolvedVersion: k.ResolvedVersion, Digest: k.Digest})
		}
		return out, true
	}
	return nil, false
}

func schemaLibraryKey(l SchemaLibrary) driftKey {
	return driftKey{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest}
}
func mappingLibraryKey(l MappingLibrary) driftKey {
	return driftKey{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest}
}
func workloadLibraryKey(l WorkloadLibrary) driftKey {
	return driftKey{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest}
}
func artifactLibraryKey(l ArtifactLibrary) driftKey {
	return driftKey{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest}
}

func schemaLibraryView(l *SchemaLibrary) libraryRowView {
	return libraryRowView{l.URI, &l.Consumers, &l.Signature, &l.Deprecation}
}
func mappingLibraryView(l *MappingLibrary) libraryRowView {
	return libraryRowView{l.URI, &l.Consumers, &l.Signature, &l.Deprecation}
}
func workloadLibraryView(l *WorkloadLibrary) libraryRowView {
	return libraryRowView{l.URI, &l.Consumers, &l.Signature, &l.Deprecation}
}
func artifactLibraryView(l *ArtifactLibrary) libraryRowView {
	return libraryRowView{l.URI, &l.Consumers, &l.Signature, &l.Deprecation}
}

// driftKeysOf projects a typed section onto the identity triple.
func driftKeysOf[T any](rows []T, key func(T) driftKey) []driftKey {
	out := make([]driftKey, 0, len(rows))
	for _, r := range rows {
		out = append(out, key(r))
	}
	return out
}

// canonicaliseLibrarySection deep-copies a library section and sorts it by URI.
//
// Every slice and pointer a row holds is reallocated, so sorting the result cannot
// reach back into the caller's value and a caller mutating the result cannot reach
// into the input. A new slice- or pointer-valued field on a library row needs an
// analogous clone here and in libraryRowView.
func canonicaliseLibrarySection[T any](rows []T, view func(*T) libraryRowView) []T {
	out := make([]T, len(rows))
	copy(out, rows)
	for i := range out {
		v := view(&out[i])
		*v.Consumers = canonicaliseConsumers(*v.Consumers)
		*v.Signature = cloneSignature(*v.Signature)
		*v.Deprecation = cloneDeprecation(*v.Deprecation)
	}
	sort.Slice(out, func(i, j int) bool { return view(&out[i]).URI < view(&out[j]).URI })
	return out
}
