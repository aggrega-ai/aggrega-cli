// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/nodeorigin"
	"github.com/aggrega-ai/aggrega-cli/internal/schemasurface"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
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
// Errors are cerrors.CatalogError-categorised CompilerError values carrying the
// source file path, so authors locate the fragment from the diagnostic alone. A
// refusal of the block's shape or of a pillar body carries the offending line as
// well, paired with the file that line belongs to: a body the entity read through
// `$import:` is located in the imported file rather than in filePath, whose lines
// it does not occupy. A refusal of a key that names no pillar carries the key's
// authored locator instead, which names the site rather than a position in the
// file.
// Validation runs fragment-level (as authored) rather than
// post-extends-resolution: an error in a base entity surfaces at the base file,
// not at every derived entity's merged view.
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
		return cerrors.Unidentified(
			cerrors.CatalogError,
			cerrors.SourceLocation{File: nodeorigin.DocumentOr(node, filePath), Line: node.Line},
			fmt.Sprintf(
				"%s %q: spec.schema must be a YAML map of pillars (values / infra / provides), not a %s. "+
					"Compact-form authoring uses inline YAML mapping, as vocabulary/compact-form-authoring.md describes.",
				entityKind, entityName, yamlNodeKindName(node.Kind),
			),
			nil, nil,
		)
	}

	// Whether every key names a pillar is settled over the whole block before any
	// body is compiled, rather than key by key as the walk reaches them. The two
	// questions are ordered rather than interleaved: this one asks whether a key
	// names a pillar at all, the compile below asks what is inside one, and the
	// second is unaskable until the first is settled — a body beneath a key no
	// consumer reads is a contract nothing would ever measure against.
	if failure := unknownSchemaPillarFailure(entityKind, entityName, filePath, node); failure != nil {
		return failure
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]
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
				cerrors.SourceLocation{File: nodeorigin.DocumentOr(valueNode, filePath), Line: valueNode.Line},
				fmt.Sprintf(
					"%s %q: %s body failed to compile to JSON Schema 2020-12: %s",
					entityKind, entityName, schemasurface.Pillar(keyNode.Value), err.Error(),
				),
			)
		}
	}

	return nil
}

// unknownSchemaPillarFailure enforces the pillar-key rule over one entity's
// schema block: every top-level key names one of the data pillars.
//
// The rule itself is declared rather than written out here — see
// componentSchemaKeyNamesAPillar and featureSchemaKeyNamesAPillar, which share one
// predicate and raise one condition. What this function does is present the block
// the load is holding, pick the declaration for the kind that holds it, and render
// whatever came back into the failure shape this package's callers read.
//
// mapping is the block's own mapping node, after the caller has settled which of
// its authoring shapes is in hand. Passing the settled node is what keeps that
// settling in one place.
//
// It returns the FIRST finding rather than the set, because the caller returns one
// error and a block with two offending keys is two renames the author makes from
// the same file. The rule is invoked once per key regardless, so which finding is
// first is the order the author wrote the keys in rather than an artifact of the
// walk.
//
// The finding is positioned at the file AND at the key: the file is what a reader
// opens, and the key is what they change. The two travel in separate fields of one
// location for exactly that reason — folding the key into the file would displace
// the path, and a single string cannot be both without one of them losing. What the
// migrated form does not carry is the key's LINE: the framework attributes a finding
// to an authored site rather than to a position in a file, so no line reaches the
// conversion. An author is sent to the file and to the key within it.
//
// It panics on an entity kind that carries no schema block, for the reason the
// failure constructors in this package panic on an unmappable kind: the kind
// reaches here as a constant chosen at the call site, never from an authored
// document, so an unrecognised one means a caller passed something no declaration
// covers — and answering nil would silently pass every block of that kind.
func unknownSchemaPillarFailure(entityKind, entityName, filePath string, mapping *yaml.Node) error {
	var (
		objections []constraint.Objection
		kind       envelope.Kind
	)
	switch entityKind {
	case string(envelope.KindComponent):
		// The presentation is held as the view the rule declared rather than as
		// its own concrete type, because the entry takes the check and the view
		// together and infers one type from both. Handing over the concrete type
		// would infer that type and then refuse the rule, which declares the view.
		presented := newComponentSchemaBlockView(filePath, mapping)
		var view constraint.DocumentView = presented
		objections = constraint.RunOnDocumentSchemaPillars(
			filePath, presented.ComponentSchemaPillarKeys(filePath), view,
			componentSchemaKeyNamesAPillar,
		)
		kind = envelope.KindComponent
	case string(envelope.KindFeature):
		presented := newFeatureSchemaBlockView(filePath, mapping)
		var view constraint.DocumentView = presented
		objections = constraint.RunOnDocumentSchemaPillars(
			filePath, presented.FeatureSchemaPillarKeys(filePath), view,
			featureSchemaKeyNamesAPillar,
		)
		kind = envelope.KindFeature
	default:
		panic(fmt.Sprintf("validator: entity kind %q declares no schema block, so its pillar keys "+
			"cannot be checked; pass the kind of an entity that carries one", entityKind))
	}

	failures := shapeRuleErrorsFrom(objections, ruleSubject{
		Kind: kind,
		Name: entityName,
		Rule: "schema-pillars",
	})
	if len(failures) == 0 {
		return nil
	}
	// The conversion renders one failure per objection in the order they were
	// raised, so the first failure and the first objection are about one key.
	return cerrors.Wrap(
		failures[0],
		cerrors.CatalogError,
		cerrors.SourceLocation{File: filePath, Locator: objections[0].Locator()},
		failures[0].Error(),
	)
}

// UnusableContract is one declared schema contract nothing can be measured
// against: a socket contract whose body does not compile, or any contract whose
// body compiles and then builds no validator.
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
	// author to the fragment rather than to the file. It is a line of the document
	// the body was read from, which for a body read through `$import:` is the
	// imported file rather than the entity's.
	Line int

	// Err is why no validator could be built, as the validator reported it.
	Err error
}

// UnusableSchemaContracts reports every schema contract a Component or Feature
// declares that nothing can be measured against: a body readable as compact form
// that is not a schema anything can validate against, such as a bare scalar
// where a property's schema belongs, and a socket contract whose body does not
// compile at all.
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
// an entity pillar that does not compile is refused there, under the compact-form
// condition its compile raised, while a readable body that yields no validator —
// and a socket body that does not compile, which nothing reads before this — is
// the declared-contract-does-not-compile condition the conformance rules already
// raise. This returns findings rather than an error so the caller can attach that
// condition, which is declared above this package and therefore not nameable
// here.
//
// An entity pillar that does not compile yields no finding, because
// [ValidateSchemaPillars] has already refused it and reporting it twice would
// name one fault under two conditions. A socket body that does not compile is a
// finding, so the catalog read refuses it where the entity that declared it is
// known, rather than leaving it to the first Release whose resolution reaches
// the socket. A schema-less entity, and an extending entity carrying the
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
		for _, contract := range schemasurface.SocketContractFields() {
			node := entry.PillarNode(contract.Pillar)
			if node == nil || node.Kind == 0 {
				continue
			}
			if u, bad := unusableContractAt(
				schemasurface.SocketContract(socket, contract.Field),
				contract.Pillar, filePath, node, libraries, true,
			); bad {
				out = append(out, u)
			}
		}
	}
	return out
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
			schemasurface.Pillar(keyNode.Value), keyNode.Value, filePath, valueNode, libraries, false,
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
//
// refuseCompileFailure says whether a body that does not compile is a finding
// here. It is for a socket contract, which no earlier reading compiles; an entity
// pillar's compile failure is [ValidateSchemaPillars]'s to report.
func unusableContractAt(
	field, pillar, filePath string,
	body *yaml.Node,
	libraries map[string]types.SchemaLibrary,
	refuseCompileFailure bool,
) (UnusableContract, bool) {
	opts := []compactform.CompileOption{compactform.WithConsumerFile(filePath)}
	if libraries != nil {
		opts = append(opts, compactform.WithSchemaLibraries(libraries))
	}
	compiled, err := compactform.Compile(body, opts...)
	if err != nil {
		if !refuseCompileFailure {
			return UnusableContract{}, false
		}
		return UnusableContract{
			Field:  field,
			Pillar: pillar,
			Line:   body.Line,
			Err:    err,
		}, true
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
