// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/merger"
)

// maxInheritanceDepth prevents unbounded chains.
const maxInheritanceDepth = 10

// CatalogEntry identifies an entity in an extends chain and carries the
// schema payload the chain composition reads. The caller populates it
// from the loaded catalog.
//
// It deliberately carries no metadata: an extending entity's inherited
// labels and annotations are merged on the typed entity by
// ResolveComponentExtends and ResolveFeatureExtends, which is the single
// home for that mechanic.
type CatalogEntry struct {
	URI     string // e.g. "core/api-server:1.0.0"
	Extends string // parent URI, empty if no inheritance

	// Kind is the entity kind the URI names. It is required: a failure
	// resolving this chain is reported against the entity, and the kind is
	// what tells the reader — and the compiler's error categorisation —
	// which authoring surface the entity lives on. Leaving it unset is a
	// caller mistake, and NewValidationError refuses such a failure outright
	// rather than attributing it to a default surface.
	Kind envelope.Kind

	// JSONSchema is the entity's JSON Schema 2020-12 payload (the
	// output of pkg/compactform's compile step). When populated, the
	// inheritance walker also runs the JSON-Schema merge phase to
	// produce a merged JSON Schema with source-position provenance
	// metadata via the pkg/validator/merger package. The field is
	// opt-in: entries that omit it skip the merge phase as a no-op.
	JSONSchema *jsonschema.Schema
}

// InheritanceResult holds the output of resolving an extends chain.
//
// MergedJSONSchema carries the composed chain schema when any chain
// entry supplies a JSONSchema payload; it stays nil when no chain entry
// carries JSON Schema content. Errors carries the chain-structural
// failures — a cycle, the depth bound, an unresolvable parent — and a
// caller that needs only those may read Errors and discard the rest.
type InheritanceResult struct {
	MergedJSONSchema *merger.Schema
	Errors           []*ValidationError
}

// ResolveInheritance walks the extends chain for an entity using the
// provided catalog lookup, and does two things with the chain it walks.
//
// It gates the chain: a repeated URI (visited-set keyed by URI) reports
// ErrCyclicExtension, a chain longer than the depth bound of 10 reports
// ErrInheritanceMerge, and so does a parent the lookup cannot resolve.
// Each halts the walk and returns the failure alone. Callers that only
// need an extends chain validated read Errors and ignore the rest.
//
// It composes the chain's schemas: when any chain entry supplies a
// JSONSchema payload, the chain is merged root-ancestor-first through
// pkg/validator/merger into MergedJSONSchema, carrying source-position
// provenance. Entries supplying no payload make this a no-op.
//
// Inherited labels and annotations are not merged here — that happens on
// the typed entity in ResolveComponentExtends and ResolveFeatureExtends.
func ResolveInheritance(entry CatalogEntry, lookup func(uri string) (CatalogEntry, bool)) InheritanceResult {
	chain := []CatalogEntry{entry}
	visited := map[string]bool{entry.URI: true}
	current := entry

	for current.Extends != "" {
		if len(chain) >= maxInheritanceDepth {
			return InheritanceResult{
				Errors: []*ValidationError{NewValidationError(entry.Kind, entry.URI, ValidationDetail{
					FieldPath:  ".spec.extends",
					Constraint: fmt.Sprintf("inheritance chain exceeds maximum depth of %d", maxInheritanceDepth),
					Suggestion: "Reduce the inheritance chain length",
					Err:        ErrInheritanceMerge(),
				})},
			}
		}

		if visited[current.Extends] {
			return InheritanceResult{
				Errors: []*ValidationError{NewValidationError(entry.Kind, entry.URI, ValidationDetail{
					FieldPath:  ".spec.extends",
					Constraint: fmt.Sprintf("circular extends detected: %s already in chain", current.Extends),
					Suggestion: "Remove the circular extends reference",
					Err:        ErrCyclicExtension(),
				})},
			}
		}

		parent, ok := lookup(current.Extends)
		if !ok {
			return InheritanceResult{
				Errors: []*ValidationError{NewValidationError(entry.Kind, entry.URI, ValidationDetail{
					FieldPath:  ".spec.extends",
					Constraint: fmt.Sprintf("parent entity %q not found in catalog", current.Extends),
					Suggestion: "Verify the extends URI points to an existing catalog entity",
					Err:        ErrInheritanceMerge(),
				})},
			}
		}

		visited[current.Extends] = true
		chain = append(chain, parent)
		current = parent
	}

	var result InheritanceResult

	// Merge JSON Schema payloads via pkg/validator/merger. A no-op
	// unless at least one chain entry carries a JSONSchema payload:
	// the catalog walker supplies them for Features, while a caller
	// resolving a Component chain leaves the field unset because an
	// extending Component inherits its base's pillar block verbatim
	// and has nothing to compose. The merger emits structural
	// narrowing-violation signals via the returned Schema's
	// NarrowingViolations slice.
	hasJSONSchema := false
	for _, ent := range chain {
		if ent.JSONSchema != nil {
			hasJSONSchema = true
			break
		}
	}
	if hasJSONSchema {
		// Build a root-ancestor-first slice of *merger.Schema. Each
		// catalog entry's URI becomes the merger Schema's Source so
		// __source_origin metadata surfaces the originating entity.
		mergerChain := make([]*merger.Schema, 0, len(chain))
		for i := len(chain) - 1; i >= 0; i-- {
			ent := chain[i]
			if ent.JSONSchema == nil {
				continue
			}
			mergerChain = append(mergerChain, &merger.Schema{
				Schema: ent.JSONSchema,
				Source: ent.URI,
			})
		}
		if len(mergerChain) > 0 {
			merged, err := merger.Merge(mergerChain)
			if err != nil {
				// TODO: preserve the merger sentinel chain
				// alongside ErrInheritanceMerge so callers can
				// distinguish cycle / max-depth / invalid-input
				// via errors.Is. Squashing the inner sentinel into
				// the Constraint string is acceptable until a
				// caller depends on the distinction.
				result.Errors = append(result.Errors, NewValidationError(entry.Kind, entry.URI, ValidationDetail{
					FieldPath:  ".spec.jsonSchema",
					Constraint: "JSON Schema merge failed: " + err.Error(),
					Suggestion: "Verify the JSON Schema payloads on each catalog entry in the extends chain",
					Err:        ErrInheritanceMerge(),
				}))
			} else {
				result.MergedJSONSchema = merged
			}
		}
	}

	return result
}
