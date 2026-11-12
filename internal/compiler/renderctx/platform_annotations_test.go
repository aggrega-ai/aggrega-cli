// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderctx

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// orderedPairs builds an ordered bucket from key-value pairs, in the order given.
func orderedPairs(pairs ...string) types.OrderedMap[string] {
	var m types.OrderedMap[string]
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

// pairsOf reads an ordered bucket back as key=value strings, in its own order,
// so a row asserts the order as well as the content.
func pairsOf(m types.OrderedMap[string]) []string {
	out := []string{}
	for k, v := range m.All() {
		out = append(out, k+"="+v)
	}
	return out
}

// TestPlatformAnnotationsLayerTheReleaseOverTheAggregate pins the one layering
// every reader of a node's platform annotations shares: the aggregate bucket,
// with the release bucket written over it per key, the catalog bucket taking no
// part, in author order — the aggregate bucket's keys first, then the keys only
// the Release writes. The row with an empty aggregate bucket is what keeps every
// build that writes nothing on the Aggregate byte-identical: the result is the
// release bucket, in its own order.
//
// 🔴 PLANTED DEFECT: swap the two loops in PlatformAnnotations: the "both write"
// and "per key" rows read the Aggregate's value and fail.
func TestPlatformAnnotationsLayerTheReleaseOverTheAggregate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		buckets RenderAnnotations
		want    []string
	}{
		{
			name: "both write one key: the Release's value, in the Aggregate's position",
			buckets: RenderAnnotations{
				Aggregate: orderedPairs("aggrega.ai/namespace", "from-aggregate", "team.example/owner", "billing"),
				Release:   orderedPairs("aggrega.ai/namespace", "from-release"),
			},
			want: []string{"aggrega.ai/namespace=from-release", "team.example/owner=billing"},
		},
		{
			name: "per key: each key from the last bucket that writes it, Release-only keys after",
			buckets: RenderAnnotations{
				Aggregate: orderedPairs("aggrega.ai/image-registry", "aggregate.registry.example", "aggrega.ai/namespace", "from-aggregate"),
				Release:   orderedPairs("example.org/zone", "z1", "aggrega.ai/namespace", "from-release"),
			},
			want: []string{
				"aggrega.ai/image-registry=aggregate.registry.example",
				"aggrega.ai/namespace=from-release",
				"example.org/zone=z1",
			},
		},
		{
			name: "only the Aggregate writes: its values, in its order",
			buckets: RenderAnnotations{
				Aggregate: orderedPairs("aggrega.ai/namespace", "from-aggregate", "aggrega.ai/service-account", "dev-sa"),
			},
			want: []string{"aggrega.ai/namespace=from-aggregate", "aggrega.ai/service-account=dev-sa"},
		},
		{
			name: "the Aggregate writes nothing: exactly the release bucket, in its order",
			buckets: RenderAnnotations{
				Release: orderedPairs("example.org/zone", "z1", "aggrega.ai/namespace", "from-release"),
				Catalog: orderedPairs("aggrega.ai/namespace", "from-catalog"),
			},
			want: []string{"example.org/zone=z1", "aggrega.ai/namespace=from-release"},
		},
		{
			name: "only the catalog writes: nothing",
			buckets: RenderAnnotations{
				Catalog: orderedPairs("aggrega.ai/namespace", "from-catalog"),
			},
			want: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := pairsOf(tc.buckets.PlatformAnnotations()); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("PlatformAnnotations = %v, want %v", got, tc.want)
			}
		})
	}
}
