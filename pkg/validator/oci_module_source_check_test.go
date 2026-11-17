// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/documentview"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const ociModuleDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// ociModuleSourceRows are the authored sources the OCI module source rule is held
// to, one fault per row, with sources that name one artifact as the controls.
var ociModuleSourceRows = []struct {
	name   string
	source map[string]any
	// advises is the text the one objection's advice carries; empty for a control.
	advises string
}{
	{"bicep at a tag (control)", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "oci://mcr.microsoft.com/bicep/avm/res/app/container-app", "version": "0.23.0"}, ""},
	{"terraform with a module_subdir (control)", map[string]any{"kind": sourcekind.TerraformModuleOCI, "url": "oci://localhost:5000/modules/net", "version": "v1.2.0", "module_subdir": "modules/vpc"}, ""},
	{"a digest version (control)", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "oci://r/m", "version": ociModuleDigest}, ""},
	{"a templated version (control)", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "oci://r/m", "version": `{{ optional .infra "source_version" "0.23.0" }}`}, ""},
	{"a local url with chart written (control)", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "./module", "chart": "x"}, ""},
	{"a helm chart with chart written (control)", map[string]any{"kind": sourcekind.HelmOCIChart, "url": "oci://r/c", "version": "1.0.0", "chart": "c"}, ""},
	{"a tag in the url", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "oci://r/m:1.0.0", "version": "1.0.0"}, "oci://<registry>/<repository>"},
	{"a digest in the url", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "oci://r/m@" + ociModuleDigest, "version": "1.0.0"}, "oci://<registry>/<repository>"},
	{"a query in the url", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "oci://r/m?tag=v1", "version": "1.0.0"}, "carries a query"},
	{"a double slash in the url", map[string]any{"kind": sourcekind.TerraformModuleOCI, "url": "oci://r/m//sub", "version": "1.0.0"}, "oci://<registry>/<repository>"},
	{"another scheme in the url", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "https://r/m", "version": "1.0.0"}, "is not an oci:// reference"},
	{"no repository in the url", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "oci://r", "version": "1.0.0"}, "names no repository"},
	{"an uppercase repository", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "oci://r/Modules/x", "version": "1.0.0"}, "oci://<registry>/<repository>"},
	{"no version", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "oci://r/m"}, "version: <tag or sha256 manifest digest>"},
	{"an empty version", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "oci://r/m", "version": ""}, "version: <tag or sha256 manifest digest>"},
	{"a version that is not a tag", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "oci://r/m", "version": "a/b"}, `"a/b"`},
	{"chart written", map[string]any{"kind": sourcekind.TerraformModuleOCI, "url": "oci://r/m", "version": "1.0.0", "chart": "c"}, "chart"},
	{"ref written", map[string]any{"kind": sourcekind.TerraformModuleOCI, "url": "oci://r/m", "version": "1.0.0", "ref": "main"}, "ref"},
	{"module_subdir on bicep", map[string]any{"kind": sourcekind.BicepModuleOCI, "url": "oci://r/m", "version": "1.0.0", "module_subdir": "x"}, "module_subdir"},
}

func assertOCIModuleSourceOutcome(t *testing.T, err error, advises string) {
	t.Helper()
	if advises == "" {
		if errors.Is(err, sourcekind.ErrOCIModuleSourceMalformed()) {
			t.Fatalf("a source naming one artifact was refused: %v", err)
		}
		return
	}
	if !errors.Is(err, sourcekind.ErrOCIModuleSourceMalformed()) {
		t.Fatalf("want the OCI module source condition; got %v", err)
	}
	if !strings.Contains(err.Error(), advises) {
		t.Errorf("the advice does not carry %q: %v", advises, err)
	}
}

// TestValidateRecipe_RefusesAMalformedOCIModuleSource drives the rule through the
// Recipe's load-time entry.
func TestValidateRecipe_RefusesAMalformedOCIModuleSource(t *testing.T) {
	t.Parallel()
	for _, tc := range ociModuleSourceRows {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &types.Recipe{}
			r.Metadata.Name = "svc"
			r.Metadata.Version = "1.0.0"
			r.Spec.Capability = "bicep"
			r.Spec.Implements = "test/svc"
			r.Spec.Source = tc.source
			assertOCIModuleSourceOutcome(t, ValidateRecipe(r, "recipe.yaml"), tc.advises)
		})
	}
}

// TestValidateFeatureRecipe_RefusesAMalformedOCIModuleSource is the FeatureRecipe's
// leg of the same rule.
func TestValidateFeatureRecipe_RefusesAMalformedOCIModuleSource(t *testing.T) {
	t.Parallel()
	for _, tc := range ociModuleSourceRows {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := &types.FeatureRecipe{}
			fr.Metadata.Name = "cred"
			fr.Metadata.Version = "1.0.0"
			fr.Spec.Capability = "bicep"
			fr.Spec.Implements = "test/cred"
			fr.Spec.Source = tc.source
			assertOCIModuleSourceOutcome(t, ValidateFeatureRecipe(fr, "featurerecipe.yaml"), tc.advises)
		})
	}
}

// TestMalformedOCIModuleSource_RaisesOneObjectionPerFaultInOrder runs the checks
// over a source with every fault and reads the objections in the order they are
// raised: url, version, chart, ref, module_subdir.
func TestMalformedOCIModuleSource_RaisesOneObjectionPerFaultInOrder(t *testing.T) {
	t.Parallel()
	source := map[string]any{
		"kind": sourcekind.BicepModuleOCI, "url": "oci://r/m:1.0.0",
		"chart": "c", "ref": "main", "module_subdir": "x",
	}
	run := func(t *testing.T, objections []constraint.Objection) {
		t.Helper()
		var keys []string
		for _, o := range objections {
			if !errors.Is(o.Sentinel(), sourcekind.ErrOCIModuleSourceMalformed()) {
				t.Fatalf("an objection carries %v", o.Sentinel())
			}
			// Each advice opens with the key it is about.
			for _, key := range []string{"url:", "version:", "chart:", "ref:", "module_subdir:"} {
				opens := "the source writes " + key
				if key == "version:" {
					opens = "the source writes no version"
				}
				if strings.HasPrefix(o.Advice(), opens) {
					keys = append(keys, key)
					break
				}
			}
		}
		want := []string{"url:", "version:", "chart:", "ref:", "module_subdir:"}
		if !slices.Equal(keys, want) {
			t.Errorf("advice names %v in order; want %v", keys, want)
		}
	}
	t.Run("Recipe", func(t *testing.T) {
		r := &types.Recipe{}
		r.Spec.Source = source
		var presented constraint.DocumentView = documentview.NewRecipe("recipe.yaml", r)
		run(t, constraint.RunOnDocument("recipe.yaml", presented, recipeOCIModuleSourceNamesOneArtifact))
	})
	t.Run("FeatureRecipe", func(t *testing.T) {
		fr := &types.FeatureRecipe{}
		fr.Spec.Source = source
		var presented constraint.DocumentView = documentview.NewFeatureRecipe("featurerecipe.yaml", fr)
		run(t, constraint.RunOnDocument("featurerecipe.yaml", presented, featureRecipeOCIModuleSourceNamesOneArtifact))
	})
}
