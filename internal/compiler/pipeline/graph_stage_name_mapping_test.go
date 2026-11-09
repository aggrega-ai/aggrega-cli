// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The proofs in this file cover the rules over the Manifest's name-tier mapping
// entries as the compilation asks them: once the graph is final, over the
// Manifest the build was compiled with. They compile the fixture workspace for
// real, varying only the Manifest's name tier, so what is reported is a property
// of the compiled documents.
//
// The fixture carries a component a sub-aggregate contributes, which is the case a
// rule over the Aggregate's own instance list could not see: its identity is the
// qualified one the composition gives it, and a components.name key is matched
// against that identity. And it carries an attachment the Release writes on a
// component, so a features.name key written as that component's name is the slip
// the features rule exists for.

// nameTierManifest is the fixture's committed Manifest with the given name-tier
// entries written under each tier.
func nameTierManifest(componentNames, featureNames []string) string {
	var b strings.Builder
	b.WriteString(`apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: prod-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
`)
	for _, entry := range mapEvery() {
		b.WriteString("        " + entry + "\n")
	}
	if len(componentNames) > 0 {
		b.WriteString("      name:\n")
		for _, entry := range componentNames {
			b.WriteString("        " + entry + "\n")
		}
	}
	b.WriteString("    features:\n      type:\n        core/k8s-dns-resolver: k8s-dns-patcher:1.0.0\n")
	if len(featureNames) > 0 {
		b.WriteString("      name:\n")
		for _, entry := range featureNames {
			b.WriteString("        " + entry + "\n")
		}
	}
	return b.String()
}

// TestANameMappingKeyThatNamesNothingRefusesAnEmittingRunAndIsCarriedByADescribingOne
// drives each section's slip through both postures, against a control whose keys
// name what the composition holds.
//
// Both halves are asserted for every row because the severity is the property
// under proof: a rule that refused everywhere, and one nobody reported, would each
// pass a proof driving one posture alone.
func TestANameMappingKeyThatNamesNothingRefusesAnEmittingRunAndIsCarriedByADescribingOne(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name           string
		componentNames []string
		featureNames   []string
		wantLocator    string
		want           []string
	}{
		{
			name:         "a features.name key written as the component the attachment sits on",
			featureNames: []string{"frontend: k8s-dns-patcher:1.0.0"},
			wantLocator:  ".spec.mappings.features.name.frontend",
			want: []string{
				"Manifest prod-manifest",
				`"frontend"`,
				`attachments of that type in this build "cross-namespace-routing"`,
				`did you mean "cross-namespace-routing"`,
			},
		},
		{
			name:           "a components.name key one edit from a component a sub-aggregate contributes",
			componentNames: []string{"data.main-dbb: " + fixturePostgresRecipe},
			// A key carrying the locator's own separator cannot be spelled as a
			// segment of it, so the finding is positioned at the tier the key is
			// written in and the key itself travels in the finding's values.
			wantLocator: ".spec.mappings.components.name",
			want: []string{
				"Manifest prod-manifest",
				`"data.main-dbb"`,
				`did you mean "` + fixtureModuleNodeID + `"`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			manifest := nameTierManifest(tc.componentNames, tc.featureNames)

			_, err := compileWithManifest(t, fixtureAggregate(t), manifest)
			if err == nil {
				t.Fatal("a run that emits compiled a Manifest whose name-tier key names nothing")
			}
			if !errors.Is(err, catalog.ErrNameMappingMatchedNothing()) {
				t.Fatalf("the halt does not carry the name-mapping-matched-nothing condition: %v", err)
			}
			reported := violationsCarrying(t, err, catalog.ErrNameMappingMatchedNothing())
			if len(reported) != 1 {
				t.Fatalf("findings carrying this condition = %d, want 1: %+v", len(reported), reported)
			}
			assertNameMappingFinding(t, reported[0], tc.wantLocator, tc.want)

			described, err := describeWithManifest(t, fixtureAggregate(t), manifest)
			if err != nil {
				t.Fatalf("a run that only describes the composition refused it: %v", err)
			}
			var carried []*resolver.ResolverError
			for _, diagnostic := range described.ResolveDiagnostics {
				if errors.Is(diagnostic.Err, catalog.ErrNameMappingMatchedNothing()) {
					carried = append(carried, diagnostic)
				}
			}
			if len(carried) != 1 {
				t.Fatalf("diagnostics carrying this condition = %d, want 1: %+v", len(carried), carried)
			}
			assertNameMappingFinding(t, carried[0], tc.wantLocator, tc.want)
		})
	}

	t.Run("keys naming a component a sub-aggregate contributes and an attachment are compiled with no finding", func(t *testing.T) {
		t.Parallel()
		manifest := nameTierManifest(
			[]string{fixtureModuleNodeID + ": " + fixturePostgresRecipe},
			[]string{"cross-namespace-routing: k8s-dns-patcher:1.0.0"},
		)
		if _, err := compileWithManifest(t, fixtureAggregate(t), manifest); err != nil {
			t.Fatalf("a Manifest whose name-tier keys name what the composition holds halted: %v", err)
		}
		described, err := describeWithManifest(t, fixtureAggregate(t), manifest)
		if err != nil {
			t.Fatalf("a Manifest whose name-tier keys name what the composition holds was refused: %v", err)
		}
		for _, diagnostic := range described.ResolveDiagnostics {
			if errors.Is(diagnostic.Err, catalog.ErrNameMappingMatchedNothing()) {
				t.Errorf("a Manifest whose name-tier keys all bind reported %v", diagnostic)
			}
		}
	})
}

// assertNameMappingFinding checks one finding's short code, position, category and
// rendered text.
func assertNameMappingFinding(t *testing.T, f *resolver.ResolverError, wantLocator string, want []string) {
	t.Helper()
	if short, ok := cerrors.ShortCodeOf(f.Err); !ok || short != "E0357" {
		t.Errorf("short code = %q (present %t), want %q", short, ok, "E0357")
	}
	if f.Field != wantLocator {
		t.Errorf("locator = %q, want %q", f.Field, wantLocator)
	}
	if got := f.ToCompilerError().Category(); got != cerrors.OpsError {
		t.Errorf("category = %v, want %v", got, cerrors.OpsError)
	}
	rendered := f.Error()
	for _, w := range want {
		if !strings.Contains(rendered, w) {
			t.Errorf("the rendered finding does not carry %q: %q", w, rendered)
		}
	}
}
