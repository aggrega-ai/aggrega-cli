// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validateops

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestInputKindCapabilityErrors_RaisesTheRenderGatesCondition drives the early
// input-kind detector over a Recipe and a FeatureRecipe that each declare a
// workload for a provider claiming only sourced input, and pins that each finding
// is an occurrence of the condition the render-time gate raises for the same
// fault — matchable with errors.Is, reporting that condition's codes — while its
// text stays exactly what the detector composed.
//
// The codes are read off the condition's own accessor rather than written out, so
// a renumbered condition cannot leave this asserting a stale code. The text is
// read both whole and through the one-line flattening whole-catalog validate
// prints a finding with, because that flattening is where a condition carried
// inside the text would show.
func TestInputKindCapabilityErrors_RaisesTheRenderGatesCondition(t *testing.T) {
	t.Parallel()
	condition := renderer.ErrUnsupportedInputKind()
	wantCode, ok := cerrors.CodeOf(condition)
	if !ok || wantCode == "" {
		t.Fatal("the input-kind condition reports no code")
	}
	wantShort, ok := cerrors.ShortCodeOf(condition)
	if !ok || wantShort == "" {
		t.Fatal("the input-kind condition reports no short code")
	}

	providerPlugin := func(ref string) (types.Plugin, bool, error) {
		if ref != "sourced-only-cap" {
			return types.Plugin{}, false, nil
		}
		var p types.Plugin
		p.Spec.Provider.Contract.InputKinds.Sourced = true
		return p, true, nil
	}
	workload := types.WorkloadTemplate{Inline: "kind: Workload\n"}

	for _, row := range []struct {
		name           string
		recipes        []types.Recipe
		featureRecipes []types.FeatureRecipe
		want           string
	}{
		{
			name: "a Recipe declaring a workload",
			recipes: []types.Recipe{func() types.Recipe {
				var r types.Recipe
				r.Metadata.Name, r.Metadata.Version = "api-sourceonly", "1.0.0"
				r.Spec.Capability, r.Spec.Workload = "sourced-only-cap", workload
				return r
			}()},
			want: `Recipe "api-sourceonly:1.0.0": provider "sourced-only-cap" does not support synthesized ` +
				`recipes (this provider supports: sourced); declare one of the supported input kinds, or ` +
				`target a provider that supports synthesized`,
		},
		{
			name: "a FeatureRecipe declaring a workload",
			featureRecipes: []types.FeatureRecipe{func() types.FeatureRecipe {
				var fr types.FeatureRecipe
				fr.Metadata.Name, fr.Metadata.Version = "sidecar-sourceonly", "1.0.0"
				fr.Spec.Capability, fr.Spec.Workload = "sourced-only-cap", workload
				return fr
			}()},
			want: `FeatureRecipe "sidecar-sourceonly:1.0.0": provider "sourced-only-cap" does not support ` +
				`synthesized recipes (this provider supports: sourced); declare one of the supported input ` +
				`kinds, or target a provider that supports synthesized`,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			errs := InputKindCapabilityErrors(providerPlugin, row.recipes, row.featureRecipes)
			if len(errs) != 1 {
				t.Fatalf("findings = %v, want exactly one", errs)
			}
			err := errs[0]
			if !errors.Is(err, renderer.ErrUnsupportedInputKind()) {
				t.Errorf("errors.Is against the input-kind condition = false, want true; got %v", err)
			}
			if got, ok := cerrors.CodeOf(err); !ok || got != wantCode {
				t.Errorf("CodeOf = %q (found=%v), want %q", got, ok, wantCode)
			}
			if got, ok := cerrors.ShortCodeOf(err); !ok || got != wantShort {
				t.Errorf("ShortCodeOf = %q (found=%v), want %q", got, ok, wantShort)
			}
			if got := err.Error(); got != row.want {
				t.Errorf("finding text\n got: %s\nwant: %s", got, row.want)
			}
			if got := cerrors.CauseChain(err); got != row.want {
				t.Errorf("flattened finding\n got: %s\nwant: %s", got, row.want)
			}
		})
	}
}
