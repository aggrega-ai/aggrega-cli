// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// WriteLockfile writes lf to path in canonical (deterministic) form.
// Canonical form:
//   - Top-level keys emitted in alphabetical order (driven by struct
//     field declaration order in types.go).
//   - Slice fields sorted by their canonical key: Entities,
//     SchemaLibraries, MappingLibraries, WasmPlugins by URI;
//     SourceArtifacts by the composite (Kind, URL, PinnedVersion).
//   - Inside each Entity / WasmPlugin: ReferencedFrom sorted
//     lexicographically.
//   - Inside each SchemaLibrary / MappingLibrary: Consumers sorted by
//     ConsumerURI; within each LibraryConsumer, Fragments sorted
//     lexicographically.
//   - Inside each SourceArtifact: ReferencedBy sorted by the
//     composite (URI, Field).
//
// Determinism scope: two independent WriteLockfile invocations
// against the same in-memory *Lockfile produce byte-identical bytes.
// Pull-time determinism — same Release → same lockfile bytes over
// time — is NOT a target: GeneratedAt records wall-clock provenance
// and intentionally changes between pulls.
//
// WriteLockfile deep-copies lf before sorting so the caller's value
// is not mutated.
func WriteLockfile(path string, lf *Lockfile) error {
	if lf == nil {
		return fmt.Errorf("lockfile: WriteLockfile called with nil *Lockfile")
	}

	canonical := canonicalise(lf)
	out, err := yaml.Marshal(canonical)
	if err != nil {
		return fmt.Errorf("lockfile: marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("lockfile: write %s: %w", path, err)
	}
	return nil
}

// canonicalise returns a deep-copy of lf with every slice sorted by
// its canonical key. The caller's value is not mutated — every
// section slice is reallocated, and every slice field nested inside
// (ReferencedFrom, Consumers, Fragments) is also reallocated via
// sortedCopy or canonicaliseConsumers.
//
// Maintenance hazard: any new field added to Entity / SchemaLibrary
// / MappingLibrary / WasmPlugin / LibraryConsumer that holds a slice
// MUST be reallocated here. The outer struct-copy via `entryCopy := m`
// clones scalar fields but shares slice headers, so without explicit
// reallocation a subsequent sort would mutate the caller's slice. The
// existing sortedCopy helper covers []string; new shapes need
// analogous deep-copy helpers.
func canonicalise(lf *Lockfile) *Lockfile {
	out := &Lockfile{
		GeneratedAt: lf.GeneratedAt,
		Release:     lf.Release,
		Version:     lf.Version,
	}

	out.Entities = make([]Entity, len(lf.Entities))
	for i, e := range lf.Entities {
		entryCopy := e
		entryCopy.ReferencedFrom = sortedCopy(e.ReferencedFrom)
		out.Entities[i] = entryCopy
	}
	sort.Slice(out.Entities, func(i, j int) bool {
		return out.Entities[i].URI < out.Entities[j].URI
	})

	out.SchemaLibraries = make([]SchemaLibrary, len(lf.SchemaLibraries))
	for i, l := range lf.SchemaLibraries {
		entryCopy := l
		entryCopy.Consumers = canonicaliseConsumers(l.Consumers)
		out.SchemaLibraries[i] = entryCopy
	}
	sort.Slice(out.SchemaLibraries, func(i, j int) bool {
		return out.SchemaLibraries[i].URI < out.SchemaLibraries[j].URI
	})

	out.MappingLibraries = make([]MappingLibrary, len(lf.MappingLibraries))
	for i, l := range lf.MappingLibraries {
		entryCopy := l
		entryCopy.Consumers = canonicaliseConsumers(l.Consumers)
		out.MappingLibraries[i] = entryCopy
	}
	sort.Slice(out.MappingLibraries, func(i, j int) bool {
		return out.MappingLibraries[i].URI < out.MappingLibraries[j].URI
	})

	out.SourceArtifacts = make([]SourceArtifact, len(lf.SourceArtifacts))
	for i, sa := range lf.SourceArtifacts {
		entryCopy := sa
		entryCopy.ReferencedBy = canonicaliseReferencedBy(sa.ReferencedBy)
		out.SourceArtifacts[i] = entryCopy
	}
	sort.Slice(out.SourceArtifacts, func(i, j int) bool {
		a, b := out.SourceArtifacts[i], out.SourceArtifacts[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.URL != b.URL {
			return a.URL < b.URL
		}
		return a.PinnedVersion < b.PinnedVersion
	})

	out.WasmPlugins = make([]WasmPlugin, len(lf.WasmPlugins))
	for i, p := range lf.WasmPlugins {
		entryCopy := p
		entryCopy.ReferencedFrom = sortedCopy(p.ReferencedFrom)
		out.WasmPlugins[i] = entryCopy
	}
	sort.Slice(out.WasmPlugins, func(i, j int) bool {
		return out.WasmPlugins[i].URI < out.WasmPlugins[j].URI
	})

	return out
}

// canonicaliseReferencedBy returns a fresh slice of
// SourceArtifactReference values sorted by the composite (URI,
// Field). Two independent invocations against the same input produce
// byte-identical output.
func canonicaliseReferencedBy(in []SourceArtifactReference) []SourceArtifactReference {
	out := make([]SourceArtifactReference, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.URI != b.URI {
			return a.URI < b.URI
		}
		return a.Field < b.Field
	})
	return out
}

func canonicaliseConsumers(in []LibraryConsumer) []LibraryConsumer {
	out := make([]LibraryConsumer, len(in))
	for i, c := range in {
		out[i] = LibraryConsumer{
			ConsumerURI: c.ConsumerURI,
			Fragments:   sortedCopy(c.Fragments),
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ConsumerURI < out[j].ConsumerURI
	})
	return out
}

// sortedCopy returns a fresh, lexicographically-sorted []string.
// Always allocates a new backing array (even for empty input) so
// callers receive a slice the receiver may freely mutate without
// aliasing the input. The "Copy" in the name is honest at every
// input size.
func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}
