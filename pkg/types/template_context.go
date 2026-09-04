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

// TemplateContext is the runtime view assembled by the Go compiler and injected into
// Recipe and FeatureRecipe Go templates during rendering. This is NOT persisted to disk;
// it exists only in compiler memory during rendering.
// Schema: template-context-v1alpha1.schema.json
//
// Field availability within base rendering:
//   - .source — populated at source resolution from Recipe.Spec.Source after
//     Go-template evaluation. nil before source resolution; nil for Recipes that
//     don't declare source.
//   - .workload — populated at workload synthesis after Workload yaml.Unmarshal +
//     validator.ValidateWorkload + validator.ValidateWorkloadAttachments
//     succeed. nil before workload synthesis; nil for Recipes that don't declare
//     workload:.
type TemplateContext struct {
	Context  ContextBlock       `json:"context" yaml:"context"`
	Values   OrderedMap[any]    `json:"values" yaml:"values"`
	Infra    OrderedMap[any]    `json:"infra" yaml:"infra"`
	Locals   OrderedMap[string] `json:"locals" yaml:"locals"`
	Provides OrderedMap[string] `json:"provides,omitempty,omitzero" yaml:"provides,omitempty"`
	// Features is every attachment on the node, keyed by the instance name
	// the author gave it. One pillar for every attachment family — what
	// differs between attachments is what they produce, not how a template
	// reads them.
	Features       OrderedMap[TemplateAttachment] `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Metadata       *TemplateMetadata              `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	AttachmentName string                         `json:"attachment_name,omitempty" yaml:"attachment_name,omitempty"`
	Inject         *InjectPipe                    `json:"inject,omitempty" yaml:"inject,omitempty"`         // the attachment's own authored delivery pipe
	InjectRaw      any                            `json:"inject_raw,omitempty" yaml:"inject_raw,omitempty"` // immutable Developer intent; null if omitted
	Steps          OrderedMap[StepOutput]         `json:"steps,omitempty,omitzero" yaml:"steps,omitempty"`
	Source         any                            `json:"source,omitempty" yaml:"source,omitempty"`     // resolved Recipe.Spec.Source (from source resolution onward); nil for sourceless Recipes
	Workload       *Workload                      `json:"workload,omitempty" yaml:"workload,omitempty"` // parsed Workload (from workload synthesis onward); nil for non-synthesized Recipes
	Boundary       *BoundaryContext               `json:"boundary,omitempty" yaml:"boundary,omitempty"` // populated only for boundary-scope FRs (spec.executionScope: boundary); nil otherwise
}

// ContextBlock is the compiler-assembled execution context providing identity, topology, and delivery data.
// Schema: template-context-v1alpha1.schema.json#/properties/context
type ContextBlock struct {
	InstanceID string `json:"instance_id" yaml:"instance_id"`
	// RenderedName is the name the node's own provider gives the resources it
	// renders from source the author supplied — the name those supplied templates
	// render against, such as a chart's release name.
	//
	// It is empty where the bound provider derives no such name, and empty is an
	// answer rather than a gap: what the compiler BUILT for a node — a synthesized
	// workload, a placed artifact set — is named after InstanceID instead, because
	// there are no author templates for this name to reach. The two are separate
	// concepts and neither stands in for the other, which is why there is one name
	// for each rather than one value answering both.
	//
	// The value is composed inside the provider's own guest and carried here
	// unread: no alphabet, grammar or length from any target reaches the compiler
	// through it.
	RenderedName string                   `json:"rendered_name,omitempty" yaml:"rendered_name,omitempty"`
	Labels       OrderedMap[string]       `json:"labels,omitempty,omitzero" yaml:"labels,omitempty"`
	Annotations  *AnnotationBuckets       `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	Inject       InjectPipe               `json:"inject" yaml:"inject"`
	Uses         OrderedMap[EnrichedEdge] `json:"uses" yaml:"uses"`
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

// TemplateUntaggedKeys returns each custom mechanism's resolved delivery map
// keyed by the mechanism name the author wrote, so a template addresses a custom
// mechanism exactly the way it addresses env / file / arg.
//
// The core three and the merged union reach a template through their struct tags.
// Additional cannot: it carries `json:"-" yaml:"-"` so that the (un)marshal
// methods above own the routing of custom keys, and the template boundary is
// neither JSON nor YAML — it is a reflective walk that reads the tag directly and
// therefore skips the field. This method is how the same data reaches that third
// boundary, without giving the field a tag that would change what either marshal
// path emits.
//
// Iteration is over the ordered map, so the projection is assembled in author
// order. The returned container is a plain map because every pillar reaches a
// template as one, and a template ranges over it alphabetically like any other.
//
// A pipe carrying no custom mechanism returns nil, which keeps the template view
// of a core-three-only pipe exactly what it was.
func (p InjectPipe) TemplateUntaggedKeys() map[string]any {
	if p.Additional.Len() == 0 {
		return nil
	}
	out := make(map[string]any, p.Additional.Len())
	for mechanism, deliveries := range p.Additional.All() {
		out[mechanism] = deliveries
	}
	return out
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
	// config, opaque to the core, projected at
	// .context.uses.<alias>.{values,infra} (the uses map is alias-keyed).
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

// TemplateAttachment is one attachment available in the template context —
// the per-instance view of an attached Feature, keyed by the instance name the
// author gave it. Every attachment exposes the same shape; what differs
// between attachments is what they produce, not the shape they are read through.
//
// Type is the attached Feature's catalog URI. Values and Infra are the
// attachment's own per-instance pillars — developer intent and operational
// sizing — carried verbatim from the attachment declaration and opaque to the
// compiler.
//
// ResolvedVersion is the attached Feature's version after Manifest resolution,
// so a template reads the version actually bound rather than restating a pin.
// Labels are the attached Feature's catalog labels, carried verbatim in the
// order its catalog entity declares them — neither filtered, normalised nor
// prefix-stripped, so a template reads exactly what the catalog author wrote.
// Both mirror the fields of the same name on EnrichedEdge, so an attachment and
// an edge are read the same way. Both are empty when the attachment's type
// resolves to no catalog entity, and Labels is additionally empty when that
// entity declares none.
//
// Both are carried, never interpreted: a template author branches on them, and
// no behaviour is selected from them on the way here. Whether an attachment is
// admitted into a declared slot is decided earlier, against the Feature's own
// catalog metadata, and does not read this projection.
//
// Provides is the bound FeatureRecipe's resolved provides: the attachment's
// published output (an emitted resource's name, a data key, an identity) exposed
// to the consumer as .features.<name>.provides.<key>, mirroring
// .uses.<alias>.provides. The consumer reads the producer's published contract
// instead of re-deriving it. An attachment whose FeatureRecipe publishes nothing
// carries no provides key at all rather than an empty map, on the same terms as
// Labels above, so a template guards before addressing an individual key.
//
// The shape is the same for every attachment family, which is what keeps a
// credential attachment's infra and provides readable rather than silently
// dropped on the way to a template.
//
// Schema: template-context-v1alpha1.schema.json#/$defs/template_attachment
type TemplateAttachment struct {
	Type            string             `json:"type" yaml:"type"`
	ResolvedVersion string             `json:"resolved_version,omitempty" yaml:"resolved_version,omitempty"`
	Labels          OrderedMap[string] `json:"labels,omitempty,omitzero" yaml:"labels,omitempty"`
	Values          OrderedMap[any]    `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
	Infra           OrderedMap[any]    `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
	Provides        OrderedMap[string] `json:"provides,omitempty,omitzero" yaml:"provides,omitempty"`
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
// at feature rendering's boundary-scope dispatch branch; component-scope FRs leave the
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
	FeatureAttachments             []TemplateAttachment      `json:"feature_attachments,omitempty" yaml:"feature_attachments,omitempty"`
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
