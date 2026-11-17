// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
)

// ociModuleRecipe is the Recipe the workspace carries, its source block written by
// the row.
func ociModuleRecipe(kind, source string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  source:
    kind: ` + kind + `
` + source + `  valuesMapping: |
    {}
`
}

// TestPullRefusesAMalformedOCIModuleSourceBeforeAnyFetch pulls an offline workspace
// whose Recipe writes an OCI module source. A source naming more than one artifact
// is refused under the OCI module source condition as the Recipe is read; a
// well-formed one reaches the fetch, which the offline workspace refuses.
func TestPullRefusesAMalformedOCIModuleSourceBeforeAnyFetch(t *testing.T) {
	rows := []struct {
		name      string
		kind      string
		source    string
		malformed bool
	}{
		{"a tag in the url", sourcekind.TerraformModuleOCI, "    url: oci://127.0.0.1:9/modules/net:1.0.0\n    version: 1.0.0\n", true},
		{"chart written", sourcekind.TerraformModuleOCI, "    url: oci://127.0.0.1:9/modules/net\n    version: 1.0.0\n    chart: net\n", true},
		{"a well-formed source (control)", sourcekind.TerraformModuleOCI, "    url: oci://127.0.0.1:9/modules/net\n    version: 1.0.0\n", false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root, release := writeSourceKindWorkspace(t, row.kind)
			writeFixture(t, filepath.Join(root, "catalog/recipe/helm/svc/1.0.0/recipe.yaml"), ociModuleRecipe(row.kind, row.source))
			t.Chdir(root)
			var diag, out bytes.Buffer
			err := pullRun(context.Background(), pullConfig{
				ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				ReleasePath: release,
				DiagOut:     &diag,
				Out:         &out,
			})
			if err == nil {
				t.Fatalf("the pull succeeded\n%s", diag.String())
			}
			if !row.malformed {
				if !errors.Is(err, egress.ErrOfflineEgress()) {
					t.Fatalf("want the offline refusal at the fetch; got %v\n%s", err, diag.String())
				}
				return
			}
			if errors.Is(err, egress.ErrOfflineEgress()) {
				t.Fatalf("the pull reached the fetch: %v", err)
			}
			if !refusedForOCIModuleSource(err) {
				t.Fatalf("want the OCI module source condition; got %v\n%s", err, diag.String())
			}
			if head := formatCompilerError(err); !bytes.Contains([]byte(head), []byte("E0379")) {
				t.Errorf("the printed refusal does not carry E0379:\n%s", head)
			}
		})
	}
}

// refusedForOCIModuleSource reports whether err is led by the OCI module source
// condition, carried on the chain or promoted from a skipped-at-load report.
func refusedForOCIModuleSource(err error) bool {
	if errors.Is(err, sourcekind.ErrOCIModuleSourceMalformed()) {
		return true
	}
	var skipped *compilerops.SkipContextError
	if !errors.As(err, &skipped) {
		return false
	}
	refusal, promoted := skipped.PromotedRefusal()
	return promoted && errors.Is(refusal.Err, sourcekind.ErrOCIModuleSourceMalformed())
}
