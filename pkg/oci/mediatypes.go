// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package oci wraps oras.land/oras-go/v2 for catalog-artifact + source-
// artifact OCI transport. Pull and Push are the canonical entry points;
// cache management, the mediatype table, and the bundling that packs a
// publication from the files a caller names live in adjacent files within
// the same package.
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

import (
	"sort"
	"strings"
)

// The two shapes an entity publication takes, as the structured-syntax suffix of
// its media type. An entity whose publication carries nothing beside its own
// document publishes as that document, byte for byte; one that carries files
// beside it publishes as a gzipped tar holding the document and those files.
// Which of the two an entity takes follows from what the entity declares, never
// from its kind, so every kind has both.
const (
	documentSuffix = "+yaml"
	archiveSuffix  = "+tar+gz"
)

// mediaTypeVendor opens the media types an entity, a Plugin's executable and a
// documentation bundle are published under, and mediaTypeVersion is the version
// segment each of them carries: every media type below is built from the two, so
// they cannot come to carry different versions. The artifacts attached beside a
// publication — its publication date (MediaTypePublishedAt) and a deprecation
// marker — version their own media types on an axis of their own.
const (
	mediaTypeVendor  = "application/vnd.aggrega."
	mediaTypeVersion = "v1beta1"
)

// retiredMediaTypeVersions are the version segments this transport once
// published under and reads no longer. An artifact whose manifest carries a media
// type spelled with one of them is refused by name when it is fetched (see
// retiredMediaType), telling the reader to publish it again. A version leaves the
// list never, so a stale artifact is recognised for as long as the compiler can
// recognise it.
var retiredMediaTypeVersions = []string{"v1alpha1"}

// The vendor type and version each publishable kind's two media types share.
// Each media type below is one of these followed by one of the two suffixes
// above, so a kind's two shapes cannot come to name different kinds or
// versions.
const (
	stemComponent       = mediaTypeVendor + "component." + mediaTypeVersion
	stemFeature         = mediaTypeVendor + "feature." + mediaTypeVersion
	stemRecipe          = mediaTypeVendor + "recipe." + mediaTypeVersion
	stemFeatureRecipe   = mediaTypeVendor + "featurerecipe." + mediaTypeVersion
	stemSchemaLibrary   = mediaTypeVendor + "schemalibrary." + mediaTypeVersion
	stemMappingLibrary  = mediaTypeVendor + "mappinglibrary." + mediaTypeVersion
	stemWorkloadLibrary = mediaTypeVendor + "workloadlibrary." + mediaTypeVersion
	stemArtifactLibrary = mediaTypeVendor + "artifactlibrary." + mediaTypeVersion
	stemAggregate       = mediaTypeVendor + "aggregate." + mediaTypeVersion
	stemPlugin          = mediaTypeVendor + "wasmplugin." + mediaTypeVersion
)

// Canonical OCI mediatypes for Aggrega catalog entities, each naming one kind in
// the one shape its suffix names. [MediaTypeFor] is what derives the media type a
// publication carries, from a kind and a shape together; these constants are the
// values it yields, spelled for a reader who needs one of them by name.
//
// An Aggregate is publishable: it is a reusable module, resolvable as
// a Release's top-level reference or as a sub-aggregate of another
// Aggregate. A Release and a Manifest are not publishable — both are
// deployment-side, lived in the workspace's source-control system, not
// pushed to a registry.
const (
	MediaTypeComponent       = stemComponent + documentSuffix
	MediaTypeFeature         = stemFeature + documentSuffix
	MediaTypeRecipe          = stemRecipe + archiveSuffix
	MediaTypeFeatureRecipe   = stemFeatureRecipe + archiveSuffix
	MediaTypeSchemaLibrary   = stemSchemaLibrary + documentSuffix
	MediaTypeMappingLibrary  = stemMappingLibrary + documentSuffix
	MediaTypeWorkloadLibrary = stemWorkloadLibrary + documentSuffix
	MediaTypeArtifactLibrary = stemArtifactLibrary + documentSuffix
	MediaTypeAggregate       = stemAggregate + documentSuffix
	// MediaTypePlugin is the mediatype of the Plugin entity's YAML, which a
	// Plugin publication carries as its manifest's config blob.
	MediaTypePlugin = stemPlugin + documentSuffix
)

const (
	// MediaTypeWasm is the mediatype of a Plugin's .wasm executable, which a
	// Plugin publication carries as the layer of the same manifest whose config
	// blob is the entity (see PushPluginManifest). It is not resolved through
	// MediaTypeFor: the .wasm is part of a Plugin publication, not a catalog
	// entity kind of its own.
	MediaTypeWasm = mediaTypeVendor + "wasm." + mediaTypeVersion + "+wasm"
	// MediaTypeDocs is the mediatype of a documentation bundle — a gzipped
	// tar of rendered documentation, published as a standalone data artifact
	// rather than a catalog entity. It is a non-kind artifact, never resolved
	// through MediaTypeFor: no entity kind lowercases to "docs", so the
	// bundle carries no entity envelope and rides the kind-agnostic transport.
	MediaTypeDocs = mediaTypeVendor + "docs." + mediaTypeVersion + archiveSuffix
	// MediaTypeSignatureBundle is the cosign-compatible Sigstore bundle media
	// type a signature is attached under: the artifactType of the OCI referrer
	// that carries the bundle, and the media type of that referrer's one layer.
	// It is declared with the transport so the transport can name what it
	// carries; the signing package re-exports it as its bundle media type.
	MediaTypeSignatureBundle = "application/vnd.dev.sigstore.bundle.v0.3+json"
)

// kindMediaTypes maps the catalog entity kind string (matching the values
// pkg/catalog/catalog.go switches on) to the stem both of that kind's media types
// share. It is the one home of which kinds are publishable and of what each
// publishes under: [MediaTypeFor] appends a shape's suffix to a stem read here,
// and [KindOfMediaType] reads a media type back to its kind through the same
// table, so neither direction keeps a list of its own.
var kindMediaTypes = map[string]string{
	"Component":       stemComponent,
	"Feature":         stemFeature,
	"Recipe":          stemRecipe,
	"FeatureRecipe":   stemFeatureRecipe,
	"SchemaLibrary":   stemSchemaLibrary,
	"MappingLibrary":  stemMappingLibrary,
	"WorkloadLibrary": stemWorkloadLibrary,
	"ArtifactLibrary": stemArtifactLibrary,
	"Aggregate":       stemAggregate,
	"Plugin":          stemPlugin,
}

// MediaTypeFor returns the media type an entity of kind publishes under, given
// whether its publication carries files beside the entity's own document —
// archive true — or is the document alone.
//
// A kind alone does not decide it, and that is the point of taking both: which
// shape a publication takes follows from what the entity declares, so one kind
// publishes under either of its two media types depending on its content, and a
// caller that asked by kind alone would be answering a question about one
// entity with a fact about every entity of that kind.
//
// The kind parameter is the plain string value the catalog loader switches on,
// such as "Component" or "SchemaLibrary". Returns ErrUnknownEntityKind for a
// kind this transport does not publish — a Release and a Manifest among them.
func MediaTypeFor(kind string, archive bool) (string, error) {
	stem, ok := kindMediaTypes[kind]
	if !ok {
		return "", ErrUnknownEntityKind()
	}
	if archive {
		return stem + archiveSuffix, nil
	}
	return stem + documentSuffix, nil
}

// IsPublishableKind reports whether kind is one this transport publishes. It
// is the membership question on its own, for a caller that must refuse a kind
// no registry carries — a Release or a Manifest — before any media type is in
// question.
func IsPublishableKind(kind string) bool {
	_, ok := kindMediaTypes[kind]
	return ok
}

// KindOfMediaType returns the entity kind a media type publishes, and whether
// the media type is that kind's archive shape; ok is false for a media type that
// names no publishable kind in either shape — a Plugin's executable, a
// signature, a documentation bundle, or anything a registry holds beside the
// catalog.
func KindOfMediaType(mediaType string) (kind string, archive bool, ok bool) {
	for k, stem := range kindMediaTypes {
		switch mediaType {
		case stem + documentSuffix:
			return k, false, true
		case stem + archiveSuffix:
			return k, true, true
		}
	}
	return "", false, false
}

// MediaTypeKinds returns every entity kind MediaTypeFor resolves, in lexical
// order. The returned slice is freshly allocated, so a caller may not mutate the
// mediatype table through it.
//
// This is the enumeration counterpart to the lookups above: it exists so a
// caller that must reason about the whole kind set — deciding which kinds to
// walk, or checking that its own handling accounts for all of them — reads that
// set from here rather than restating it. A restated copy agrees on the day it
// is written and silently disagrees the day a kind is added.
func MediaTypeKinds() []string {
	out := make([]string, 0, len(kindMediaTypes))
	for kind := range kindMediaTypes {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

// retiredMediaType reports whether mediaType is an Aggrega media type spelled with
// a version segment this transport published under once and reads no longer, and
// if so the media type it publishes the same artifact under now. It matches by
// the media type's shape — the vendor prefix, a name, then a retired version
// segment ending the type or followed by its structured-syntax suffix — rather
// than against a list of the types published, so no list of them has to be kept
// in step with the declarations above. A third party's media type — a chart, a
// signature — never carries the vendor prefix, and never matches.
func retiredMediaType(mediaType string) (current string, ok bool) {
	rest, ours := strings.CutPrefix(mediaType, mediaTypeVendor)
	if !ours {
		return "", false
	}
	for _, retired := range retiredMediaTypeVersions {
		name, after, found := strings.Cut(rest, "."+retired)
		if found && (after == "" || strings.HasPrefix(after, "+")) {
			return mediaTypeVendor + name + "." + mediaTypeVersion + after, true
		}
	}
	return "", false
}
