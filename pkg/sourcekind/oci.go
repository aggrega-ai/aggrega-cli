// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcekind

import (
	"maps"
	"slices"
)

// Layout is how the layer an OCI module kind selects is laid out in a source
// slot.
type Layout int

const (
	// LayoutTarGz is a gzip-compressed tar archive, stored as it was published.
	LayoutTarGz Layout = iota
	// LayoutDocument is a single document stored under a fixed name.
	LayoutDocument
	// LayoutZip is a zip archive whose entries are the module's files.
	LayoutZip
)

// OCIArtifact is the row an OCI module kind carries: which layer of the
// published artifact the kind selects and how that layer is laid out.
type OCIArtifact struct {
	// ContentMediaType is the media type of the one layer the kind selects.
	ContentMediaType string
	// Layout is how the selected layer is laid out in a source slot.
	Layout Layout
	// DocumentName is the file name a LayoutDocument layer is stored under; it
	// is empty for every other layout.
	DocumentName string
	// TakesModuleSubdir reports whether a source of the kind may name a
	// directory inside the package with `module_subdir`.
	TakesModuleSubdir bool
}

// ociArtifacts holds the row of each OCI module kind.
var ociArtifacts = map[string]OCIArtifact{
	BicepModuleOCI: {
		ContentMediaType: "application/vnd.ms.bicep.module.layer.v1+json",
		Layout:           LayoutDocument,
		DocumentName:     "main.json",
	},
	TerraformModuleOCI: {
		ContentMediaType:  "archive/zip",
		Layout:            LayoutZip,
		TakesModuleSubdir: true,
	},
}

// OCIDistributed reports whether sources of kind are pulled from an OCI
// registry: a Helm chart or a module of either module kind.
func OCIDistributed(kind string) bool {
	if kind == HelmOCIChart {
		return true
	}
	_, found := ociArtifacts[kind]
	return found
}

// OCIArtifactOf returns the row of an OCI module kind. It reports false for any
// other kind, a Helm chart included, which carries no row.
func OCIArtifactOf(kind string) (OCIArtifact, bool) {
	row, found := ociArtifacts[kind]
	return row, found
}

// OCIModuleKinds returns the kinds that carry a row, sorted. The slice is a
// copy, so a caller reordering it changes nothing OCIArtifactOf answers.
func OCIModuleKinds() []string {
	return slices.Sorted(maps.Keys(ociArtifacts))
}
