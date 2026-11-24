// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package libraryimport

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrMappingImportShape fires when a valuesMapping $import: (or a recursive
// MappingLibrary entry body's field-root $import:) is neither a host-bearing
// reference nor a well-formed bare-name `<Library>#/mappings/<entry>` identity.
func ErrMappingImportShape() error { return errMappingImportShape }

var errMappingImportShape = cerrors.Register("mapping-import-shape", cerrors.Meta{
	ShortCode: "E0045",
	Message:   "valuesMapping $import: is not a bare-name mapping-library reference (<Library>#/mappings/<entry>)",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrMappingLibraryNotFound fires when a bare-name mapping-library import
// names a MappingLibrary absent from the resolved set (the local catalog plus
// any lockfile-pinned libraries bridged in at load).
//
// # Why no rule over the authored reference is declared for it
//
// A values-mapping reference naming a library nothing publishes is refused as the
// Release's dependencies are resolved, under the condition raised for a reference
// the catalog holds no entity for — and that resolution precedes the render on
// every surface that renders at all. So a rule declared over the authored
// reference could not be invoked ahead of the resolution that already refuses it,
// and the declaration would read as enforcement while nothing could reach it.
//
// The arms that raise this condition stay where they are: they are the refusal for
// the values mapping a FeatureRecipe carries, for a reference lifted out of a
// library entry's own body by the recursion, and for a caller assembling a render's
// input without resolving dependencies first.
func ErrMappingLibraryNotFound() error { return errMappingLibraryNotFound }

var errMappingLibraryNotFound = cerrors.Register("mapping-library-not-found", cerrors.Meta{
	ShortCode: "E0046",
	Message:   "mapping-library import references a MappingLibrary not present in the catalog",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrMappingEntryNotFound fires when a bare-name mapping-library import names
// an entry absent from the resolved MappingLibrary's mappings map.
func ErrMappingEntryNotFound() error { return errMappingEntryNotFound }

var errMappingEntryNotFound = cerrors.Register("mapping-entry-not-found", cerrors.Meta{
	ShortCode: "E0044",
	Message:   "mapping-library import references an entry not present in the MappingLibrary",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrArtifactImportShape fires when an artifacts entry `$import:` value is
// neither a host-bearing reference nor a well-formed bare-name
// `<Library>#/artifacts/<entry>` identity — including a field-root `$import:`
// nested inside a bundle file body, which is not supported (a single bundle
// file cannot expand into another bundle; compose at the consumption site).
func ErrArtifactImportShape() error { return errArtifactImportShape }

var errArtifactImportShape = cerrors.Register("artifact-import-shape", cerrors.Meta{
	ShortCode: "E0027",
	Message:   "artifacts $import: is not a bare-name artifact-library reference (<Library>#/artifacts/<entry>)",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrArtifactLibraryNotFound fires when a bare-name artifact-library import
// names an ArtifactLibrary absent from the resolved set (the local catalog
// plus any lockfile-pinned libraries bridged in at load).
func ErrArtifactLibraryNotFound() error { return errArtifactLibraryNotFound }

var errArtifactLibraryNotFound = cerrors.Register("artifact-library-not-found", cerrors.Meta{
	ShortCode: "E0031",
	Message:   "artifact-library import references an ArtifactLibrary not present in the catalog",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrArtifactLibraryEntryNotFound fires when a bare-name artifact-library
// import names a bundle absent from the resolved ArtifactLibrary's artifacts map.
func ErrArtifactLibraryEntryNotFound() error { return errArtifactLibraryEntryNotFound }

var errArtifactLibraryEntryNotFound = cerrors.Register("artifact-library-entry-not-found", cerrors.Meta{
	ShortCode: "E0030",
	Message:   "artifact-library import references a bundle not present in the ArtifactLibrary",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrHostInLibraryImport fires when a library $import: carries a
// registry host — the `oci://…` form. It is shared by all three $import
// fragment surfaces: valuesMapping (`<Library>#/mappings/<entry>`),
// workload (`<Library>#/workloads/<entry>`) and artifacts
// (`<Library>#/artifacts/<entry>`). A library import is addressed by
// bare-name identity, never by physical registry location — the library
// name resolves against the loaded catalog and the version is supplied by
// the lockfile. A host in the reference is rejected so physical location
// cannot leak into authored source. The per-surface wrapping context names
// which surface carried the host.
//
// # Why no rule over the authored reference is declared for it
//
// The reference an entity itself authors never reaches this sentinel. A Recipe
// or FeatureRecipe carrying an `oci://` prefix at any of its three $import
// positions is refused when its document envelope is validated as the catalog
// loads, because no entity schema admits one there. That file is skipped, so the
// catalog holds nothing under the identity it declares, and every surface that
// renders then fails resolving the reference to it — ahead of any render. A rule
// declared over the authored reference could not be invoked ahead of that, and
// the declaration would read as enforcement while nothing could reach it.
//
// What still reaches this sentinel is the reference no entity schema describes:
// one lifted out of a library entry's own body by the recursion the mapping and
// workload resolvers perform. A library entry body is under no such constraint,
// and the reference is one the consuming entity never wrote, so no rule over
// that entity's authored text can see it however completely its own references
// are covered.
//
// The arms that raise this condition therefore stay where they are: they are the
// refusal for a reference the recursion lifts out of a library entry's body, for
// the values mapping and the workload a FeatureRecipe carries, and for a caller
// assembling a render's input without loading it through the catalog first.
func ErrHostInLibraryImport() error { return errHostInLibraryImport }

var errHostInLibraryImport = cerrors.Register("host-in-library-import", cerrors.Meta{
	ShortCode: "E0040",
	Message:   "library $import: carries a registry host; reference the library by bare-name identity",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})
