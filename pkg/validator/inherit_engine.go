// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"cuelang.org/go/cue/cuecontext"
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/cognitio/aggrega-cli/pkg/validator/merger"
)

// maxInheritanceDepth prevents unbounded chains.
const maxInheritanceDepth = 10

// CatalogEntry provides the metadata and CUE schema for an entity resolved by URI.
// The caller (Step 1 resolver) populates these from the loaded catalog.
type CatalogEntry struct {
	URI      string         // e.g. "core/api-server:1.0.0"
	Extends  string         // parent URI, empty if no inheritance
	Metadata map[string]any // raw YAML metadata for deep merge
	CUE      string         // cueSchema string

	// JSONSchema is the entity's JSON Schema 2020-12 payload (the
	// output of pkg/compactform's compile step). When populated, the
	// inheritance walker also runs Phase 3 to produce a merged
	// JSON Schema with source-position provenance metadata via the
	// pkg/validator/merger package. The field is opt-in: catalog
	// entries authored before the migration omit it, and Phase 3
	// is a no-op for those entries.
	JSONSchema *jsonschema.Schema
}

// InheritanceResult holds the output of resolving an extends chain.
type InheritanceResult struct {
	MergedMetadata   map[string]any     // Phase 1 result: deep-merged YAML metadata
	UnifiedCUE       string             // Phase 2 result: unified CUE schema string (for downstream use)
	MergedJSONSchema *merger.Schema     // Phase 3 result: merged JSON Schema with provenance; nil when no chain entry carried a JSONSchema payload
	Errors           []*ValidationError // any errors encountered
}

// ResolveInheritance resolves the extends chain for an entity using the provided catalog lookup.
// Phase 1: deep-merges YAML metadata bottom-up.
// Phase 2: unifies CUE schemas via the & operator bottom-up.
func ResolveInheritance(entry CatalogEntry, lookup func(uri string) (CatalogEntry, bool)) InheritanceResult {
	// Collect the chain from child → parent → grandparent
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

	// Phase 1: deep-merge metadata from root ancestor → child (child wins)
	mergedMeta := map[string]any{}
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].Metadata != nil {
			mergedMeta = deepMergeYAML(mergedMeta, chain[i].Metadata)
		}
	}

	// Phase 2: verify CUE schemas unify without conflict, emit concatenated source.
	// CUE's `&` semantics are preserved by concatenation — compiling the joined
	// string is equivalent to pairwise unification, with the upside that downstream
	// consumers (evaluateSchema, ValidatePillar) receive a real CUE source they can
	// recompile without losing constraints (MarshalJSON would have dropped them).
	ctx := cuecontext.New()
	cueVal := ctx.CompileString("{}")

	var sourceParts []string
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].CUE == "" {
			continue
		}
		parentVal := ctx.CompileString(chain[i].CUE)
		if parentVal.Err() != nil {
			return InheritanceResult{
				MergedMetadata: mergedMeta,
				Errors: []*ValidationError{{
					Entity:     chain[i].URI,
					FieldPath:  ".spec.cueSchema",
					Constraint: "CUE schema compilation failed",
					Suggestion: "Check the cueSchema syntax",
					Err:        ErrInheritanceMerge,
				}},
			}
		}
		cueVal = cueVal.Unify(parentVal)
		if cueVal.Err() != nil {
			return InheritanceResult{
				MergedMetadata: mergedMeta,
				Errors:         translateCUEErrors(cueVal.Err(), entry.URI, ErrInheritanceMerge),
			}
		}
		sourceParts = append(sourceParts, chain[i].CUE)
	}

	result := InheritanceResult{
		MergedMetadata: mergedMeta,
		UnifiedCUE:     joinCUESources(sourceParts),
	}

	// Phase 3: merge JSON Schema payloads via pkg/validator/merger.
	// Dormant unless at least one chain entry carries a JSONSchema
	// payload — current callers don't populate the field; the
	// catalog walker activates this path once entries carry their
	// JSON Schema 2020-12 payloads alongside (or in place of) their
	// CUE sources. The merger emits structural narrowing-violation
	// signals via the returned Schema's NarrowingViolations slice;
	// language-level CUE narrowing enforcement above runs in parallel
	// for the legacy pipeline and co-exists with the structural
	// signal until the CUE imports retire.
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
				// TODO: when the catalog walker activates this hook,
				// preserve the merger sentinel chain alongside
				// ErrInheritanceMerge so callers can distinguish
				// cycle / max-depth / invalid-input via errors.Is.
				// Current shape squashes the inner sentinel into the
				// Constraint string; the dormant state has no
				// callers depending on the distinction yet.
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

// joinCUESources concatenates CUE source fragments with a blank line between each.
// CUE parses the joined document as pairwise unification.
func joinCUESources(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	var total int
	for _, p := range parts {
		total += len(p) + 2
	}
	buf := make([]byte, 0, total)
	for i, p := range parts {
		if i > 0 {
			buf = append(buf, '\n', '\n')
		}
		buf = append(buf, p...)
	}
	return string(buf)
}
