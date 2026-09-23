// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Drives what the descent's routing decision changes about an acquisition
// failure, which is where that decision becomes something a reader can see.
//
// The descent resolves a source once per using instance. Where the author wrote
// the artifact's identity out in full, every instance resolves the same artifact,
// so a failure belongs to the entity and naming one instance would misreport it.
// Where a field that fixes the identity is still template text, the artifact is
// settled by that instance's own infra, so the failure is that instance's alone
// and a reader cannot begin looking until they are told which one.
//
// Both directions run against one resolver failing for one reason, so the only
// thing that moves between the rows is the routing decision.

package closure_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// errRefusedByFixture is the one cause every row below fails with, so a
// difference between two rows' messages is the routing decision and nothing else.
var errRefusedByFixture = errors.New("registry refused the request")

// refusingSourceResolver fails every resolution with the same cause.
type refusingSourceResolver struct{}

func (refusingSourceResolver) Resolve(closure.SourceSpec) (*closure.ResolvedSource, error) {
	return nil, errRefusedByFixture
}

func TestAcquisitionFailureNamesTheInstanceOnlyWhereTheInstanceDecidesTheArtifact(t *testing.T) {
	t.Parallel()

	const uri = "hub.aggrega.ai/recipe/helm/mariadb:1.0.0"
	const instance = "orders-db"

	cases := []struct {
		name        string
		source      any
		wantNamed   bool
		wantReason  string
		instanceOff bool
	}{
		{
			name: "identity written out in full",
			source: map[string]any{
				"kind":    "helm-oci-chart",
				"url":     "oci://hub.aggrega.ai/charts/mariadb",
				"version": "25.0.8",
			},
			wantNamed:  false,
			wantReason: "every instance resolves the same artifact, so the failure is not one instance's",
		},
		{
			name: "version filled in per instance",
			source: map[string]any{
				"kind":    "helm-oci-chart",
				"url":     "oci://hub.aggrega.ai/charts/mariadb",
				"version": "{{ .infra.chart_version }}",
			},
			wantNamed:  true,
			wantReason: "the artifact is whatever this instance's infra names, so the failure is this instance's",
		},
		{
			name: "reference filled in per instance",
			source: map[string]any{
				"kind": "git-repository",
				"url":  "https://example.com/org/repo.git",
				"ref":  "{{ .infra.chart_version }}",
			},
			wantNamed:  true,
			wantReason: "a git source's reference fixes its identity exactly as a version does elsewhere",
		},
		{
			name:   "scalar reference, which is its own identity",
			source: "oci://hub.aggrega.ai/charts/mariadb",
			// A scalar carries no template here, so its identity is written out and
			// the failure is the entity's. The row exists because the reading answers
			// over both shapes and a repair covering only the structured one would
			// pass every other row.
			wantNamed:  false,
			wantReason: "a concrete scalar reference names one artifact for every instance",
		},
		{
			name: "no instance recorded",
			source: map[string]any{
				"kind":    "helm-oci-chart",
				"url":     "oci://hub.aggrega.ai/charts/mariadb",
				"version": "{{ .infra.chart_version }}",
			},
			instanceOff: true,
			wantNamed:   false,
			wantReason:  "there is no instance to name, and a message naming an empty one helps nobody",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			loader := &fakeRecipeLoader{
				recipes: map[string]*types.Recipe{uri: makeRecipe(uri, tc.source)},
			}
			cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
			used := map[string][]closure.ComponentSourceInfra{
				uri: {{
					Component: instance,
					Infra:     map[string]any{"chart_version": "25.0.8"},
				}},
			}
			if tc.instanceOff {
				used[uri][0].Component = ""
			}

			_, err := closure.DescendRecipeSources(used, cl, loader, refusingSourceResolver{}, "/ws", ".aggrega/cache")
			if err == nil {
				t.Fatal("the fixture resolver refuses every request, so the descent must fail")
			}
			if !errors.Is(err, errRefusedByFixture) {
				t.Fatalf("the failure must carry the resolver's own cause, or the rows are comparing something else: %v", err)
			}
			named := strings.Contains(err.Error(), instance)
			if named != tc.wantNamed {
				t.Errorf("instance named = %v, want %v — %s\nmessage: %s",
					named, tc.wantNamed, tc.wantReason, err.Error())
			}
			if !strings.Contains(err.Error(), uri) {
				t.Errorf("every acquisition failure names the entity it was resolving: %s", err.Error())
			}
		})
	}
}

// TestLocalityIsTheOnlySkip pins the ruling the routing decision is built on: a
// source whose identity is still text is DESCENDED, not skipped.
//
// The descent resolves such a source once per using instance against that
// instance's own infra, which is how a templated identity is made concrete and
// pinned. Reading the fact as a second skip would leave unpinned exactly the
// sources a lockfile exists to pin — and it would do so silently, since a skipped
// entity produces no artifact and no diagnostic.
func TestLocalityIsTheOnlySkip(t *testing.T) {
	t.Parallel()

	const uri = "hub.aggrega.ai/recipe/helm/mariadb:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://hub.aggrega.ai/charts/mariadb",
		"version": "{{ .infra.chart_version }}",
	}
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: makeRecipe(uri, source)}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	used := map[string][]closure.ComponentSourceInfra{
		uri: {{Component: "orders-db", Infra: map[string]any{"chart_version": "25.0.8"}}},
	}

	got, err := closure.DescendRecipeSources(used, cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("a source whose identity is filled in per instance must still be pinned; got %d artifacts", len(got))
	}
	if got[0].PinnedVersion != "25.0.8" {
		t.Errorf("PinnedVersion = %q; want the value this instance's infra produced", got[0].PinnedVersion)
	}
}
