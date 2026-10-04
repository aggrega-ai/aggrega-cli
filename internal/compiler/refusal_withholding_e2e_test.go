// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — a refusal's text never carries a value an author declared
// sensitive, through the real pipeline, at both stages a template refuses at: a
// Component's and a Feature's assertion at the graph stage, and a Recipe's assertion
// and a rendered Workload's schema finding while rendering — the Workload filled once
// from the Component's declared value and once from the declared leaf of a Feature
// the node holds itself. Each refusing fixture sits beside a control quoting a value
// nobody declared, which must reach the text unchanged, and each build runs twice —
// with the trace's withholding on, and switched off for the trace — because a
// refusal's text must not depend on that switch.
package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

const (
	// The declared-sensitive value every refusing fixture carries, and the value
	// nobody declared that the control quotes.
	refusalSecret = "refusal-secret-plaintext"
	refusalNote   = "refusal-note-plaintext"
)

var refusalCatalog = []string{"testdata/refusal-withholding/catalog/base"}

// refusalText builds one fixture through the real pipeline and returns the whole
// text a surface can render of its refusal — the error with every cause beneath
// it — failing when the build does not refuse at all.
func refusalText(t *testing.T, release string, traceUnredacted bool) (string, error) {
	t.Helper()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:     "testdata/refusal-withholding/" + release,
		CatalogPaths:    refusalCatalog,
		IncludeTrace:    traceUnredacted,
		TraceUnredacted: traceUnredacted,
	})
	if err == nil {
		t.Fatalf("%s built; the fixture must refuse, or every assertion below is vacuous", release)
	}
	return err.Error() + "\n" + cerrors.CauseChain(err), err
}

// TestRefusalText_WithholdsADeclaredValueOnEveryStage is the table: one row per
// stage a refusal is raised at, each naming the text that must stand where the
// value stood and the condition the refusal must still report.
func TestRefusalText_WithholdsADeclaredValueOnEveryStage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		release   string
		want      string // what must stand in the text
		condition error  // the registered condition the refusal must still carry
	}{
		{
			name:      "a Recipe assertion quoting the declared value",
			release:   "assert-release.yaml",
			want:      "the admin password " + redact.Placeholder() + " must be supplied as a reference",
			condition: renderer.ErrAssertionFailed(),
		},
		{
			name:      "a Component assertion quoting the declared value",
			release:   "asserting-release.yaml",
			want:      "the admin password " + redact.Placeholder() + " must be supplied as a reference",
			condition: renderer.ErrAssertionFailed(),
		},
		{
			name:      "a Feature assertion quoting the leaf the Feature declares",
			release:   "feature-asserting-release.yaml",
			want:      "the credential origin " + redact.Placeholder() + " must name a secret resource",
			condition: renderer.ErrAssertionFailed(),
		},
		{
			name:    "a rendered Workload whose field the template filled from the declared value",
			release: "workload-release.yaml",
			want: "enum: " + redact.Placeholder() + ` does not satisfy enum ["Always","Never","IfNotPresent"]` +
				" (the value is withheld because the template that rendered this document reads a value declared sensitive)",
			condition: validator.ErrWorkloadValidation(),
		},
		{
			// The declaration is the attached Feature's, not the Component's, so
			// this row is the one that fails when a refusal's set is not widened
			// with the attachments the node holds itself.
			name:    "a rendered Workload whose field the template filled from an attachment the node holds itself",
			release: "keyholder-release.yaml",
			want: "enum: " + redact.Placeholder() + ` does not satisfy enum ["Always","Never","IfNotPresent"]` +
				" (the value is withheld because the template that rendered this document reads a value declared sensitive)",
			condition: validator.ErrWorkloadValidation(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			text, err := refusalText(t, c.release, false)
			if strings.Contains(text, refusalSecret) {
				t.Errorf("the refusal carries the declared value:\n%s", text)
			}
			if !strings.Contains(text, c.want) {
				t.Errorf("the refusal must read %q where the value stood:\n%s", c.want, text)
			}
			if !errors.Is(err, c.condition) {
				t.Errorf("the refusal lost its condition %v:\n%s", c.condition, text)
			}

			unredacted, _ := refusalText(t, c.release, true)
			if unredacted != text {
				t.Errorf("switching off the trace's withholding moved the refusal's text:\nwithheld trace: %s\nunredacted trace: %s", text, unredacted)
			}
		})
	}
}

// TestRefusalText_AnUndeclaredValueIsUnchanged is the control: the same refusal
// quoting a value no declaration covers reaches the text as the author wrote it.
func TestRefusalText_AnUndeclaredValueIsUnchanged(t *testing.T) {
	t.Parallel()
	text, _ := refusalText(t, "undeclared-release.yaml", false)
	if !strings.Contains(text, "the admin note "+refusalNote+" must not be written inline") {
		t.Errorf("a refusal quoting an undeclared value must reach the text unchanged:\n%s", text)
	}
	if strings.Contains(text, redact.Placeholder()) {
		t.Errorf("nothing declared was read, so nothing may be withheld:\n%s", text)
	}
}
