// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// bindingCatalog declares a consumer with two sockets: "databases", which is
// required and carries an infraSchema, and "caches", which is optional and
// carries a valueSchema with a required key. The two are kept apart so a fixture
// can exercise one rule without tripping the other.
func bindingCatalog(t *testing.T) resolver.CatalogLookup {
	t.Helper()
	const svcYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: app/svc
  version: "1.0.0"
spec:
  usesSchema:
    databases:
      required: true
      selector: "component.aggrega.ai/type=db"
      infraSchema:
        storage_class: { type: string, enum: ["fast", "slow"] }
    caches:
      selector: "component.aggrega.ai/type=cache"
      valueSchema:
        pool_size: { type: integer, required: true }
`
	const dbYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: app/db
  version: "1.0.0"
spec: {}
`
	var svc, db types.Component
	if err := yaml.Unmarshal([]byte(svcYAML), &svc); err != nil {
		t.Fatalf("svc fixture parse: %v", err)
	}
	if err := yaml.Unmarshal([]byte(dbYAML), &db); err != nil {
		t.Fatalf("db fixture parse: %v", err)
	}
	return resolver.CatalogLookup{
		Component: func(u string) (types.Component, bool) {
			switch u {
			case "app/svc:1.0.0":
				return svc, true
			case "app/db:1.0.0":
				return db, true
			}
			return types.Component{}, false
		},
	}
}

func mustAggregate(t *testing.T, y string) *types.Aggregate {
	t.Helper()
	var a types.Aggregate
	if err := yaml.Unmarshal([]byte(y), &a); err != nil {
		t.Fatalf("aggregate parse: %v\n%s", err, y)
	}
	return &a
}

func mustRelease(t *testing.T, y string) *types.Release {
	t.Helper()
	var r types.Release
	if err := yaml.Unmarshal([]byte(y), &r); err != nil {
		t.Fatalf("release parse: %v\n%s", err, y)
	}
	return &r
}

// buildWith runs the whole topology build over an Aggregate, an optional
// Release, and an optional sub-aggregate document reachable as module "mod".
func buildWith(t *testing.T, aggYAML, relYAML, modYAML string) error {
	t.Helper()
	agg := mustAggregate(t, aggYAML)
	var rel *types.Release
	var boundaryInfra map[string]*resolver.BoundaryInfra
	if relYAML != "" {
		rel = mustRelease(t, relYAML)
		// The Release edge pass reads each node's resolved boundary path, which
		// production supplies from the same wiring call. Omitting it would make
		// every fixture look like a Release whose entries reach no node.
		boundaryInfra, _ = resolver.WireBoundaryInfra(types.NormalizeRelease(rel))
	}
	var loader SubAggregateLoader
	if modYAML != "" {
		mod := mustAggregate(t, modYAML)
		loader = func(string, map[string]any) (*types.Aggregate, string, map[string]any, error) {
			return mod, "mod.yaml", nil, nil
		}
	}
	_, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			Release:       rel,
			BoundaryInfra: boundaryInfra,
		},
		CatalogLookup:    bindingCatalog(t),
		LoadSubAggregate: loader,
	})
	return err
}

// flatAggregate is a two-component Aggregate whose consumer binds its required
// socket, so a fixture built on it halts only for the reason it is testing.
const flatAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
        databases:
          main:
            target: db
    db:
      type: app/db:1.0.0
`

// TestReleaseEdgeConfigMustReachAnEdge proves that Release per-edge
// configuration reaching no edge halts, and that the same configuration reaching
// an edge passes.
//
// The passing half is what keeps the halt from being a ban on configuring edges
// at all: a targetless entry is the supported way to configure an edge the
// developer declared, and it must keep working.
func TestReleaseEdgeConfigMustReachAnEdge(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		release  string
		wantHalt bool
	}{
		{
			name: "configures the edge the developer declared",
			release: `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        api:
          uses:
            databases:
              main:
                infra:
                  storage_class: fast
`,
			wantHalt: false,
		},
		{
			name: "configures an alias the developer never declared",
			release: `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        api:
          uses:
            databases:
              spare:
                infra:
                  storage_class: fast
`,
			wantHalt: true,
		},
		{
			name: "names a component the Aggregate does not declare",
			release: `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        data.api:
          uses:
            databases:
              main:
                infra:
                  storage_class: fast
`,
			wantHalt: true,
		},
		{
			name: "wires the edge it configures, by naming a target",
			release: `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        api:
          uses:
            databases:
              spare:
                target: db
                infra:
                  storage_class: fast
`,
			wantHalt: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := buildWith(t, flatAggregate, tc.release, "")
			if tc.wantHalt {
				if !errors.Is(err, ErrUnboundReleaseEdgeConfig()) {
					t.Fatalf("configuration reaching no edge must halt; got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("configuration reaching an edge must pass; got %v", err)
			}
		})
	}
}

// TestReleaseEdgeConfig_PrunedConsumerIsNotAFault proves an entry left on a
// component the operator switched off is accepted rather than halted. Switching
// a component off removes its whole edge set by design, so the entry reaches no
// edge for a reason that is not a mistake — and halting would make an `active:`
// toggle unusable while any Release configured the component.
func TestReleaseEdgeConfig_PrunedConsumerIsNotAFault(t *testing.T) {
	t.Parallel()
	const aggYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      active: "false"
      uses:
        databases:
          main:
            target: db
    db:
      type: app/db:1.0.0
`
	const relYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        api:
          uses:
            databases:
              main:
                infra:
                  storage_class: fast
`
	if err := buildWith(t, aggYAML, relYAML, ""); err != nil {
		t.Fatalf("an entry on a switched-off component must be accepted; got %v", err)
	}
}

// TestReleaseEdgeConfig_TargetThatNamesNothingIsDangling proves an entry naming
// a target no component answers to is reported as a dangling target rather than
// as unbound configuration. An entry with a target draws its edge, so it is
// never unbound — which is what keeps the more informative halt in front.
func TestReleaseEdgeConfig_TargetThatNamesNothingIsDangling(t *testing.T) {
	t.Parallel()
	const relYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        api:
          uses:
            databases:
              spare:
                target: nowhere
                infra:
                  storage_class: fast
`
	err := buildWith(t, flatAggregate, relYAML, "")
	if errors.Is(err, ErrUnboundReleaseEdgeConfig()) {
		t.Fatalf("a missing target must not be reported as unbound configuration; got %v", err)
	}
	if !errors.Is(err, ErrDanglingUsesTarget()) {
		t.Fatalf("want the dangling-target halt; got %v", err)
	}
}

// TestReleaseEdgeConfig_UndeclaredSocketKeepsItsOwnDiagnostic proves the new
// halt does not take over a case that already had a better diagnostic. A socket
// the consuming component does not declare is the undeclared-socket rule's, and
// stays so even when the entry also carries configuration reaching no edge —
// otherwise a rule added for one class would silently disable another.
func TestReleaseEdgeConfig_UndeclaredSocketKeepsItsOwnDiagnostic(t *testing.T) {
	t.Parallel()
	const modYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mod
spec:
  components:
    consumer:
      type: app/svc:1.0.0
      uses:
        databases:
          main:
            target: store
    store:
      type: app/db:1.0.0
`
	const aggYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    mod:
      type: ./mod.yaml
`
	const relYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        mod.consumer:
          uses:
            nope:
              main:
                infra:
                  storage_class: fast
`
	err := buildWith(t, aggYAML, relYAML, modYAML)
	if errors.Is(err, ErrUnboundReleaseEdgeConfig()) {
		t.Fatalf("an undeclared socket must not be reported as unbound configuration; got %v", err)
	}
	if !errors.Is(err, resolver.ErrUnknownUsesSocket()) {
		t.Fatalf("an undeclared socket must halt with the undeclared-socket rule; got %v", err)
	}
}

// TestSubAggregateNodesAreInspectedByEveryRule proves the third node class — a
// component that exists only once the graph is flattened — is inspected by the
// socket-allowlist rule, the per-edge conformance rule and the required-socket
// rule, in both directions.
//
// The class is reached by a dotted node id, which is what qualification actually
// marks: a module prefix, never a subsystem. A component in a declared subsystem
// carries a bare id and is inspected before flattening.
func TestSubAggregateNodesAreInspectedByEveryRule(t *testing.T) {
	t.Parallel()
	const aggYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    mod:
      type: ./mod.yaml
`
	// modBoundSocket binds the required socket, so a fixture built on it halts
	// only for the rule under test.
	const modBoundSocket = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mod
spec:
  components:
    consumer:
      type: app/svc:1.0.0
      uses:
        databases:
          main:
            target: store
    store:
      type: app/db:1.0.0
`
	const modUnboundSocket = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mod
spec:
  components:
    consumer:
      type: app/svc:1.0.0
    store:
      type: app/db:1.0.0
`
	const modEmptyCacheEdge = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mod
spec:
  components:
    consumer:
      type: app/svc:1.0.0
      uses:
        databases:
          main:
            target: store
        caches:
          spare:
            target: store
    store:
      type: app/db:1.0.0
`
	const modFilledCacheEdge = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mod
spec:
  components:
    consumer:
      type: app/svc:1.0.0
      uses:
        databases:
          main:
            target: store
        caches:
          spare:
            target: store
            values:
              pool_size: 5
    store:
      type: app/db:1.0.0
`
	releaseOnSocket := func(socket string) string {
		return `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        mod.consumer:
          uses:
            ` + socket + `:
              wired:
                target: mod.store
`
	}

	cases := []struct {
		name     string
		mod      string
		release  string
		sentinel error
	}{
		{
			name:     "socket allowlist refuses a socket the component does not declare",
			mod:      modBoundSocket,
			release:  releaseOnSocket("nope"),
			sentinel: resolver.ErrUnknownUsesSocket(),
		},
		{
			name:    "socket allowlist accepts a socket the component declares",
			mod:     modBoundSocket,
			release: releaseOnSocket("caches"),
		},
		{
			name:     "required socket left unbound halts",
			mod:      modUnboundSocket,
			sentinel: resolver.ErrMissingRequiredSocket(),
		},
		{
			name: "required socket bound inside the module passes",
			mod:  modBoundSocket,
		},
		{
			name:     "per-edge required key missing on an edge that authors nothing halts",
			mod:      modEmptyCacheEdge,
			sentinel: resolver.ErrSchemaConformance(),
		},
		{
			name: "per-edge required key present passes",
			mod:  modFilledCacheEdge,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := buildWith(t, aggYAML, tc.release, tc.mod)
			if tc.sentinel != nil {
				if !errors.Is(err, tc.sentinel) {
					t.Fatalf("violating input must halt with the named rule; got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("legitimate input must pass; got %v", err)
			}
		})
	}
}
