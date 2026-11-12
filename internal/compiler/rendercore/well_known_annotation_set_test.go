// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// bucketOf builds an ordered annotation bucket from key-value pairs, in the order
// given.
func bucketOf(pairs ...string) types.OrderedMap[string] {
	var m types.OrderedMap[string]
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

// TestWellKnownAnnotationSetLayersTheReleaseOverTheAggregate pins which buckets a
// provider render reads the well-known keys from, over every way the three
// buckets can carry them: the Aggregate's values, with the Release's written over
// them, and never the catalog's. Each row names its buckets and the map the render
// must read; a row where the Release writes a key the Aggregate also writes is
// what proves the Release wins, and a row where only the catalog writes a key is
// what proves the catalog decides nothing.
//
// 🔴 PLANTED DEFECT: in RenderAnnotations.PlatformAnnotations
// (internal/compiler/renderctx/renderctx.go) drop the loop over the aggregate
// bucket: the "only the Aggregate" and "per key" rows fail. Swap the two loops:
// the "both write" row renders the Aggregate's values and fails.
func TestWellKnownAnnotationSetLayersTheReleaseOverTheAggregate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		buckets renderctx.RenderAnnotations
		want    map[string]string
	}{
		{
			name: "all three buckets write both keys: the Release's values",
			buckets: renderctx.RenderAnnotations{
				Aggregate: bucketOf(AnnotationNamespace, "from-aggregate", AnnotationImageRegistry, "aggregate.registry.example"),
				Release:   bucketOf(AnnotationNamespace, "from-release", AnnotationImageRegistry, "release.registry.example"),
				Catalog:   bucketOf(AnnotationNamespace, "from-catalog", AnnotationImageRegistry, "catalog.registry.example"),
			},
			want: map[string]string{AnnotationNamespace: "from-release", AnnotationImageRegistry: "release.registry.example"},
		},
		{
			name: "the Release writes one key and the Aggregate both: per key, the Release's where it writes",
			buckets: renderctx.RenderAnnotations{
				Aggregate: bucketOf(AnnotationNamespace, "from-aggregate", AnnotationImageRegistry, "aggregate.registry.example"),
				Release:   bucketOf(AnnotationNamespace, "from-release"),
			},
			want: map[string]string{AnnotationNamespace: "from-release", AnnotationImageRegistry: "aggregate.registry.example"},
		},
		{
			name: "the catalog and the Release write the key: the Release's value",
			buckets: renderctx.RenderAnnotations{
				Release: bucketOf(AnnotationNamespace, "from-release"),
				Catalog: bucketOf(AnnotationNamespace, "from-catalog"),
			},
			want: map[string]string{AnnotationNamespace: "from-release"},
		},
		{
			name: "only the Aggregate and the catalog write the keys: the Aggregate's, never the catalog's",
			buckets: renderctx.RenderAnnotations{
				Aggregate: bucketOf(AnnotationNamespace, "from-aggregate"),
				Catalog:   bucketOf(AnnotationImageRegistry, "catalog.registry.example"),
			},
			want: map[string]string{AnnotationNamespace: "from-aggregate"},
		},
		{
			name: "the Release writes keys of its own beside the well-known one: all of them",
			buckets: renderctx.RenderAnnotations{
				Release: bucketOf("example.org/zone", "z1", AnnotationServiceAccount, "op-sa"),
			},
			want: map[string]string{"example.org/zone": "z1", AnnotationServiceAccount: "op-sa"},
		},
		{
			name:    "no bucket carries anything: an empty map, never nil",
			buckets: renderctx.RenderAnnotations{},
			want:    map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := WellKnownAnnotationSet(tc.buckets)
			if got == nil {
				t.Fatal("WellKnownAnnotationSet returned nil; the render cores read it as a map")
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("WellKnownAnnotationSet = %v, want %v", got, tc.want)
			}
			// Read through the accessors the render cores use, so the row fails
			// where a key would decide placement, not only where the map differs.
			if ns := NamespaceFromAnnotations(got); ns != tc.want[AnnotationNamespace] {
				t.Errorf("namespace read = %q, want %q", ns, tc.want[AnnotationNamespace])
			}
			if reg := ImageRegistryFromAnnotations(got); reg != tc.want[AnnotationImageRegistry] {
				t.Errorf("image registry read = %q, want %q", reg, tc.want[AnnotationImageRegistry])
			}
		})
	}
}
