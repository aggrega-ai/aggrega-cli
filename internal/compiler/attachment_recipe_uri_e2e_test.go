// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end coverage of the Recipe identity a per-component Feature
// attachment's rendered documents are stamped with, across the three ways a
// FeatureRecipe can deliver them: a chart or fragment the author supplies, a
// workload the compiler synthesizes, and a standalone artifact set.
//
// The fixture attaches all three to ONE component in one boundary, so the three
// deliveries meet in one build and are compared against each other rather than
// each against its own expectation. It is built for both a Kubernetes target and
// a compose target, because the value is chosen once host-side and each target
// writes it onto a different surface — an object annotation on one, a service
// label on the other — so a value reaching one surface and not the other would
// otherwise pass unnoticed.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// attachmentRecipeURIWant is the FeatureRecipe identity each attachment's own
// documents must name, per target. Written out literally rather than composed
// from anything the build produced, so it cannot agree with a derivation that
// moved.
var attachmentRecipeURIWant = map[string]map[string]string{
	"helm": {
		"src": "aux-sourced-helm:1.0.0",
		"wl":  "aux-workload-helm:1.0.0",
		"art": "aux-artifacts-helm:1.0.0",
	},
	"compose": {
		"src": "aux-sourced-compose:1.0.0",
		"wl":  "aux-workload-compose:1.0.0",
		"art": "aux-artifacts-compose:1.0.0",
	},
}

// hostRecipeURIWant is the identity the HOST component's own documents name, per
// target — the component path, which names the component's own Recipe.
var hostRecipeURIWant = map[string]string{
	"helm":    "host-helm:1.0.0",
	"compose": "host-compose:1.0.0",
}

// TestAttachmentProvenance_EveryDeliveryNamesTheFeatureRecipeThatProducedIt
// pins the value, not its presence. A document a Feature attachment produced
// names the attachment's OWN FeatureRecipe, for each of the three deliveries and
// on both targets.
//
// Asserting the value is the whole point of the case: a criterion satisfied by
// "every document carries a recipe-uri" is satisfied by a build that stamps the
// host component's Recipe onto documents the host's Recipe did not produce, and
// a wrong identity there is worse than an absent one — an absent value is
// visibly absent, while a wrong one is indistinguishable from a right one and is
// read precisely when someone is tracing a document back to its producer.
func TestAttachmentProvenance_EveryDeliveryNamesTheFeatureRecipeThatProducedIt(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"helm", "compose"} {
		res := pipelinetest.Build(t, attachmentIdentityInput(target))

		for _, instance := range []string{"src", "wl", "art"} {
			want := attachmentRecipeURIWant[target][instance]
			prefix := "features/alpha/" + instance + "/"

			var got []string
			for _, k := range sortedKeys(res.Artifacts) {
				if strings.Contains(k, prefix) {
					got = append(got, recipeURIValues(string(res.Artifacts[k]))...)
				}
			}
			// An attachment that emitted no stamped document at all would make
			// every comparison below vacuous, so the empty case fails on its own.
			if len(got) == 0 {
				t.Errorf("%s: attachment %q emitted no document carrying a recipe-uri; keys:\n%s",
					target, instance, strings.Join(sortedKeys(res.Artifacts), "\n"))
				continue
			}
			for _, g := range got {
				if g != want {
					t.Errorf("%s: attachment %q names %q as the Recipe that produced its documents; want %q",
						target, instance, g, want)
				}
			}
		}
	}
}

// TestAttachmentProvenance_TheHostComponentStillNamesItsOwnRecipe is the other
// side of the same property, and it is a separate case because the assertions
// above would pass just as well if the component path had moved too.
//
// A component's own documents name the component's own Recipe. Nothing about an
// attachment's provenance reaches them: the attachment identity is established
// on the copy of the render context an attachment's render is handed, so the
// component's own render is untouched by it.
func TestAttachmentProvenance_TheHostComponentStillNamesItsOwnRecipe(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"helm", "compose"} {
		res := pipelinetest.Build(t, attachmentIdentityInput(target))
		want := hostRecipeURIWant[target]

		var got []string
		for _, k := range sortedKeys(res.Artifacts) {
			if strings.Contains(k, "features/") {
				continue
			}
			got = append(got, recipeURIValues(string(res.Artifacts[k]))...)
		}
		if len(got) == 0 {
			t.Errorf("%s: the host component emitted no document carrying a recipe-uri; keys:\n%s",
				target, strings.Join(sortedKeys(res.Artifacts), "\n"))
			continue
		}
		for _, g := range got {
			if g != want {
				t.Errorf("%s: the host component's own documents name %q; want its own Recipe %q", target, g, want)
			}
		}
	}
}

// TestAttachmentProvenance_ThreeDeliveriesNameThreeDifferentRecipes asserts the
// separation as a property rather than as three values, by set difference: the
// three deliveries plus the host reach four distinct identities in one build.
//
// It is a separate case because it fails on the shape the value assertions
// cannot see — three deliveries collapsing onto ONE identity that happens to be
// the value each was expected to carry would need all three expectations to be
// wrong in the same direction, whereas this case fails the moment any two of the
// four meet.
func TestAttachmentProvenance_ThreeDeliveriesNameThreeDifferentRecipes(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"helm", "compose"} {
		res := pipelinetest.Build(t, attachmentIdentityInput(target))

		seen := map[string]string{} // recipe-uri -> the output key that carried it first
		for _, k := range sortedKeys(res.Artifacts) {
			for _, uri := range recipeURIValues(string(res.Artifacts[k])) {
				if first, dup := seen[uri]; dup {
					// Two documents under one producer prefix legitimately share a
					// value; two different producers sharing one is the failure.
					if producerPrefix(first) != producerPrefix(k) {
						t.Errorf("%s: %q and %q are produced by different Recipes yet both name %q",
							target, first, k, uri)
					}
					continue
				}
				seen[uri] = k
			}
		}
		if len(seen) != 4 {
			t.Errorf("%s: want 4 distinct recipe-uri values (the host plus its three attachments); got %d: %v",
				target, len(seen), seen)
		}
	}
}

// producerPrefix reduces an output key to the producer that emitted it — an
// attachment's own directory, or the empty string for the host component's own
// output. Two keys sharing a prefix are one producer's documents.
func producerPrefix(key string) string {
	for _, instance := range []string{"src", "wl", "art"} {
		if strings.Contains(key, "features/alpha/"+instance+"/") {
			return instance
		}
	}
	return ""
}

// recipeURIValues collects every aggrega.ai/recipe-uri value a rendered document
// carries. Read by scanning lines rather than by parsing YAML, because the
// property under test is what the emitted TEXT says and a parse would let a
// value reach the assertion through a path the bytes do not show. The surrounding
// quotes a YAML writer may add are stripped, since the quoting is the writer's
// choice and not part of the identity.
func recipeURIValues(body string) []string {
	const key = "aggrega.ai/recipe-uri:"
	var out []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(trimmed, key); ok {
			out = append(out, strings.Trim(strings.TrimSpace(v), `"'`))
		}
	}
	return out
}
