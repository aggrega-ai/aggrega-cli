// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"gopkg.in/yaml.v3"
)

// SchemaLibrary is the typed Go representation of the aggrega.ai/v1alpha1
// SchemaLibrary catalog entity. The entity holds reusable JSON Schema 2020-12
// definitions plus optional parameterized helpers (`$templates:`). Tier 3 in
// the entity-tier model — opt-in for catalogs that need cross-Component
// schema reuse.
//
// Schema: schemalibrary-v1alpha1.schema.json (in the aggrega-schemas repo).
type SchemaLibrary struct {
	APIVersion string                `json:"apiVersion" yaml:"apiVersion"`
	Kind       string                `json:"kind" yaml:"kind"`
	Metadata   SchemaLibraryMetadata `json:"metadata" yaml:"metadata"`
	Spec       SchemaLibrarySpec     `json:"spec" yaml:"spec"`
}

// SchemaLibraryMetadata carries identity for a SchemaLibrary. The name field
// enforces PascalCase at the JSON Schema envelope level (`^[A-Z][A-Za-z0-9]*$`);
// the Go-side struct doesn't re-validate that constraint — the envelope
// validation at parse time is authoritative.
type SchemaLibraryMetadata struct {
	Name        string             `json:"name" yaml:"name"`
	Version     string             `json:"version" yaml:"version"`
	Description string             `json:"description,omitempty" yaml:"description,omitempty"`
	Labels      OrderedMap[string] `json:"labels,omitempty,omitzero" yaml:"labels,omitempty"`
	Annotations OrderedMap[string] `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
}

// SchemaLibrarySpec carries the library's content: the required schemas map
// plus the optional templates map. Both are OrderedMap-backed so author-
// order survives YAML/JSON round-trips and the emit path is determinism-
// stable.
type SchemaLibrarySpec struct {
	Schemas   OrderedMap[SchemaEntry]   `json:"schemas" yaml:"schemas"`
	Templates OrderedMap[TemplateEntry] `json:"templates,omitempty,omitzero" yaml:"templates,omitempty"`
}

// SchemaEntry is one named schema in the library's schemas map. Content is
// carried as yaml.Node so the parser dispatch (compact form vs JSON Schema
// YAML vs mixed) defers to the compact-form compiler's content-shape
// heuristic at parse time; storing the raw node preserves authoring fidelity
// for re-emission (deterministic output requires byte-identical round-trip).
//
// ID is the resolved canonical $id, computed post-parse from the workspace
// lockfile entry: `oci://<host>/<library>@<version>#/schemas/<name>`. Local
// development (where the library is unpacked under the catalog tree) uses
// the placeholder host `local.aggrega`; publish-time resolution replaces
// the placeholder with the canonical host.
type SchemaEntry struct {
	Content yaml.Node `json:"-" yaml:"-"`
	ID      string    `json:"-" yaml:"-"`
}

// UnmarshalYAML captures the entry's raw YAML node so the parser dispatch
// (compact form vs JSON Schema) can inspect content shape at parse time.
// The ID field is populated post-parse by the canonical-$id derivation.
func (e *SchemaEntry) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return nil
	}
	e.Content = *value
	return nil
}

// MarshalYAML re-emits the captured node so round-trip fidelity is
// preserved at byte level (the deterministic-output contract).
func (e SchemaEntry) MarshalYAML() (any, error) {
	if e.Content.Kind == 0 {
		return nil, nil
	}
	return &e.Content, nil
}

// TemplateEntry is one named template in the library's templates map.
// Params carries the named typed parameter declarations; Body carries the
// template's compact-form / JSON Schema YAML body referencing those
// parameters via literal scalar substitution at `$instantiate:` time.
//
// The substitution semantic is literal-scalar-only — no embedded Go
// templates, no conditionals, no loops. The parameter-substitution
// implementation lives in `pkg/types/schemalibrary_templates.go`.
type TemplateEntry struct {
	Params OrderedMap[ParamDecl] `json:"$params" yaml:"$params"`
	Body   yaml.Node             `json:"-" yaml:"-"`
}

// UnmarshalYAML splits the template entry into the typed $params field plus
// the body (everything else, captured as a yaml.Node for later
// substitution). The split lets the substitution engine walk the body
// without re-parsing while keeping the parameter declarations typed.
func (t *TemplateEntry) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Kind != yaml.MappingNode {
		return nil
	}

	// Build the body node by copying all entries EXCEPT $params, which is
	// peeled off into the typed Params field.
	body := *value
	body.Content = body.Content[:0]
	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i]
		val := value.Content[i+1]
		if key.Value == "$params" {
			if err := val.Decode(&t.Params); err != nil {
				return err
			}
			continue
		}
		body.Content = append(body.Content, key, val)
	}
	t.Body = body
	return nil
}

// MarshalYAML re-assembles the template entry as a mapping node with
// $params first (author-order convention) followed by the body's content.
// Round-trip fidelity is preserved at field-presence level; per-field byte
// equality depends on yaml.v3's emitter behavior.
func (t TemplateEntry) MarshalYAML() (any, error) {
	out := yaml.Node{Kind: yaml.MappingNode}
	if t.Params.Len() > 0 {
		paramsKey := &yaml.Node{Kind: yaml.ScalarNode, Value: "$params"}
		paramsVal := &yaml.Node{}
		if err := paramsVal.Encode(&t.Params); err != nil {
			return nil, err
		}
		out.Content = append(out.Content, paramsKey, paramsVal)
	}
	if t.Body.Kind != 0 {
		out.Content = append(out.Content, t.Body.Content...)
	}
	return out, nil
}

// ParamDecl is one named template parameter declaration. Carries the
// parameter's type + optional default + optional constraint. The
// substitution engine uses Type to type-check `$args:` bindings at
// instantiation; Default fires when the binding omits the parameter;
// Constraint (optional, opaque to the substitution engine itself) is
// metadata for tooling.
//
// SchemaLibrary template parameters carry literal-scalar-substitution
// semantic (no Go templating, no conditionals, no loops); a fresh type
// keeps that semantic distinct from Aggregate-level parameters' full-
// Go-template substitution and prevents downstream consumers from
// conflating the two evaluation models.
type ParamDecl struct {
	Type       string `json:"type" yaml:"type"`
	Default    any    `json:"default,omitempty" yaml:"default,omitempty"`
	Constraint string `json:"constraint,omitempty" yaml:"constraint,omitempty"`
}
