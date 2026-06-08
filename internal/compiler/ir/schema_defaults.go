// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

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

// mergeSchemaDefaults fills declared defaults into author for keys the author
// omitted; author-supplied values always win. Delegates to the shared
// implementation in pkg/compactform.
func mergeSchemaDefaults(author, defaults types.OrderedMap[any]) types.OrderedMap[any] {
	return compactform.MergeSchemaDefaults(author, defaults)
}
