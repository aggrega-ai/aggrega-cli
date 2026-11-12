// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package envelope

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	aggerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// duplicateStepRecipe is a Recipe its schema accepts whose pipeline declares
// the id write_retry_count on both of its steps: the first `id` key sits at
// line 13, column 7, and the repeat at line 16, column 7.
const duplicateStepRecipe = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: probe
  version: "1.0.0"
spec:
  capability: helm
  implements: example/compute/probe
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
  pipeline:
    - id: write_retry_count
      plugin: pipeline-step/litellm-gateway-config:1.0.0
      function: step
    - id: write_retry_count
      plugin: pipeline-step/litellm-gateway-config:1.0.0
      function: step
`

// featureRecipeWithSteps is the shape of the hub's helm/retry-policy
// FeatureRecipe, its pipeline carrying one step per id given. The first step's
// `id` key sits at line 10, column 7, and each later one three lines further
// down.
func featureRecipeWithSteps(ids ...string) string {
	var b strings.Builder
	b.WriteString(`apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: retry-policy
  version: 1.0.0
spec:
  capability: helm
  implements: core/retry-policy:1.0.0
  pipeline:
`)
	for _, id := range ids {
		fmt.Fprintf(&b, "    - id: %s\n      plugin: pipeline-step/litellm-gateway-config:1.0.0\n      function: step\n", id)
	}
	return b.String()
}

// repeatAt names one finding a refusal must carry: the step index of the
// repeat, the index of the step that declared the id first, both positions,
// and the id.
type repeatAt struct {
	again, first           int
	againLine, againColumn int
	firstLine, firstColumn int
	id                     string
}

// TestDuplicatePipelineStepID pins the one rule the gate applies that no
// schema can state: no two steps of one Recipe's or FeatureRecipe's pipeline
// share an id. A refusal must name the condition by its own identity and by
// the gate's, carry one finding per repeat positioned at the repeat, and name
// the step that declared the id first with its line and column. A document
// whose ids are distinct passes untouched, and a document failing its schema
// as well reports the schema first.
func TestDuplicatePipelineStepID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		kind Kind
		doc  string
		// want lists the findings a refusal carries, in order; empty means
		// the document passes.
		want []repeatAt
		// wantSchemaFirst marks a document that also fails its schema, so the
		// schema condition is the one reported.
		wantSchemaFirst bool
	}{
		{
			name: "a Recipe repeating one id",
			kind: KindRecipe,
			doc:  duplicateStepRecipe,
			want: []repeatAt{{again: 1, first: 0, againLine: 16, againColumn: 7, firstLine: 13, firstColumn: 7, id: "write_retry_count"}},
		},
		{
			name: "a FeatureRecipe repeating one id",
			kind: KindFeatureRecipe,
			doc:  featureRecipeWithSteps("write_retry_count", "write_retry_count"),
			want: []repeatAt{{again: 1, first: 0, againLine: 13, againColumn: 7, firstLine: 10, firstColumn: 7, id: "write_retry_count"}},
		},
		{
			name: "two repeats each naming the step that declared the id first",
			kind: KindFeatureRecipe,
			doc:  featureRecipeWithSteps("produce", "inject", "produce", "produce"),
			want: []repeatAt{
				{again: 2, first: 0, againLine: 16, againColumn: 7, firstLine: 10, firstColumn: 7, id: "produce"},
				{again: 3, first: 0, againLine: 19, againColumn: 7, firstLine: 10, firstColumn: 7, id: "produce"},
			},
		},
		{
			name: "distinct ids pass",
			kind: KindFeatureRecipe,
			doc:  featureRecipeWithSteps("produce", "inject"),
		},
		{
			name: "a single step passes",
			kind: KindFeatureRecipe,
			doc:  featureRecipeWithSteps("write_retry_count"),
		},
		{
			name:            "a schema violation is reported before a repeated id",
			kind:            KindFeatureRecipe,
			doc:             featureRecipeWithSteps("write_retry_count", "write_retry_count") + "strayTopLevelKey: nope\n",
			wantSchemaFirst: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ValidateBytes(tc.kind, []byte(tc.doc), "recipe.yaml")

			if tc.wantSchemaFirst {
				if err == nil {
					t.Fatal("a document failing its schema must be refused")
				}
				if errors.Is(err, ErrDuplicatePipelineStepID()) {
					t.Fatalf("the schema's findings must be reported before a repeated id; got %v", err)
				}
				if !errors.Is(err, ErrEntityEnvelope()) {
					t.Fatalf("the schema refusal must match ErrEntityEnvelope; got %v", err)
				}
				return
			}
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("a pipeline whose ids are distinct must pass the gate; got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("a pipeline repeating a step id must be refused")
			}

			// Both identities, through a wrap as well as directly, because every
			// caller of the gate wraps what it returns.
			for _, got := range []error{err, fmt.Errorf("load %s: %w", "recipe.yaml", err)} {
				if !errors.Is(got, ErrDuplicatePipelineStepID()) {
					t.Errorf("the refusal must match ErrDuplicatePipelineStepID; got %v", got)
				}
				if !errors.Is(got, ErrEntityEnvelope()) {
					t.Errorf("the refusal must also match ErrEntityEnvelope, the gate's one sentinel; got %v", got)
				}
			}
			// The short code a diagnostic prints is read off the first identity
			// in the chain, which must be this condition's.
			if code, ok := aggerrors.ShortCodeOf(err); !ok || code != "E0370" {
				t.Errorf("the short code printed must be E0370; got %q (found %v)", code, ok)
			}
			if code, ok := aggerrors.CodeOf(err); !ok || code != "duplicate-pipeline-step-id" {
				t.Errorf("the code must be duplicate-pipeline-step-id; got %q (found %v)", code, ok)
			}

			var violation *Error
			if !errors.As(err, &violation) {
				t.Fatalf("the refusal must be an *Error carrying its findings; got %T", err)
			}
			if violation.Condition() != ErrDuplicatePipelineStepID() {
				t.Errorf("the violation must report ErrDuplicatePipelineStepID as its condition; got %v", violation.Condition())
			}
			if len(violation.Findings) != len(tc.want) {
				t.Fatalf("want %d findings, one per repeat; got %d: %v", len(tc.want), len(violation.Findings), violation.Findings)
			}
			text := err.Error()
			if want := ErrDuplicatePipelineStepID().Error() + " (" + string(tc.kind) + ", at recipe.yaml)"; !strings.HasPrefix(text, want) {
				t.Errorf("the report must open with the condition's sentence and the occurrence %q; got %q", want, text)
			}
			if strings.Contains(text, ErrEntityEnvelope().Error()) || strings.Contains(text, adviceCorrectDocument) {
				t.Errorf("a document the schema accepts must not be told it fails its schema; got %q", text)
			}
			if !strings.HasSuffix(text, ". "+adviceDistinctStepIDs) {
				t.Errorf("the advice true of this condition must close the report; got %q", text)
			}

			for i, w := range tc.want {
				f := violation.Findings[i]
				againPath := fmt.Sprintf("spec.pipeline[%d].id", w.again)
				firstPath := fmt.Sprintf("spec.pipeline[%d].id", w.first)
				if f.FieldPath != againPath {
					t.Errorf("finding %d must sit at the repeat %s; got %s", i, againPath, f.FieldPath)
				}
				if got := violation.Positions[againPath]; got.Line != w.againLine || got.Column != w.againColumn || got.File != "recipe.yaml" {
					t.Errorf("the repeat %s must be positioned at recipe.yaml %d:%d; got %+v", againPath, w.againLine, w.againColumn, got)
				}
				if got := violation.Positions[firstPath]; got.Line != w.firstLine || got.Column != w.firstColumn {
					t.Errorf("the first declaration %s must be positioned at %d:%d; got %+v", firstPath, w.firstLine, w.firstColumn, got)
				}
				wantMsg := fmt.Sprintf("step id %q is already declared at %s (line %d, column %d)", w.id, firstPath, w.firstLine, w.firstColumn)
				if f.Message != wantMsg {
					t.Errorf("finding %d must name the id and the first step's position:\n want %q\n  got %q", i, wantMsg, f.Message)
				}
				if want := fmt.Sprintf("; %d:%d: %s: %s", w.againLine, w.againColumn, againPath, wantMsg); !strings.Contains(text, want) {
					t.Errorf("the rendered report must carry finding %d as %q; got %q", i, want, text)
				}
			}
		})
	}
}

// TestDuplicatePipelineStepIDAddsNoRefusalToADocumentItsKindCannotDecode pins
// what the rule does with a document that does not decode into its kind's type:
// it is not asked, so whatever already refuses that document is the refusal the
// author reads. The document here repeats a step id and writes its capability as
// a list, which its kind's type cannot hold: the rule's own entry yields nothing
// for it, and the gate reports the schema's refusal and not this condition.
func TestDuplicatePipelineStepIDAddsNoRefusalToADocumentItsKindCannotDecode(t *testing.T) {
	t.Parallel()

	doc := []byte(strings.Replace(duplicateStepRecipe, "capability: helm", "capability: [helm]", 1))

	if err := pipelineStepIDsUnique(compiledKey{kind: KindRecipe}, "recipe.yaml", doc); err != nil {
		t.Fatalf("a document its kind's type cannot hold must not be asked about its step ids; got %v", err)
	}

	_, err := ValidateBytes(KindRecipe, doc, "recipe.yaml")
	if err == nil {
		t.Fatal("a document whose capability is not a string must be refused")
	}
	if errors.Is(err, ErrDuplicatePipelineStepID()) {
		t.Fatalf("the refusal must be the one that already refuses the document, not a repeated id; got %v", err)
	}
	if !errors.Is(err, ErrEntityEnvelope()) {
		t.Fatalf("the refusal must be the schema's; got %v", err)
	}
}

// TestDuplicatePipelineStepIDWithheld pins the withholding entry point: where
// every value of a document may carry data an author declared sensitive, the
// refusal still names both positions but quotes no id.
func TestDuplicatePipelineStepIDWithheld(t *testing.T) {
	t.Parallel()

	const notice = " (withheld for this test)"
	_, err := ValidateBytesWithholdingEvery(KindRecipe, []byte(duplicateStepRecipe), "recipe.yaml", notice)
	if !errors.Is(err, ErrDuplicatePipelineStepID()) || !errors.Is(err, ErrEntityEnvelope()) {
		t.Fatalf("the refusal must match both sentinels; got %v", err)
	}
	text := err.Error()
	if strings.Contains(text, "write_retry_count") {
		t.Errorf("a withheld refusal must not quote the id; got %q", text)
	}
	if want := "16:7: spec.pipeline[1].id: this step's id is already declared at spec.pipeline[0].id (line 13, column 7)" + notice; !strings.Contains(text, want) {
		t.Errorf("a withheld refusal must still name both positions as %q; got %q", want, text)
	}
}
