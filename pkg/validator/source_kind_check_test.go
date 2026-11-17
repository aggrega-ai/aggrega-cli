// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// sourceKindRows are the authored sources the source-kind rule is held to, one
// fault per row, with the served kinds and the two forms that name no kind as the
// controls.
var sourceKindRows = []struct {
	name   string
	source any
	// refused is whether the rule refuses the source.
	refused bool
}{
	{"a structured source naming a served kind (control)", map[string]any{"kind": sourcekind.HelmOCIChart, "url": "oci://r/c", "version": "1.0.0"}, false},
	{"a structured source naming another served kind (control)", map[string]any{"kind": sourcekind.GitRepository, "url": "https://h/r.git"}, false},
	{"a source written as a single URI (control)", "oci://r/c", false},
	{"a structured source writing no kind (control)", map[string]any{"url": "oci://r/c", "version": "1.0.0"}, false},
	{"a structured source writing an empty kind (control)", map[string]any{"kind": "", "url": "oci://r/c"}, false},
	{"a misspelt kind", map[string]any{"kind": "nosuch", "url": "https://example.com/x", "version": "1.0.0"}, true},
	{"a well-formed kind no adapter ships for", map[string]any{"kind": "nosuch-module-oci", "url": "oci://r/m"}, true},
	{"a served kind in another case", map[string]any{"kind": "Helm-OCI-Chart", "url": "oci://r/c"}, true},
}

// TestValidateRecipe_RefusesASourceKindNothingServes drives the rule through the
// Recipe's load-time entry, the one every load and the single-entity check reach.
//
// INVARIANT: a structured source naming a kind no adapter serves is refused under
// the source-kind condition, naming the kind; every other source passes the rule.
// CONSEQUENCE: were a row to pass, the Recipe would be admitted and refused only
// at a pull, under a condition naming a template evaluation rather than the kind.
func TestValidateRecipe_RefusesASourceKindNothingServes(t *testing.T) {
	t.Parallel()
	for _, tc := range sourceKindRows {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &types.Recipe{}
			r.Metadata.Name = "svc"
			r.Metadata.Version = "1.0.0"
			r.Spec.Capability = "helm"
			r.Spec.Implements = "test/svc"
			r.Spec.Source = tc.source
			assertSourceKindOutcome(t, ValidateRecipe(r, "recipe.yaml"), tc.source, tc.refused)
		})
	}
}

// TestValidateFeatureRecipe_RefusesASourceKindNothingServes is the FeatureRecipe's
// leg of the same rule, through the FeatureRecipe's load-time entry.
func TestValidateFeatureRecipe_RefusesASourceKindNothingServes(t *testing.T) {
	t.Parallel()
	for _, tc := range sourceKindRows {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := &types.FeatureRecipe{}
			fr.Metadata.Name = "cred"
			fr.Metadata.Version = "1.0.0"
			fr.Spec.Capability = "helm"
			fr.Spec.Implements = "test/cred"
			fr.Spec.Source = tc.source
			assertSourceKindOutcome(t, ValidateFeatureRecipe(fr, "featurerecipe.yaml"), tc.source, tc.refused)
		})
	}
}

func assertSourceKindOutcome(t *testing.T, err error, source any, refused bool) {
	t.Helper()
	if !refused {
		if errors.Is(err, sourcekind.ErrUnknownKind()) {
			t.Fatalf("the source %v was refused for its kind: %v", source, err)
		}
		return
	}
	if !errors.Is(err, sourcekind.ErrUnknownKind()) {
		t.Fatalf("want the source-kind condition for %v; got %v", source, err)
	}
	kind := source.(map[string]any)["kind"].(string)
	if !strings.Contains(err.Error(), `"`+kind+`"`) {
		t.Errorf("the refusal does not name the kind %q: %v", kind, err)
	}
	for _, served := range sourcekind.ServedKinds() {
		if !strings.Contains(err.Error(), served) {
			t.Errorf("the refusal's advice does not name the served kind %q: %v", served, err)
		}
	}
}
