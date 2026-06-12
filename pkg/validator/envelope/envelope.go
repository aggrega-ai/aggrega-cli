// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package envelope validates a raw YAML document against the embedded
// JSON schema for its entity kind — the document-envelope layer that
// runs BEFORE typed unmarshal at every entity parse boundary. It is
// the single implementation of the validate-then-unmarshal pattern;
// the workspaceconfig, lockfile, and workload loaders consume it
// rather than carrying private copies.
//
// The envelope layer is distinct from the content-validation pipeline
// (compact form → resolved JSON Schema → instance validation): the
// envelope checks the document's own structure against its kind's
// published schema; the content pipeline checks pillar payloads
// against schemas the entities themselves declare.
package envelope

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	libschema "github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	clischemas "github.com/aggrega-ai/aggrega-cli/pkg/schemas/jsonschema"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// ErrEntityEnvelope is the sentinel for a document failing its
// kind's envelope schema. errors.Is-recoverable at the pipeline
// boundary.
var ErrEntityEnvelope = errors.New("document envelope validation failed")

// Kind names a document family with an embedded envelope schema.
type Kind string

const (
	KindComponent      Kind = "Component"
	KindFeature        Kind = "Feature"
	KindRecipe         Kind = "Recipe"
	KindFeatureRecipe  Kind = "FeatureRecipe"
	KindProvider       Kind = "Provider"
	KindSchemaLibrary  Kind = "SchemaLibrary"
	KindMappingLibrary Kind = "MappingLibrary"
	KindAggregate      Kind = "Aggregate"
	KindManifest       Kind = "Manifest"
	KindRelease        Kind = "Release"
	KindAggregaConfig  Kind = "AggregaConfig"
	KindLockfile       Kind = "Lockfile"
	KindWorkload       Kind = "Workload"
)

// schemaSource maps each kind to its embedded schema document.
var schemaSource = map[Kind]string{
	KindComponent:      schemas.ComponentV1alpha1,
	KindFeature:        schemas.FeatureV1alpha1,
	KindRecipe:         schemas.RecipeV1alpha1,
	KindFeatureRecipe:  schemas.FeatureRecipeV1alpha1,
	KindProvider:       schemas.ProviderV1alpha1,
	KindSchemaLibrary:  schemas.SchemaLibraryV1alpha1,
	KindMappingLibrary: schemas.MappingLibraryV1alpha1,
	KindAggregate:      schemas.AggregateV1alpha1,
	KindManifest:       schemas.ManifestV1alpha1,
	KindRelease:        schemas.ReleaseV1alpha1,
	KindAggregaConfig:  schemas.AggregaConfigV1alpha1,
	KindLockfile:       schemas.LockfileV1alpha1,
	KindWorkload:       clischemas.WorkloadJSONSchema,
}

// compiled caches one compiled validator per kind. Catalog loads
// validate hundreds of documents; the lazy lookup path in particular
// must not recompile per file.
var (
	compiledMu sync.Mutex
	compiled   = map[Kind]jsonvalidator.Validator{}
)

func validatorFor(kind Kind) (jsonvalidator.Validator, error) {
	compiledMu.Lock()
	defer compiledMu.Unlock()
	if v, ok := compiled[kind]; ok {
		return v, nil
	}
	raw, ok := schemaSource[kind]
	if !ok {
		return nil, fmt.Errorf("envelope: no embedded schema for kind %q", kind)
	}
	s := &libschema.Schema{}
	if err := json.Unmarshal([]byte(raw), s); err != nil {
		return nil, fmt.Errorf("envelope: decode embedded %s schema: %w", kind, err)
	}
	v, err := jsonvalidator.New(s)
	if err != nil {
		return nil, fmt.Errorf("envelope: compile embedded %s schema: %w", kind, err)
	}
	compiled[kind] = v
	return v, nil
}

// Error is the structured envelope violation: the file, the entity
// kind, and the engine's path-pointed findings. Unwraps to
// ErrEntityEnvelope.
type Error struct {
	Kind     Kind
	Path     string
	Findings []jsonvalidator.Finding
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s document violates its envelope schema", e.Path, e.Kind)
	for i, f := range e.Findings {
		if i >= 4 {
			fmt.Fprintf(&b, "; (+%d more)", len(e.Findings)-4)
			break
		}
		fp := f.FieldPath
		if fp == "" {
			fp = "(root)"
		}
		fmt.Fprintf(&b, "; %s: %s", fp, f.Message)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return ErrEntityEnvelope }

// ValidateBytes parses raw YAML document bytes, validates the
// document against kind's embedded schema, and returns the parsed,
// normalised generic instance for caller reuse. A schema violation
// returns *Error (wrapping ErrEntityEnvelope); parse and engine
// failures return their own errors.
func ValidateBytes(kind Kind, data []byte, path string) (any, error) {
	var instance any
	if err := yaml.Unmarshal(data, &instance); err != nil {
		// Aggregate values may carry unquoted Go-template expressions
		// ({{ .parameters.x }}) that parse as mapping-shaped YAML keys —
		// valid to the product (the typed path defers those nodes), but
		// unhashable for a generic map decode. Re-parse via yaml.Node
		// and stringify complex keys so the document still validates.
		var node yaml.Node
		if nerr := yaml.Unmarshal(data, &node); nerr != nil {
			return nil, fmt.Errorf("envelope: parse %s: %w", path, err)
		}
		instance = nodeToAny(&node)
	}
	if instance == nil {
		return nil, &Error{Kind: kind, Path: path, Findings: []jsonvalidator.Finding{{Message: "document is empty"}}}
	}
	instance = NormaliseYAML(instance)
	findings, err := ValidateInstance(kind, instance)
	if err != nil {
		return instance, err
	}
	if len(findings) > 0 {
		return instance, &Error{Kind: kind, Path: path, Findings: findings}
	}
	return instance, nil
}

// ValidateInstance validates an already-parsed, normalised generic
// instance against kind's embedded schema and returns the engine's
// findings. Engine-level failures (schema decode/compile/run) are
// returned as errors distinct from instance findings.
func ValidateInstance(kind Kind, instance any) ([]jsonvalidator.Finding, error) {
	v, err := validatorFor(kind)
	if err != nil {
		return nil, err
	}
	report, err := v.Validate(instance, nil)
	if err != nil {
		return nil, fmt.Errorf("envelope: validate %s instance: %w", kind, err)
	}
	return report.Findings, nil
}

// nodeToAny converts a yaml.Node tree into the generic shape the
// validator consumes. Mapping keys that are not scalars (template
// expressions parsed as flow mappings) are represented by an indexed
// placeholder — lossy, but those keys live inside free-form value
// content the envelope schemas do not constrain per-key.
func nodeToAny(n *yaml.Node) any {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil
		}
		return nodeToAny(n.Content[0])
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			key := k.Value
			if k.Kind != yaml.ScalarNode {
				key = fmt.Sprintf("{{template-key-%d}}", i/2)
			}
			out[key] = nodeToAny(n.Content[i+1])
		}
		return out
	case yaml.SequenceNode:
		out := make([]any, len(n.Content))
		for i, c := range n.Content {
			out[i] = nodeToAny(c)
		}
		return out
	case yaml.AliasNode:
		return nodeToAny(n.Alias)
	default:
		var v any
		if err := n.Decode(&v); err != nil {
			return n.Value
		}
		return NormaliseYAML(v)
	}
}

// NormaliseYAML walks the value yaml.Unmarshal produced for an `any`
// destination and converts it into the shape the JSON Schema engine
// consumes: map[any]any becomes map[string]any (non-string keys are
// dropped — they cannot exist on a schema-conforming document and a
// retained mangled key would mask the real diagnostic), and time.Time
// values (yaml.v3 auto-detects ISO-8601 literals) become UTC RFC 3339
// strings, preserving the documented wire format.
func NormaliseYAML(v any) any {
	switch val := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			if ks, ok := k.(string); ok {
				out[ks] = NormaliseYAML(vv)
			}
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = NormaliseYAML(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = NormaliseYAML(item)
		}
		return out
	case time.Time:
		return val.UTC().Format(time.RFC3339)
	default:
		return v
	}
}
