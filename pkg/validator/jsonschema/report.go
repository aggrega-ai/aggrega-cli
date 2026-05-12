// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import "sort"

// Report carries structured findings from a Validate call. An empty
// Findings slice indicates the instance validates cleanly; otherwise
// each Finding describes a single failure with field path, the failing
// keyword, a human-readable message, and (where the merger has populated
// it) provenance metadata pointing to the source schema position the
// constraint originated from.
type Report struct {
	// Findings carries the per-failure entries in deterministic order
	// (sorted by FieldPath, Keyword, Message — see Report.sort).
	Findings []Finding
}

// Finding is a single validation failure.
type Finding struct {
	// FieldPath names the location in the instance where the failure
	// occurred (e.g. "spec.values.port"). Empty when the failure is at
	// the instance root.
	FieldPath string

	// Keyword is the JSON Schema or x-aggrega keyword whose constraint
	// failed (e.g. "minimum", "pattern", "x-aggrega-validate").
	Keyword string

	// Message is a human-readable description of the failure.
	Message string

	// SourceOrigin is the merger-populated provenance pointer (file:line
	// of the source schema position the constraint originated from).
	// Empty until the custom merger ships and populates leaves.
	SourceOrigin string

	// LibrarySource carries the originating SchemaLibrary URI for
	// constraints contributed by a cross-document $ref expansion at
	// compactform compile time. The value matches the cross-document
	// $ref authoring shape (e.g. "BaseComponents#/schemas/Probe").
	// Empty when the failing constraint is locally authored. Populated
	// by EnrichFinding after Validate returns; the validator itself
	// does not consult the annotation.
	LibrarySource string

	// ConsumerFile carries the source file path of the consumer entity
	// whose schema the validator was given. Paired with LibrarySource
	// by EnrichFinding so error formatters can name both the originating
	// library and the consumer site in one message.
	ConsumerFile string
}

// sort orders the report's findings deterministically per the
// deterministic-output invariant. Three-tier sort: FieldPath, then
// Keyword, then Message — the tertiary key disambiguates when multiple
// findings share the same path and keyword (e.g. allOf producing two
// failures at the same path; a value failing two constraint clauses of
// the same keyword family).
func (r *Report) sort() {
	if r == nil || len(r.Findings) < 2 {
		return
	}
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a := r.Findings[i]
		b := r.Findings[j]
		if a.FieldPath != b.FieldPath {
			return a.FieldPath < b.FieldPath
		}
		if a.Keyword != b.Keyword {
			return a.Keyword < b.Keyword
		}
		return a.Message < b.Message
	})
}

// append adds a finding to the report. The findings are sorted on
// emission rather than on insert to keep the per-call overhead linear.
func (r *Report) append(f Finding) {
	r.Findings = append(r.Findings, f)
}
