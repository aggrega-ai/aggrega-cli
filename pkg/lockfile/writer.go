// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"sort"

	"go.yaml.in/yaml/v3"
)

// WriteLockfile writes lf to path in canonical (deterministic) form.
// Canonical form:
//   - Top-level keys emitted in alphabetical order (driven by struct
//     field declaration order in types.go).
//   - Slice fields sorted by their canonical key: Entities,
//     SchemaLibraries, MappingLibraries, WorkloadLibraries, Plugins by URI;
//     SourceArtifacts by the composite (Kind, URL, Chart, PinnedVersion);
//     ExternalInputs by URL; VerdictInputs by (Plugin, Role), with each
//     entry's Sources by base name. Mutators is the exception: its order
//     is semantic (each transform runner applies its role's attachments
//     in list order), so it is copied in author order and never sorted.
//   - Inside each Entity / Plugin: ReferencedFrom sorted
//     lexicographically.
//   - Inside each ExternalInput: RequiredBy sorted lexicographically
//     and deduplicated.
//   - Inside each SchemaLibrary / MappingLibrary / WorkloadLibrary /
//     ArtifactsLibrary: Consumers sorted by ConsumerURI; within each
//     LibraryConsumer, Fragments sorted lexicographically.
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
// Maintenance hazard: any new field added to Entity, ExternalInput, Plugin or
// LibraryConsumer that holds a slice OR a pointer MUST be cloned here. The outer
// struct-copy via `entryCopy := m` clones scalar fields but shares slice headers
// and pointers, so without an explicit clone a subsequent sort would mutate the
// caller's slice and a caller mutating the output could reach back into the input.
// The existing sortedCopy helper covers []string, cloneSignature and
// cloneDeprecation cover the two recorded-fact pointers; new shapes need analogous
// deep-copy helpers.
//
// The same hazard on a library row is handled beside the section binding, where
// canonicaliseLibrarySection reallocates each row's slice and pointer fields for
// every axis at once.
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
		entryCopy.Signature = cloneSignature(e.Signature)
		entryCopy.Deprecation = cloneDeprecation(e.Deprecation)
		out.Entities[i] = entryCopy
	}
	sort.Slice(out.Entities, func(i, j int) bool {
		return out.Entities[i].URI < out.Entities[j].URI
	})

	out.ExternalInputs = make([]ExternalInput, len(lf.ExternalInputs))
	for i, x := range lf.ExternalInputs {
		entryCopy := x
		entryCopy.RequiredBy = sortedUniqueCopy(x.RequiredBy)
		out.ExternalInputs[i] = entryCopy
	}
	sort.Slice(out.ExternalInputs, func(i, j int) bool {
		return out.ExternalInputs[i].URL < out.ExternalInputs[j].URL
	})

	// Every library section, deep-copied and sorted through the one binding that
	// names them, so a section cannot be left un-canonicalised by omission.
	for _, sec := range librarySections() {
		sec.canonicalise(out, lf)
	}

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
		if a.Chart != b.Chart {
			return a.Chart < b.Chart
		}
		return a.PinnedVersion < b.PinnedVersion
	})

	out.Plugins = make([]Plugin, len(lf.Plugins))
	for i, p := range lf.Plugins {
		entryCopy := p
		entryCopy.ReferencedFrom = sortedCopy(p.ReferencedFrom)
		entryCopy.Signature = cloneSignature(p.Signature)
		entryCopy.Deprecation = cloneDeprecation(p.Deprecation)
		out.Plugins[i] = entryCopy
	}
	sort.Slice(out.Plugins, func(i, j int) bool {
		return out.Plugins[i].URI < out.Plugins[j].URI
	})

	out.Providers = make([]ProviderBinding, len(lf.Providers))
	copy(out.Providers, lf.Providers)
	sort.Slice(out.Providers, func(i, j int) bool {
		return out.Providers[i].Capability < out.Providers[j].Capability
	})

	// Mutators carries a semantic order (the mutator runner applies attachments
	// in list order), so it is copied in author order and deliberately NOT
	// sorted — unlike every URI-sorted section above. Two independent
	// canonicalise calls over the same input still produce byte-identical output
	// because the copy preserves the same order each time. The shallow copy
	// shares each attachment's Settings map and Sources slice with the caller —
	// the same aliasing the Entities / Plugins shallow copies carry above — which
	// is safe because the canonicalised output is written once (marshalled) and
	// never mutated. The pinned sources are recorded in base-name order at pull,
	// so the marshalled bytes stay deterministic without a re-sort here.
	out.Mutators = make([]MutatorAttachment, len(lf.Mutators))
	copy(out.Mutators, lf.Mutators)

	// VerdictInputs is provenance (not a semantic-order build input), so unlike
	// Mutators it is sorted for a canonical form: by plugin coordinate, then role,
	// so the same plugin attached at both verdict stages orders deterministically.
	// Each entry's Sources slice is reallocated so the sort cannot mutate the
	// caller's slice, and is ordered by source base name.
	out.VerdictInputs = make([]VerdictInput, len(lf.VerdictInputs))
	for i, v := range lf.VerdictInputs {
		entryCopy := v
		entryCopy.Sources = make([]VerdictSourcePin, len(v.Sources))
		copy(entryCopy.Sources, v.Sources)
		sort.Slice(entryCopy.Sources, func(a, b int) bool {
			return entryCopy.Sources[a].Path < entryCopy.Sources[b].Path
		})
		out.VerdictInputs[i] = entryCopy
	}
	sort.Slice(out.VerdictInputs, func(i, j int) bool {
		a, b := out.VerdictInputs[i], out.VerdictInputs[j]
		if a.Plugin != b.Plugin {
			return a.Plugin < b.Plugin
		}
		return a.Role < b.Role
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
	out.Signer = cloneSignature(in.Signer)
	return &out
}

// cloneSignature deep-copies a VerifiedSignature pointer so a caller mutating the
// canonicalised output cannot reach back into the input. The struct-copy of a row
// shares the pointer, so every marker-bearing section clones through here rather
// than spelling the clone per section — six copies of one two-line rule is six
// places for a newly added section to be forgotten. The value holds no slices, so
// a shallow clone of the struct suffices. Returns nil for a nil input (a row with
// no recorded signature).
func cloneSignature(in *VerifiedSignature) *VerifiedSignature {
	if in == nil {
		return nil
	}
	out := *in
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

// sortedUniqueCopy returns a fresh, lexicographically-sorted []string
// with duplicates removed. Like sortedCopy it always allocates a new
// backing array so callers receive a slice free of aliasing; unlike
// sortedCopy it deduplicates, because ExternalInput provenance
// accumulates the same requiring site across repeated fetches and the
// canonical form records each site once.
func sortedUniqueCopy(in []string) []string {
	return slices.Compact(sortedCopy(in))
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
