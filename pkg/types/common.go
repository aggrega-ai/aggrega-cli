// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

// Maintainer identifies an entity owner.
// Shared across all entity metadata blocks.
type Maintainer struct {
	Name  string `json:"name" yaml:"name"`
	Email string `json:"email,omitempty" yaml:"email,omitempty"`
	URL   string `json:"url,omitempty" yaml:"url,omitempty"`
}

// SourceDigest is a recipe-carried content pin for one resolved remote source.
// Identity is the triple {Kind, URL, Version}; Digest is the SHA-256 of the
// fetched archive and ManifestDigest is the OCI manifest digest for an
// OCI-distributed source (empty otherwise). The resolution phase freezes these
// from the fetched bytes into a writable recipe file; the build reads them back
// and verifies the cached source has not drifted.
//
// Defined in recipe-v1alpha1.schema.json#/properties/spec/properties/sourceDigests/items
// and mirrored by featurerecipe-v1alpha1.schema.json.
type SourceDigest struct {
	Kind           string `json:"kind" yaml:"kind"`
	URL            string `json:"url" yaml:"url"`
	Version        string `json:"version" yaml:"version"`
	Digest         string `json:"digest" yaml:"digest"`
	ManifestDigest string `json:"manifestDigest,omitempty" yaml:"manifestDigest,omitempty"`
}

// ArtifactDigest is a recipe-carried content pin for one artifact whose value is
// a file import. Name is the artifacts: map key; Digest is the SHA-256 of the
// imported file's bytes. Inline (literal) artifacts are part of the entity and
// carry no separate pin.
//
// Defined in recipe-v1alpha1.schema.json#/properties/spec/properties/artifactDigests/items
// and mirrored by featurerecipe-v1alpha1.schema.json.
type ArtifactDigest struct {
	Name   string `json:"name" yaml:"name"`
	Digest string `json:"digest" yaml:"digest"`
}

// WellKnownAnnotation documents one platform-native annotation key a
// provider's rendering engine reads, for catalog readers and the inspect
// surface. It is descriptive contract metadata — the compiler does not
// consume it to gate or branch a build.
type WellKnownAnnotation struct {
	Key             string `json:"key" yaml:"key"`
	Purpose         string `json:"purpose" yaml:"purpose"`
	DefaultBehavior string `json:"defaultBehavior,omitempty" yaml:"defaultBehavior,omitempty"`
}

// ComponentType is the 7-type Cloud-Native Ontology from component-v1alpha1.schema.json.
type ComponentType string

const (
	ComponentTypeServer         ComponentType = "server"
	ComponentTypeWorker         ComponentType = "worker"
	ComponentTypeTask           ComponentType = "task"
	ComponentTypeFunction       ComponentType = "function"
	ComponentTypeStatefulServer ComponentType = "stateful-server"
	ComponentTypeResource       ComponentType = "resource"
	ComponentTypeMessaging      ComponentType = "messaging"
)

// ValidComponentTypes is the set of valid ComponentType values for quick lookup.
var ValidComponentTypes = map[ComponentType]bool{
	ComponentTypeServer:         true,
	ComponentTypeWorker:         true,
	ComponentTypeTask:           true,
	ComponentTypeFunction:       true,
	ComponentTypeStatefulServer: true,
	ComponentTypeResource:       true,
	ComponentTypeMessaging:      true,
}

// IsValidComponentType returns true if t is one of the 7-type ontology values.
func IsValidComponentType(t ComponentType) bool {
	return ValidComponentTypes[t]
}

// ComponentTypeLabel is the reserved label key for the 7-type ontology.
const ComponentTypeLabel = "component.aggrega.ai/type"

// FeatureKindLabel is the reserved label key for credential feature discrimination.
const FeatureKindLabel = "feature.aggrega.ai/kind"

// FeatureKindSecret is the label value marking a Feature as a credential Feature.
const FeatureKindSecret = "secret"

// InjectBlock is the agnostic injection routing block from
// aggregate-v1alpha1.schema.json#/$defs/inject_block. Known mechanisms are env,
// file, arg. Custom mechanisms beyond the core three are routed into Additional
// preserving author order; the schema's `additionalProperties` slot accepts any
// mechanism conforming to the inject_delivery shape, and the custom UnmarshalYAML
// / UnmarshalJSON hooks below populate Additional from non-core top-level keys.
//
// Each core mechanism value is an InjectDelivery: either compact map[string]string
// or verbose []{in,from}. Both forms are represented as any and normalized to
// map[string]string downstream. Additional values carry the same polymorphic
// shape and the same downstream normalization.
//
// Duplicate top-level mechanism keys (any combination of env/file/arg/custom) are
// rejected at the unmarshal layer with a wrapped error naming the offending key
// and its source line. The check fires uniformly across all key kinds — yaml.v3's
// parser-layer strictness varies, and encoding/json silently overwrites duplicate
// keys in default decode paths; the hook closes the asymmetry.
//
// The `json:"-" yaml:"-"` tag on Additional keeps standard tag-driven dispatch
// from observing the field; the UnmarshalYAML / UnmarshalJSON / MarshalYAML /
// MarshalJSON methods take responsibility for routing keys into and out of it.
type InjectBlock struct {
	Env        any             `json:"env,omitempty" yaml:"env,omitempty"`
	File       any             `json:"file,omitempty" yaml:"file,omitempty"`
	Arg        any             `json:"arg,omitempty" yaml:"arg,omitempty"`
	Additional OrderedMap[any] `json:"-" yaml:"-"`
}

// UnmarshalYAML decodes a mapping node into the InjectBlock, dispatching env /
// file / arg into the typed fields and routing every other top-level key into
// Additional preserving author order. Duplicate top-level keys at any kind
// (core or custom) return a wrapped error.
func (b *InjectBlock) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("InjectBlock: expected mapping node, got kind %d at line %d", node.Kind, node.Line)
	}
	seen := make(map[string]struct{}, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		keyNode, valNode := node.Content[i], node.Content[i+1]
		var key string
		if err := keyNode.Decode(&key); err != nil {
			return fmt.Errorf("InjectBlock: decoding key at line %d: %w", keyNode.Line, err)
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("InjectBlock: duplicate mechanism key %q at line %d", key, keyNode.Line)
		}
		seen[key] = struct{}{}
		switch key {
		case "env":
			if err := valNode.Decode(&b.Env); err != nil {
				return fmt.Errorf("InjectBlock.env at line %d: %w", valNode.Line, err)
			}
		case "file":
			if err := valNode.Decode(&b.File); err != nil {
				return fmt.Errorf("InjectBlock.file at line %d: %w", valNode.Line, err)
			}
		case "arg":
			if err := valNode.Decode(&b.Arg); err != nil {
				return fmt.Errorf("InjectBlock.arg at line %d: %w", valNode.Line, err)
			}
		default:
			var v any
			if err := valNode.Decode(&v); err != nil {
				return fmt.Errorf("InjectBlock.%s at line %d: %w", key, valNode.Line, err)
			}
			b.Additional.Set(key, v)
		}
	}
	return nil
}

// UnmarshalJSON decodes a JSON object into the InjectBlock with the same
// dispatch as UnmarshalYAML. Uses a streaming json.Decoder with Token() to
// observe keys in source order — encoding/json's default object decode collapses
// order, so we parse the object manually one key-value pair at a time.
func (b *InjectBlock) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("InjectBlock: reading JSON opening token: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("InjectBlock: expected JSON object, got token %v", tok)
	}
	seen := make(map[string]struct{})
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("InjectBlock: reading JSON key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("InjectBlock: expected string key, got token %v", keyTok)
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("InjectBlock: duplicate mechanism key %q", key)
		}
		seen[key] = struct{}{}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("InjectBlock: reading JSON value for key %q: %w", key, err)
		}
		switch key {
		case "env":
			if err := json.Unmarshal(raw, &b.Env); err != nil {
				return fmt.Errorf("InjectBlock.env: %w", err)
			}
		case "file":
			if err := json.Unmarshal(raw, &b.File); err != nil {
				return fmt.Errorf("InjectBlock.file: %w", err)
			}
		case "arg":
			if err := json.Unmarshal(raw, &b.Arg); err != nil {
				return fmt.Errorf("InjectBlock.arg: %w", err)
			}
		default:
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return fmt.Errorf("InjectBlock.%s: %w", key, err)
			}
			b.Additional.Set(key, v)
		}
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return fmt.Errorf("InjectBlock: reading JSON closing token: %w", err)
	}
	return nil
}

// MarshalYAML emits a mapping node with env / file / arg first (in struct-field
// order, matching the existing serialised emission) followed by Additional
// entries in author order. Value receiver matches OrderedMap.MarshalYAML's
// dispatch rationale — yaml.v3's reflection sees value-receiver methods on the
// type's method set regardless of addressability.
func (b InjectBlock) MarshalYAML() (any, error) {
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addPair := func(k string, v any) error {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
		valNode := &yaml.Node{}
		if err := valNode.Encode(v); err != nil {
			return fmt.Errorf("InjectBlock: encoding value for key %q: %w", k, err)
		}
		out.Content = append(out.Content, keyNode, valNode)
		return nil
	}
	if b.Env != nil {
		if err := addPair("env", b.Env); err != nil {
			return nil, err
		}
	}
	if b.File != nil {
		if err := addPair("file", b.File); err != nil {
			return nil, err
		}
	}
	if b.Arg != nil {
		if err := addPair("arg", b.Arg); err != nil {
			return nil, err
		}
	}
	for k, v := range b.Additional.All() {
		if err := addPair(k, v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// MarshalJSON emits a JSON object preserving the same emission order as
// MarshalYAML — env / file / arg first if non-nil, then Additional entries in
// author order. Builds the object manually because encoding/json does not expose
// a way to influence its default map-key ordering.
func (b InjectBlock) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	addPair := func(k string, v any) error {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		keyBytes, err := json.Marshal(k)
		if err != nil {
			return fmt.Errorf("InjectBlock: encoding JSON key %q: %w", k, err)
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')
		valBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("InjectBlock: encoding JSON value for key %q: %w", k, err)
		}
		buf.Write(valBytes)
		return nil
	}
	if b.Env != nil {
		if err := addPair("env", b.Env); err != nil {
			return nil, err
		}
	}
	if b.File != nil {
		if err := addPair("file", b.File); err != nil {
			return nil, err
		}
	}
	if b.Arg != nil {
		if err := addPair("arg", b.Arg); err != nil {
			return nil, err
		}
	}
	for k, v := range b.Additional.All() {
		if err := addPair(k, v); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// DecodeVerboseInjectEntry extracts (in, value, from) from a single schema-
// verbose-form inject entry. The entry is one element of the schema's
// inject_delivery array — shaped as {in: K, from?: P, value?: V} per the
// aggregate-v1alpha1 schema. The inner shape can decode as either
// map[string]any (default YAML/JSON unmarshal) or *OrderedMap[any]
// (round-trip-fidelity decode path); the helper handles both uniformly
// so consumer-site decode arms don't asymmetrically skip valid entries
// that happen to arrive via the ordered path.
//
// Schema validation enforces required "in" plus exactly one of "from" or
// "value" per entry; ok=true only when "in" is present, non-empty, and
// string-typed. The hasValue and hasFrom flags let callers pick a
// preference at decode time (sites that propagate both fields into a
// downstream entry struct can ignore the preference; sites that collapse
// into a single string can prefer one). Non-string in/value/from fields,
// or absent entries, return zero-valued slots; ok=false.
func DecodeVerboseInjectEntry(entry any) (in, value, from string, hasValue, hasFrom, ok bool) {
	var get func(key string) (any, bool)
	switch e := entry.(type) {
	case map[string]any:
		get = func(key string) (any, bool) { v, ok := e[key]; return v, ok }
	case *OrderedMap[any]:
		if e == nil {
			return
		}
		get = e.Get
	default:
		return
	}

	inRaw, hasIn := get("in")
	if !hasIn {
		return
	}
	inStr, isStr := inRaw.(string)
	if !isStr || inStr == "" {
		return
	}
	in = inStr

	if vRaw, hv := get("value"); hv {
		if s, isStr := vRaw.(string); isStr {
			value = s
			hasValue = true
		}
	}
	if fRaw, hf := get("from"); hf {
		if s, isStr := fRaw.(string); isStr {
			from = s
			hasFrom = true
		}
	}
	ok = true
	return
}

// FeatureInstance is a feature attachment on a component or subsystem.
// Used in Aggregate component.features, subsystem.features, and Release boundary features.
type FeatureInstance struct {
	Type        string                      `json:"type" yaml:"type"`
	Description string                      `json:"description,omitempty" yaml:"description,omitempty"`
	Values      OrderedMap[any]             `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
	Uses        OrderedMap[UsesSocketValue] `json:"uses,omitempty,omitzero" yaml:"uses,omitempty"`
	Inject      *InjectBlock                `json:"inject,omitempty" yaml:"inject,omitempty"`
	Secrets     OrderedMap[SecretInstance]  `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
}

// SecretInstance is a credential feature attachment (labeled
// feature.aggrega.ai/kind: secret). Used in Aggregate component.secrets,
// feature.secrets, and Release boundary secrets. Structurally parallel to
// [FeatureInstance] except for nested Secrets: credentials are leaf
// attachments and a SecretInstance cannot itself carry nested secrets.
type SecretInstance struct {
	Type        string                      `json:"type" yaml:"type"`
	Description string                      `json:"description,omitempty" yaml:"description,omitempty"`
	Values      OrderedMap[any]             `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
	Uses        OrderedMap[UsesSocketValue] `json:"uses,omitempty,omitzero" yaml:"uses,omitempty"`
	Inject      *InjectBlock                `json:"inject,omitempty" yaml:"inject,omitempty"`
}

// PipelineStep is one Wasm plugin invocation in a Recipe or FeatureRecipe pipeline.
// Defined in recipe-v1alpha1.schema.json#/properties/spec/properties/pipeline/items.
type PipelineStep struct {
	ID string `json:"id" yaml:"id"`
	// Plugin is the URI of the Plugin entity this step runs (e.g.
	// `name:version` or `provider/name:version`). The entity's spec.plugin is the
	// content-addressed .wasm artifact; the build resolves the entity to its
	// artifact and loads the bytes. A bare .wasm or artifact reference is not
	// accepted.
	Plugin   string `json:"plugin" yaml:"plugin"`
	Function string `json:"function" yaml:"function"`
	With     string `json:"with,omitempty" yaml:"with,omitempty"`
	// Optional downgrades a failure of this step from a build-halting
	// error to a loud warning: the pre-step document set is restored, the
	// step's output is discarded, and the pipeline continues. When false,
	// any step failure halts after restoring the pre-step set.
	Optional bool `json:"optional,omitempty" yaml:"optional,omitempty"`
}
