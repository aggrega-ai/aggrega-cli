package validator

import (
	"fmt"

	"cuelang.org/go/cue/cuecontext"
)

// maxInheritanceDepth prevents unbounded chains.
const maxInheritanceDepth = 10

// CatalogEntry provides the metadata and CUE schema for an entity resolved by URI.
// The caller (Step 1 resolver) populates these from the loaded catalog.
type CatalogEntry struct {
	URI      string                 // e.g. "core/api-server:1.0.0"
	Extends  string                 // parent URI, empty if no inheritance
	Metadata map[string]interface{} // raw YAML metadata for deep merge
	CUE      string                 // cueSchema string
}

// InheritanceResult holds the output of resolving an extends chain.
type InheritanceResult struct {
	MergedMetadata map[string]interface{} // Phase 1 result: deep-merged YAML metadata
	UnifiedCUE     string                 // Phase 2 result: unified CUE schema string (for downstream use)
	Errors         []*ValidationError     // any errors encountered
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
					Err:        ErrInheritanceMerge,
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
	mergedMeta := map[string]interface{}{}
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].Metadata != nil {
			mergedMeta = deepMergeYAML(mergedMeta, chain[i].Metadata)
		}
	}

	// Phase 2: unify CUE schemas from root ancestor → child
	var unifiedCUE string
	ctx := cuecontext.New()
	var cueVal = ctx.CompileString("{}")

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
	}

	// Extract the unified CUE as a string for downstream consumption
	if cueBytes, err := cueVal.MarshalJSON(); err == nil {
		unifiedCUE = string(cueBytes)
	}

	return InheritanceResult{
		MergedMetadata: mergedMeta,
		UnifiedCUE:     unifiedCUE,
	}
}
