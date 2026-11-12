// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
	const svcYAML = `apiVersion: aggrega.ai/v1beta1
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
	const dbYAML = `apiVersion: aggrega.ai/v1beta1
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
// Release, and an optional sub-aggregate document reachable as module "mod",
// and reports only whether it halted.
func buildWith(t *testing.T, aggYAML, relYAML, modYAML string) error {
	t.Helper()
	_, err := buildResultWith(t, aggYAML, relYAML, modYAML)
	return err
}

// buildResultWith is buildWith for a caller that needs what the build produced —
// a rule declared over this stage is invoked against the presentation and the
// enumeration the result carries, so a proof of such a rule needs the result and
// not just the verdict.
//
// The two share one body rather than one calling Build itself, so a fixture
// cannot be built one way for one proof and another way for the next.
func buildResultWith(t *testing.T, aggYAML, relYAML, modYAML string) (*TopologyResult, error) {
	t.Helper()
	return buildResultWithCatalog(t, bindingCatalog(t), aggYAML, relYAML, modYAML)
}

// buildResultWithCatalog is buildResultWith over a catalog the caller supplies,
// for a proof whose components need sockets the binding catalog does not
// declare. It is the body buildWith and buildResultWith share.
func buildResultWithCatalog(t *testing.T, catalog resolver.CatalogLookup, aggYAML, relYAML, modYAML string) (*TopologyResult, error) {
	t.Helper()
	agg := mustAggregate(t, aggYAML)
	var rel *types.Release
	var boundaryInfra map[string]*resolver.BoundaryInfra
	var routedInfra map[string]*resolver.BoundaryInfra
	if relYAML != "" {
		// Fold a root-level components surface into the synthesized `default`
		// boundary before anything reads the document, which is what loading a
		// Release does at birth, so every consumer here sees the same
		// boundary-bearing shape one gets in a real build. The fold is shared
		// rather than repeated per reader: handing the raw document to the build
		// while the wiring call saw the folded one would make a fixture authored
		// at root level look like a Release declaring no entries at all. A
		// fixture that already declares boundaries is returned untouched.
		normalized := types.NormalizeRelease(mustRelease(t, relYAML))
		rel = normalized.Release()
		// The Release edge pass reads each node's resolved boundary path, which
		// production supplies from the same wiring call. Omitting it would make
		// every fixture look like a Release whose entries reach no node.
		//
		// The routed-module contexts are taken as well as the per-component ones,
		// because a boundary places a component either way: a fixture routing a
		// whole module places nothing without them, so a rule reading placement
		// would judge every routed fixture as if the Release had been silent.
		boundaryInfra, _, routedInfra = resolver.WireBoundaryContexts(normalized)
	}
	var loader SubAggregateLoader
	if modYAML != "" {
		mod := mustAggregate(t, modYAML)
		loader = func(string, map[string]any) (*types.Aggregate, string, map[string]any, error) {
			return mod, "mod.yaml", nil, nil
		}
	}
	var releasePath string
	if rel != nil {
		// A real build carries the file each document was read from, and a rule
		// declared over this stage positions its findings with it. A fixture
		// leaving it empty would prove such a rule's locator and never its file,
		// so the fixture names one — the same name for every fixture, because
		// which name it is carries nothing.
		releasePath = "release.yaml"
	}
	return Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:            agg,
			Release:              rel,
			ReleasePath:          releasePath,
			BoundaryInfra:        boundaryInfra,
			RoutedAggregateInfra: routedInfra,
		},
		CatalogLookup:    catalog,
		LoadSubAggregate: loader,
	})
}

// flatAggregate is a two-component Aggregate whose consumer binds its required
// socket, so a fixture built on it halts only for the reason it is testing.
const flatAggregate = `apiVersion: aggrega.ai/v1beta1
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
//
// Both authoring positions are covered, because they are two documents rather
// than one: a Release may name its components under a boundary it declares, or
// at the root of the spec, and the second is folded into a synthesized boundary
// before the graph is built. A rule proven only on the first position would be
// proven only for the shape that needs no folding.
func TestReleaseEdgeConfigMustReachAnEdge(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		release  string
		wantHalt bool
	}{
		{
			name: "configures the edge the developer declared",
			release: `apiVersion: aggrega.ai/v1beta1
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
        db: {}
`,
			wantHalt: false,
		},
		{
			name: "configures an alias the developer never declared",
			release: `apiVersion: aggrega.ai/v1beta1
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
        db: {}
`,
			wantHalt: true,
		},
		{
			name: "names a component the Aggregate does not declare",
			release: `apiVersion: aggrega.ai/v1beta1
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
        api: {}
        db: {}
`,
			wantHalt: true,
		},
		{
			name: "wires the edge it configures, by naming a target",
			release: `apiVersion: aggrega.ai/v1beta1
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
        db: {}
`,
			wantHalt: false,
		},
		{
			name: "configures an alias the developer never declared, named at the root of the spec",
			release: `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  components:
    api:
      uses:
        databases:
          spare:
            infra:
              storage_class: fast
    db: {}
`,
			wantHalt: true,
		},
		{
			name: "configures the edge the developer declared, named at the root of the spec",
			release: `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  components:
    api:
      uses:
        databases:
          main:
            infra:
              storage_class: fast
    db: {}
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
	const aggYAML = `apiVersion: aggrega.ai/v1beta1
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
	const relYAML = `apiVersion: aggrega.ai/v1beta1
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
        db: {}
`
	if err := buildWith(t, aggYAML, relYAML, ""); err != nil {
		t.Fatalf("an entry on a switched-off component must be accepted; got %v", err)
	}
}

// TestReleaseEdgeConfig_TargetThatNamesNothingIsNotUnboundConfiguration proves an
// entry naming a target no component answers to is not reported as unbound
// configuration. An entry with a target draws its edge, so it is never unbound —
// which is what keeps the entry in front of the rule that has the informative
// sentence for it, the one over the finalised graph about a connection reaching
// nothing.
//
// It asserts the absence rather than the refusal because the two rules are asked
// at different stages now: this one here, the other where the graph exists. The
// refusal is proved there, end to end, over exactly this authoring shape.
//
// Every component is placed in the boundary deliberately. The fixture's whole
// point is one fault, and an unplaced component is a second one the composition
// reports on its own — which would let this pass on a refusal that has nothing to
// do with the target.
func TestReleaseEdgeConfig_TargetThatNamesNothingIsNotUnboundConfiguration(t *testing.T) {
	t.Parallel()
	const relYAML = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        db: {}
        api:
          uses:
            databases:
              spare:
                target: nowhere
                infra:
                  storage_class: fast
`
	result, err := buildResultWith(t, flatAggregate, relYAML, "")
	if err != nil {
		t.Fatalf("the composition must reach the graph for the rule over it to be asked; got %v", err)
	}
	if errors.Is(err, ErrUnboundReleaseEdgeConfig()) {
		t.Fatalf("a missing target must not be reported as unbound configuration; got %v", err)
	}
	spare := -1
	for i, e := range result.Edges {
		if e.Source == "api" && e.Socket == "databases" && e.Alias == "spare" {
			spare = i
		}
	}
	if spare < 0 {
		t.Fatalf("the entry must draw its edge, or it would be unbound configuration after all; edges = %+v", result.Edges)
	}
	if got := result.Edges[spare].Target; got != "nowhere" {
		t.Errorf("target = %q, want %q", got, "nowhere")
	}
}

// TestReleaseEdgeConfig_UndeclaredSocketKeepsItsOwnDiagnostic proves the new
// halt does not take over a case that already had a better diagnostic. A socket
// the consuming component does not declare is the undeclared-socket rule's, and
// stays so even when the entry also carries configuration reaching no edge —
// otherwise a rule added for one class would silently disable another.
func TestReleaseEdgeConfig_UndeclaredSocketKeepsItsOwnDiagnostic(t *testing.T) {
	t.Parallel()
	const modYAML = `apiVersion: aggrega.ai/v1beta1
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
	const aggYAML = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    mod:
      type: ./mod.yaml
`
	const relYAML = `apiVersion: aggrega.ai/v1beta1
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
	const aggYAML = `apiVersion: aggrega.ai/v1beta1
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
	const modBoundSocket = `apiVersion: aggrega.ai/v1beta1
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
	const modUnboundSocket = `apiVersion: aggrega.ai/v1beta1
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
	const modEmptyCacheEdge = `apiVersion: aggrega.ai/v1beta1
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
	const modFilledCacheEdge = `apiVersion: aggrega.ai/v1beta1
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
	// The boundary names both of the module's components, not only the one the
	// edge entry configures. Naming the consumer alone would leave the store
	// placed by nothing, so every row using this Release would carry a placement
	// fault on top of the socket fault it is about — and the positive rows would
	// halt for a reason they are not testing.
	releaseOnSocket := func(socket string) string {
		return `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        mod.store: {}
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
		// The required-socket rule is not in this table: it is declared over the
		// composition and invoked by the pass that reads this stage's
		// presentation, so a build alone does not report it. Both directions of
		// it — a module leaving the socket unbound and a module binding it — are
		// proved beside the declaration instead.
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
