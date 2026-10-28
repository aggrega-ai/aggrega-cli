// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// TestValueTemplateNumbers_E2E builds, through the real pipeline, a Release whose
// Aggregate writes numbers — a 0, a 1, a value below one millionth and one of
// 10^21 — and whose Release writes an operator 0, and reads what the Recipe's
// workload template and the component's own self-inject value: templates
// rendered from them. Each number is read as a number on both surfaces: a 0 is
// empty to `default`, false to `if`, equal to a 0 written in the template and
// below 1; an integer compares with a decimal; and a non-integral number prints
// in decimal form rather than in exponent form.
func TestValueTemplateNumbers_E2E(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath: "testdata/value-template-numbers/release.yaml",
		CatalogPaths: []string{
			"testdata/value-template-numbers/catalog/components",
			"testdata/value-template-numbers/catalog/recipes",
			"testdata/value-template-numbers/catalog/providers",
		},
	})
	merged := joinFiles(result.Artifacts)
	for _, want := range []string{
		`DEFAULT: "1"`,
		`IF: "false"`,
		`EQ: "true"`,
		`LT: "true"`,
		`MIXED: "true"`,
		`INFRA_DEFAULT: "1"`,
		`SMALL: "0.0000001"`,
		`LARGE: "1000000000000000000000"`,
		`SI_DEFAULT: "1"`,
		`SI_LT: "true"`,
		`SI_SMALL: "0.0000001"`,
	} {
		if !strings.Contains(merged, want) {
			t.Errorf("expected %q in the rendered output; got:\n%s", want, merged)
		}
	}
}
