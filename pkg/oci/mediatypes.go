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

// Canonical OCI mediatypes for Aggrega catalog entities. Seven
// declarations covering the seven first-party kinds.
//
// Aggregates are intentionally NOT registered as an OCI-published
// kind: Aggregates are the developer's authoring surface, lived in
// the workspace's source-control system, not as catalog content
// pushed to a registry.
const (
	MediaTypeComponent      = "application/vnd.aggrega.component.v1alpha1+yaml"
	MediaTypeFeature        = "application/vnd.aggrega.feature.v1alpha1+yaml"
	MediaTypeRecipe         = "application/vnd.aggrega.recipe.v1alpha1+tar+gz"
	MediaTypeFeatureRecipe  = "application/vnd.aggrega.featurerecipe.v1alpha1+tar+gz"
	MediaTypeProvider       = "application/vnd.aggrega.provider.v1alpha1+yaml"
	MediaTypeSchemaLibrary  = "application/vnd.aggrega.schemalibrary.v1+json"
	MediaTypeMappingLibrary = "application/vnd.aggrega.mappinglibrary.v1+json"
)

// kindMediaTypes maps the catalog entity kind string (matching the
// values pkg/catalog/catalog.go switches on) to its canonical OCI
// mediatype. Aggregate is intentionally absent — see the package
// godoc for the rationale.
var kindMediaTypes = map[string]string{
	"Component":      MediaTypeComponent,
	"Feature":        MediaTypeFeature,
	"Recipe":         MediaTypeRecipe,
	"FeatureRecipe":  MediaTypeFeatureRecipe,
	"Provider":       MediaTypeProvider,
	"SchemaLibrary":  MediaTypeSchemaLibrary,
	"MappingLibrary": MediaTypeMappingLibrary,
}

// MediaTypeForKind returns the canonical OCI mediatype for the given
// entity kind. The kind parameter matches the convention used in
// pkg/catalog/catalog.go: plain string values "Component", "Feature",
// "Recipe", "FeatureRecipe", "Provider", "SchemaLibrary",
// "MappingLibrary". Returns ErrUnknownEntityKind for unrecognised
// values (a programming error, not a user-input error).
func MediaTypeForKind(kind string) (string, error) {
	if mt, ok := kindMediaTypes[kind]; ok {
		return mt, nil
	}
	return "", ErrUnknownEntityKind
}
