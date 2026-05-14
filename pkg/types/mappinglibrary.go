// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrMappingEntryNotScalar fires when a MappingLibrary mapping entry's
// YAML value is not a scalar (e.g., the author wrote a mapping or
// sequence instead of a string body). The envelope JSON Schema rejects
// the same shape at workspace-load time; this sentinel covers the
// Go-side typed-unmarshal path so consumers can errors.Is against the
// failure without parsing the formatted error text.
var ErrMappingEntryNotScalar = errors.New("mappinglibrary: entry body must be a string scalar")

// MappingLibrary is the typed Go representation of the aggrega.ai/v1alpha1
// MappingLibrary catalog entity. The entity holds reusable Go template
// fragments that Recipes and FeatureRecipes consume at render time via
// `valuesMapping: { $import: ... }` directives. Tier 3 in the entity-tier
// model — opt-in for catalogs that need cross-Recipe mapping reuse.
//
// Schema: mappinglibrary-v1alpha1.schema.json (in the aggrega-schemas repo).
type MappingLibrary struct {
	APIVersion string                 `json:"apiVersion" yaml:"apiVersion"`
	Kind       string                 `json:"kind" yaml:"kind"`
	Metadata   MappingLibraryMetadata `json:"metadata" yaml:"metadata"`
	Spec       MappingLibrarySpec     `json:"spec" yaml:"spec"`
}

// MappingLibraryMetadata carries identity for a MappingLibrary. The name
// field enforces PascalCase at the JSON Schema envelope level
// (`^[A-Z][A-Za-z0-9]*$`); the Go-side struct doesn't re-validate that
// constraint — the envelope validation at parse time is authoritative.
type MappingLibraryMetadata struct {
	Name        string             `json:"name" yaml:"name"`
	Version     string             `json:"version" yaml:"version"`
	Description string             `json:"description,omitempty" yaml:"description,omitempty"`
	Labels      OrderedMap[string] `json:"labels,omitempty,omitzero" yaml:"labels,omitempty"`
	Annotations OrderedMap[string] `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
}

// MappingLibrarySpec carries the library's content: the required mappings
// map. OrderedMap-backed so author-order survives YAML/JSON round-trips
// and the emit path is determinism-stable. No `mappingsJsonnet:` sibling
// field; the envelope schema rejects unknown spec siblings via
// `additionalProperties: false` so the single-authoring-mode posture is
// enforced at load time.
type MappingLibrarySpec struct {
	Mappings OrderedMap[MappingEntry] `json:"mappings" yaml:"mappings"`
}

// MappingEntry is one named mapping in the library's mappings map. Body
// is the Go template fragment captured verbatim at MappingLibrary load.
// The compiler does not parse the fragment at entity load; the template
// parser runs at Recipe / FeatureRecipe consumption time when the entry
// is imported via `valuesMapping: { $import: ... }`. Storing the fragment
// verbatim preserves authoring fidelity for re-emission and lets the
// consumption-side parser handle template-content semantics including
// any cross-library `$import:` references inside the body.
//
// URL is the resolved canonical reference URL computed post-parse:
// `oci://<host>/<library>@<version>#/mappings/<name>`. Named URL rather
// than ID because consumers reference these via URL-syntax strings at
// `valuesMapping: { $import: <url> }` consumption sites; SchemaLibrary's
// parallel field is named ID because JSON Schema spec calls the cross-
// document identifier `$id` and that field's primary consumer is the
// JSON Schema validator's `$ref` resolution machinery.
type MappingEntry struct {
	Body string `json:"-" yaml:"-"`
	URL  string `json:"-" yaml:"-"`
}

// UnmarshalYAML captures the entry's YAML scalar value into Body. The
// envelope schema constrains the value to a string at JSON Schema
// validation time; the Go-side unmarshal accepts any scalar shape and
// stores the rendered Value (yaml.v3 produces the raw scalar text).
// Non-scalar nodes (mapping / sequence) produce an error.
func (e *MappingEntry) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return nil
	}
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("%w: got kind %d at line %d", ErrMappingEntryNotScalar, value.Kind, value.Line)
	}
	e.Body = value.Value
	return nil
}

// MarshalYAML emits Body as a YAML literal-style scalar (`|`) so multi-
// line template fragments preserve newlines and indentation through
// round-trip. Single-line bodies fall back to yaml.v3's default scalar
// emission (plain or double-quoted depending on content); literal style
// is reserved for multi-line bodies because literal style on a single-
// line scalar produces a leading `|` that visually misleads.
func (e MappingEntry) MarshalYAML() (any, error) {
	if e.Body == "" {
		return nil, nil
	}
	node := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: e.Body,
	}
	if strings.ContainsRune(e.Body, '\n') {
		node.Style = yaml.LiteralStyle
	}
	return node, nil
}
