// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func bag(kv ...string) types.OrderedMap[string] {
	m := types.OrderedMap[string]{}
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i], kv[i+1])
	}
	return m
}

// TestNarrowToDeclared pins the projection ceiling AND the reading of an absent
// entry, which is the half a later refactor is most likely to invert. A node the
// declared set does not cover must be projected WHOLE: reading absence as an
// empty contract would empty that node's entire bag, which is the silent-empty
// failure the ceiling exists to remove rather than to create.
func TestNarrowToDeclared(t *testing.T) {
	produced := bag("endpoint", "https://x", "model", "gpt", "provider", "openai")

	tests := []struct {
		name     string
		declared NodeDeclaredProvides
		want     []string
	}{
		{
			name:     "node absent from the declared set is not narrowed",
			declared: NodeDeclaredProvides{"other": {"endpoint": true}},
			want:     []string{"endpoint", "model", "provider"},
		},
		{
			name:     "nil declared set narrows nothing",
			declared: nil,
			want:     []string{"endpoint", "model", "provider"},
		},
		{
			name:     "every produced key declared leaves the bag untouched",
			declared: NodeDeclaredProvides{"t": {"endpoint": true, "model": true, "provider": true}},
			want:     []string{"endpoint", "model", "provider"},
		},
		{
			name:     "undeclared key is removed and author order survives",
			declared: NodeDeclaredProvides{"t": {"endpoint": true, "model": true}},
			want:     []string{"endpoint", "model"},
		},
		{
			name:     "a node declaring nothing at all projects an empty bag",
			declared: NodeDeclaredProvides{"t": {}},
			want:     nil,
		},
		{
			name:     "a declared key the Recipe never published adds nothing",
			declared: NodeDeclaredProvides{"t": {"endpoint": true, "never_published": true}},
			want:     []string{"endpoint"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := narrowToDeclared(produced, tc.declared, "t")
			keys := got.Keys()
			if len(keys) != len(tc.want) {
				t.Fatalf("keys = %v, want %v", keys, tc.want)
			}
			for i, k := range tc.want {
				if keys[i] != k {
					t.Fatalf("keys = %v, want %v", keys, tc.want)
				}
				v, ok := got.Get(k)
				if !ok {
					t.Fatalf("key %q missing from the narrowed bag", k)
				}
				orig, _ := produced.Get(k)
				if v != orig {
					t.Errorf("key %q value = %q, want the produced value %q", k, v, orig)
				}
			}
		})
	}

	// The input is never mutated: the narrowed bag is a new value, so a second
	// consumer of the same target still sees the whole produced bag.
	if produced.Len() != 3 {
		t.Errorf("produced bag was mutated: len = %d, want 3", produced.Len())
	}
}
