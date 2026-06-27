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

// ValidateSchemaPillars enforces the structural contract on a Component or
// Feature's spec.schema field at catalog load time. The node must be a YAML
// mapping whose top-level keys are a subset of {values, infra, provides};
// each pillar body must compile cleanly under pkg/compactform with the
// caller-supplied SchemaLibraries map available for cross-document $ref
// expansion. A schema-less entity (the field absent on disk, surfaced as
// a zero-value yaml.Node with Kind == 0) passes without inspection — Feature
// and Component entities that omit spec.schema entirely are legitimate.
//
// Errors are cerrors.CatalogError-categorised CompilerError values carrying
// the source file path and offending line so authors locate the fragment
// from the diagnostic alone. Validation runs fragment-level (as authored)
// rather than post-extends-resolution: an error in a base entity surfaces
// at the base file, not at every derived entity's merged view.
//
// The function is called from pkg/catalog/catalog.go's Pass 2 after Pass 1
// has hydrated every entity and the SchemaLibraries map is fully populated.
// Calling it per file during Pass 1 is unsafe — cross-document $ref targets
// would be unresolvable when a Feature precedes its SchemaLibrary in
// filesystem walk order.
func ValidateSchemaPillars(
	entityKind, entityName, filePath string,
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
			return cerrors.New(
				cerrors.CatalogError,
				cerrors.SourceLocation{File: filePath, Line: keyNode.Line},
				fmt.Sprintf(
					"%s %q: spec.schema unknown pillar key %q; allowed: values, infra, provides. "+
						"See vocabulary/data-pillars.md for the canonical pillar set.",
					entityKind, entityName, keyNode.Value,
				),
				nil, nil,
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
