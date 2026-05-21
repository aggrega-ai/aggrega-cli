// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/merger"
)

// maxInheritanceDepth prevents unbounded chains.
const maxInheritanceDepth = 10

// CatalogEntry provides the metadata and schema for an entity resolved
// by URI. The caller (Step 1 resolver) populates these from the loaded
// catalog.
type CatalogEntry struct {
	URI      string         // e.g. "core/api-server:1.0.0"
	Extends  string         // parent URI, empty if no inheritance
	Metadata map[string]any // raw YAML metadata for deep merge

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
// Phase 1 (YAML metadata deep-merge) populates MergedMetadata. The
// JSON-Schema-merge phase populates MergedJSONSchema when any chain
// entry carries a JSONSchema payload; the field stays nil when no
// chain entry carries JSON Schema content.
type InheritanceResult struct {
	MergedMetadata   map[string]any
	MergedJSONSchema *merger.Schema
	Errors           []*ValidationError
}

// ResolveInheritance resolves the extends chain for an entity using
// the provided catalog lookup.
//
//   - Phase 1: deep-merges YAML metadata bottom-up.
//   - Phase 2: when any chain entry carries a JSONSchema payload,
//     composes the chain via pkg/validator/merger to produce a merged
//     schema with provenance metadata.
//
// Cycle detection (visited-set keyed by URI) and max-depth bound
// (10 levels) gate both phases.
func ResolveInheritance(entry CatalogEntry, lookup func(uri string) (CatalogEntry, bool)) InheritanceResult {
	chain := []CatalogEntry{entry}
	visited := map[string]bool{entry.URI: true}
	current := entry

	for current.Extends != "" {
		if len(chain) >= maxInheritanceDepth {
			return InheritanceResult{
				Errors: []*ValidationError{{
					Entity:     entry.URI,
					FieldPath:  ".spec.extends",
					Constraint: fmt.Sprintf("inheritance chain exceeds maximum depth of %d", maxInheritanceDepth),
					Suggestion: "Reduce the inheritance chain length",
					Err:        ErrInheritanceMerge,
				}},
			}
		}

		if visited[current.Extends] {
			return InheritanceResult{
				Errors: []*ValidationError{{
					Entity:     entry.URI,
					FieldPath:  ".spec.extends",
					Constraint: fmt.Sprintf("circular extends detected: %s already in chain", current.Extends),
					Suggestion: "Remove the circular extends reference",
					Err:        ErrCyclicExtension,
				}},
			}
		}

		parent, ok := lookup(current.Extends)
		if !ok {
			return InheritanceResult{
				Errors: []*ValidationError{{
					Entity:     entry.URI,
					FieldPath:  ".spec.extends",
					Constraint: fmt.Sprintf("parent entity %q not found in catalog", current.Extends),
					Suggestion: "Verify the extends URI points to an existing catalog entity",
					Err:        ErrInheritanceMerge,
				}},
			}
		}

		visited[current.Extends] = true
		chain = append(chain, parent)
		current = parent
	}

	// Phase 1: deep-merge YAML metadata from root ancestor → child.
	mergedMeta := map[string]any{}
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].Metadata != nil {
			mergedMeta = deepMergeYAML(mergedMeta, chain[i].Metadata)
		}
	}

	result := InheritanceResult{MergedMetadata: mergedMeta}

	// Phase 2: merge JSON Schema payloads via pkg/validator/merger.
	// Dormant unless at least one chain entry carries a JSONSchema
	// payload — current callers don't populate the field; the
	// catalog walker activates this path once entries carry their
	// JSON Schema 2020-12 payloads (handled by the schema-authoring
	// phase). The merger emits structural narrowing-violation
	// signals via the returned Schema's NarrowingViolations slice.
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
				result.Errors = append(result.Errors, &ValidationError{
					Entity:     entry.URI,
					FieldPath:  ".spec.jsonSchema",
					Constraint: "JSON Schema merge failed: " + err.Error(),
					Suggestion: "Verify the JSON Schema payloads on each catalog entry in the extends chain",
					Err:        ErrInheritanceMerge,
				})
			} else {
				result.MergedJSONSchema = merged
			}
		}
	}

	return result
}
