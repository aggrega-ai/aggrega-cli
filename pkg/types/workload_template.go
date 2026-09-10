// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// ErrWorkloadTemplateShape fires when a Recipe `workload:` content
// carries a shape outside the documented modes: not a string scalar;
// not a mapping with a single `$import:` key; or a mapping with extra
// siblings beyond `$import:`.
func ErrWorkloadTemplateShape() error { return errWorkloadTemplateShape }

var errWorkloadTemplateShape = cerrors.Register("workload-template-shape", cerrors.Meta{
	ShortCode: "E0200",
	Message:   "workload: shape must be a string scalar or {$import: <path-or-library-identity>}",
})

// WorkloadTemplate carries the Recipe's authored workload-YAML producer
// with three content-shape-dispatched modes per the file-import
// vocabulary (chapter 07):
//
//   - Mode 1 (inline): a Go template string scalar whose output parses
//     as 'aggrega.ai/v1alpha1 Workload'. Inline carries the content;
//     Import stays empty.
//   - Mode 2 (file import): a YAML map of exactly one key `$import:
//     <local-path>` pointing at a Go-template file. Import carries the
//     path; IsLibraryImport returns false. A relative path is anchored to
//     the declaring entity's own directory when the catalog loads the
//     entity, so what Import carries downstream of a catalog load is
//     absolute.
//   - Mode 3 (library import): a YAML map of exactly one key
//     `$import: <Library>#/workloads/<entry>`, a host-less bare-name
//     reference into a WorkloadLibrary entry (the version is supplied by
//     the per-Release lockfile; no registry host appears in the
//     reference). Import carries the identity reference; IsLibraryImport
//     returns true.
//
// A host-bearing `oci://…` reference is not a library import; the
// envelope schema rejects it via a `not: pattern` constraint on the
// `$import:` value and the consumption site rejects it via HasOCIPrefix.
//
// Type name is deliberately WorkloadTemplate (not Workload) because
// pkg/types/workload.go's Workload type carries the rendered Workload
// YAML structure (apiVersion / kind / spec / containers / services).
// Different layers, same domain word; the suffix prevents the
// confusion.
type WorkloadTemplate struct {
	Inline string `json:"-" yaml:"-"`
	Import string `json:"-" yaml:"-"`
}

// IsEmpty reports whether neither mode is populated.
func (w WorkloadTemplate) IsEmpty() bool {
	return w.Inline == "" && w.Import == ""
}

// IsLibraryImport reports whether w.Import is a host-less bare-name
// WorkloadLibrary reference (mode 3, `<Library>#/workloads/<entry>`) as
// opposed to a local file path (mode 2). The discriminator is the
// `#/workloads/` fragment surface with no registry host, mirroring
// ValuesMapping's `#/mappings/` gate. The host-bearing `oci://…` form is
// not a library import; HasOCIPrefix reports it for rejection.
func (w WorkloadTemplate) IsLibraryImport() bool {
	_, _, ok := IdentityRefParts(w.Import, "/workloads/")
	return ok
}

// HasOCIPrefix reports whether w.Import carries a registry host (the
// `oci://…` form). A library import is addressed by bare-name identity,
// never by physical registry location; the consumption site rejects a
// host-bearing reference rather than treating it as a library import.
func (w WorkloadTemplate) HasOCIPrefix() bool {
	return strings.HasPrefix(w.Import, "oci://")
}

// UnmarshalYAML dispatches via the shared unmarshalImportable helper.
func (w *WorkloadTemplate) UnmarshalYAML(node *yaml.Node) error {
	inline, importTarget, err := unmarshalImportable(node, ErrWorkloadTemplateShape())
	if err != nil {
		return err
	}
	w.Inline = inline
	w.Import = importTarget
	return nil
}

// MarshalYAML emits the populated mode via marshalImportable.
func (w WorkloadTemplate) MarshalYAML() (any, error) {
	return marshalImportable(w.Inline, w.Import)
}
