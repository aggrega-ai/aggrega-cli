// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// ErrValuesMappingShape fires when a Recipe / FeatureRecipe valuesMapping
// node carries a shape outside the three documented modes: not a string
// scalar; not a mapping with a single $import key; or a mapping carrying
// extra siblings beyond $import.
func ErrValuesMappingShape() error { return errValuesMappingShape }

var errValuesMappingShape = cerrors.Register("types/values-mapping-shape", cerrors.Meta{
	Message: "valuesmapping: shape must be a string scalar or {$import: <path-or-library-identity>}",
})

// ValuesMapping carries one of three content-shape-dispatched forms per
// the schema-language migration's mapping authoring surface:
//
//   - Mode 1 (inline): a Go template string scalar. Inline carries the
//     authored content verbatim; Import stays empty.
//   - Mode 2 (file import): a YAML map of exactly one key
//     `$import: <local-path>`. Import carries the path; Inline stays
//     empty. IsLibraryImport returns false. A relative path is anchored
//     to the declaring entity's own directory when the catalog loads the
//     entity, so what Import carries downstream of a catalog load is
//     absolute.
//   - Mode 3 (library import): a YAML map of exactly one key
//     `$import: <Library>#/mappings/<entry>`, a host-less bare-name
//     reference into a MappingLibrary entry (the version is supplied by
//     the per-Release lockfile; no registry host appears in the
//     reference). Import carries the identity reference;
//     IsLibraryImport returns true.
//
// The struct uses two string fields with a runtime discriminator rather
// than the yaml.Node pattern used by ComponentSpec.Schema because the
// field's shape is bounded — three discrete modes — while Schema
// content is arbitrary JSON Schema 2020-12. Bounded shape benefits
// from type-level capture; unbounded shape benefits from yaml.Node
// pass-through. Future readers comparing the two: the asymmetry is
// principled, not an inconsistency to harmonize.
type ValuesMapping struct {
	// Inline carries the authored Go template content when the YAML
	// value was a string scalar (mode 1). Empty for modes 2 and 3.
	Inline string `json:"-" yaml:"-"`

	// Import carries the $import: target when the YAML value was
	// {$import: ...} (modes 2 and 3). Local path (mode 2) or
	// bare-name library identity (mode 3); use IsLibraryImport to
	// discriminate. Named Import (not URL or Ref) to mirror the YAML
	// key authors write, distinct from a library entry's
	// canonical-resolved reference.
	//
	// A mode-2 path is the authored text as unmarshalled and the
	// absolute resolved path once a catalog load has anchored it to the
	// declaring entity's directory; a mode-3 identity is never rewritten,
	// because the consumption site reads its exact text.
	Import string `json:"-" yaml:"-"`
}

// IsLibraryImport reports whether v.Import is a host-less bare-name
// MappingLibrary reference (mode 3, `<Library>#/mappings/<entry>`) as
// opposed to a local file path (mode 2). The discriminator is the
// `#/mappings/` fragment surface with no registry host — the consumption
// mirror of the closure walker's identity-import gate. The host-bearing
// `oci://…` form is not a library import; HasOCIPrefix reports it for
// rejection.
func (v ValuesMapping) IsLibraryImport() bool {
	_, _, ok := IdentityRefParts(v.Import, "/mappings/")
	return ok
}

// HasOCIPrefix reports whether v.Import carries a registry host (the
// `oci://…` form). A library import is addressed by bare-name identity,
// never by physical registry location; the consumption site rejects a
// host-bearing reference rather than treating it as a library import.
func (v ValuesMapping) HasOCIPrefix() bool {
	return strings.HasPrefix(v.Import, "oci://")
}

// IsEmpty reports whether neither mode is populated. The validator
// uses this to enforce the "at-least-one-input-kind" gate on
// non-eval Recipes.
func (v ValuesMapping) IsEmpty() bool {
	return v.Inline == "" && v.Import == ""
}

// UnmarshalYAML dispatches by content shape via the shared
// unmarshalImportable helper. A nil node is a no-op so callers using
// *ValuesMapping with omitempty don't trip the sentinel.
func (v *ValuesMapping) UnmarshalYAML(node *yaml.Node) error {
	inline, importTarget, err := unmarshalImportable(node, ErrValuesMappingShape())
	if err != nil {
		return err
	}
	v.Inline = inline
	v.Import = importTarget
	return nil
}

// MarshalYAML emits the populated mode via the shared marshalImportable
// helper. Inline → string scalar (literal style for multi-line). Import
// → {$import: <value>}. Empty struct → nil (omitempty on the owning
// field elides the key entirely).
func (v ValuesMapping) MarshalYAML() (any, error) {
	return marshalImportable(v.Inline, v.Import)
}
