// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import "testing"

// TestLookupNameFromURI_PreservesPathSegments pins the URI-to-name
// helper that ClosureLookup's Tier-1 dispatch uses to find Component
// / Feature / Provider / Aggregate entries in the catalog. The
// helper strips only the trailing ":<version>" suffix; path segments
// must remain intact because catalog entries are indexed by the
// verbatim metadata.name string (which may itself contain "/"
// separators — e.g., "core/server", "hub.aggrega.ai/component/core/api-server").
//
// An earlier shape of this helper stripped to the last "/" segment,
// returning "server" for "core/server"; that lost load-bearing prefix
// content and prevented Tier-1 from finding entities whose
// metadata.name was a multi-segment URI.
func TestLookupNameFromURI_PreservesPathSegments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"bare-single-segment", "server", "server"},
		{"versioned-single-segment", "server:1.0.0", "server"},
		{"bare-two-segments", "core/server", "core/server"},
		{"versioned-two-segments", "core/server:1.0.0", "core/server"},
		{"versioned-registry-prefixed", "hub.aggrega.ai/component/core/api-server:1.0.0", "hub.aggrega.ai/component/core/api-server"},
		{"bare-registry-prefixed", "hub.aggrega.ai/component/core/api-server", "hub.aggrega.ai/component/core/api-server"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := lookupNameFromURI(tc.input)
			if got != tc.want {
				t.Errorf("lookupNameFromURI(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSchemaLibraryNameFromURI_TakesLastSegment pins the
// library-identifier extraction the SchemaLibrary / MappingLibrary
// Tier-1 dispatch uses. Library identifiers are PascalCase tokens at
// the URI's final segment (e.g., "Probes", "BaseMappings");
// canonical URIs may carry registry + namespace prefix segments
// which are stripped here to recover the catalog index key.
//
// The trailing ":<version>" suffix is stripped before the
// last-segment extraction.
func TestSchemaLibraryNameFromURI_TakesLastSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"bare-identifier", "Probes", "Probes"},
		{"versioned-bare", "Probes:1.0.0", "Probes"},
		{"namespaced", "core/Probes", "Probes"},
		{"versioned-namespaced", "core/Probes:1.0.0", "Probes"},
		{"versioned-registry-prefixed", "hub.aggrega.ai/schemalibrary/core/Probes:1.0.0", "Probes"},
		{"versioned-mapping-library", "hub.aggrega.ai/mappinglibrary/core/BaseMappings:2.1.0", "BaseMappings"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := schemaLibraryNameFromURI(tc.input)
			if got != tc.want {
				t.Errorf("schemaLibraryNameFromURI(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}
