// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The rule about a delivery key a component's own injection block writes twice
// under one mechanism.
//
// One report per repeated key, at the mechanism, naming the mechanism and the key:
// the later entry would otherwise replace the earlier one's value without a word.
// A key written once under each of two mechanisms is the rule beside this one's to
// report, and draws nothing here.
func TestTheSelfDeliveryKeyWrittenOnceRuleReportsEachRepeatedKey(t *testing.T) {
	t.Parallel()

	catalog := selfDeliveryRuleCatalog()

	for _, tc := range []struct {
		name     string
		block    []mechanismDelivery
		locators []string
		details  [][]string
	}{
		{
			name:     "a key written twice under one mechanism is reported once, at the mechanism",
			block:    []mechanismDelivery{{name: "env", keys: []string{"MY_TOKEN", "LOG_LEVEL", "MY_TOKEN"}}},
			locators: []string{".spec.components.web.inject.env"},
			details:  [][]string{{"env", "MY_TOKEN"}},
		},
		{
			name:     "a key written three times is still one report",
			block:    []mechanismDelivery{{name: "env", keys: []string{"A", "A", "A"}}},
			locators: []string{".spec.components.web.inject.env"},
			details:  [][]string{{"env", "A"}},
		},
		{
			name:     "two repeated keys are two reports, in the order the keys first appear",
			block:    []mechanismDelivery{{name: "env", keys: []string{"B", "A", "B", "A"}}},
			locators: []string{".spec.components.web.inject.env", ".spec.components.web.inject.env"},
			details:  [][]string{{"env", "B"}, {"env", "A"}},
		},
		{
			name:  "keys written once each draw nothing",
			block: []mechanismDelivery{{name: "env", keys: []string{"A", "B"}}},
		},
		{
			name: "one key under two mechanisms, once each, draws nothing here",
			block: []mechanismDelivery{
				{name: "env", keys: []string{"SHARED"}},
				{name: "arg", keys: []string{"SHARED"}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			na := normalized(t, selfDeliveryAggregate(selfDeliveryComponent{
				name: "web", typ: selfDeliveryOpenType, block: writtenBlock(tc.block...),
			}))
			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)

			objections, err := constraint.RunOnNodeMechanisms[constraint.NormalizedView](
				nil, coordinates, coordinates, view, selfInjectKeyWrittenOnce)
			if err != nil {
				t.Fatalf("the mechanism walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.locators) {
				t.Fatalf("raised sequence = %v; want %v", got, tc.locators)
			}
			for i, o := range objections {
				if !errors.Is(o.Sentinel(), ErrInjectKeyCollision()) {
					t.Errorf("objection %d is raised under %v; want the duplicate-delivery-key condition", i, o.Sentinel())
				}
				if got := strings.Join(o.Detail(), "|"); got != strings.Join(tc.details[i], "|") {
					t.Errorf("objection %d detail = %q; want %q", i, o.Detail(), tc.details[i])
				}
			}
		})
	}
}

// TestTheGuardsHaltOnAKeyWrittenTwiceInAComponentsOwnBlock proves the rule is
// invoked where a build resolves the Aggregate: the guards refuse a block writing
// one key twice under one mechanism, naming the mechanism and the key, and accept
// the same block with the key written once.
func TestTheGuardsHaltOnAKeyWrittenTwiceInAComponentsOwnBlock(t *testing.T) {
	t.Parallel()

	run := func(keys ...string) []*ResolverError {
		t.Helper()
		na := normalized(t, selfDeliveryAggregate(selfDeliveryComponent{
			name: "web", typ: selfDeliveryOpenType,
			block: writtenBlock(mechanismDelivery{name: "env", keys: keys}),
		}))
		errs, err := RunGuards(nil, na, nil, "", selfDeliveryRuleCatalog(), nil, nil)
		if err != nil {
			t.Fatalf("the guards returned an error: %v", err)
		}
		return errs
	}

	if errs := run("MY_TOKEN", "LOG_LEVEL"); len(errs) != 0 {
		t.Fatalf("a block writing each key once was refused: %v", errs)
	}

	errs := run("MY_TOKEN", "LOG_LEVEL", "MY_TOKEN")
	if len(errs) != 1 {
		t.Fatalf("got %d findings for a key written twice, want 1: %v", len(errs), errs)
	}
	if !errors.Is(errs[0], ErrInjectKeyCollision()) {
		t.Errorf("the finding is not the duplicate-delivery-key condition: %v", errs[0])
	}
	for _, want := range []string{"env", "MY_TOKEN"} {
		if !strings.Contains(errs[0].Error(), want) {
			t.Errorf("the finding does not name %q: %v", want, errs[0])
		}
	}
}
