// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestSelfInjectSeparatesAnEmptyBlockFromAnAbsentOne pins the distinction the
// self-inject allowlist rule turns on, and the reason it is worth pinning is
// that the distinction is invisible in the mechanism names the block yields.
//
// A component type declaring no injection allowlist reserves delivery to its
// Recipe, so an instance of it that writes an injection block at all is
// refused. Writing an EMPTY block is still writing one: the author has
// dictated delivery on a type that permits none, and the refusal names the
// block. Omitting the block entirely dictates nothing and is accepted.
//
// Both cases yield the same mechanism names — none — so a rule reading only
// the mechanisms an instance declares cannot tell them apart while they
// require opposite verdicts. Any reformulation of this rule has to read
// whether the block was written, not what it carries; the two assertions
// below are one pair and neither is meaningful alone.
func TestSelfInjectSeparatesAnEmptyBlockFromAnAbsentOne(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		inject    *types.InjectBlock
		wantRaise bool
	}{
		{
			name:      "no block written",
			inject:    nil,
			wantRaise: false,
		},
		{
			name:      "block written and left empty",
			inject:    &types.InjectBlock{},
			wantRaise: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			components := types.OrderedMap[types.ComponentInstance]{}
			components.Set("db", types.ComponentInstance{
				Type:   "core/datastore/postgres", // declares no injection allowlist
				Inject: tc.inject,
			})
			agg := &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test"},
				Spec:     types.AggregateSpec{Components: components},
			}

			errs := guardSelfInject(normalized(t, agg), selfInjectCatalog())

			if tc.wantRaise {
				if len(errs) != 1 {
					t.Fatalf("an instance that wrote an injection block on a type permitting none "+
						"raised %d finding(s); want exactly 1.\n"+
						"INVARIANT: writing the block is what the rule refuses, whatever the block carries.\n"+
						"CONSEQUENCE: raising nothing here retires a live refusal — the author has dictated "+
						"delivery on a type that reserves it, and nothing says so.\ngot: %v", len(errs), errs)
				}
				if !errors.Is(errs[0], ErrSelfInjectForbidden()) {
					t.Errorf("the finding carries %v; want the forbidden-self-injection rule.\n"+
						"INVARIANT: the rule that refuses an injection block on a type permitting none is "+
						"identified by its own sentinel.\n"+
						"CONSEQUENCE: a caller matching on the sentinel stops seeing this refusal.", errs[0])
				}
				return
			}
			if len(errs) != 0 {
				t.Fatalf("an instance that wrote no injection block raised %d finding(s); want 0.\n"+
					"INVARIANT: dictating no delivery is not dictating forbidden delivery.\n"+
					"CONSEQUENCE: raising here refuses a document every component that simply has no "+
					"injection block can ship.\ngot: %v", len(errs), errs)
			}
		})
	}

	// The two inputs above are indiscernible by the mechanism names they
	// declare. This is the half that makes the pair a finding rather than two
	// unrelated rows: the verdicts differ and this input does not.
	absent := InjectMechanismNames(nil)
	empty := InjectMechanismNames(&types.InjectBlock{})
	if len(absent) != 0 || len(empty) != 0 {
		t.Fatalf("the mechanism names were %v for no block and %v for an empty block; want both empty.\n"+
			"INVARIANT: neither an absent block nor an empty one dictates any delivery mechanism.\n"+
			"CONSEQUENCE: if either is non-empty the indiscernibility below is not the one described, "+
			"and a rule reformulated around mechanism names would behave differently than measured.",
			absent, empty)
	}
}
