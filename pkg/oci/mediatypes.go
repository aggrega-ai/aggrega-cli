// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package oci wraps oras.land/oras-go/v2 for catalog-artifact + source-
// artifact OCI transport. Pull and Push are the canonical entry points;
// cache management, mediatype dispatch, and per-entity-kind bundling
// live in adjacent files within the same package.
//
// Authentication delegates entirely to oras-go/v2's standard
// credential mechanisms (Docker / Helm / containers config files plus
// credential-helper delegation). No Aggrega-specific authentication
// surface is exposed.
//
// The four fetch-error sentinels at errors.go are pure declarations
// at this implementation: ErrFetchNotFound, ErrFetchUnauthorized,
// ErrFetchNetworkUnreachable, ErrFetchIntegrity. The category
// integration and call-site wrapping that bridge these sentinels
// into the compiler's diagnostic surface land at a follow-up
// implementation.
package oci

// Canonical OCI mediatypes for Aggrega catalog entities.
//
// An Aggregate is publishable: it is a reusable module, resolvable as
// a Release's top-level reference or as a sub-aggregate of another
// Aggregate. A Release and a Manifest are not publishable — both are
// deployment-side, lived in the workspace's source-control system, not
// pushed to a registry.
const (
	MediaTypeComponent       = "application/vnd.aggrega.component.v1alpha1+yaml"
	MediaTypeFeature         = "application/vnd.aggrega.feature.v1alpha1+yaml"
	MediaTypeRecipe          = "application/vnd.aggrega.recipe.v1alpha1+tar+gz"
	MediaTypeFeatureRecipe   = "application/vnd.aggrega.featurerecipe.v1alpha1+tar+gz"
	MediaTypeSchemaLibrary   = "application/vnd.aggrega.schemalibrary.v1+json"
	MediaTypeMappingLibrary  = "application/vnd.aggrega.mappinglibrary.v1+json"
	MediaTypeWorkloadLibrary = "application/vnd.aggrega.workloadlibrary.v1+json"
	MediaTypeAggregate       = "application/vnd.aggrega.aggregate.v1alpha1+yaml"
	// MediaTypePlugin is the mediatype of the Plugin entity's YAML —
	// the authoring/metadata surface that pins the .wasm artifact by digest.
	MediaTypePlugin = "application/vnd.aggrega.wasmplugin.v1alpha1+yaml"
	// MediaTypeWasm is the mediatype of the raw .wasm artifact a Plugin
	// entity references by ref+digest. It is published as its own OCI artifact,
	// separate from the entity, and is passed directly to Push rather than
	// resolved through MediaTypeForKind (the .wasm is an artifact, not a
	// catalog entity kind).
	MediaTypeWasm = "application/vnd.aggrega.wasm.v1alpha1+wasm"
	// MediaTypeDocs is the mediatype of a documentation bundle — a gzipped
	// tar of rendered documentation, published as a standalone data artifact
	// rather than a catalog entity. Like MediaTypeWasm it is a non-kind
	// artifact passed directly to Push, never resolved through
	// MediaTypeForKind: no entity kind lowercases to "docs", so the bundle
	// carries no entity envelope and rides the kind-agnostic transport.
	MediaTypeDocs = "application/vnd.aggrega.docs.v1alpha1+tar+gz"
)

// kindMediaTypes maps the catalog entity kind string (matching the
// values pkg/catalog/catalog.go switches on) to its canonical OCI
// mediatype.
var kindMediaTypes = map[string]string{
	"Component":       MediaTypeComponent,
	"Feature":         MediaTypeFeature,
	"Recipe":          MediaTypeRecipe,
	"FeatureRecipe":   MediaTypeFeatureRecipe,
	"SchemaLibrary":   MediaTypeSchemaLibrary,
	"MappingLibrary":  MediaTypeMappingLibrary,
	"WorkloadLibrary": MediaTypeWorkloadLibrary,
	"Aggregate":       MediaTypeAggregate,
	"Plugin":          MediaTypePlugin,
}

// MediaTypeForKind returns the canonical OCI mediatype for the given
// entity kind. The kind parameter matches the convention used in
// pkg/catalog/catalog.go: plain string values "Component", "Feature",
// "Recipe", "FeatureRecipe", "SchemaLibrary",
// "MappingLibrary", "WorkloadLibrary", "Aggregate". Returns ErrUnknownEntityKind for
// unrecognised values (a programming error, not a user-input error).
func MediaTypeForKind(kind string) (string, error) {
	if mt, ok := kindMediaTypes[kind]; ok {
		return mt, nil
	}
	return "", ErrUnknownEntityKind
}
