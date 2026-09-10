// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"os"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// ErrArtifactEntryShape fires when a Recipe `artifacts.<name>:` value
// carries a shape outside the two documented modes: not a string
// scalar; not a mapping with a single `$import:` key; or a mapping
// with extra siblings beyond `$import:`.
func ErrArtifactEntryShape() error { return errArtifactEntryShape }

var errArtifactEntryShape = cerrors.Register("artifact-entry-shape", cerrors.Meta{
	ShortCode: "E0188",
	Message:   "artifacts entry: shape must be a string scalar or {$import: <local-path>}",
})

// ArtifactEntry carries one Recipe artifact entry with three content-
// shape-dispatched modes per the file-import vocabulary (chapter 07):
//
//   - Mode 1 (inline): a Go template string scalar whose output is the
//     artifact body emitted to the boundary's output directory.
//   - Mode 2 (file import): a YAML map of exactly one key `$import:
//     <local-path>` pointing at a Go-template file. Import carries the
//     path; IsLibraryImport returns false. A relative path is anchored to
//     the declaring entity's own directory when the catalog loads the
//     entity, so what Import carries downstream of a catalog load is
//     absolute, while AuthoredImport still carries the relative path as
//     written.
//   - Mode 3 (library import): a YAML map of exactly one key
//     `$import: <Library>#/artifacts/<entry>`, a host-less bare-name
//     reference into an ArtifactsLibrary bundle (the version is supplied
//     by the per-Release lockfile; no registry host appears in the
//     reference). Import carries the identity reference; IsLibraryImport
//     returns true. The referenced entry is a bundle of files that expands
//     into one output per file at the consumption site.
//
// A host-bearing `oci://…` reference is not a library import; the envelope
// schema rejects it via a `not: pattern` constraint on the `$import:` value
// and the consumption site rejects it via HasOCIPrefix.
//
// AuthoredImport carries the `$import:` value exactly as it appears in the
// document the entry was read from, and no path resolution rewrites it.
// Import is the value to open; AuthoredImport is the value to show, so a
// reader is shown the reference they wrote rather than one whose leading
// directories describe wherever the catalog happens to sit. Reading it
// needs no test of which mode is in play: a file import yields the authored
// path, a library import yields the authored identity reference, and an
// inline entry yields "" because there is no `$import:` to carry. For an
// entry decoded from a document that empty value has exactly one meaning,
// because AuthoredImport is empty precisely when Import is, and an entry
// carrying no import has no reference to show.
//
// The owning Recipe.Spec.Artifacts is an OrderedMap[ArtifactEntry]
// keyed by output filename: the key names the output for an inline or a
// file-loaded entry, and prefixes it for each file a library bundle
// expands into.
type ArtifactEntry struct {
	Inline         string `json:"-" yaml:"-"`
	Import         string `json:"-" yaml:"-"`
	AuthoredImport string `json:"-" yaml:"-"`
}

// IsEmpty reports whether neither mode is populated.
func (a ArtifactEntry) IsEmpty() bool {
	return a.Inline == "" && a.Import == ""
}

// IsLibraryImport reports whether a.Import is a host-less bare-name
// ArtifactsLibrary reference (mode 3, `<Library>#/artifacts/<entry>`) as
// opposed to a local file path (mode 2). The discriminator is the
// `#/artifacts/` fragment surface with no registry host, mirroring
// WorkloadTemplate's `#/workloads/` gate. The host-bearing `oci://…` form is
// not a library import; HasOCIPrefix reports it for rejection.
func (a ArtifactEntry) IsLibraryImport() bool {
	_, _, ok := IdentityRefParts(a.Import, "/artifacts/")
	return ok
}

// HasOCIPrefix reports whether a.Import carries a registry host (the
// `oci://…` form). A library import is addressed by bare-name identity,
// never by physical registry location; the consumption site rejects a
// host-bearing reference rather than treating it as a library import.
func (a ArtifactEntry) HasOCIPrefix() bool {
	return strings.HasPrefix(a.Import, "oci://")
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
	inline, importTarget, err := unmarshalImportable(node, ErrArtifactEntryShape())
	if err != nil {
		return err
	}
	a.Inline = inline
	a.Import = importTarget
	a.AuthoredImport = importTarget
	return nil
}

// MarshalYAML emits the populated mode via marshalImportable. The emitted
// `$import:` value is Import, so re-marshalling an entry whose path a catalog
// load resolved emits the resolved path rather than the authored one.
func (a ArtifactEntry) MarshalYAML() (any, error) {
	return marshalImportable(a.Inline, a.Import)
}
