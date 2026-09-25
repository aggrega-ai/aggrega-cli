// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// errNoArtifactHere stands in for the refusal a pull's check returns, so the test
// can tell the check's own error from anything else the descent might raise.
var errNoArtifactHere = errors.New("no artifact for this entity")

// checkCall records one question the descent asked.
type checkCall struct {
	entityURI   string
	artifactRef string
}

// TestLocalPluginArtifactCheckIsAskedAboutCatalogEntitiesOnly drives the entity
// descent with a recording check and holds it to three facts: the check is asked
// about a Plugin entity authored in a local catalog directory, with the artifact
// reference the entity derives; a refusal from it halts the descent with that very
// refusal; and it is never asked about an entity a registry served, whose artifact
// the pull pins and caches instead. A nil check is the reconstruction's posture and
// is covered by every other descent test in this package, which pass nil.
func TestLocalPluginArtifactCheckIsAskedAboutCatalogEntitiesOnly(t *testing.T) {
	cases := []struct {
		name       string
		entityURI  string
		entity     *types.Plugin
		refuse     bool
		wantCalls  []checkCall
		wantHalted bool
	}{
		{
			name:      "a catalog-local entity whose artifact the check finds",
			entityURI: localPluginEntity,
			entity:    pluginEntity(t, localPluginName, "pipeline-step"),
			wantCalls: []checkCall{{localPluginEntity, localPluginArtifact}},
		},
		{
			name:       "a catalog-local entity whose artifact the check refuses",
			entityURI:  localPluginEntity,
			entity:     pluginEntity(t, localPluginName, "pipeline-step"),
			refuse:     true,
			wantCalls:  []checkCall{{localPluginEntity, localPluginArtifact}},
			wantHalted: true,
		},
		{
			name:      "a registry-resolved entity",
			entityURI: registryPluginEntity,
			entity:    pluginEntity(t, registryPluginName, "pipeline-step"),
			refuse:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loader := &fakeRecipeLoader{
				recipes: map[string]*types.Recipe{
					recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, tc.entityURI),
				},
				plugins: map[string]*types.Plugin{tc.entityURI: tc.entity},
			}
			lookup := pluginDescentLookup()
			rel := loadRelease(t, providerDescentRelease)
			man := loadManifest(t, providerDescentManifest)
			cl, err := closure.ComputeClosure(context.Background(), rel, man, types.NormalizedAggregate{}, workspaceconfig.Config{}, lookup)
			if err != nil {
				t.Fatalf("ComputeClosure: %v", err)
			}

			var calls []checkCall
			check := func(entityURI, artifactRef string) error {
				calls = append(calls, checkCall{entityURI, artifactRef})
				if tc.refuse {
					return errNoArtifactHere
				}
				return nil
			}
			err = closure.DescendRecipePluginEntities(context.Background(), &cl, loader, lookup, nil, check)

			if tc.wantHalted {
				if !errors.Is(err, errNoArtifactHere) {
					t.Fatalf("the descent must halt with the check's own refusal; got %v", err)
				}
			} else if err != nil {
				t.Fatalf("descent: %v", err)
			}
			if len(calls) != len(tc.wantCalls) {
				t.Fatalf("the check was asked %v; want %v", calls, tc.wantCalls)
			}
			for i := range calls {
				if calls[i] != tc.wantCalls[i] {
					t.Errorf("question %d = %+v; want %+v", i, calls[i], tc.wantCalls[i])
				}
			}
			if _, pinned := entityByURI(cl, localPluginEntity); pinned {
				t.Errorf("a catalog-local plugin entity was pinned; the registry-only rule leaves it out")
			}
		})
	}
}
