// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// effectiveComponentSchema returns the composed effective schema for an
// extends-ing Component, or (nil, false) for a non-extends Component (in which
// case the caller compiles the authored schema). Routing defaults materialisation
// through the same effective schema the conformance gate validates against keeps
// inherited defaults in the rendered output consistent with the validated schema.
func effectiveComponentSchema(lookup resolver.CatalogLookup, componentType string) (compactform.Schema, bool) {
	if lookup.EffectiveSchema == nil {
		return nil, false
	}
	return lookup.EffectiveSchema(componentType)
}

// compileFeatureSchema resolves one attachment Feature type to its compiled
// whole-entity schema. It is the ONE home for that resolution in the IR builder,
// so every reader of an attachment's declared schema — the pillar-conformance
// gate and the sensitivity derivation — sees the same schema for the same type
// and cannot disagree about what the Feature declares.
//
// The composed effective schema wins for an extends-ing Feature, so an inherited
// declaration reaches every reader exactly as an inherited constraint does; a
// non-extends Feature compiles its authored schema, with the catalog's schema
// libraries supplied so cross-library `$ref`s resolve the same way the validator
// resolves them. An absent Feature, an absent schema, or content that fails to
// compile yields (nil, false): a malformed schema surfaces through the validator,
// not here.
//
// Callers that resolve the same type repeatedly memoise around this function
// rather than inside it, so the cache's lifetime stays the caller's decision.
func compileFeatureSchema(lookup resolver.CatalogLookup, featureType string) (compactform.Schema, bool) {
	if lookup.EffectiveSchema != nil {
		if eff, ok := lookup.EffectiveSchema(featureType); ok {
			return eff, true
		}
	}
	if lookup.Feature == nil || featureType == "" {
		return nil, false
	}
	feat, ok := lookup.Feature(featureType)
	if !ok || feat.Spec.Schema.Kind == 0 {
		return nil, false
	}
	var opts []compactform.CompileOption
	if len(lookup.SchemaLibraries) > 0 {
		opts = append(opts, compactform.WithSchemaLibraries(lookup.SchemaLibraries))
	}
	compiled, err := compactform.Compile(&feat.Spec.Schema, opts...)
	if err != nil {
		return nil, false
	}
	return compiled, true
}

// componentPillarDefaults compiles a Component's schema and returns the declared
// defaults for the values and infra pillars as nested OrderedMaps. A Component
// with no schema, or one whose schema fails to compile, yields empty maps — a
// malformed schema surfaces through the validator, not here.
//
// The default extraction itself lives in pkg/compactform (PillarDefaults), the
// single source of truth shared with the Step-1 schema-conformance gate, so
// build-time materialisation and validate-time conformance interpret an
// authored schema identically.
func componentPillarDefaults(comp types.Component, libraries map[string]types.SchemaLibrary) (values, infra types.OrderedMap[any]) {
	if comp.Spec.Schema.Kind == 0 {
		return types.OrderedMap[any]{}, types.OrderedMap[any]{}
	}
	// Resolve cross-library schema `$ref`s (whole-pillar and base+flavor) against
	// the catalog's SchemaLibraries so library-declared defaults materialize. The
	// libraries are passed through the same compactform option the validator uses,
	// so build-time and validate-time interpret the schema identically. Omitted
	// when absent (inline-only behavior, unchanged). WithConsumerFile is not needed:
	// cross-library refs resolve by library name, not by relative path.
	var opts []compactform.CompileOption
	if len(libraries) > 0 {
		opts = append(opts, compactform.WithSchemaLibraries(libraries))
	}
	compiled, err := compactform.Compile(&comp.Spec.Schema, opts...)
	if err != nil {
		return types.OrderedMap[any]{}, types.OrderedMap[any]{}
	}
	return compactform.PillarDefaults(compiled, "values"), compactform.PillarDefaults(compiled, "infra")
}

// socketPillarDefaults compiles the per-edge contract a socket declares for one
// pillar and returns its declared defaults. The contract lives on the CONSUMING
// component — the edge's source — under spec.usesSchema.<socket>.valueSchema
// for the `values` pillar and spec.usesSchema.<socket>.infraSchema for the
// `infra` pillar, which is the same place the Step-1 per-edge conformance guard
// reads it from.
//
// An unknown component type, an undeclared socket, a socket declaring no
// contract for the pillar, or content that fails to compile all yield an empty
// map: a malformed schema surfaces through the validator, not here.
//
// The pillar is a parameter rather than one wrapper per pillar so both pillars
// materialise through a single function and cannot acquire divergent behaviour.
// The compile and the default extraction both route through the shared
// primitives (resolver.CompileSocketPillarSchema and
// compactform.PillarDefaults) that the conformance guard uses, so a socket's
// declared defaults are interpreted identically whether they are being
// validated against or materialised into the graph.
func socketPillarDefaults(lookup resolver.CatalogLookup, componentType, socket, pillar string) types.OrderedMap[any] {
	compiled, ok := resolver.CompileSocketPillarSchema(lookup, componentType, socket, pillar)
	if !ok {
		return types.OrderedMap[any]{}
	}
	return compactform.PillarDefaults(compiled, pillar)
}

// mergeSchemaDefaults fills declared defaults into author for keys the author
// omitted; author-supplied values always win. Delegates to the shared
// implementation in pkg/compactform.
func mergeSchemaDefaults(author, defaults types.OrderedMap[any]) types.OrderedMap[any] {
	return compactform.MergeSchemaDefaults(author, defaults)
}
