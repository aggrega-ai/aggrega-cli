// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ExtendsComposition carries the products of composing an extends chain's JSON
// Schemas: the effective merged schema in compactform map form (ready for the
// schema-conformance gate and the provides / defaults consumers, which all read
// compactform.Schema) and the signal-only narrowing notices. EffectiveSchema is
// nil when the chain composed no schema (no entry declared a schema pillar);
// Narrowings is empty unless a child narrowed an inherited constraint.
type ExtendsComposition struct {
	EffectiveSchema compactform.Schema
	Narrowings      []string
}

// CompileSchemaForMerge compiles an entity's authored schema pillar block into a
// *jsonschema.Schema for the inheritance merge phase, via the established
// compactform path (compile → JSON → library schema). SchemaLibraries are passed
// through so cross-library $refs resolve, matching how the conformance gate and
// defaults extraction compile the same schema. Returns nil when the entity
// declares no schema or the schema does not compile — a malformed schema is
// surfaced by the catalog pillar validator at load, not here.
func CompileSchemaForMerge(node yaml.Node, libraries map[string]types.SchemaLibrary) *jsonschema.Schema {
	if node.Kind == 0 {
		return nil
	}
	var opts []compactform.CompileOption
	if len(libraries) > 0 {
		opts = append(opts, compactform.WithSchemaLibraries(libraries))
	}
	compiled, err := compactform.Compile(&node, opts...)
	if err != nil {
		return nil
	}
	raw, err := json.Marshal(compiled)
	if err != nil {
		return nil
	}
	out := &jsonschema.Schema{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil
	}
	return out
}

// CompositionFromResult converts a JSON-Schema-merge result into the effective
// compactform schema plus formatted, signal-only narrowing notices. Returns nil
// when the merge produced no schema (the entity's chain carried no schema
// pillar). The effective schema is the merged library schema round-tripped to a
// compactform.Schema (= map[string]any) so every conformance/provides/defaults
// consumer reads it without a second compile.
func CompositionFromResult(entityURI string, result InheritanceResult) *ExtendsComposition {
	if result.MergedJSONSchema == nil {
		return nil
	}
	raw, err := json.Marshal(result.MergedJSONSchema.Schema)
	if err != nil {
		return nil
	}
	eff := compactform.Schema{}
	if err := json.Unmarshal(raw, &eff); err != nil {
		return nil
	}
	comp := &ExtendsComposition{EffectiveSchema: eff}
	for _, v := range result.MergedJSONSchema.NarrowingViolations {
		comp.Narrowings = append(comp.Narrowings, fmt.Sprintf(
			"%s: child narrows inherited %s at %s: %s",
			entityURI, v.Keyword, v.FieldPath, v.Detail))
	}
	return comp
}
