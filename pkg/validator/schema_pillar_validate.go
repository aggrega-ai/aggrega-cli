// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// allowedSchemaPillars enumerates the canonical free-form pillar keys for
// a Component or Feature spec.schema body. The corresponding contract is
// stated on feature-v1alpha1.schema.json and component-v1alpha1.schema.json
// (the spec.schema description), and on vocabulary/data-pillars.md.
var allowedSchemaPillars = map[string]bool{
	"values":   true,
	"infra":    true,
	"provides": true,
}

// InheritedSchemaMarker is the value spec.schema carries on an entity that
// declares spec.extends: the entity's pillars are its base's, as the base
// authored them, and the entity authors none of its own. It is the fourth
// content-shape mode of spec.schema documented on
// component-v1alpha1.schema.json, beside inline content, {$import: <path>}
// and {$ref: <Library>#/schemas/<name>} — a scalar where those three are
// maps.
const InheritedSchemaMarker = "inherited"

// ValidateSchemaPillars enforces the structural contract on a Component or
// Feature's spec.schema field at catalog load time. The node must be a YAML
// mapping whose top-level keys are a subset of {values, infra, provides};
// each pillar body must compile cleanly under pkg/compactform with the
// caller-supplied SchemaLibraries map available for cross-document $ref
// expansion. A schema-less entity (the field absent on disk, surfaced as
// a zero-value yaml.Node with Kind == 0) passes without inspection — Feature
// and Component entities that omit spec.schema entirely are legitimate.
//
// The extends argument is the entity's authored spec.extends URI. An entity
// that declares one may write the InheritedSchemaMarker scalar in place of a
// pillar mapping, and only that scalar: the marker states that the pillars
// are the base entity's, so there is no mapping to inspect here. Every other
// non-mapping node — on an extending entity or not — is rejected with the
// mapping-node diagnostic below. Which of the three outcomes an extending
// entity's spec.schema may carry (the marker, an authored mapping, or
// nothing) is settled by the published entity schema at load; this function
// admits the marker rather than restating that rule.
//
// Errors are cerrors.CatalogError-categorised CompilerError values carrying
// the source file path and offending line so authors locate the fragment
// from the diagnostic alone. Validation runs fragment-level (as authored)
// rather than post-extends-resolution: an error in a base entity surfaces
// at the base file, not at every derived entity's merged view.
//
// The function is called from pkg/catalog/catalog.go's pillar validation after
// the indexing walk
// has hydrated every entity and the SchemaLibraries map is fully populated.
// Calling it per file during the indexing walk is unsafe — cross-document $ref targets
// would be unresolvable when a Feature precedes its SchemaLibrary in
// filesystem walk order.
func ValidateSchemaPillars(
	entityKind, entityName, filePath, extends string,
	schemaNode *yaml.Node,
	libraries map[string]types.SchemaLibrary,
) error {
	if schemaNode == nil || schemaNode.Kind == 0 {
		return nil
	}

	node := schemaNode
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}

	if extends != "" && node.Kind == yaml.ScalarNode && node.Value == InheritedSchemaMarker {
		return nil
	}

	if node.Kind != yaml.MappingNode {
		return cerrors.New(
			cerrors.CatalogError,
			cerrors.SourceLocation{File: filePath, Line: node.Line},
			fmt.Sprintf(
				"%s %q: spec.schema must be a YAML map of pillars (values / infra / provides), not a %s. "+
					"Compact-form authoring uses inline YAML mapping; see vocabulary/compact-form-authoring.md.",
				entityKind, entityName, yamlNodeKindName(node.Kind),
			),
			nil, nil,
		)
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]
		if !allowedSchemaPillars[keyNode.Value] {
			// The diagnostic composes through the condition's registered
			// sentence rather than emitting the clause alone, so it opens with
			// that sentence and carries the occurrence's clause after it.
			// Leading with the sentence is structural rather than stylistic:
			// the chain walk that renders a halt prints the outermost frame
			// whose text the output does not already carry, so a message that
			// did not lead with it would print the sentence a second time as a
			// redundant continuation line.
			//
			// The clause names the pillar vocabulary a second time, after the
			// sentence has already said the key names no pillar. That is a
			// cost rather than an oversight, and it is the price of the split:
			// the sentence is one static string per condition, so it can name
			// neither the entity at fault, nor the key met, nor the keys the
			// block admits, nor where the pillar set is documented — and every
			// one of those is what an author acts on. Reducing the clause to
			// the offending key alone would delete them from a diagnostic the
			// sentence cannot carry them in.
			return cerrors.Wrap(
				ErrUnknownSchemaPillar(),
				cerrors.CatalogError,
				cerrors.SourceLocation{File: filePath, Line: keyNode.Line},
				cerrors.ComposeMessage(ErrUnknownSchemaPillar(), nil, fmt.Sprintf(
					"%s %q: spec.schema unknown pillar key %q; allowed: values, infra, provides. "+
						"See vocabulary/data-pillars.md for the canonical pillar set.",
					entityKind, entityName, keyNode.Value,
				)),
			)
		}
		opts := []compactform.CompileOption{
			compactform.WithConsumerFile(filePath),
		}
		if libraries != nil {
			opts = append(opts, compactform.WithSchemaLibraries(libraries))
		}
		if _, err := compactform.Compile(valueNode, opts...); err != nil {
			return cerrors.Wrap(
				err,
				cerrors.CatalogError,
				cerrors.SourceLocation{File: filePath, Line: valueNode.Line},
				fmt.Sprintf(
					"%s %q: spec.schema.%s body failed to compile to JSON Schema 2020-12: %s",
					entityKind, entityName, keyNode.Value, err.Error(),
				),
			)
		}
	}

	return nil
}

func yamlNodeKindName(k yaml.Kind) string {
	switch k {
	case yaml.ScalarNode:
		return "scalar / block-scalar string"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	case yaml.AliasNode:
		return "alias"
	default:
		return "unknown YAML node kind"
	}
}
