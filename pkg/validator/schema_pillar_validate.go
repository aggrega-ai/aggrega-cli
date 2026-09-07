// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
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

// UnusableContract is one declared schema contract whose body compiles and then
// builds no validator, so nothing can be measured against it.
//
// It carries no condition identity. The condition this reading feeds is declared
// where the conformance rules live, which this package sits beneath, so the
// caller attaches it — see [UnusableSchemaContracts].
type UnusableContract struct {
	// Field is the authored field the contract was declared in, relative to the
	// entity — "spec.schema.values", or "spec.usesSchema.<socket>.valueSchema".
	// It is the key the correction goes in.
	Field string

	// Pillar is the pillar the contract covers (values / infra / provides).
	Pillar string

	// Line is the source line the body was declared on, so a diagnostic sends an
	// author to the fragment rather than to the file.
	Line int

	// Err is why no validator could be built, as the validator reported it.
	Err error
}

// UnusableSchemaContracts reports every schema contract a Component or Feature
// declares whose body compiles cleanly and then builds no validator — a body
// readable as compact form that is not a schema anything can validate against,
// such as a bare scalar where a property's schema belongs.
//
// Both places an entity declares one are read: the entity's own spec.schema
// pillars, and the per-edge valueSchema / infraSchema of each socket in
// spec.usesSchema. Covering only the first would leave the socket contracts
// measured nowhere until conformance, which is the stage that cannot attribute
// the fault to the entity that declared it — so a reading that stopped at
// spec.schema would refuse one half of the class and pass the other silently.
//
// It is separate from [ValidateSchemaPillars] because it answers a different
// question, and because the two faults are refused under different conditions:
// an unreadable body is a catalog-shape fault, while a readable body that yields
// no validator is the declared-contract-does-not-compile condition the
// conformance rules already raise. This returns findings rather than an error so
// the caller can attach that condition, which is declared above this package and
// therefore not nameable here.
//
// A body that does not compile yields no finding: an entity pillar has already
// been refused by [ValidateSchemaPillars] and a socket body is refused at
// resolution, and reporting either twice would name one fault under two
// conditions. A schema-less entity, and an extending entity carrying the
// inherited marker, yield none for the same reason they pass there — there is no
// authored body to read.
//
// Findings come back in authored order — the entity's pillars as written, then
// each socket in declared order — so a diagnostic built from them is
// deterministic.
func UnusableSchemaContracts(
	filePath, extends string,
	schemaNode *yaml.Node,
	sockets types.OrderedMap[types.UsesSchemaEntry],
	libraries map[string]types.SchemaLibrary,
) []UnusableContract {
	var out []UnusableContract
	out = append(out, unusableEntityPillars(filePath, extends, schemaNode, libraries)...)
	for socket, entry := range sockets.All() {
		for _, pillar := range socketPillarFields {
			node := entry.PillarNode(pillar.pillar)
			if node == nil || node.Kind == 0 {
				continue
			}
			if u, bad := unusableContractAt(
				fmt.Sprintf("spec.usesSchema.%s.%s", socket, pillar.field),
				pillar.pillar, filePath, node, libraries,
			); bad {
				out = append(out, u)
			}
		}
	}
	return out
}

// socketPillarFields pairs each pillar a socket may constrain with the field it
// is declared in, so the two are named together rather than derived apart.
var socketPillarFields = [...]struct {
	pillar string
	field  string
}{
	{pillar: "values", field: "valueSchema"},
	{pillar: "infra", field: "infraSchema"},
}

// unusableEntityPillars is the spec.schema half of [UnusableSchemaContracts].
func unusableEntityPillars(
	filePath, extends string,
	schemaNode *yaml.Node,
	libraries map[string]types.SchemaLibrary,
) []UnusableContract {
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
		return nil
	}

	var out []UnusableContract
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]
		if !allowedSchemaPillars[keyNode.Value] {
			continue
		}
		if u, bad := unusableContractAt(
			"spec.schema."+keyNode.Value, keyNode.Value, filePath, valueNode, libraries,
		); bad {
			out = append(out, u)
		}
	}
	return out
}

// unusableContractAt compiles one authored contract body and reports whether a
// validator can be built from it, which is the whole predicate.
//
// The compiled body of one pillar is exactly the subtree a whole-entity compile
// exposes for that pillar, and a socket's body is what its own compile exposes
// beneath the key that compile wraps it in — so building the validator from the
// body here asks the same question the conformance read asks later, of the same
// bytes. Were the two shapes to differ, this refusal would either pass what that
// read drops or refuse what it accepts.
func unusableContractAt(
	field, pillar, filePath string,
	body *yaml.Node,
	libraries map[string]types.SchemaLibrary,
) (UnusableContract, bool) {
	opts := []compactform.CompileOption{compactform.WithConsumerFile(filePath)}
	if libraries != nil {
		opts = append(opts, compactform.WithSchemaLibraries(libraries))
	}
	compiled, err := compactform.Compile(body, opts...)
	if err != nil {
		return UnusableContract{}, false
	}
	if _, err := jsonvalidator.NewUnderProperty(pillar, compiled, nil); err != nil {
		return UnusableContract{
			Field:  field,
			Pillar: pillar,
			Line:   body.Line,
			Err:    err,
		}, true
	}
	return UnusableContract{}, false
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
