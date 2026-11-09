// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package scaffoldops

import (
	"errors"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// TestSkeletonDraftsTheDocumentTheSchemaRequires pins the bytes, not a property
// of them, because the bytes are what every caller of this layer hands on: a
// surface that reformatted, reordered or padded them would be handing a
// different document to its author than the next surface does.
//
// The rows are chosen so each covers a shape the walk decides differently — a
// nested required mapping, an extra required field beside the name, required
// scalars under spec, a required object under spec, and a fixed value the schema
// pins — rather than six kinds that happen to exist. A schema that legitimately
// changes one of these shapes moves its row, which is the point: the drafting
// output is derived from the schema, so a schema change belongs in this diff.
func TestSkeletonDraftsTheDocumentTheSchemaRequires(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kind  string
		seed  string
		want  string
		shape string
	}{
		{
			name:  "a nested required mapping under metadata",
			kind:  "component",
			seed:  "checkout",
			shape: "labels is a required object with a required key of its own",
			want: "apiVersion: aggrega.ai/v1beta1\n" +
				"kind: Component\n" +
				"metadata:\n" +
				"  name: checkout\n" +
				"  version: \"\"\n" +
				"  labels:\n" +
				"    component.aggrega.ai/type: \"\"\n" +
				"spec: {}\n",
		},
		{
			name:  "an empty required object under spec",
			kind:  "aggregate",
			seed:  "storefront",
			shape: "spec is required and declares nothing required within it",
			want: "apiVersion: aggrega.ai/v1beta1\n" +
				"kind: Aggregate\n" +
				"metadata:\n" +
				"  name: storefront\n" +
				"  version: \"\"\n" +
				"spec: {}\n",
		},
		{
			name:  "an extra required field beside the name",
			kind:  "release",
			seed:  "storefront-staging",
			shape: "metadata requires environment as well as name and version",
			want: "apiVersion: aggrega.ai/v1beta1\n" +
				"kind: Release\n" +
				"metadata:\n" +
				"  name: storefront-staging\n" +
				"  environment: \"\"\n" +
				"  version: \"\"\n" +
				"spec: {}\n",
		},
		{
			name:  "required scalars under spec",
			kind:  "recipe",
			seed:  "postgres-helm",
			shape: "spec requires two free-form scalars",
			want: "apiVersion: aggrega.ai/v1beta1\n" +
				"kind: Recipe\n" +
				"metadata:\n" +
				"  name: postgres-helm\n" +
				"  version: \"\"\n" +
				"spec:\n" +
				"  capability: \"\"\n" +
				"  implements: \"\"\n",
		},
		{
			name:  "a required object under spec",
			kind:  "manifest",
			seed:  "staging",
			shape: "spec requires an object that declares nothing required within it",
			want: "apiVersion: aggrega.ai/v1beta1\n" +
				"kind: Manifest\n" +
				"metadata:\n" +
				"  name: staging\n" +
				"  version: \"\"\n" +
				"spec:\n" +
				"  mappings: {}\n",
		},
		{
			name:  "a fixed value the schema pins",
			kind:  "plugin",
			seed:  "helm-renderer",
			shape: "spec.abiVersion is a const, so it is filled rather than placeheld",
			want: "apiVersion: aggrega.ai/v1beta1\n" +
				"kind: Plugin\n" +
				"metadata:\n" +
				"  name: helm-renderer\n" +
				"  version: \"\"\n" +
				"  pluginType: \"\"\n" +
				"spec:\n" +
				"  abiVersion: v1alpha1\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Skeleton(tc.kind, tc.seed)
			if err != nil {
				t.Fatalf("Skeleton(%q): %v", tc.kind, err)
			}
			if got != tc.want {
				t.Errorf("the %q skeleton (%s) is not the document expected.\n got:\n%s\nwant:\n%s",
					tc.kind, tc.shape, got, tc.want)
			}
		})
	}
}

// TestSkeletonIsTheSameBytesOnEveryCall is the determinism floor the byte
// identity between surfaces rests on: two callers reach this layer at different
// moments, so a walk that varied with map ordering would let them disagree
// without either being wrong.
//
// It walks the whole embedded set rather than a sample, because the ordering
// hazard is per-schema — one kind whose required list is read from a map would
// be the only one that wobbled.
func TestSkeletonIsTheSameBytesOnEveryCall(t *testing.T) {
	kinds := schemas.Kinds()
	if len(kinds) == 0 {
		t.Fatal("the embedded schema set is empty, so this walks nothing")
	}
	for _, kind := range kinds {
		first, err := Skeleton(kind, "repeated-name")
		if err != nil {
			t.Errorf("Skeleton(%q): %v", kind, err)
			continue
		}
		for attempt := 0; attempt < 4; attempt++ {
			again, err := Skeleton(kind, "repeated-name")
			if err != nil {
				t.Errorf("Skeleton(%q) on attempt %d: %v", kind, attempt, err)
				break
			}
			if again != first {
				t.Errorf("the %q skeleton changed between calls.\nfirst:\n%s\nlater:\n%s", kind, first, again)
				break
			}
		}
	}
}

// TestEveryEmbeddedKindDrafts is the census the sampled table above cannot be:
// every kind the binary carries a schema for answers, so a schema that joined
// the set is drafted rather than refused, and none of them makes the walk fail.
//
// It asserts the answer is non-empty as well as error-free. A walk that returned
// nothing would satisfy an error check while handing an author a blank file.
func TestEveryEmbeddedKindDrafts(t *testing.T) {
	kinds := schemas.Kinds()
	if len(kinds) == 0 {
		t.Fatal("the embedded schema set is empty, so this census asserts nothing")
	}
	for _, kind := range kinds {
		got, err := Skeleton(kind, "census-name")
		switch {
		case err != nil:
			t.Errorf("the embedded %q schema does not draft: %v", kind, err)
		case strings.TrimSpace(got) == "":
			t.Errorf("the embedded %q schema drafts an empty document", kind)
		}
	}
}

// TestTheNameSeedsOnlyWhereTheKindDeclaresOne pins both directions. A kind whose
// schema requires metadata.name carries the seed; a kind whose schema requires no
// such field is drafted unchanged rather than growing one, because inventing a
// field the schema does not require would hand the author a document that fails
// the check it was drafted to pass.
func TestTheNameSeedsOnlyWhereTheKindDeclaresOne(t *testing.T) {
	t.Run("a kind that declares one carries the seed", func(t *testing.T) {
		seeded, err := Skeleton("component", "checkout")
		if err != nil {
			t.Fatalf("Skeleton: %v", err)
		}
		if !strings.Contains(seeded, "name: checkout\n") {
			t.Fatalf("the seed did not reach metadata.name:\n%s", seeded)
		}
	})

	t.Run("no seed leaves a placeholder in its place", func(t *testing.T) {
		bare, err := Skeleton("component", "")
		if err != nil {
			t.Fatalf("Skeleton: %v", err)
		}
		if !strings.Contains(bare, "name: \"\"\n") {
			t.Fatalf("an unseeded name is not a placeholder:\n%s", bare)
		}
	})

	t.Run("a kind that declares none is unchanged by a seed", func(t *testing.T) {
		// workload requires no metadata block at all, so there is no name field
		// for a seed to reach.
		bare, err := Skeleton("workload", "")
		if err != nil {
			t.Fatalf("Skeleton: %v", err)
		}
		seeded, err := Skeleton("workload", "ignored-name")
		if err != nil {
			t.Fatalf("Skeleton: %v", err)
		}
		if bare != seeded {
			t.Fatalf("a seed changed a kind that declares no name.\nwithout:\n%s\nwith:\n%s", bare, seeded)
		}
		if strings.Contains(seeded, "ignored-name") {
			t.Fatalf("a name field was invented for a kind whose schema requires none:\n%s", seeded)
		}
	})
}

// TestAnUnresolvableKindIsRefusedWithTheValidSet pins the refusal and the
// positive control beside it. The control is not decoration: an assertion that
// only checks a failing token fails cannot tell a working resolution apart from
// one that refuses everything.
func TestAnUnresolvableKindIsRefusedWithTheValidSet(t *testing.T) {
	t.Run("a token no schema answers to", func(t *testing.T) {
		got, err := Skeleton("compoent", "")
		if err == nil {
			t.Fatalf("a token no schema answers to drafted a document:\n%s", got)
		}
		if got != "" {
			t.Errorf("a refused draft still returned text: %q", got)
		}

		var unknown *UnknownKindError
		if !errors.As(err, &unknown) {
			t.Fatalf("the refusal is not distinguishable from a failed walk: %#v", err)
		}
		if unknown.Kind != "compoent" {
			t.Errorf("the refusal reports the kind as %q, not the token that was sent", unknown.Kind)
		}
		if !strings.Contains(err.Error(), `"compoent"`) {
			t.Errorf("the refusal does not quote the token that was sent: %s", err)
		}
		for _, kind := range schemas.Kinds() {
			if !strings.Contains(err.Error(), kind) {
				t.Errorf("the refusal omits the valid kind %q, so a caller cannot correct from it: %s", kind, err)
			}
		}
	})

	t.Run("a token a schema does answer to", func(t *testing.T) {
		_, err := Skeleton("component", "")
		if err != nil {
			t.Fatalf("a resolvable kind was refused: %v", err)
		}
	})

	t.Run("an empty token", func(t *testing.T) {
		_, err := Skeleton("", "")
		var unknown *UnknownKindError
		if !errors.As(err, &unknown) {
			t.Fatalf("an empty token is not refused as an unresolvable kind: %#v", err)
		}
	})
}

// TestSurroundingSpaceDoesNotChangeWhatIsDrafted pins the one remaining way two
// callers sharing this computation could still disagree about its answer: one
// that trimmed its argument and one that did not would resolve different kinds
// from the same value a client sent.
func TestSurroundingSpaceDoesNotChangeWhatIsDrafted(t *testing.T) {
	tight, err := Skeleton("component", "checkout")
	if err != nil {
		t.Fatalf("Skeleton: %v", err)
	}
	padded, err := Skeleton("  component\t", "\n checkout  ")
	if err != nil {
		t.Fatalf("Skeleton with padded arguments: %v", err)
	}
	if tight != padded {
		t.Fatalf("padding an argument changed the document.\ntight:\n%s\npadded:\n%s", tight, padded)
	}
}

// TestUnknownKindGuidanceIsOneRendering pins that the corrective text a caller
// renders directly and the one carried on the refusal are the same words, so a
// surface that resolves a kind token itself cannot phrase the rejection
// differently from one that goes through the draft.
func TestUnknownKindGuidanceIsOneRendering(t *testing.T) {
	_, err := Skeleton("compoent", "")
	if err == nil {
		t.Fatal("a token no schema answers to drafted a document")
	}
	if got, want := err.Error(), UnknownKindGuidance("compoent"); got != want {
		t.Fatalf("the refusal and the rendered guidance are two texts.\n got: %s\nwant: %s", got, want)
	}
}
