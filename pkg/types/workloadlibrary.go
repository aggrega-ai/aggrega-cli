// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// ErrWorkloadEntryNotScalar fires when a WorkloadLibrary workload entry's
// YAML value is not a scalar (e.g., the author wrote a mapping or
// sequence instead of a string body). The envelope JSON Schema rejects
// the same shape at workspace-load time; this sentinel covers the
// Go-side typed-unmarshal path so consumers can errors.Is against the
// failure without parsing the formatted error text.
func ErrWorkloadEntryNotScalar() error { return errWorkloadEntryNotScalar }

var errWorkloadEntryNotScalar = cerrors.Register("workload-entry-not-scalar", cerrors.Meta{
	ShortCode: "E0199",
	Message:   "workloadlibrary: entry body must be a string scalar",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

//go:generate go run ../../internal/tools/typegen workloadlibrary_gen.go

// WorkloadLibrary's struct declarations (WorkloadLibrary, WorkloadLibraryMetadata,
// WorkloadLibrarySpec) are generated into workloadlibrary_gen.go from the
// published workloadlibrary-v1alpha1 schema; their godoc is the schema's
// descriptions. This hand-written companion holds the WorkloadEntry type the
// generated spec references — its custom UnmarshalYAML/MarshalYAML captures the
// Workload-template fragment body verbatim — plus the ErrWorkloadEntryNotScalar
// sentinel above.

// WorkloadEntry is one named workload in the library's workloads map. Body
// is the Go template fragment captured verbatim at WorkloadLibrary load.
// The compiler does not parse the fragment at entity load; the template
// parser runs at Recipe consumption time when the entry is imported via
// `workload: { $import: ... }`. Storing the fragment verbatim preserves
// authoring fidelity for re-emission and lets the consumption-side parser
// handle template-content semantics including any cross-library `$import:`
// references inside the body.
type WorkloadEntry struct {
	Body string `json:"-" yaml:"-"`
}

// UnmarshalYAML captures the entry's YAML scalar value into Body. The
// envelope schema constrains the value to a string at JSON Schema
// validation time; the Go-side unmarshal accepts any scalar shape and
// stores the rendered Value (yaml.v3 produces the raw scalar text).
// Non-scalar nodes (mapping / sequence) produce an error.
func (e *WorkloadEntry) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return nil
	}
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("%w: got kind %d at line %d", ErrWorkloadEntryNotScalar(), value.Kind, value.Line)
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
func (e WorkloadEntry) MarshalYAML() (any, error) {
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
