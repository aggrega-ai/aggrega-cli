// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — a Release places a sub-module's components by routing
// the module rather than by naming each component.
//
// The two spellings have to be equivalent, and one of them is already pinned: the
// control fixture names a sub-module's components explicitly, with EMPTY bodies,
// so the only thing those entries contribute is which boundary the components
// belong to. Routing the module says the same thing about the same components, so
// the artifacts must come out byte-identical. That makes the expectation a
// measurement of the existing spelling rather than a fresh assertion about the new
// one — nothing here encodes what the output "should" look like.
//
// Measured before the routed axis was consumed, the routed spelling built at exit
// 0 and emitted to the wrong directory with the boundary provenance missing from
// every service, so a passing suite proved nothing about it: no test named it, and
// the boundary it declared was ignored in silence.

package compiler_test

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestSubaggRouting_WholeModuleMatchesExplicitPlacement is the byte-identity
// proof. The control names `data.consumer` and `data.store` under the boundary
// with empty bodies; the routed fixture names no components at all and routes the
// whole `data` module into the same boundary. Same artifact keys, same bytes.
func TestSubaggRouting_WholeModuleMatchesExplicitPlacement(t *testing.T) {
	t.Parallel()

	explicit := pipelinetest.Build(t, subaggCoverageInput("release-ok.yaml"))
	routed := pipelinetest.Build(t, subaggCoverageInput("release-routed.yaml"))

	// artifactKeys does not order its result despite what its comment says, so the
	// comparison sorts here rather than relying on that.
	explicitKeys := artifactKeys(explicit.Artifacts)
	routedKeys := artifactKeys(routed.Artifacts)
	sort.Strings(explicitKeys)
	sort.Strings(routedKeys)
	if len(routedKeys) == 0 {
		t.Fatal("the routed spelling emitted no artifacts at all")
	}
	if !equalStrings(explicitKeys, routedKeys) {
		t.Fatalf("routing a sub-module must emit the same artifacts as naming its components:\n  explicit: %v\n  routed:   %v",
			explicitKeys, routedKeys)
	}
	for _, key := range explicitKeys {
		if want, got := string(explicit.Artifacts[key]), string(routed.Artifacts[key]); want != got {
			t.Errorf("artifact %q differs between the two spellings:\n--- naming the components ---\n%s\n--- routing the module ---\n%s",
				key, want, got)
		}
	}
}

// TestSubaggRouting_RoutedModulePlacedTwiceHalts — routing one sub-module into two
// boundaries places every component it contains in both, so one node would take
// its output directory from whichever placement was written last. Halting is what
// keeps that unreachable.
func TestSubaggRouting_RoutedModulePlacedTwiceHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, subaggCoverageInput("release-routed-twice.yaml"))
	if !errors.Is(err, resolver.ErrDuplicateBoundaryComponent()) {
		t.Fatalf("want ErrDuplicateBoundaryComponent for a sub-module routed into two boundaries; got %v", err)
	}
}

// TestSubaggRouting_RoutedAndNamedInDifferentBoundariesHalts — the mixed case:
// the module is routed into one boundary while one of its components is named in
// another. The two placements disagree about where that component belongs.
func TestSubaggRouting_RoutedAndNamedInDifferentBoundariesHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, subaggCoverageInput("release-routed-and-named.yaml"))
	if !errors.Is(err, resolver.ErrDuplicateBoundaryComponent()) {
		t.Fatalf("want ErrDuplicateBoundaryComponent for a component named in one boundary and routed into another; got %v", err)
	}
}

// TestSubaggRouting_RoutedAndNamedInSameBoundaryBuilds is the counter-case the two
// halts are read against: routing a module and naming one of its components under
// the SAME boundary is not a conflict — the routing places the component and the
// named entry configures it, and both agree on where it belongs. Without this, a
// guard that rejected every co-occurrence would look like two successes.
func TestSubaggRouting_RoutedAndNamedInSameBoundaryBuilds(t *testing.T) {
	t.Parallel()
	if _, err := pipelinetest.BuildE(t, subaggCoverageInput("release-routed-and-named-same.yaml")); err != nil {
		t.Fatalf("routing a module and configuring one of its components under the same boundary must build; got %v", err)
	}
}

// fullstackRoutingInput compiles a fullstack-app Release against the catalog its
// manifest pins, the same pairing the other fullstack cases use.
func fullstackRoutingInput(release string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath: "testdata/fullstack-app/releases/" + release,
		CatalogPaths: []string{
			"testdata/fullstack-app/catalog/base",
			"testdata/fullstack-app/catalog/recipes/helm-synthesized-with-artifacts",
		},
	}
}

// TestSubaggRouting_PerComponentPayloadMatchesExplicitPlacement is the byte-identity
// proof for the payload half: a routing entry that names one of the module's
// components and gives it the same sizing the explicit spelling gives it must
// render identically. Where the whole-module case proves the placement arrives,
// this proves what is written ON a routed component arrives with it.
func TestSubaggRouting_PerComponentPayloadMatchesExplicitPlacement(t *testing.T) {
	t.Parallel()

	explicit := pipelinetest.Build(t, fullstackRoutingInput("helm-synthesized-with-artifacts.yaml"))
	routed := pipelinetest.Build(t, fullstackRoutingInput("routed-payload.yaml"))

	explicitKeys := artifactKeys(explicit.Artifacts)
	routedKeys := artifactKeys(routed.Artifacts)
	sort.Strings(explicitKeys)
	sort.Strings(routedKeys)
	if len(routedKeys) == 0 {
		t.Fatal("the routed spelling emitted no artifacts at all")
	}
	if !equalStrings(explicitKeys, routedKeys) {
		t.Fatalf("a routed per-component entry must emit the same artifacts as the named one:\n  explicit: %v\n  routed:   %v",
			explicitKeys, routedKeys)
	}
	for _, key := range explicitKeys {
		if want, got := string(explicit.Artifacts[key]), string(routed.Artifacts[key]); want != got {
			t.Errorf("artifact %q differs between the two spellings:\n--- naming the component ---\n%s\n--- routing the module ---\n%s",
				key, want, got)
		}
	}
}

// TestSubaggRouting_FeatureOnRoutedComponentReachesArtifacts reads the rendered
// output for the attachment itself. A Feature attached to a component the Release
// routes in carries infra its FeatureRecipe turns into environment entries, so the
// evidence that the attachment arrived is those entries appearing in the routed
// component's own rendered manifest — and the values being the ones the Release
// wrote, not the schema's defaults.
//
// Measured before the routed axis was consumed, the same document built and the
// attachment appeared nowhere: it was not resolved, not pinned and not rendered.
func TestSubaggRouting_FeatureOnRoutedComponentReachesArtifacts(t *testing.T) {
	t.Parallel()

	const target = "dist/svc/helm/svc-zdedge-deployment.yaml"
	named := pipelinetest.Build(t, fullstackRoutingInput("named-feature.yaml"))
	routed := pipelinetest.Build(t, fullstackRoutingInput("routed-feature.yaml"))

	namedRendered, ok := named.Artifacts[target]
	if !ok {
		t.Fatalf("the named spelling emitted no deployment at %q; artifacts: %v", target, artifactKeys(named.Artifacts))
	}
	routedRendered, ok := routed.Artifacts[target]
	if !ok {
		t.Fatalf("the routed spelling emitted no deployment at %q; artifacts: %v", target, artifactKeys(routed.Artifacts))
	}

	// The control first: naming the component and attaching the Feature to it is the
	// spelling already supported, so it establishes what the attachment looks like
	// once rendered. Without it, a routed spelling that rendered nothing would be
	// indistinguishable from a Feature that renders nothing on this component.
	for _, want := range []string{"POOL_SIZE", "25", "MAX_CONNECTIONS", "200"} {
		if !strings.Contains(string(namedRendered), want) {
			t.Fatalf("control: the Feature must reach the artifact when the component is NAMED; %q absent from %s\n%s",
				want, target, namedRendered)
		}
	}
	if want, got := string(namedRendered), string(routedRendered); want != got {
		t.Errorf("the Feature attached through the routing entry must render as it does when the component is named:\n--- named ---\n%s\n--- routed ---\n%s",
			want, got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
