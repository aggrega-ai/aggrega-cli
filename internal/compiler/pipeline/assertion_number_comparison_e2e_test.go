// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proof that an assertion comparing a number an author wrote with a
// number literal — the form the assertion pages teach — builds, through the real
// pipeline, at both stages assertions run: a Component's against the finalised
// graph and a Recipe's once each node's locals and provides are resolved.

package pipeline_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const assertionNumberDir = "testdata/assertion-number-comparison/"

// TestAnAssertionComparingAnAuthoredNumberWithALiteralBuilds builds one Component
// whose assertion reads `lt .values.replicas 2` and whose Recipe's assertion reads
// `gt .values.replicas 10.5`, once with a value both hold for and once with a
// value each one refuses. The row that holds builds and renders; each other row
// halts on the author's own fail and its sentence, at the stage that assertion
// runs — never on the comparison itself.
//
// 🔴 PLANTED DEFECT: drop gotmpl.AssertionComparisons from EvaluateAssertion's
// function set (internal/compiler/renderer/assertions.go): every row halts on the
// comparison, naming two Go types, before any fail runs.
func TestAnAssertionComparingAnAuthoredNumberWithALiteralBuilds(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		variant string
		halt    string // the author's sentence the build halts on; empty where it builds
	}{
		{"holds", ""},
		{"below", "replicas must be at least 2"},
		{"above", "replicas must be at most 10"},
	} {
		t.Run(row.variant, func(t *testing.T) {
			t.Parallel()
			result, err := pipelinetest.BuildE(t, pipelinetest.Input{
				ReleasePath:  assertionNumberDir + "release-" + row.variant + ".yaml",
				CatalogPaths: []string{assertionNumberDir + "catalog/base"},
			})
			if row.halt == "" {
				if err != nil {
					t.Fatalf("the build halted: %s", cerrors.CauseChain(err))
				}
				if result == nil || len(result.Artifacts) == 0 {
					t.Fatal("the build rendered nothing")
				}
				return
			}
			if err == nil {
				t.Fatalf("the build succeeded; want the halt %q", row.halt)
			}
			if !errors.Is(err, renderer.ErrAssertionFailed()) || !renderer.AssertionCalledFail(err) {
				t.Fatalf("want the author's fail to halt the build; got %s", cerrors.CauseChain(err))
			}
			if chain := cerrors.CauseChain(err); !strings.Contains(chain, row.halt) {
				t.Errorf("the halt does not carry %q: %s", row.halt, chain)
			}
		})
	}
}
