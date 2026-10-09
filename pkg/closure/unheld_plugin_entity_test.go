// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// errNoSourceHoldsIt stands in for the refusal the production loader composes for a
// Plugin entity it cannot load, so the test can tell that refusal from anything else
// the descent might raise.
var errNoSourceHoldsIt = errors.New("no source holds this plugin entity")

// refusingPluginLoader is the descent fixture's loader with a LoadPlugin that
// refuses an entity it does not hold with errNoSourceHoldsIt, recording every entity
// it is asked about.
type refusingPluginLoader struct {
	*fakeRecipeLoader
	asked []string
}

func (l *refusingPluginLoader) LoadPlugin(uri string) (*types.Plugin, error) {
	l.asked = append(l.asked, uri)
	if wp, ok := l.plugins[uri]; ok {
		return wp, nil
	}
	return nil, fmt.Errorf("%w: %q", errNoSourceHoldsIt, uri)
}

// TestTheEntityDescentRefusesAPluginNoSourceHoldsWhenAPullAsks drives the entity
// descent over a Recipe whose pipeline step names a Plugin the lookup resolves
// through no tier, and holds it to four facts.
//
// A pull — the descent handed a question about catalog-authored artifacts — asks
// the loader about such a Plugin and, where the loader cannot load it either,
// refuses with the loader's own refusal, naming the Recipe that names it, and pins
// nothing. Where the loader does hold it — a Plugin only the binary carries, which
// the lookup sends to the registry tiers while the build reads it from the binary —
// the pull completes and pins nothing, as before. A build — the descent handed no
// question — asks the loader nothing and refuses nothing, because its own load of
// the step raises the condition. And a Plugin a registry serves is pinned without
// the loader being asked at this step, which is the positive control: a descent that
// refused or skipped every Plugin fails it.
func TestTheEntityDescentRefusesAPluginNoSourceHoldsWhenAPullAsks(t *testing.T) {
	const (
		unheld        = "pipeline-step/nowhere:1.0.0"
		binaryCarried = "governance/shipped:1.0.0"
	)
	cases := []struct {
		name        string
		ref         string
		loaderHolds bool
		pull        bool
		wantRefused bool
		wantAsked   []string
		wantPinned  bool
	}{
		{name: "a pull, a Plugin no source holds", ref: unheld, pull: true, wantRefused: true, wantAsked: []string{unheld}},
		{name: "a pull, a Plugin only the binary carries", ref: binaryCarried, loaderHolds: true, pull: true, wantAsked: []string{binaryCarried}},
		{name: "a build, a Plugin no source holds", ref: unheld},
		{name: "a pull, a Plugin a registry serves", ref: registryPluginEntity, loaderHolds: true, pull: true, wantPinned: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plugins := map[string]*types.Plugin{}
			if tc.loaderHolds {
				name, pluginType := "shipped", "governance"
				if tc.ref == registryPluginEntity {
					name, pluginType = registryPluginName, "pipeline-step"
				}
				plugins[tc.ref] = pluginEntity(t, name, pluginType)
			}
			loader := &refusingPluginLoader{fakeRecipeLoader: &fakeRecipeLoader{
				recipes: map[string]*types.Recipe{recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, tc.ref)},
				plugins: plugins,
			}}
			lookup := pluginDescentLookup()
			if _, served := lookup.entities[tc.ref]; served != (tc.ref == registryPluginEntity) {
				t.Fatalf("fixture: the lookup serves %q = %v; only the registry row's Plugin is served by it", tc.ref, served)
			}
			rel := loadRelease(t, providerDescentRelease)
			man := loadManifest(t, providerDescentManifest)
			cl, err := closure.ComputeClosure(context.Background(), rel, man, types.NormalizedAggregate{}, workspaceconfig.Config{}, lookup)
			if err != nil {
				t.Fatalf("ComputeClosure: %v", err)
			}

			var local closure.LocalPluginArtifact
			if tc.pull {
				local = func(entityURI, _ string) (closure.Resolution, error) {
					t.Errorf("the question about catalog-authored artifacts was asked about %q; no entity here is authored in a catalog directory", entityURI)
					return closure.Resolution{}, nil
				}
			}
			err = closure.DescendRecipePluginEntities(context.Background(), &cl, loader, lookup, nil, local)

			if tc.wantRefused {
				if !errors.Is(err, errNoSourceHoldsIt) {
					t.Fatalf("the descent must refuse with the loader's own refusal; got %v", err)
				}
				if !strings.Contains(err.Error(), recipeURI) {
					t.Errorf("the refusal does not name the Recipe %s whose pipeline names the Plugin: %v", recipeURI, err)
				}
			} else if err != nil {
				t.Fatalf("descent: %v", err)
			}
			if !reflect.DeepEqual(loader.asked, tc.wantAsked) {
				t.Errorf("the loader was asked about %v; want %v", loader.asked, tc.wantAsked)
			}
			if _, pinned := entityByURI(cl, tc.ref); pinned != tc.wantPinned {
				t.Errorf("the Plugin entity %q pinned = %v; want %v (entities=%+v)", tc.ref, pinned, tc.wantPinned, cl.Entities)
			}
		})
	}
}
