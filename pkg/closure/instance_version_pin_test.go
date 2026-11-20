// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"context"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The walk asks a lookup that settles instance versions (closure.InstanceVersionSelector)
// for the version each instance's reference resolves at, and the Recipe a mapping
// names is what constrains that version, so the walk resolves every Recipe before
// it asks, and resolves the references the answer covers only after.

// selectingLookup is fakeLookup with an instance-version selector: it records, in
// order, every catalog-entity resolution it is asked for and every time it is asked
// to settle versions, together with what it was handed then, and answers each
// settlement with selections.
type selectingLookup struct {
	*fakeLookup
	selections map[closure.EntityKey]string
	log        []string
	asked      []selectionAsk
}

// selectionAsk is what one settlement was handed.
type selectionAsk struct {
	manifest  *types.Manifest
	instances []closure.InstanceReference
	resolved  []closure.ResolvedRef
}

func (l *selectingLookup) ResolveCatalogEntity(ctx context.Context, uri, kindHint string) (closure.Resolution, error) {
	l.log = append(l.log, "resolve "+closure.EntityKindOf(kindHint)+" "+uri)
	return l.fakeLookup.ResolveCatalogEntity(ctx, uri, kindHint)
}

func (l *selectingLookup) SelectInstanceVersions(_ context.Context, manifest *types.Manifest, instances []closure.InstanceReference, resolved []closure.ResolvedRef) map[closure.EntityKey]string {
	l.log = append(l.log, "select")
	l.asked = append(l.asked, selectionAsk{manifest: manifest, instances: slices.Clone(instances), resolved: slices.Clone(resolved)})
	return l.selections
}

// The quickstart's shape: the Aggregate writes the server with no version, and the
// Manifest maps it at 1.0.0 to the Recipe implementing 1.0.0, while 1.1.0 is the
// highest version a bare request resolves to.
const pinWalkAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: shop
  version: 1.0.0
spec:
  components:
    web:
      type: core/compute/server
`

const pinWalkModuleHost = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: host
  version: 1.0.0
spec:
  aggregates:
    site:
      type: example/site
`

const pinWalkModule = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: example/site
  version: 1.0.0
spec:
  components:
    web:
      type: core/compute/server
`

const pinWalkManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: shop-compose
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        core/compute/server:1.0.0: compose/server:1.0.0
`

const pinWalkRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: shop-local
  environment: development
  version: 1.0.0
spec:
  aggregate: shop
  manifest: shop-compose
`

// pinWalkLookup serves the entities the fixtures above name: the server at 1.1.0
// for a bare request and at 1.0.0 for an exact one, the Recipe, and the module.
func pinWalkLookup() *selectingLookup {
	return &selectingLookup{
		fakeLookup: &fakeLookup{entities: map[string]closure.Resolution{
			"core/compute/server":       resolutionOK("1.1.0", "sha256:server-1.1.0", "catalog"),
			"core/compute/server:1.0.0": resolutionOK("1.0.0", "sha256:server-1.0.0", "catalog"),
			"compose/server:1.0.0":      resolutionOK("1.0.0", "sha256:recipe", "tier-3-pull"),
			"example/site":              resolutionOK("1.0.0", "sha256:site", "catalog"),
			"shop":                      resolutionOK("1.0.0", "sha256:shop", "catalog"),
			"host":                      resolutionOK("1.0.0", "sha256:host", "catalog"),
			"shop-compose":              resolutionOK("1.0.0", "sha256:manifest", "catalog"),
		}},
		selections: map[closure.EntityKey]string{
			{Kind: closure.KindComponent, URI: "core/compute/server"}: "core/compute/server:1.0.0",
		},
	}
}

// moduleLoader serves the one module the host Aggregate includes.
type moduleLoader struct{ t *testing.T }

func (l moduleLoader) LoadAggregate(uri, _, _ string) (*types.Aggregate, error) {
	if uri != "example/site" {
		l.t.Fatalf("the descent loaded %q, which no fixture includes", uri)
	}
	return loadAggregate(l.t, pinWalkModule), nil
}

// assertSelectedBetween fails unless, in log, every Recipe resolution comes before
// the first settlement and the server's resolution comes after it, asked as the
// exact reference the settlement returned and never as written.
func assertSelectedBetween(t *testing.T, log []string) {
	t.Helper()
	selectAt := slices.Index(log, "select")
	if selectAt < 0 {
		t.Fatalf("the walk never asked the lookup to settle instance versions: %q", log)
	}
	recipeAt := slices.Index(log, "resolve "+closure.KindRecipe+" compose/server:1.0.0")
	if recipeAt < 0 || recipeAt > selectAt {
		t.Errorf("the Recipe is resolved at %d and the versions are settled at %d; want the Recipe first: %q", recipeAt, selectAt, log)
	}
	lastSelect := selectAt
	for i, entry := range log {
		if entry == "select" {
			lastSelect = i
		}
	}
	if !slices.Contains(log[lastSelect:], "resolve "+closure.KindComponent+" core/compute/server:1.0.0") {
		t.Errorf("the server is not resolved at the selected version after the versions are settled: %q", log)
	}
	if slices.Contains(log, "resolve "+closure.KindComponent+" core/compute/server") {
		t.Errorf("the walk resolved the server as written, at the version a bare request selects: %q", log)
	}
}

// assertRowAt fails unless the closure records uri, as written, at version.
func assertRowAt(t *testing.T, cl closure.Closure, uri, version string) {
	t.Helper()
	for _, e := range cl.Entities {
		if e.URI == uri && e.Kind() == closure.KindComponent {
			if e.ResolvedVersion != version {
				t.Errorf("the closure records %q at %q, want %q", uri, e.ResolvedVersion, version)
			}
			return
		}
	}
	t.Errorf("the closure records no Component row for %q, as written", uri)
}

// TestClosureResolvesRecipesBeforeConstrainedReferences holds the walk to the
// order the instance-version settlement needs: every Recipe a mapping names is
// resolved before the lookup is asked to settle versions, the settlement is handed
// the instances with the Manifest and the Recipes resolved, and a reference the
// settlement covers is resolved after it, at the reference it returned, and
// recorded under the reference as written.
//
// A module's instances are settled as the composition names them, which is known
// only once every module is walked, so the descent asks after it has walked them
// all and resolves a module's reference only then.
func TestClosureResolvesRecipesBeforeConstrainedReferences(t *testing.T) {
	t.Parallel()
	manifest := loadManifest(t, pinWalkManifest)

	t.Run("the Release's own instances", func(t *testing.T) {
		t.Parallel()
		lookup := pinWalkLookup()
		cl, err := closure.ComputeClosure(context.Background(), loadRelease(t, pinWalkRelease), manifest,
			types.Normalize(loadAggregate(t, pinWalkAggregate)), workspaceconfig.Config{}, lookup)
		if err != nil {
			t.Fatalf("ComputeClosure: %v", err)
		}
		assertSelectedBetween(t, lookup.log)
		if len(lookup.asked) != 1 {
			t.Fatalf("the walk asked for a settlement %d times, want once", len(lookup.asked))
		}
		ask := lookup.asked[0]
		if ask.manifest != manifest {
			t.Error("the settlement was not handed the Manifest the walk was handed")
		}
		if len(ask.instances) != 1 || ask.instances[0].Name != "web" || ask.instances[0].Reference != "core/compute/server" {
			t.Errorf("the settlement was handed the instances %+v, want web of core/compute/server", ask.instances)
		}
		if !slices.ContainsFunc(ask.resolved, func(r closure.ResolvedRef) bool {
			return r.Kind() == closure.KindRecipe && r.URI == "compose/server:1.0.0" && r.ResolvedVersion == "1.0.0"
		}) {
			t.Errorf("the settlement was not handed the Recipe the walk resolved: %+v", ask.resolved)
		}
		assertRowAt(t, cl, "core/compute/server", "1.0.0")
		// The Manifest's own key is a reference no instance writes, and is resolved
		// as written.
		assertRowAt(t, cl, "core/compute/server:1.0.0", "1.0.0")
	})

	t.Run("a module's instances", func(t *testing.T) {
		t.Parallel()
		lookup := pinWalkLookup()
		cl, err := closure.ComputeClosure(context.Background(), loadRelease(t, pinWalkRelease), manifest,
			types.Normalize(loadAggregate(t, pinWalkModuleHost)), workspaceconfig.Config{}, lookup)
		if err != nil {
			t.Fatalf("ComputeClosure: %v", err)
		}
		if err := closure.DescendSubAggregates(context.Background(), &cl, moduleLoader{t: t}, lookup); err != nil {
			t.Fatalf("DescendSubAggregates: %v", err)
		}
		assertSelectedBetween(t, lookup.log)
		last := lookup.asked[len(lookup.asked)-1]
		if !slices.ContainsFunc(last.instances, func(in closure.InstanceReference) bool {
			return in.Name == "site.web" && in.Reference == "core/compute/server"
		}) {
			t.Errorf("the descent's settlement was not handed the module's instance under the name the composition gives it: %+v", last.instances)
		}
		if last.manifest != manifest {
			t.Error("the descent's settlement was not handed the Manifest the walk was handed")
		}
		assertRowAt(t, cl, "core/compute/server", "1.0.0")
	})
}
