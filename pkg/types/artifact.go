// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"errors"

	"gopkg.in/yaml.v3"
)

// ErrArtifactEntryShape fires when a Recipe `artifacts.<name>:` value
// carries a shape outside the two documented modes: not a string
// scalar; not a mapping with a single `$import:` key; or a mapping
// with extra siblings beyond `$import:`.
var ErrArtifactEntryShape = errors.New("artifacts entry: shape must be a string scalar or {$import: <local-path>}")

// ArtifactEntry carries one Recipe artifact entry with two content-
// shape-dispatched modes per chapter 07:
//
//   - Mode 1 (inline): a Go template string scalar whose output is the
//     artifact body emitted to the boundary's output directory.
//   - Mode 2 (file import): a YAML map of exactly one key `$import:
//     <local-path>` pointing at a Go-template file.
//
// Library-import mode (oci:// URL) is not supported at this site; the
// envelope schema rejects oci:// prefixes via a `not: pattern` regex
// constraint on the `$import:` value.
//
// The owning Recipe.Spec.Artifacts is an OrderedMap[ArtifactEntry]
// keyed by output filename. File-loaded entries derive their map-key
// name from the input path's basename.
type ArtifactEntry struct {
	Inline string `json:"-" yaml:"-"`
	Import string `json:"-" yaml:"-"`
}

// IsEmpty reports whether neither mode is populated.
func (a ArtifactEntry) IsEmpty() bool {
	return a.Inline == "" && a.Import == ""
}

// UnmarshalYAML dispatches via the shared unmarshalImportable helper.
func (a *ArtifactEntry) UnmarshalYAML(node *yaml.Node) error {
	inline, importTarget, err := unmarshalImportable(node, ErrArtifactEntryShape)
	if err != nil {
		return err
	}
	a.Inline = inline
	a.Import = importTarget
	return nil
}

// MarshalYAML emits the populated mode via marshalImportable.
func (a ArtifactEntry) MarshalYAML() (any, error) {
	return marshalImportable(a.Inline, a.Import)
}
