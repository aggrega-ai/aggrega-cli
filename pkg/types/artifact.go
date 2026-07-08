// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"errors"
	"os"

	"go.yaml.in/yaml/v3"
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

// Text resolves the entry to its Go-template source string: an empty
// entry yields "", an inline entry yields its authored content, and a
// file-import entry yields the bytes read from Import. The returned
// error is the raw file-read error so callers can apply their own error
// taxonomy; Import is the resolved absolute path (the catalog rewrites
// relative $import paths to absolute at load). This is the single reader
// of an artifact entry's content, shared by every surface that needs the
// entry's text — the render-time projection and the resolution-time pin
// digest — so the two never diverge on how an entry is read.
func (a ArtifactEntry) Text() (string, error) {
	if a.IsEmpty() {
		return "", nil
	}
	if a.Inline != "" {
		return a.Inline, nil
	}
	data, err := os.ReadFile(a.Import)
	if err != nil {
		return "", err
	}
	return string(data), nil
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
