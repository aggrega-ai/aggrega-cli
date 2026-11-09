// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestInspectFeatureRecipe_NamesEachRenderFactsDeclaration holds the overview's
// report of the two declarations that move a FeatureRecipe's attachments after
// every component: each is printed where it is set and absent where it is not, so
// a reader of the overview sees which connections' facts the templates read.
func TestInspectFeatureRecipe_NamesEachRenderFactsDeclaration(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		keys        string
		wantPrinted []string
		wantAbsent  []string
	}{
		{name: "neither", keys: "",
			wantAbsent: []string{"readsCallerRenderFacts", "readsTargetRenderFacts"}},
		{name: "the callers' key", keys: "  readsCallerRenderFacts: true\n",
			wantPrinted: []string{"readsCallerRenderFacts: true"}, wantAbsent: []string{"readsTargetRenderFacts"}},
		{name: "the targets' key", keys: "  readsTargetRenderFacts: true\n",
			wantPrinted: []string{"readsTargetRenderFacts: true"}, wantAbsent: []string{"readsCallerRenderFacts"}},
		{name: "both", keys: "  readsCallerRenderFacts: true\n  readsTargetRenderFacts: true\n",
			wantPrinted: []string{"readsCallerRenderFacts: true", "readsTargetRenderFacts: true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := "apiVersion: aggrega.ai/v1beta1\n" +
				"kind: FeatureRecipe\n" +
				"metadata:\n  name: fact-reader\n  version: 1.0.0\n" +
				"spec:\n  capability: helm\n  implements: example/fact-reader:1.0.0\n" + tc.keys
			var buf bytes.Buffer
			if err := renderFeatureRecipeFile(context.Background(), nil, "recipe.yaml", []byte(doc), nil,
				testInspectOptions(&buf)); err != nil {
				t.Fatalf("inspecting the FeatureRecipe: %v", err)
			}
			out := buf.String()
			for _, want := range tc.wantPrinted {
				if !strings.Contains(out, want+"\n") {
					t.Errorf("the overview does not print %q:\n%s", want, out)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(out, absent) {
					t.Errorf("the overview prints %q for a FeatureRecipe that does not set it:\n%s", absent, out)
				}
			}
		})
	}
}
