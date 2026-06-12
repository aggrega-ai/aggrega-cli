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

//go:generate go run ../../internal/tools/typegen mappinglibrary_gen.go

// MappingLibrary's struct declarations (MappingLibrary, MappingLibraryMetadata,
// MappingLibrarySpec) are generated into mappinglibrary_gen.go from the
// published mappinglibrary-v1alpha1 schema; their godoc is the schema's
// descriptions. This hand-written companion holds the MappingEntry type the
// generated spec references — its custom UnmarshalYAML/MarshalYAML captures the
// Go-template fragment body verbatim — plus the ErrMappingEntryNotScalar
// sentinel above.

// MappingEntry is one named mapping in the library's mappings map. Body
// is the Go template fragment captured verbatim at MappingLibrary load.
// The compiler does not parse the fragment at entity load; the template
// parser runs at Recipe / FeatureRecipe consumption time when the entry
// is imported via `valuesMapping: { $import: ... }`. Storing the fragment
// verbatim preserves authoring fidelity for re-emission and lets the
// consumption-side parser handle template-content semantics including
// any cross-library `$import:` references inside the body.
type MappingEntry struct {
	Body string `json:"-" yaml:"-"`
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
