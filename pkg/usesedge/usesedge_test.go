// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package usesedge_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/usesedge"
)

// TestAccessorsForwardToTheirOwnCondition asserts each accessor publishes the
// condition it names and no other.
//
// The pairwise half is the point. Each accessor forwards to a condition raised
// from the same unexported package, so a body wired to the sibling's condition
// compiles, returns a real error, and matches an errors.Is written against it —
// the swap is invisible to any check that only asserts a match succeeds. Testing
// that every other condition does NOT match is what fixes each name to one
// condition.
func TestAccessorsForwardToTheirOwnCondition(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		published error
		origin    error
	}{
		{name: "ErrDanglingUsesTarget", published: usesedge.ErrDanglingUsesTarget(), origin: topology.ErrDanglingUsesTarget()},
		{name: "ErrReleaseEdgeRetarget", published: usesedge.ErrReleaseEdgeRetarget(), origin: topology.ErrReleaseEdgeRetarget()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !errors.Is(tc.published, tc.origin) {
				t.Fatalf("%s does not match the condition it publishes", tc.name)
			}
			// A consumer matches through a wrap chain, which is the only shape the
			// published accessor is ever used in.
			if !errors.Is(fmt.Errorf("wrapped: %w", tc.origin), tc.published) {
				t.Errorf("%s does not match the condition through a wrap chain", tc.name)
			}
			for _, other := range []struct {
				name string
				err  error
			}{
				{name: "ErrDanglingUsesTarget", err: topology.ErrDanglingUsesTarget()},
				{name: "ErrReleaseEdgeRetarget", err: topology.ErrReleaseEdgeRetarget()},
				{name: "ErrUnboundReleaseEdgeConfig", err: topology.ErrUnboundReleaseEdgeConfig()},
			} {
				if other.name == tc.name {
					continue
				}
				if errors.Is(tc.published, other.err) {
					t.Errorf("%s also matches %s, so the two conditions are not distinguishable",
						tc.name, other.name)
				}
			}
		})
	}
}
