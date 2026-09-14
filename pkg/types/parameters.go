// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// ErrParametersShape fires when a Release `parameters:` content carries
// a shape outside the two documented modes: not an object (inline
// parameter map); a mapping with `$import:` plus other siblings; or a
// `$import:` value that isn't a string scalar.
func ErrParametersShape() error { return errParametersShape }

var errParametersShape = cerrors.Register("parameters-shape", cerrors.Meta{
	ShortCode: "E0193",
	Message:   "parameters: shape must be an inline object or {$import: <local-path>}",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ParametersBlock carries the Release's environment-specific parameter
// fulfillment with two content-shape-dispatched modes per chapter 07:
//
//   - Mode 1 (inline): a YAML map carrying structured nested parameter
//     values directly. Inline is an OrderedMap so author key order
//     survives round-trip.
//   - Mode 2 (file import): a YAML map of exactly one key `$import:
//     <local-path>` pointing at a single YAML file supplying the
//     parameter values.
//
// Single-source-only at this site. Multi-file layering is the
// province of Release `extends:` chains: the parent Release supplies
// base parameters; the child overrides via the entity-envelope
// inheritance machinery.
//
// The discriminator is not symmetric with WorkloadTemplate /
// ArtifactEntry / ValuesMapping because Inline carries structured
// content rather than a Go-template string: parameter values are
// data, not templates.
type ParametersBlock struct {
	// Inline carries the inline parameter map (mode 1). Empty when
	// the field used $import: or wasn't authored.
	Inline OrderedMap[any] `json:"-" yaml:"-"`

	// Import carries the $import: target path (mode 2). Empty when
	// the field used inline content or wasn't authored.
	Import string `json:"-" yaml:"-"`
}

// IsEmpty reports whether neither mode is populated. The validator and
// resolver use this to enforce "parameters: declared OR not" semantics.
func (p ParametersBlock) IsEmpty() bool {
	return p.Inline.Len() == 0 && p.Import == ""
}

// UnmarshalYAML dispatches by content shape. The inline branch decodes
// the mapping into the OrderedMap; the import branch extracts the
// $import: target. Maps with both an $import: key and other siblings
// produce ErrParametersShape (the parameters: site is single-source
// only — mixing inline keys with $import: is not authoring guidance
// the language supports today; the migration path for "inline +
// imported common defaults" is extends: chains).
func (p *ParametersBlock) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: got kind %d at line %d", ErrParametersShape(), node.Kind, node.Line)
	}
	// Check whether the mapping declares $import:.
	hasImport := false
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "$import" {
			hasImport = true
			break
		}
	}
	if hasImport {
		// Mode 2: must be exactly one key, $import, string-scalar value.
		if len(node.Content) != 2 {
			return fmt.Errorf("%w: $import: must be the only key (got %d keys) at line %d", ErrParametersShape(), len(node.Content)/2, node.Line)
		}
		keyNode := node.Content[0]
		valNode := node.Content[1]
		if keyNode.Value != "$import" {
			return fmt.Errorf("%w: $import: must be the only key at line %d", ErrParametersShape(), node.Line)
		}
		if valNode.Kind != yaml.ScalarNode {
			return fmt.Errorf("%w: $import value must be a string scalar at line %d", ErrParametersShape(), valNode.Line)
		}
		p.Import = valNode.Value
		return nil
	}
	// Mode 1: decode the mapping into the inline OrderedMap.
	return node.Decode(&p.Inline)
}

// MarshalYAML emits the populated mode. Inline → emit the OrderedMap.
// Import → emit {$import: <value>}. Empty struct → nil (omitempty on
// the owning field elides the key entirely).
func (p ParametersBlock) MarshalYAML() (any, error) {
	switch {
	case p.Inline.Len() > 0:
		return p.Inline, nil
	case p.Import != "":
		return map[string]string{"$import": p.Import}, nil
	default:
		return nil, nil
	}
}
