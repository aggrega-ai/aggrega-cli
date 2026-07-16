// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Searchable-annotation keys stamped onto a published catalog or plugin
// manifest so a registry index or catalog browser can filter artifacts by
// their catalog metadata without fetching and parsing every entity body.
//
// The human-readable summary reuses the standard OCI description annotation
// (org.opencontainers.image.description); the aggrega-scoped keys carry the
// catalog facets the standard vocabulary has no slot for. Each key is declared
// once here and read back through the same constant, so the write side and the
// index reader cannot disagree about a facet's key.
const (
	// AnnotationCategory carries an entity's catalog category — the value of
	// its component.aggrega.ai/category label. Absent when the entity declares
	// no category.
	AnnotationCategory = "ai.aggrega.category"
	// AnnotationKind carries the entity kind (Component, Feature, Recipe,
	// FeatureRecipe, Plugin, and the other publishable kinds).
	AnnotationKind = "ai.aggrega.kind"
	// AnnotationPluginType carries a plugin's socket type (provider, linter,
	// frp_step, mutator, policy-validator, cli). Absent on non-plugin entities.
	AnnotationPluginType = "ai.aggrega.plugin-type"
	// AnnotationCapabilities carries a plugin's declared capabilities as a
	// single comma-joined value in author order. Absent on non-plugin entities
	// and on plugins that declare none.
	AnnotationCapabilities = "ai.aggrega.capabilities"
)

// SearchableMetadata is the catalog-facet set a publish stamps onto its OCI
// manifest as searchable annotations. It is the single assembly home for those
// annotations: a write site populates the facets it knows and calls Annotations
// to render them, and an index reader reads them back through the same key
// constants — neither side spells an annotation key as a bare string literal.
//
// Facets are optional. A catalog entity populates Description, Category, and
// Kind; a plugin populates Description, Kind, PluginType, and Capabilities.
// Whatever a given entity does not carry is left zero and omitted from the
// rendered map.
type SearchableMetadata struct {
	Description  string
	Category     string
	Kind         string
	PluginType   string
	Capabilities []string
}

// Annotations renders the metadata to the manifest-annotation map keyed by the
// searchable-annotation constants. Empty-valued facets are omitted, so a
// manifest carries a key only when the facet is present. Capabilities join with
// commas in author order into one deterministic value; an empty capability list
// is omitted. The returned map is a fresh value the caller passes to both the
// digest preview and the push so the two stamp identical annotations.
func (m SearchableMetadata) Annotations() map[string]string {
	a := map[string]string{}
	if m.Description != "" {
		a[ocispec.AnnotationDescription] = m.Description
	}
	if m.Category != "" {
		a[AnnotationCategory] = m.Category
	}
	if m.Kind != "" {
		a[AnnotationKind] = m.Kind
	}
	if m.PluginType != "" {
		a[AnnotationPluginType] = m.PluginType
	}
	if len(m.Capabilities) > 0 {
		a[AnnotationCapabilities] = strings.Join(m.Capabilities, ",")
	}
	return a
}
