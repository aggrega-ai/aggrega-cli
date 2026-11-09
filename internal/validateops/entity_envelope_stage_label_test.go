// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// stageLabelFeatureRecipe is a FeatureRecipe its schema accepts, carrying one
// pipeline step per id given, with extra appended at the end of the document.
func stageLabelFeatureRecipe(extra string, ids ...string) string {
	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: FeatureRecipe\nmetadata:\n  name: retry-policy\n  version: 1.0.0\nspec:\n  capability: helm\n  implements: core/retry-policy:1.0.0\n  pipeline:\n")
	for _, id := range ids {
		b.WriteString("    - id: " + id + "\n      plugin: pipeline-step/litellm-gateway-config:1.0.0\n      function: step\n")
	}
	b.WriteString(extra)
	return b.String()
}

// TestEnvelopeRefusalStageLabel pins the stage label the single-entity check
// reports a document-envelope refusal under. A document failing its kind's
// schema is labelled schema, exactly as it always was, and so is one declaring a
// retired API or plugin ABI version, which the schema refuses too. A document the
// schema accepts and the gate refuses under a condition of its own — a pipeline
// repeating a step id — is labelled shape, because a schema label would tell
// the reader the schema refused a document it accepts. Each row carries one
// fault, and the row's condition is asserted by its identity and its short code
// as well as by the label, so a row cannot pass by reaching the other path.
func TestEnvelopeRefusalStageLabel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		doc       string
		wantLabel string
		condition error
		shortCode string
	}{
		{
			name:      "a repeated pipeline step id is labelled shape",
			doc:       stageLabelFeatureRecipe("", "write_retry_count", "write_retry_count"),
			wantLabel: "shape: ",
			condition: envelope.ErrDuplicatePipelineStepID(),
			shortCode: "E0370",
		},
		{
			name: "a retired api version keeps the schema label under its own condition",
			doc: strings.Replace(stageLabelFeatureRecipe("", "write_retry_count"),
				"apiVersion: aggrega.ai/v1beta1", "apiVersion: aggrega.ai/v1alpha1", 1),
			wantLabel: "schema: ",
			condition: envelope.ErrRetiredAPIVersion(),
			shortCode: "E0373",
		},
		{
			name:      "a retired plugin ABI version keeps the schema label under its own condition",
			doc:       "apiVersion: aggrega.ai/v1beta1\nkind: Plugin\nmetadata:\n  name: probe\nspec:\n  abiVersion: v1alpha1\n",
			wantLabel: "schema: ",
			condition: envelope.ErrRetiredABIVersion(),
			shortCode: "E0374",
		},
		{
			name:      "a schema violation keeps the schema label",
			doc:       stageLabelFeatureRecipe("strayTopLevelKey: nope\n", "write_retry_count"),
			wantLabel: "schema: ",
			condition: envelope.ErrEntityEnvelope(),
			shortCode: "E0215",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "recipe.yaml")
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("validation panicked: %v", r)
					}
				}()
				_, err = ValidateEntityContent(context.Background(), dir, t.TempDir(), nil, nil, path, []byte(tc.doc), false)
			}()
			if err == nil {
				t.Fatal("the document must be refused")
			}
			if !errors.Is(err, tc.condition) {
				t.Errorf("the refusal must match %v; got %v", tc.condition, err)
			}
			if !errors.Is(err, envelope.ErrEntityEnvelope()) {
				t.Errorf("every refusal of the gate must match ErrEntityEnvelope; got %v", err)
			}
			if code, ok := cerrors.ShortCodeOf(err); !ok || code != tc.shortCode {
				t.Errorf("the short code printed must be %s; got %q (found %v)", tc.shortCode, code, ok)
			}
			if want := tc.wantLabel + tc.condition.Error(); !strings.HasPrefix(err.Error(), want) {
				t.Errorf("the refusal must open with %q; got %q", want, err.Error())
			}
		})
	}
}

// TestEnvelopeStageLabelOfAFailureThatIsNoViolation pins the third case: a
// refusal that carries no findings at all — here a document that will not
// parse — keeps the schema label, as it always has.
func TestEnvelopeStageLabelOfAFailureThatIsNoViolation(t *testing.T) {
	t.Parallel()

	_, err := envelope.ValidateBytes(envelope.KindFeatureRecipe, []byte("apiVersion: aggrega.ai/v1beta1\nkind: FeatureRecipe\nmetadata:\n\t- mixed tabs and spaces"), "recipe.yaml")
	if err == nil {
		t.Fatal("a document mixing tabs and spaces must fail to parse")
	}
	if got := envelopeStageLabel(err); got != "schema" {
		t.Errorf("a parse failure must keep the schema label; got %q", got)
	}
}
