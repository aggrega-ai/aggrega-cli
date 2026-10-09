// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// errPassRefused stands in for whatever a pass's own source refuses with, so the
// test can tell that refusal apart from anything the descent adds around it.
var errPassRefused = errors.New("the source this pass reads refused")

// refusingAggregateLoader refuses every Aggregate it is asked for.
type refusingAggregateLoader struct{}

func (refusingAggregateLoader) LoadAggregate(string, string, string) (*types.Aggregate, error) {
	return nil, errPassRefused
}

// refusingBodiesLoader refuses every Recipe and every Plugin entity it is asked for.
type refusingBodiesLoader struct{}

func (refusingBodiesLoader) LoadRecipe(string) (*types.Recipe, error) { return nil, errPassRefused }

func (refusingBodiesLoader) LoadFeatureRecipe(string) (*types.FeatureRecipe, error) {
	return nil, errPassRefused
}

func (refusingBodiesLoader) LoadPlugin(string) (*types.Plugin, error) { return nil, errPassRefused }

// TestARefusalFromThePinnedAxesSaysWhatWasBeingResolved makes each pass the pinned
// axes run refuse in turn, through the one orchestration a pull and a build share,
// and holds the refusal to three facts: the condition the pass's source raised still
// matches through every wrapper, so nothing a caller tests for is lost; the refusal
// opens with what was being resolved, in the terms a reader of the Release knows —
// sub-aggregates, plugins, a plugin's .wasm; and it carries none of the code's own
// labels for its passes. Each row's closure is shaped so that only its own pass can
// refuse, and the passes before it complete.
func TestARefusalFromThePinnedAxesSaysWhatWasBeingResolved(t *testing.T) {
	const (
		subAggregate = "acme/inner:1.0.0"
		recipe       = "helm/svc:1.0.0"
		plugin       = "pipeline-step/gateway-step:1.0.0"
	)
	registryServed := map[string]closure.Resolution{
		plugin: {ResolvedVersion: "1.0.0", Digest: "sha256:entity", ResolutionPath: closure.ResolutionPathRegistry},
	}
	cases := []struct {
		name       string
		entities   []closure.ResolvedRef
		aggLoader  closure.AggregateLoader
		loader     closure.RecipeLoader
		refold     func() (closure.RecipeLoader, error)
		wantPrefix string
		wantWithin string
	}{
		{
			name:       "the sub-aggregates an Aggregate imports",
			entities:   []closure.ResolvedRef{{URI: subAggregate, ResolvedVersion: "1.0.0", KindHint: "aggregate"}},
			aggLoader:  refusingAggregateLoader{},
			wantPrefix: "resolving the sub-aggregates the Aggregate imports: ",
		},
		{
			name:       "the plugins a Recipe names",
			entities:   []closure.ResolvedRef{{URI: recipe, ResolvedVersion: "1.0.0", KindHint: "recipe"}},
			loader:     refusingBodiesLoader{},
			wantPrefix: "resolving the plugins the Release depends on: ",
			wantWithin: `load recipe "` + recipe + `" to read the plugins it names`,
		},
		{
			name:       "reading the plugins just resolved",
			loader:     refusingBodiesLoader{},
			refold:     func() (closure.RecipeLoader, error) { return nil, errPassRefused },
			wantPrefix: "reading the plugins just resolved: ",
		},
		{
			name:       "the .wasm of a plugin",
			entities:   []closure.ResolvedRef{{URI: plugin, ResolvedVersion: "1.0.0", ResolutionPath: closure.ResolutionPathRegistry, KindHint: "plugin"}},
			loader:     refusingBodiesLoader{},
			wantPrefix: "resolving the .wasm of each plugin the Release depends on: ",
			wantWithin: `load wasm plugin entity "` + plugin + `" to read its .wasm`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cl := closure.Closure{Entities: append([]closure.ResolvedRef(nil), tc.entities...)}
			lookup := &fakeLookup{entities: registryServed}

			err := closure.DescendPinnedAxes(context.Background(), &cl, tc.aggLoader, tc.loader, lookup, nil, nil, tc.refold, nil, nil)

			if !errors.Is(err, errPassRefused) {
				t.Fatalf("the refusal no longer matches the condition its pass's source raised: %v", err)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, tc.wantPrefix) {
				t.Errorf("the refusal does not open with what was being resolved\nwant prefix: %q\ngot: %q", tc.wantPrefix, msg)
			}
			if tc.wantWithin != "" && !strings.Contains(msg, tc.wantWithin) {
				t.Errorf("the refusal does not say %q: %q", tc.wantWithin, msg)
			}
			for _, label := range []string{"descent", "re-fold", "pipeline-plugin"} {
				if strings.Contains(msg, label) {
					t.Errorf("the refusal carries the code's label %q for a pass, which no reader authored or was taught: %q", label, msg)
				}
			}
		})
	}
}
