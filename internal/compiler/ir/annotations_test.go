// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestBuildAnnotationBuckets_AggregateBucketIsTheNodesSet pins that the aggregate
// bucket is the topology node's effective set, in its order, held as a copy: a
// later write to the node's map does not reach the graph node built from it. A
// node carrying no set yields an empty bucket.
func TestBuildAnnotationBuckets_AggregateBucketIsTheNodesSet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		set  []string
	}{
		{"a node carrying a set", []string{"team.example/owner", "billing-squad", "cost.example/center", "CC-3421"}},
		{"a node carrying none", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			set := types.OrderedMap[string]{}
			for i := 0; i+1 < len(tc.set); i += 2 {
				set.Set(tc.set[i], tc.set[i+1])
			}
			_, _, aggregate := buildAnnotationBuckets("node", "core/worker", resolver.CatalogLookup{}, nil, set)

			got := []string{}
			for k, v := range aggregate.All() {
				got = append(got, k, v)
			}
			want := tc.set
			if want == nil {
				want = []string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("aggregate bucket = %v, want %v", got, want)
			}

			set.Set("team.example/late", "after")
			if _, leaked := aggregate.Get("team.example/late"); leaked {
				t.Error("a write to the node's set after the build reached the bucket; the bucket must be a copy")
			}
		})
	}
}
