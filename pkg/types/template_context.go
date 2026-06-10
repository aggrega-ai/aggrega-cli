// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// TemplateContext is the runtime view assembled by the Go compiler and injected into
// Recipe and FeatureRecipe Go templates during Step 5. This is NOT persisted to disk;
// it exists only in compiler memory during rendering.
// Schema: template-context-v1alpha1.schema.json
//
// Phase B sub-pass field availability:
//   - .source — populated at Phase B.0 from Recipe.Spec.Source after Go-template
//     evaluation. nil before B.0; nil for Recipes that don't declare source.
//   - .workload — populated at Phase B.2 after Workload yaml.Unmarshal +
//     validator.ValidateWorkload + validator.ValidateWorkloadSecrets succeed.
//     nil before B.2; nil for Recipes that don't declare workload:.
type TemplateContext struct {
	Context    ContextBlock                `json:"context" yaml:"context"`
	Values     OrderedMap[any]             `json:"values" yaml:"values"`
	Infra      OrderedMap[any]             `json:"infra" yaml:"infra"`
	Locals     OrderedMap[string]          `json:"locals" yaml:"locals"`
	Provides   OrderedMap[string]          `json:"provides,omitempty,omitzero" yaml:"provides,omitempty"`
	Features   OrderedMap[TemplateFeature] `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Secrets    OrderedMap[TemplateSecret]  `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
	Metadata   *TemplateMetadata           `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	SecretName string                      `json:"secret_name,omitempty" yaml:"secret_name,omitempty"`
	Inject     *InjectPipe                 `json:"inject,omitempty" yaml:"inject,omitempty"`         // secret-specific inject
	InjectRaw  any                         `json:"inject_raw,omitempty" yaml:"inject_raw,omitempty"` // immutable Developer intent; null if omitted
	Steps      OrderedMap[StepOutput]      `json:"steps,omitempty,omitzero" yaml:"steps,omitempty"`
	Source     any                         `json:"source,omitempty" yaml:"source,omitempty"`     // resolved Recipe.Spec.Source (Phase B.0+); nil for sourceless Recipes
	Workload   *Workload                   `json:"workload,omitempty" yaml:"workload,omitempty"` // parsed Workload (Phase B.2+); nil for non-synthesized Recipes
	Boundary   *BoundaryContext            `json:"boundary,omitempty" yaml:"boundary,omitempty"` // populated only for boundary-scope FRs (spec.executionScope: boundary); nil otherwise
}

// ContextBlock is the compiler-assembled execution context providing identity, topology, and delivery data.
// Schema: template-context-v1alpha1.schema.json#/properties/context
type ContextBlock struct {
	InstanceID  string                   `json:"instance_id" yaml:"instance_id"`
	Labels      OrderedMap[string]       `json:"labels,omitempty,omitzero" yaml:"labels,omitempty"`
	Annotations *AnnotationBuckets       `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	Inject      InjectPipe               `json:"inject" yaml:"inject"`
	Uses        OrderedMap[EnrichedEdge] `json:"uses" yaml:"uses"`
}

// AnnotationBuckets holds the three cascading annotation origins for a node.
// Schema: template-context-v1alpha1.schema.json context.annotations
type AnnotationBuckets struct {
	Release   OrderedMap[string] `json:"release,omitempty,omitzero" yaml:"release,omitempty"`
	Catalog   OrderedMap[string] `json:"catalog,omitempty,omitzero" yaml:"catalog,omitempty"`
	Aggregate OrderedMap[string] `json:"aggregate,omitempty,omitzero" yaml:"aggregate,omitempty"`
}

// InjectPipe is the pre-hydrated delivery pipe available in templates.
// Known mechanisms: env, file, arg, all. Custom mechanisms beyond the core
// three are routed into Additional preserving author order; the runtime-side
// shape carries fully-resolved string-to-string maps per mechanism.
//
// Duplicate top-level mechanism keys (any combination of env/file/arg/all/custom)
// are rejected at the unmarshal layer with a wrapped error; the check fires
// uniformly across all key kinds.
//
// The `json:"-" yaml:"-"` tag on Additional keeps standard tag-driven dispatch
// from observing the field; the UnmarshalYAML / UnmarshalJSON / MarshalYAML /
// MarshalJSON methods take responsibility for routing keys into and out of it.
//
// Schema: template-context-v1alpha1.schema.json context.inject
type InjectPipe struct {
	Env        OrderedMap[string]             `json:"env,omitempty,omitzero" yaml:"env,omitempty"`
	File       OrderedMap[string]             `json:"file,omitempty,omitzero" yaml:"file,omitempty"`
	Arg        OrderedMap[string]             `json:"arg,omitempty,omitzero" yaml:"arg,omitempty"`
	All        OrderedMap[string]             `json:"all,omitempty,omitzero" yaml:"all,omitempty"`
	Additional OrderedMap[OrderedMap[string]] `json:"-" yaml:"-"`
}

// UnmarshalYAML decodes a mapping node into the InjectPipe, dispatching env /
// file / arg / all into the typed fields and routing every other top-level key
// into Additional preserving author order. Duplicate keys at any kind return
// a wrapped error.
func (p *InjectPipe) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("InjectPipe: expected mapping node, got kind %d at line %d", node.Kind, node.Line)
	}
	seen := make(map[string]struct{}, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		keyNode, valNode := node.Content[i], node.Content[i+1]
		var key string
		if err := keyNode.Decode(&key); err != nil {
			return fmt.Errorf("InjectPipe: decoding key at line %d: %w", keyNode.Line, err)
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("InjectPipe: duplicate mechanism key %q at line %d", key, keyNode.Line)
		}
		seen[key] = struct{}{}
		switch key {
		case "env":
			if err := valNode.Decode(&p.Env); err != nil {
				return fmt.Errorf("InjectPipe.env at line %d: %w", valNode.Line, err)
			}
		case "file":
			if err := valNode.Decode(&p.File); err != nil {
				return fmt.Errorf("InjectPipe.file at line %d: %w", valNode.Line, err)
			}
		case "arg":
			if err := valNode.Decode(&p.Arg); err != nil {
				return fmt.Errorf("InjectPipe.arg at line %d: %w", valNode.Line, err)
			}
		case "all":
			if err := valNode.Decode(&p.All); err != nil {
				return fmt.Errorf("InjectPipe.all at line %d: %w", valNode.Line, err)
			}
		default:
			var v OrderedMap[string]
			if err := valNode.Decode(&v); err != nil {
				return fmt.Errorf("InjectPipe.%s at line %d: %w", key, valNode.Line, err)
			}
			p.Additional.Set(key, v)
		}
	}
	return nil
}

// UnmarshalJSON decodes a JSON object into the InjectPipe with the same
// dispatch as UnmarshalYAML. Streaming json.Decoder preserves source key order.
func (p *InjectPipe) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("InjectPipe: reading JSON opening token: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("InjectPipe: expected JSON object, got token %v", tok)
	}
	seen := make(map[string]struct{})
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("InjectPipe: reading JSON key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("InjectPipe: expected string key, got token %v", keyTok)
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("InjectPipe: duplicate mechanism key %q", key)
		}
		seen[key] = struct{}{}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("InjectPipe: reading JSON value for key %q: %w", key, err)
		}
		switch key {
		case "env":
			if err := json.Unmarshal(raw, &p.Env); err != nil {
				return fmt.Errorf("InjectPipe.env: %w", err)
			}
		case "file":
			if err := json.Unmarshal(raw, &p.File); err != nil {
				return fmt.Errorf("InjectPipe.file: %w", err)
			}
		case "arg":
			if err := json.Unmarshal(raw, &p.Arg); err != nil {
				return fmt.Errorf("InjectPipe.arg: %w", err)
			}
		case "all":
			if err := json.Unmarshal(raw, &p.All); err != nil {
				return fmt.Errorf("InjectPipe.all: %w", err)
			}
		default:
			var v OrderedMap[string]
			if err := json.Unmarshal(raw, &v); err != nil {
				return fmt.Errorf("InjectPipe.%s: %w", key, err)
			}
			p.Additional.Set(key, v)
		}
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return fmt.Errorf("InjectPipe: reading JSON closing token: %w", err)
	}
	return nil
}

// MarshalYAML emits a mapping node with env / file / arg / all first if non-zero
// (in struct-field order), followed by Additional entries in author order.
func (p InjectPipe) MarshalYAML() (any, error) {
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addPair := func(k string, v any) error {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
		valNode := &yaml.Node{}
		if err := valNode.Encode(v); err != nil {
			return fmt.Errorf("InjectPipe: encoding value for key %q: %w", k, err)
		}
		out.Content = append(out.Content, keyNode, valNode)
		return nil
	}
	if !p.Env.IsZero() {
		if err := addPair("env", p.Env); err != nil {
			return nil, err
		}
	}
	if !p.File.IsZero() {
		if err := addPair("file", p.File); err != nil {
			return nil, err
		}
	}
	if !p.Arg.IsZero() {
		if err := addPair("arg", p.Arg); err != nil {
			return nil, err
		}
	}
	if !p.All.IsZero() {
		if err := addPair("all", p.All); err != nil {
			return nil, err
		}
	}
	for k, v := range p.Additional.All() {
		if err := addPair(k, v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// MarshalJSON emits a JSON object preserving the same emission order as
// MarshalYAML — env / file / arg / all first if non-zero, then Additional in
// author order.
func (p InjectPipe) MarshalJSON() ([]byte, error) {
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
			return fmt.Errorf("InjectPipe: encoding JSON key %q: %w", k, err)
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')
		valBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("InjectPipe: encoding JSON value for key %q: %w", k, err)
		}
		buf.Write(valBytes)
		return nil
	}
	if !p.Env.IsZero() {
		if err := addPair("env", p.Env); err != nil {
			return nil, err
		}
	}
	if !p.File.IsZero() {
		if err := addPair("file", p.File); err != nil {
			return nil, err
		}
	}
	if !p.Arg.IsZero() {
		if err := addPair("arg", p.Arg); err != nil {
			return nil, err
		}
	}
	if !p.All.IsZero() {
		if err := addPair("all", p.All); err != nil {
			return nil, err
		}
	}
	for k, v := range p.Additional.All() {
		if err := addPair(k, v); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// EnrichedEdge is an enriched topological edge assembled by the compiler for template consumption.
// Schema: template-context-v1alpha1.schema.json#/$defs/enriched_edge
type EnrichedEdge struct {
	Source          string                         `json:"source,omitempty" yaml:"source,omitempty"`
	Target          string                         `json:"target" yaml:"target"`
	Socket          string                         `json:"socket" yaml:"socket"`
	Provides        OrderedMap[string]             `json:"provides" yaml:"provides"`
	Inject          OrderedMap[OrderedMap[string]] `json:"inject" yaml:"inject"`
	Labels          OrderedMap[string]             `json:"labels,omitempty,omitzero" yaml:"labels,omitempty"`
	Annotations     *AnnotationBuckets             `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	Component       string                         `json:"component,omitempty" yaml:"component,omitempty"`
	ResolvedVersion string                         `json:"resolved_version,omitempty" yaml:"resolved_version,omitempty"`
	BoundaryPath    string                         `json:"boundary_path,omitempty" yaml:"boundary_path,omitempty"`
	OriginFeature   string                         `json:"origin_feature,omitempty" yaml:"origin_feature,omitempty"`
	// OriginRelease is true for a Release-created edge (authorship marker,
	// mirroring IREdge). Values / Infra carry the resolved per-connection
	// config, opaque to the core, projected at .uses.<socket>.<alias>.{values,infra}.
	OriginRelease bool            `json:"origin_release,omitempty" yaml:"origin_release,omitempty"`
	Values        OrderedMap[any] `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
	Infra         OrderedMap[any] `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`

	// Per-endpoint network inputs, carried as pure opaque data for a provider
	// engine to resolve into a network identity (the compose engine compares
	// resolved names to decide whether an edge crosses a network seam). The core
	// does not interpret these. SourceNamespace/TargetNamespace are the raw
	// `aggrega.ai/namespace` annotation values (empty when absent); the
	// *BoundaryPath fields are the endpoints' boundary paths used for the
	// no-namespace fallback.
	SourceNamespace    string `json:"source_namespace,omitempty" yaml:"source_namespace,omitempty"`
	SourceBoundaryPath string `json:"source_boundary_path,omitempty" yaml:"source_boundary_path,omitempty"`
	TargetNamespace    string `json:"target_namespace,omitempty" yaml:"target_namespace,omitempty"`
	TargetBoundaryPath string `json:"target_boundary_path,omitempty" yaml:"target_boundary_path,omitempty"`
}

// TemplateMetadata provides Release and compilation identity for deterministic naming.
// Schema: template-context-v1alpha1.schema.json#/properties/metadata
type TemplateMetadata struct {
	ReleaseName    string `json:"release_name" yaml:"release_name"`
	ReleaseVersion string `json:"release_version" yaml:"release_version"`
	Environment    string `json:"environment" yaml:"environment"`
	AggregateName  string `json:"aggregate_name" yaml:"aggregate_name"`
}

// TemplateFeature is a feature attachment available in the template context.
// Schema: template-context-v1alpha1.schema.json#/properties/features
type TemplateFeature struct {
	Type   string          `json:"type" yaml:"type"`
	Values OrderedMap[any] `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
	Infra  OrderedMap[any] `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
}

// TemplateSecret is a credential feature attachment available in the template context.
// Schema: template-context-v1alpha1.schema.json#/properties/secrets
type TemplateSecret struct {
	Type   string          `json:"type" yaml:"type"`
	Values OrderedMap[any] `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
}

// StepOutput holds the outputs exported by one pipeline step for inter-step communication.
// Schema: template-context-v1alpha1.schema.json#/properties/steps
type StepOutput struct {
	Outputs OrderedMap[string] `json:"outputs" yaml:"outputs"`
}

// BoundaryContext is the boundary-scope view assembled by the compiler when
// evaluating a FeatureRecipe with spec.executionScope: boundary. It replaces
// the per-component .context block: an FR with executionScope: boundary
// receives .boundary.* in templates instead of .context.*. Population happens
// at Phase B.6's boundary-scope dispatch branch; component-scope FRs leave the
// pointer nil. Schema: template-context-v1alpha1.schema.json#/properties/boundary
type BoundaryContext struct {
	Path                           string                    `json:"path" yaml:"path"`
	BoundaryName                   string                    `json:"boundary_name" yaml:"boundary_name"`
	Components                     OrderedMap[ComponentInfo] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
	InternalUsesEdges              []EnrichedEdge            `json:"internal_uses_edges,omitempty" yaml:"internal_uses_edges,omitempty"`
	OutgoingCrossBoundaryUsesEdges []EnrichedEdge            `json:"outgoing_cross_boundary_uses_edges,omitempty" yaml:"outgoing_cross_boundary_uses_edges,omitempty"`
	IncomingCrossBoundaryUsesEdges []EnrichedEdge            `json:"incoming_cross_boundary_uses_edges,omitempty" yaml:"incoming_cross_boundary_uses_edges,omitempty"`
	CrossBoundaryUsesEdges         []EnrichedEdge            `json:"cross_boundary_uses_edges,omitempty" yaml:"cross_boundary_uses_edges,omitempty"`
	Labels                         OrderedMap[string]        `json:"labels,omitempty,omitzero" yaml:"labels,omitempty"`
	Annotations                    OrderedMap[string]        `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
	FeatureAttachments             []TemplateFeature         `json:"feature_attachments,omitempty" yaml:"feature_attachments,omitempty"`
}

// ComponentInfo is the per-component view exposed via .boundary.components
// in boundary-scope FeatureRecipe templates. One entry per component in
// the boundary's subtree, keyed by instance ID.
//
// The Go type fixes the field set to a contract; the JSON schema for
// .boundary.components stays open (additionalProperties: true) so future
// pillars can be added without a schema-version bump. When new fields
// surface, they extend ComponentInfo and the schema description picks
// them up via documentation rather than schema constraint.
//
// Schema: template-context-v1alpha1.schema.json#/properties/boundary/properties/components
type ComponentInfo struct {
	InstanceID      string `json:"instance_id" yaml:"instance_id"`
	Component       string `json:"component" yaml:"component"`
	ResolvedVersion string `json:"resolved_version,omitempty" yaml:"resolved_version,omitempty"`
	BoundaryPath    string `json:"boundary_path,omitempty" yaml:"boundary_path,omitempty"`
}
