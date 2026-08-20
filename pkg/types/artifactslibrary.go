// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// ErrArtifactsLibraryEntryShape fires when an ArtifactsLibrary artifacts entry's
// YAML value is not a mapping of filename to a string scalar body (e.g., the
// author wrote a scalar or sequence where a bundle mapping was expected, or a
// file body that is not a scalar). The envelope JSON Schema rejects the same
// shape at workspace-load time; this sentinel covers the Go-side typed-unmarshal
// path so consumers can errors.Is against the failure without parsing the
// formatted error text.
func ErrArtifactsLibraryEntryShape() error { return errArtifactsLibraryEntryShape }

var errArtifactsLibraryEntryShape = cerrors.Register("types/artifacts-library-entry-shape", cerrors.Meta{
	ShortCode: "E0189",
	Message:   "artifactslibrary: entry must be a mapping of filename to a string body",
})

//go:generate go run ../../internal/tools/typegen artifactslibrary_gen.go

// ArtifactsLibrary's struct declarations (ArtifactsLibrary, ArtifactsLibraryMetadata,
// ArtifactsLibrarySpec) are generated into artifactslibrary_gen.go from the
// published artifactslibrary-v1alpha1 schema; their godoc is the schema's
// descriptions. This hand-written companion holds the ArtifactsLibraryEntry type
// the generated spec references — its custom UnmarshalYAML/MarshalYAML captures a
// bundle of artifact file bodies verbatim — plus the ErrArtifactsLibraryEntryShape
// sentinel above.

// ArtifactsLibraryEntry is one named bundle in the library's artifacts map. A
// bundle groups the output files that belong together (for example a CRD, its
// RBAC, and a ConfigMap); importing the bundle at a single `artifacts.<name>`
// site expands into one output per file. Files maps each output filename to its
// Go template fragment body captured verbatim at ArtifactsLibrary load. The
// distinction from the single-body WorkloadEntry / MappingEntry shape is
// deliberate: an artifact bundle addresses many files under one reference.
//
// The compiler does not parse a file body at entity load; the template parser
// runs at consumption time when the bundle is imported via
// `artifacts.<name>: { $import: ... }`. Storing each body verbatim preserves
// authoring fidelity for re-emission and lets the consumption-side parser handle
// template-content semantics including any cross-library `$import:` references
// inside a body. File author order is preserved (OrderedMap) so emitted output
// is byte-identical across runs.
//
// The type name is ArtifactsLibraryEntry, not ArtifactEntry: the latter is the
// Recipe artifacts-map slot (one output file with inline/file-import modes),
// a different concept from a library bundle of many files.
type ArtifactsLibraryEntry struct {
	Files OrderedMap[string] `json:"-" yaml:"-"`
}

// UnmarshalYAML captures the bundle's filename→body mapping into Files. The
// envelope schema constrains each file value to a string at JSON Schema
// validation time; the Go-side unmarshal accepts any scalar shape and stores the
// rendered Value (yaml.v3 produces the raw scalar text) verbatim per file. A
// non-mapping entry, or a non-scalar file body, produces an error.
func (e *ArtifactsLibraryEntry) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: got kind %d at line %d", ErrArtifactsLibraryEntryShape(), value.Kind, value.Line)
	}
	files := OrderedMap[string]{}
	for i := 0; i+1 < len(value.Content); i += 2 {
		keyNode := value.Content[i]
		valNode := value.Content[i+1]
		if valNode.Kind != yaml.ScalarNode {
			return fmt.Errorf("%w: file %q body must be a string scalar (got kind %d at line %d)",
				ErrArtifactsLibraryEntryShape(), keyNode.Value, valNode.Kind, valNode.Line)
		}
		files.Set(keyNode.Value, valNode.Value)
	}
	e.Files = files
	return nil
}

// MarshalYAML emits the bundle as a mapping of filename to body, each body a
// YAML literal-style scalar (`|`) when it spans multiple lines so template
// fragments preserve newlines and indentation through round-trip. Single-line
// bodies fall back to yaml.v3's default scalar emission; literal style is
// reserved for multi-line bodies because literal style on a single-line scalar
// produces a leading `|` that visually misleads. File order is preserved.
func (e ArtifactsLibraryEntry) MarshalYAML() (any, error) {
	if e.Files.Len() == 0 {
		return nil, nil
	}
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, name := range e.Files.Keys() {
		body, _ := e.Files.Get(name)
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: name}
		valNode := &yaml.Node{Kind: yaml.ScalarNode, Value: body}
		if strings.ContainsRune(body, '\n') {
			valNode.Style = yaml.LiteralStyle
		}
		node.Content = append(node.Content, keyNode, valNode)
	}
	return node, nil
}
