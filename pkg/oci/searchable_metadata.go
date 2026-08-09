// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"fmt"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
	// pipeline-step, governance, cli, parser, importer). Absent on non-plugin
	// entities.
	AnnotationPluginType = "ai.aggrega.plugin-type"
	// AnnotationCapabilities carries a plugin's declared capabilities as a
	// single comma-joined value in author order. Absent on non-plugin entities
	// and on plugins that declare none.
	AnnotationCapabilities = "ai.aggrega.capabilities"
	// AnnotationLabels carries an entity's whole metadata.labels map as a JSON
	// object whose keys are in author order, so a label selector can filter on
	// every label an author wrote rather than on the category alone. Absent when
	// the entity declares no labels.
	//
	// Three properties of the encoding are load-bearing, and each rules out an
	// encoding that would otherwise look simpler:
	//
	// One annotation carrying the whole map, rather than one annotation per
	// label. Author label keys are arbitrary, so promoting them to annotation
	// keys would let an entity's own labels collide with a reserved annotation
	// namespace or with the facet keys above. Nesting the map inside a single
	// value removes that possibility rather than managing it.
	//
	// JSON rather than the comma-joined shape the capabilities facet uses. A
	// label value may itself contain a comma or an equals sign, which a
	// delimiter-joined encoding cannot round-trip. Capabilities are a flat list
	// of constrained tokens and a label map is neither.
	//
	// Author order, held in an OrderedMap end to end. Identical input must
	// produce byte-identical published bytes, and Go's map iteration order is
	// randomised, so the map is never allowed to pass through a plain Go map on
	// its way to the manifest or back off it.
	AnnotationLabels = "ai.aggrega.labels"
)

// SearchableMetadata is the catalog-facet set a publish stamps onto its OCI
// manifest as searchable annotations. It is the single assembly home for those
// annotations: a write site populates the facets it knows and calls Annotations
// to render them, and an index reader reads them back through the same key
// constants — neither side spells an annotation key as a bare string literal.
//
// Facets are optional. A catalog entity populates Description, Category, Kind,
// and Labels; a plugin populates Description, Kind, PluginType, and
// Capabilities. Whatever a given entity does not carry is left zero and omitted
// from the rendered map.
type SearchableMetadata struct {
	Description string
	Category    string
	Kind        string
	PluginType  string
	// Labels is the entity's whole metadata.labels map, held in author order so
	// the rendered annotation is byte-identical for identical input. Nil on an
	// entity that declares no labels.
	Labels       *types.OrderedMap[string]
	Capabilities []string
}

// Annotations renders the metadata to the manifest-annotation map keyed by the
// searchable-annotation constants. Empty-valued facets are omitted, so a
// manifest carries a key only when the facet is present. Capabilities join with
// commas in author order into one deterministic value; an empty capability list
// is omitted. Labels render as a JSON object in author order; an entity with no
// labels renders no key. The returned map is a fresh value the caller passes to
// both the digest preview and the push so the two stamp identical annotations.
//
// The error reports a label map that cannot be encoded. It is returned rather
// than swallowed because a dropped facet is invisible at the point of loss and
// surfaces later as a filter that silently matches nothing.
func (m SearchableMetadata) Annotations() (map[string]string, error) {
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
	if m.Labels != nil && m.Labels.Len() > 0 {
		// Encoded through the ordered map's own marshaller, which is the single home
		// of the author-order JSON encoding; spelling the object out here would be a
		// second copy that agrees today and diverges later.
		encoded, err := m.Labels.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("searchable annotations: encoding labels: %w", err)
		}
		a[AnnotationLabels] = string(encoded)
	}
	return a, nil
}
