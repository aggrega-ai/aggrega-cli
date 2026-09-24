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
//     ArtifactLibrary: Consumers sorted by ConsumerURI; within each
//     LibraryConsumer, Fragments sorted lexicographically.
//   - Inside each SourceArtifact: ReferencedBy sorted by the
//     composite (URI, Field).
//   - Inside Toolchain: CorePlugins sorted by plugin coordinate.
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
	lf.GeneratedAt = generatedAtFor(path, lf)
	return WriteLockfile(path, lf)
}

// CanonicalBytesPreservingTimestamp returns the exact bytes
// WriteLockfilePreservingTimestamp would write to path, and writes nothing.
//
// It is the reporting half of that write, for a caller showing what a resolution
// would put on disk. Both halves take the stamp from generatedAtFor and the
// bytes from marshalCanonical, so what a preview reports and what a write
// performs cannot come apart — a second rendering here would agree today and
// diverge the first time either the canonical form or the stamp rule moved.
//
// The caller's value is left alone, including its stamp: a preview that mutated
// the lockfile it was asked to describe would change the thing under inspection.
// The shallow copy is safe because canonicalise deep-copies every composite field
// before sorting, so nothing here reaches back into the caller's slices.
func CanonicalBytesPreservingTimestamp(path string, lf *Lockfile) ([]byte, error) {
	if lf == nil {
		return nil, fmt.Errorf("lockfile: CanonicalBytesPreservingTimestamp called with nil *Lockfile")
	}
	stamped := *lf
	stamped.GeneratedAt = generatedAtFor(path, lf)
	return marshalCanonical(&stamped)
}

// generatedAtFor returns the provenance stamp a timestamp-preserving write puts
// in lf: the stamp the file at path already carries when the canonical bytes are
// otherwise unchanged, and lf's own stamp otherwise.
//
// It is the single home of that decision. The comparison it makes is against
// marshalCanonical(lf) — the actual bytes that would be written — and lf's own
// stamp cannot affect it, because stripGeneratedAt removes that line from both
// sides before they are compared.
func generatedAtFor(path string, lf *Lockfile) string {
	prior, rerr := os.ReadFile(path)
	if rerr != nil {
		return lf.GeneratedAt
	}
	next, merr := marshalCanonical(lf)
	if merr != nil || !bytes.Equal(stripGeneratedAt(prior), stripGeneratedAt(next)) {
		return lf.GeneratedAt
	}
	if priorStamp := generatedAtValue(prior); priorStamp != "" {
		return priorStamp
	}
	return lf.GeneratedAt
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
//
// The hazard also reaches a pointer or slice field on the Lockfile itself, not
// only on a row: the literal below copies the three scalar top-level fields by
// value and every other field is assigned explicitly, so a newly added composite
// field left out of that sequence is silently absent from the canonical form
// rather than shared with the caller. Toolchain is the first such pointer and
// clones through cloneToolchain.
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
		if a.ModuleSubdir != b.ModuleSubdir {
			return a.ModuleSubdir < b.ModuleSubdir
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

	out.Toolchain = cloneToolchain(lf.Toolchain)

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

// cloneToolchain deep-copies a ToolchainStamp pointer and returns its core-plugin
// list sorted by plugin coordinate, so a caller mutating the canonicalised output
// cannot reach back into the input and two independent calls over the same input
// produce byte-identical output. Returns nil for a nil input (a lockfile carrying
// no stamp), which is what omits the section entirely.
//
// The list is sorted here rather than trusted from the producer, for the same
// reason VerdictInputs is: a core-plugin set carries no semantic order — it is a
// set, unlike Mutators, whose list order is what the transform runners apply — so
// nothing is lost by ordering it and the canonical form stops depending on the
// producer having enumerated it in order.
func cloneToolchain(in *ToolchainStamp) *ToolchainStamp {
	if in == nil {
		return nil
	}
	out := *in
	out.CorePlugins = make([]CorePluginVersion, len(in.CorePlugins))
	copy(out.CorePlugins, in.CorePlugins)
	sort.Slice(out.CorePlugins, func(i, j int) bool {
		return out.CorePlugins[i].Plugin < out.CorePlugins[j].Plugin
	})
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
