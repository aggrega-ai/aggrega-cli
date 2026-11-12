// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/merger"
)

// CatalogEntry identifies a Feature in an extends chain and carries the
// schema payload the chain composition reads. The caller populates it
// from the loaded catalog.
//
// It deliberately carries no metadata: an extending entity's inherited
// labels and annotations are merged on the typed entity by
// ResolveComponentExtends and ResolveFeatureExtends, which is the single
// home for that mechanic.
//
// It carries no entity kind either. A failure resolving this chain is
// reported against a kind ResolveFeatureSchemaChain names for itself, so the
// authoring surface every diagnostic from this tier is attributed to is read
// from one declaration rather than from whatever each entry was built with.
type CatalogEntry struct {
	URI     string // e.g. "core/api-server:1.0.0"
	Extends string // parent URI, empty if no inheritance

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

// ResolveFeatureSchemaChain walks a Feature's extends chain using the
// provided catalog lookup, and does two things with the chain it walks.
//
// It gates the chain, through the shared walk in chain_walk.go: a chain that
// returns to an entity already in it reports ErrCyclicExtension, one that ends
// cleanly but runs past the depth bound reports ErrExtendsChainTooDeep, and a
// parent the lookup cannot resolve reports ErrInheritanceMerge. Each halts the
// walk and returns the failure alone. Callers that only need an extends chain
// validated read Errors and ignore the rest.
//
// It composes the chain's schemas: when any chain entry supplies a
// JSONSchema payload, the chain is merged root-ancestor-first through
// pkg/validator/merger into MergedJSONSchema, carrying source-position
// provenance. Entries supplying no payload make this a no-op.
//
// The Feature kind is named here rather than travelling on each entry,
// because this tier composes Feature chains and no others: an extending
// Component inherits its base's pillar block verbatim, so its chain has
// nothing to compose and never reaches here. Fixing the kind at this one
// declaration is what makes the authoring surface these three conditions are
// reported against readable without following a value back to its caller.
//
// Inherited labels and annotations are not merged here — that happens on
// the typed entity in ResolveComponentExtends and ResolveFeatureExtends.
func ResolveFeatureSchemaChain(entry CatalogEntry, lookup func(uri string) (CatalogEntry, bool)) InheritanceResult {
	chain, verr := ResolveExtendsChain(envelope.KindFeature, entry, ExtendsChainSpec[CatalogEntry]{
		Key:        func(e CatalogEntry) string { return e.URI },
		ParentLink: func(e CatalogEntry) string { return e.Extends },
		Lookup:     lookup,
	})
	if verr != nil {
		return InheritanceResult{Errors: []*ValidationError{verr}}
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
			// The merge error is dropped because this call site
			// excludes both conditions that produce one. merger.Merge
			// fails on exactly two structural inputs — an empty chain,
			// and a chain element that is nil or carries a nil schema —
			// and the loop above admits neither: it skips an entry with
			// no payload, appends only a freshly-built element carrying
			// the payload it has just checked, and this branch runs only
			// when that chain came out non-empty. Composition itself is
			// total over well-formed schemas: a child that redefines an
			// inherited constraint is reported through the merged
			// schema's narrowing side-band, never as a failure.
			merged, _ := merger.Merge(mergerChain)
			result.MergedJSONSchema = merged
		}
	}

	return result
}
