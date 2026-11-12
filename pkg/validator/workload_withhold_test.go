// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// What a Workload refusal may say when the template that rendered the document
// read a value an author declared sensitive. Each row runs the same document with
// and without WithholdRenderedValues: the plain run is the control proving the
// row's value reaches a sentence at all, and the withheld run must name no value
// and give no advice derived from one, while a finding whose sentence quotes no
// value reads exactly as it did.

// renderedWithheldNotice is the sentence closing every finding the option
// withholds. It is spelled here rather than read from the package so a change to
// the wording a reader sees is a change this test sees.
const renderedWithheldNotice = " (the value is withheld because the template that rendered this document " +
	"reads a value declared sensitive)"

// renderedSecret stands in for a declared value a template wrote into the document.
const renderedSecret = "rendered-secret-9a8b7c"

// withheldWorkload is a single-container document with one line the row supplies.
func withheldWorkload(line string) string {
	return "apiVersion: aggrega.ai/v1beta1\n" +
		"kind: Workload\n" +
		"containers:\n" +
		"  main:\n" +
		"    image: app:1.0\n" +
		line
}

func TestWorkloadBytes_WithholdRenderedValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		document string
		// plainQuotes is text the control run must carry, proving the row's
		// value or the advice derived from it reaches the refusal unwithheld.
		plainQuotes string
		// want is text the withheld run must carry; empty where the withheld run
		// must be byte-identical to the control.
		want []string
		// absent is text the withheld run must not carry.
		absent []string
	}{
		{
			name:        "a value an enumeration refuses",
			document:    withheldWorkload("    imagePullPolicy: " + renderedSecret + "\n"),
			plainQuotes: renderedSecret,
			want: []string{"enum: " + redact.Placeholder() + ` does not satisfy enum ["Always","Never","IfNotPresent"]` +
				renderedWithheldNotice},
			absent: []string{renderedSecret},
		},
		{
			name: "a value YAML read as a number where the schema wants text",
			document: withheldWorkload("    resources:\n      requests:\n        cpu: 424242\n" +
				"        memory: 512Mi\n"),
			plainQuotes: adviceOpening + " number",
			want:        []string{redact.Placeholder(), renderedWithheldNotice},
			absent:      []string{"424242", adviceOpening},
		},
		{
			name:        "a key the document does not admit, whose sentence quotes no value",
			document:    withheldWorkload("strayKey: " + renderedSecret + "\n"),
			plainQuotes: "strayKey",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			plain := validator.ValidateWorkloadBytes([]byte(c.document), "recipe.yaml", "app", "1.0.0")
			withheld := validator.ValidateWorkloadBytes([]byte(c.document), "recipe.yaml", "app", "1.0.0",
				validator.WithholdRenderedValues())
			for _, err := range []error{plain, withheld} {
				if !errors.Is(err, validator.ErrWorkloadValidation()) {
					t.Fatalf("the document must be refused either way; got %v", err)
				}
			}
			if !strings.Contains(plain.Error(), c.plainQuotes) {
				t.Fatalf("control: the refusal without the option does not carry %q, so the row proves nothing:\n%s",
					c.plainQuotes, plain)
			}
			if c.want == nil {
				if withheld.Error() != plain.Error() {
					t.Errorf("a refusal quoting no value must be byte-identical:\nwithheld: %s\nplain:    %s", withheld, plain)
				}
				return
			}
			for _, want := range c.want {
				if !strings.Contains(withheld.Error(), want) {
					t.Errorf("the withheld refusal must carry %q:\n%s", want, withheld)
				}
			}
			for _, absent := range c.absent {
				if strings.Contains(withheld.Error(), absent) {
					t.Errorf("the withheld refusal carries %q:\n%s", absent, withheld)
				}
			}
		})
	}
}

// TestWorkload_WithholdRenderedValuesOnTheTypedEntryPoint holds the typed entry
// point to the same option, since a template's output reaches a Workload
// validation through both.
func TestWorkload_WithholdRenderedValuesOnTheTypedEntryPoint(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
	w.Containers.Set("main", types.ContainerSpec{Image: "app:1.0", ImagePullPolicy: renderedSecret})

	plain := validator.ValidateWorkload(w, "recipe.yaml", "app", "1.0.0")
	if plain == nil || !strings.Contains(plain.Error(), renderedSecret) {
		t.Fatalf("control: the refusal without the option must quote the value, or the row proves nothing: %v", plain)
	}
	withheld := validator.ValidateWorkload(w, "recipe.yaml", "app", "1.0.0", validator.WithholdRenderedValues())
	if !errors.Is(withheld, validator.ErrWorkloadValidation()) {
		t.Fatalf("the document must still be refused; got %v", withheld)
	}
	if strings.Contains(withheld.Error(), renderedSecret) {
		t.Errorf("the withheld refusal carries the value:\n%s", withheld)
	}
	want := "enum: " + redact.Placeholder() + ` does not satisfy enum ["Always","Never","IfNotPresent"]` + renderedWithheldNotice
	if !strings.Contains(withheld.Error(), want) {
		t.Errorf("the withheld refusal must read %q:\n%s", want, withheld)
	}
}
