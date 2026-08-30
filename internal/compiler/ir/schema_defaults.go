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

// CompileFeatureSchema resolves one attachment Feature type to its compiled
// whole-entity schema. It delegates to resolver.CompileFeatureSchema, which is
// the one home for that resolution, so every reader of an attachment's declared
// schema — the pillar-conformance gate, the sensitivity derivation, and the
// display surfaces that redact what a Feature declares sensitive — sees the same
// schema for the same type and cannot disagree about what the Feature declares.
//
// It holds no resolution logic of its own and must not acquire any. A body here
// that re-derived the answer would be a second copy agreeing on the day it was
// written, and that agreement is exactly what would hide the divergence later.
// What the resolution does with an extending Feature, with the catalog's schema
// libraries, and with a Feature that is absent or whose declared schema does not
// resolve is stated at that one home rather than restated here.
//
// Callers that resolve the same type repeatedly memoise around this function
// rather than inside it, so the cache's lifetime stays the caller's decision.
func CompileFeatureSchema(lookup resolver.CatalogLookup, featureType string) (compactform.Schema, bool) {
	return resolver.CompileFeatureSchema(lookup, featureType)
}

// componentPillarDefaults compiles a Component's schema and returns the declared
// defaults for the values and infra pillars as nested OrderedMaps. A Component
// with no schema, or one whose schema fails to compile, yields empty maps — a
// malformed schema surfaces through the validator, not here.
//
// The default extraction itself lives in pkg/compactform (PillarDefaults), the
// single source of truth shared with the schema-conformance gate at resolution,
// so build-time materialisation and validate-time conformance interpret an
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
// `infra` pillar, which is the same place the per-edge conformance guard at
// resolution reads it from.
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
