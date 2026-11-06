// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"reflect"
	"strings"
	"testing"
)

// provenanceCtxWith returns a render context whose one fitted provenance entry,
// named by key, carries value, and whose other fitted entries are empty — so a
// row asserts one entry's label and annotation without another entry's value
// reaching either set.
func provenanceCtxWith(t *testing.T, key, value string) NodeRenderContext {
	t.Helper()
	ctx := NodeRenderContext{NodeID: "api"}
	switch key {
	case "aggrega.ai/provider":
		ctx.ProviderName = value
	case "aggrega.ai/provider-version":
		ctx.ProviderVersion = value
	case "aggrega.ai/compiler-version":
		ctx.Provenance.CompilerVersion = value
	case "aggrega.ai/aggregate-name":
		ctx.Provenance.AggregateName = value
	case "aggrega.ai/environment":
		ctx.Provenance.Environment = value
	default:
		t.Fatalf("no fitted provenance entry is keyed %q", key)
	}
	return ctx
}

// TestProvenanceBuilders_FitEachEntryAndCarryTheVerbatimValueOnlyWhenRewritten
// pins the split for every compiler-owned entry whose value is not a label
// value by construction: a value the label rules admit is the label alone, a
// value they refuse is labelled in its rewritten form with the verbatim value
// annotated under the same key, and a value with nothing a label keeps is
// annotated only.
func TestProvenanceBuilders_FitEachEntryAndCarryTheVerbatimValueOnlyWhenRewritten(t *testing.T) {
	t.Parallel()
	sixtyThree := strings.Repeat("a", 63)
	cases := []struct {
		name      string
		value     string
		wantLabel string // "" means no label under the key
		wantAnn   string // "" means no annotation under the key
	}{
		{"valid_unchanged", "storefront", "storefront", ""},
		{"valid_hyphen_run_unchanged", "shop--eu", "shop--eu", ""},
		{"valid_at_63_unchanged", sixtyThree, sixtyThree, ""},
		{"slash_rewritten", "imported/flow", "imported-flow", "imported/flow"},
		{"parentheses_rewritten", "(devel)", "devel", "(devel)"},
		{"build_metadata_rewritten", "1.0.0+build.7", "1.0.0-build.7", "1.0.0+build.7"},
		{"over_63_truncated", sixtyThree + "b", sixtyThree, sixtyThree + "b"},
		{"nothing_kept_annotated_only", "///", "", "///"},
		{"empty_elided", "", "", ""},
	}
	for _, e := range fittedProvenanceEntries(NodeRenderContext{}) {
		key := e.key
		for _, tc := range cases {
			tc := tc
			t.Run(key+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				ctx := provenanceCtxWith(t, key, tc.value)
				gotLabels := BuildAggregaProvenanceLabels(ctx)
				gotAnn := BuildAggregaProvenanceAnnotations(ctx, "")

				label, hasLabel := gotLabels[key]
				if tc.wantLabel == "" && hasLabel {
					t.Errorf("label %s = %q; want no label", key, label)
				}
				if tc.wantLabel != "" && label != tc.wantLabel {
					t.Errorf("label %s = %q; want %q", key, label, tc.wantLabel)
				}
				ann, hasAnn := gotAnn[key]
				if tc.wantAnn == "" && hasAnn {
					t.Errorf("annotation %s = %q; want no annotation, because the label carries the value as written", key, ann)
				}
				if tc.wantAnn != "" && ann != tc.wantAnn {
					t.Errorf("annotation %s = %q; want the verbatim %q", key, ann, tc.wantAnn)
				}

				// Every surface that folds the two halves records the value as the
				// build holds it, whatever form the label had to take.
				if tc.value != "" {
					if got := FoldProvenanceSet(gotLabels, gotAnn)[key]; got != tc.value {
						t.Errorf("folded %s = %q; want the verbatim %q", key, got, tc.value)
					}
					comment := string(FormatTerraformProvenanceComment(ctx, ""))
					if want := "# " + key + ": " + tc.value + "\n"; !strings.Contains(comment, want) {
						t.Errorf("terraform provenance comment lacks %q:\n%s", want, comment)
					}
				}
			})
		}
	}
}

// TestFitProvenanceLabelValue carries a value the label rules admit as written
// and rewrites only a value they refuse. The hyphen-run row is the reason the
// function exists: the sanitiser collapses a run a valid value may carry, so a
// valid value has to bypass it rather than pass through it.
func TestFitProvenanceLabelValue(t *testing.T) {
	t.Parallel()
	sixtyThree := strings.Repeat("abcdefghij", 6) + "abc"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"valid_unchanged", "storefront", "storefront"},
		{"valid_hyphen_run_unchanged", "a--b", "a--b"},
		{"valid_mixed_alphabet_unchanged", "Shop_1.v-2", "Shop_1.v-2"},
		{"valid_at_63_unchanged", sixtyThree, sixtyThree},
		{"slash_rewritten", "imported/flow", "imported-flow"},
		{"parentheses_rewritten", "(devel)", "devel"},
		{"build_metadata_rewritten", "1.0.0+abc", "1.0.0-abc"},
		{"edge_rewritten", "-api", "api"},
		{"over_63_truncated", sixtyThree + "d", sixtyThree},
		{"invalid_with_hyphen_run_collapsed", "a--b/c", "a-b-c"},
		{"nothing_kept", "///", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := fitProvenanceLabelValue(tc.in)
			if got != tc.want {
				t.Errorf("fitProvenanceLabelValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if len(got) > labelValueMaxLength {
				t.Errorf("fitProvenanceLabelValue(%q) length %d exceeds %d", tc.in, len(got), labelValueMaxLength)
			}
		})
	}
}

// TestProvenanceBuilders_AValidBuildGainsNoAnnotation pins the case every
// existing rendered output is: values that are all label values add nothing to
// the annotation set, so that output is unchanged.
func TestProvenanceBuilders_AValidBuildGainsNoAnnotation(t *testing.T) {
	t.Parallel()
	ctx := NodeRenderContext{
		NodeID:          "api",
		ProviderName:    "helm",
		ProviderVersion: "1.0.0",
		Provenance: ProvenanceContext{
			CompilerVersion: "0.1.0",
			AggregateName:   "storefront",
			Environment:     "production",
		},
	}
	if got := BuildAggregaProvenanceAnnotations(ctx, ""); len(got) != 0 {
		t.Errorf("annotations = %v; want none for a build whose values are all label values", got)
	}
}

// TestProvenanceBuilders_AreAPureFunctionOfTheirInput asserts the two builders
// return equal sets for one input on every call, the property byte-identical
// output rests on.
func TestProvenanceBuilders_AreAPureFunctionOfTheirInput(t *testing.T) {
	t.Parallel()
	ctx := NodeRenderContext{
		NodeID:          "api",
		ProviderName:    "helm",
		ProviderVersion: "1.0.0+abc",
		Provenance: ProvenanceContext{
			CompilerVersion: "(devel)",
			AggregateName:   "imported/flow",
			Environment:     "eu/prod",
		},
	}
	firstLabels := BuildAggregaProvenanceLabels(ctx)
	firstAnn := BuildAggregaProvenanceAnnotations(ctx, "r:1.0.0")
	for i := 0; i < 20; i++ {
		if got := BuildAggregaProvenanceLabels(ctx); !reflect.DeepEqual(got, firstLabels) {
			t.Fatalf("labels differ across calls: %v vs %v", got, firstLabels)
		}
		if got := BuildAggregaProvenanceAnnotations(ctx, "r:1.0.0"); !reflect.DeepEqual(got, firstAnn) {
			t.Fatalf("annotations differ across calls: %v vs %v", got, firstAnn)
		}
	}
}
