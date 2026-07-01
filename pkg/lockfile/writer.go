// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	"go.yaml.in/yaml/v3"
)

// WriteLockfile writes lf to path in canonical (deterministic) form.
// Canonical form:
//   - Top-level keys emitted in alphabetical order (driven by struct
//     field declaration order in types.go).
//   - Slice fields sorted by their canonical key: Entities,
//     SchemaLibraries, MappingLibraries, Plugins by URI;
//     SourceArtifacts by the composite (Kind, URL, PinnedVersion).
//   - Inside each Entity / Plugin: ReferencedFrom sorted
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

	out, err := marshalCanonical(lf)
	if err != nil {
		return fmt.Errorf("lockfile: marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("lockfile: write %s: %w", path, err)
	}
	return nil
}

// marshalCanonical returns the exact bytes WriteLockfile writes: the canonical
// (deterministic) form of lf marshalled to YAML. It is the single serialization
// path — WriteLockfile and the byte-identity comparison in
// WriteLockfilePreservingTimestamp both route through it, so the comparison
// cannot drift from what is written.
func marshalCanonical(lf *Lockfile) ([]byte, error) {
	return yaml.Marshal(canonicalise(lf))
}

// WriteLockfilePreservingTimestamp writes lf to path like WriteLockfile, but
// keeps the lockfile byte-idempotent: when a prior lockfile already exists at
// path and is byte-identical to lf modulo the generated_at provenance line, the
// prior generated_at is reused so re-writing an unchanged closure produces a
// byte-identical file (no spurious one-line diff for a committed lockfile). A
// first write (no prior file) or any real change keeps lf's own stamp.
//
// The unchanged-comparison reads marshalCanonical(lf) — the actual bytes that
// will be written — so equality cannot diverge from the serialized output.
func WriteLockfilePreservingTimestamp(path string, lf *Lockfile) error {
	if lf == nil {
		return fmt.Errorf("lockfile: WriteLockfilePreservingTimestamp called with nil *Lockfile")
	}
	if prior, rerr := os.ReadFile(path); rerr == nil {
		next, merr := marshalCanonical(lf)
		if merr == nil && bytes.Equal(stripGeneratedAt(prior), stripGeneratedAt(next)) {
			if priorStamp := generatedAtValue(prior); priorStamp != "" {
				lf.GeneratedAt = priorStamp
			}
		}
	}
	return WriteLockfile(path, lf)
}

// stripGeneratedAt returns b with the single top-level `generated_at:` line
// removed, so two canonical lockfiles can be compared for equality ignoring
// only the wall-clock provenance stamp. generated_at is a column-0 field (the
// sole generated_at: key in the schema), so the prefix match is unambiguous.
func stripGeneratedAt(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, line := range bytes.SplitAfter(b, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("generated_at:")) {
			continue
		}
		out = append(out, line...)
	}
	return out
}

// generatedAtValue returns the logical generated_at timestamp from canonical
// lockfile bytes, or "" when absent or unparsable. It reads the value through
// the same YAML library that writes it — not a raw line slice — so the returned
// string is the unquoted logical value; reassigning it to lf.GeneratedAt and
// re-marshalling reproduces the prior bytes exactly (no quote-doubling).
func generatedAtValue(b []byte) string {
	var probe struct {
		GeneratedAt string `yaml:"generated_at"`
	}
	if err := yaml.Unmarshal(b, &probe); err != nil {
		return ""
	}
	return probe.GeneratedAt
}

// canonicalise returns a deep-copy of lf with every slice sorted by
// its canonical key. The caller's value is not mutated — every
// section slice is reallocated, and every slice field nested inside
// (ReferencedFrom, Consumers, Fragments) is also reallocated via
// sortedCopy or canonicaliseConsumers.
//
// Maintenance hazard: any new field added to Entity / SchemaLibrary
// / MappingLibrary / Plugin / LibraryConsumer that holds a slice
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
		// The Signature pointer is shared by the struct-copy; clone it so a
		// caller mutating the output cannot reach back into the input. The
		// value has no slices, so a shallow clone of the struct suffices.
		if e.Signature != nil {
			sig := *e.Signature
			entryCopy.Signature = &sig
		}
		entryCopy.Deprecation = cloneDeprecation(e.Deprecation)
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

	out.Plugins = make([]Plugin, len(lf.Plugins))
	for i, p := range lf.Plugins {
		entryCopy := p
		entryCopy.ReferencedFrom = sortedCopy(p.ReferencedFrom)
		// The Signature pointer is shared by the struct-copy; clone it so a
		// caller mutating the output cannot reach back into the input. The
		// value has no slices, so a shallow clone of the struct suffices.
		if p.Signature != nil {
			sig := *p.Signature
			entryCopy.Signature = &sig
		}
		entryCopy.Deprecation = cloneDeprecation(p.Deprecation)
		out.Plugins[i] = entryCopy
	}
	sort.Slice(out.Plugins, func(i, j int) bool {
		return out.Plugins[i].URI < out.Plugins[j].URI
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

// cloneDeprecation deep-copies a DeprecationRecord pointer so a caller mutating
// the canonicalised output cannot reach back into the input. The nested Signer
// pointer is cloned too — a shallow struct copy would leave it shared. Returns
// nil for a nil input (an entity with no recorded marker).
func cloneDeprecation(in *DeprecationRecord) *DeprecationRecord {
	if in == nil {
		return nil
	}
	out := *in
	if in.Signer != nil {
		signer := *in.Signer
		out.Signer = &signer
	}
	return &out
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
