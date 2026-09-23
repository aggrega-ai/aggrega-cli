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
// The four fetch-error sentinels at errors.go — ErrFetchNotFound,
// ErrFetchUnauthorized, ErrFetchNetworkUnreachable and
// ErrFetchIntegrity — are wrapped at their call sites with the URL and
// a remediation hint, and a consumer matches one through errors.Is
// rather than by string. RemediationFor maps a matched sentinel to its
// operator-actionable hint, and the compiler's own diagnostic surface
// classifies a fetch failure by the same errors.Is match.
package oci

import "sort"

// Canonical OCI mediatypes for Aggrega catalog entities.
//
// An Aggregate is publishable: it is a reusable module, resolvable as
// a Release's top-level reference or as a sub-aggregate of another
// Aggregate. A Release and a Manifest are not publishable — both are
// deployment-side, lived in the workspace's source-control system, not
// pushed to a registry.
const (
	MediaTypeComponent        = "application/vnd.aggrega.component.v1alpha1+yaml"
	MediaTypeFeature          = "application/vnd.aggrega.feature.v1alpha1+yaml"
	MediaTypeRecipe           = "application/vnd.aggrega.recipe.v1alpha1+tar+gz"
	MediaTypeFeatureRecipe    = "application/vnd.aggrega.featurerecipe.v1alpha1+tar+gz"
	MediaTypeSchemaLibrary    = "application/vnd.aggrega.schemalibrary.v1alpha1+yaml"
	MediaTypeMappingLibrary   = "application/vnd.aggrega.mappinglibrary.v1alpha1+yaml"
	MediaTypeWorkloadLibrary  = "application/vnd.aggrega.workloadlibrary.v1alpha1+yaml"
	MediaTypeArtifactsLibrary = "application/vnd.aggrega.artifactslibrary.v1alpha1+yaml"
	MediaTypeAggregate        = "application/vnd.aggrega.aggregate.v1alpha1+yaml"
	// MediaTypePlugin is the mediatype of the Plugin entity's YAML, which a
	// Plugin publication carries as its manifest's config blob.
	MediaTypePlugin = "application/vnd.aggrega.wasmplugin.v1alpha1+yaml"
	// MediaTypeWasm is the mediatype of a Plugin's .wasm executable, which a
	// Plugin publication carries as the layer of the same manifest whose config
	// blob is the entity (see PushPluginManifest). It is not resolved through
	// MediaTypeForKind: the .wasm is part of a Plugin publication, not a catalog
	// entity kind of its own.
	MediaTypeWasm = "application/vnd.aggrega.wasm.v1alpha1+wasm"
	// MediaTypeDocs is the mediatype of a documentation bundle — a gzipped
	// tar of rendered documentation, published as a standalone data artifact
	// rather than a catalog entity. It is a non-kind artifact, never resolved
	// through MediaTypeForKind: no entity kind lowercases to "docs", so the
	// bundle carries no entity envelope and rides the kind-agnostic transport.
	MediaTypeDocs = "application/vnd.aggrega.docs.v1alpha1+tar+gz"
	// MediaTypeSignatureBundle is the cosign-compatible Sigstore bundle media
	// type a signature is attached under: the artifactType of the OCI referrer
	// that carries the bundle, and the media type of that referrer's one layer.
	// It is declared with the transport so the transport can name what it
	// carries; the signing package re-exports it as its bundle media type.
	MediaTypeSignatureBundle = "application/vnd.dev.sigstore.bundle.v0.3+json"
)

// kindMediaTypes maps the catalog entity kind string (matching the
// values pkg/catalog/catalog.go switches on) to its canonical OCI
// mediatype.
var kindMediaTypes = map[string]string{
	"Component":        MediaTypeComponent,
	"Feature":          MediaTypeFeature,
	"Recipe":           MediaTypeRecipe,
	"FeatureRecipe":    MediaTypeFeatureRecipe,
	"SchemaLibrary":    MediaTypeSchemaLibrary,
	"MappingLibrary":   MediaTypeMappingLibrary,
	"WorkloadLibrary":  MediaTypeWorkloadLibrary,
	"ArtifactsLibrary": MediaTypeArtifactsLibrary,
	"Aggregate":        MediaTypeAggregate,
	"Plugin":           MediaTypePlugin,
}

// MediaTypeForKind returns the canonical OCI mediatype for the given
// entity kind. The kind parameter is the plain string value the catalog
// loader switches on, such as "Component" or "SchemaLibrary"; the resolvable
// set is the kindMediaTypes table above, which MediaTypeKinds enumerates
// rather than restating here. Returns ErrUnknownEntityKind for unrecognised
// values (a programming error, not a user-input error).
func MediaTypeForKind(kind string) (string, error) {
	if mt, ok := kindMediaTypes[kind]; ok {
		return mt, nil
	}
	return "", ErrUnknownEntityKind()
}

// MediaTypeKinds returns every entity kind MediaTypeForKind resolves, in
// lexical order. The returned slice is freshly allocated, so a caller may not
// mutate the mediatype table through it.
//
// This is the enumeration counterpart to MediaTypeForKind's lookup: it exists so
// a caller that must reason about the whole kind set — deciding which kinds to
// walk, or checking that its own handling accounts for all of them — reads that
// set from here rather than restating it. A restated copy agrees on the day it
// is written and silently disagrees the day a kind is added.
//
// The set is authoritative for mediatype resolution and nothing more. A kind it
// contains is not necessarily one Bundle can bundle, so a caller that both
// enumerates and bundles must handle, or explicitly exclude, each kind it takes
// from here.
func MediaTypeKinds() []string {
	out := make([]string, 0, len(kindMediaTypes))
	for kind := range kindMediaTypes {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}
