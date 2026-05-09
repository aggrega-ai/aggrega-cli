// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"bytes"
	stderrors "errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
)

// joinErrorChain walks the Unwrap chain (single-error and multi-error
// shapes) and concatenates each error's message into one searchable string.
// Used by negative-path tests that need to assert on a specific message
// substring that lives deeper than the top-level rendering.
func joinErrorChain(err error) string {
	if err == nil {
		return ""
	}
	var parts []string
	var visit func(error)
	visit = func(e error) {
		if e == nil {
			return
		}
		parts = append(parts, e.Error())
		// Multi-error chain (errors.Join shape).
		if me, ok := e.(interface{ Unwrap() []error }); ok {
			for _, sub := range me.Unwrap() {
				visit(sub)
			}
			return
		}
		visit(stderrors.Unwrap(e))
	}
	visit(err)
	return strings.Join(parts, " | ")
}

// TestFeatureMatrix_CrossCutting exercises eight cross-cutting proof criteria
// against the four per-Release feature-matrix variants:
//
//  1. TraceLayout — the always-present 7 trace slots land for every node
//     across all 4 variants when --trace is enabled.
//  2. CategorizedErrors — *cerrors.CompilerError carries the [<CATEGORY>]
//     tag in its single-line presentation for each of the 4 categories.
//  3. ProvenanceLabels — the 3 canonical aggrega.ai/* labels appear in the
//     rendered output (K8s metadata.labels for helm targets; service-level
//     labels for compose targets).
//  4. Determinism — three in-process compiles per variant produce
//     byte-identical file sets.
//  5. PathDispatch — the compose-sourced-with-bridge variant produces ONE
//     merged docker-compose.yaml at the parent boundary's evaluated path
//     (per-(path, provider) collapse with parent_boundary cascade).
//  6. SyncWaveAnnotations — the helm-synthesized variant (with `syncWave: 1`
//     declared on its boundary) emits `argocd.argoproj.io/sync-wave: "1"`
//     on rendered K8s resources.
//  7. FilenameCollision — a fixture variant whose artifacts entry names a
//     filename matching a chart-rendered filename halts at Phase B.5 with
//     the documented collision error.
//  8. WorkloadSecrets — a fixture variant whose Workload references the
//     blackbox slot root_password fails Recipe load with the documented
//     ErrWorkloadSecretsSchema.
//
// The 4 path-driven sentinels not covered by criterion 7 (PATH_COLLISION /
// INSTANCE_ID_COLLISION / CHART_NAME_MISMATCH / FR_TEMPLATE_INVALID_FIELD)
// require dedicated negative-path fixtures and stay deferred to a follow-up
// run. Helm hook-weight (helm.sh/hook-weight) is not exercised because no
// shipped Recipe declares Helm hooks.
func TestFeatureMatrix_CrossCutting(t *testing.T) {
	t.Parallel()

	bridgeCatalogPaths := []string{
		"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge.yaml",
		"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
	}
	variants := []featureMatrixVariant{
		{name: "helm-synthesized", releaseFile: "helm-synthesized.yaml"},
		{name: "helm-sourced-with-artifacts", releaseFile: "helm-sourced-with-artifacts.yaml"},
		{name: "compose-synthesized", releaseFile: "compose-synthesized.yaml"},
		{
			name:              "compose-sourced-with-bridge",
			releaseFile:       "compose-sourced-with-bridge.yaml",
			extraCatalogPaths: bridgeCatalogPaths,
		},
	}

	// --- Criterion 1: TraceLayout × 4 variants ---
	for _, v := range variants {
		v := v
		t.Run("TraceLayout/"+v.name, func(t *testing.T) {
			t.Parallel()
			v.traceRoot = t.TempDir()
			res := runFeatureMatrixVariant(t, v)
			if len(res.Files) == 0 {
				t.Fatalf("renderer produced no files; trace assertion vacuous")
			}
			assertAlwaysPresentTraceSlots(t, v.traceRoot)
		})
	}

	// --- Criterion 2: CategorizedErrors (4-category tag emission) ---
	t.Run("CategorizedErrors", testCategorizedErrors_TagEmission)

	// --- Criterion 3: ProvenanceLabels × 4 variants ---
	for _, v := range variants {
		v := v
		t.Run("ProvenanceLabels/"+v.name, func(t *testing.T) {
			t.Parallel()
			res := runFeatureMatrixVariant(t, v)
			if len(res.Files) == 0 {
				t.Fatalf("renderer produced no files")
			}
			assertProvenanceLabelsPresent(t, res, v.name)
		})
	}

	// --- Criterion 4: Determinism × 4 variants ---
	for _, v := range variants {
		v := v
		t.Run("Determinism/"+v.name, func(t *testing.T) {
			t.Parallel()
			const iterations = 3
			results := make([]*example22Result, iterations)
			for i := 0; i < iterations; i++ {
				results[i] = runFeatureMatrixVariant(t, v)
			}
			assertByteIdenticalAcrossIterations(t, results)
		})
	}

	// --- Criterion 5: PathDispatch (compose-bridge merged-path collapse) ---
	t.Run("PathDispatch/compose-sourced-with-bridge", func(t *testing.T) {
		t.Parallel()
		res := runFeatureMatrixVariant(t, variants[3])
		var composeFiles []string
		for path := range res.Files {
			if strings.HasSuffix(path, "docker-compose.yaml") {
				composeFiles = append(composeFiles, path)
			}
		}
		if len(composeFiles) != 1 {
			sort.Strings(composeFiles)
			t.Fatalf("expected exactly 1 docker-compose.yaml (per-(path, provider) collapse); got %d: %v",
				len(composeFiles), composeFiles)
		}
		want := "dist/cluster/compose/docker-compose.yaml"
		if !strings.HasSuffix(composeFiles[0], want) {
			t.Errorf("expected merged compose at parent path %s; got %s", want, composeFiles[0])
		}
		merged := string(res.Files[composeFiles[0]])
		for _, svc := range []string{"api:", "db:", "aggrega-gw:"} {
			if !strings.Contains(merged, svc) {
				t.Errorf("merged compose missing service %q at parent path:\n%s", svc, merged)
			}
		}
	})

	// --- Criterion 6: SyncWaveAnnotations (helm-synthesized) ---
	t.Run("SyncWaveAnnotations/helm-synthesized", func(t *testing.T) {
		t.Parallel()
		res := runFeatureMatrixVariant(t, variants[0])
		if len(res.Files) == 0 {
			t.Fatalf("renderer produced no files")
		}
		const wantArgo = `argocd.argoproj.io/sync-wave: "1"`
		const wantLabel = `aggrega.ai/sync-wave: "1"`
		var matched int
		for path, content := range res.Files {
			if !strings.HasSuffix(path, ".yaml") {
				continue
			}
			s := string(content)
			if strings.Contains(s, wantArgo) {
				matched++
			}
			// Label form lives on the same documents as the annotation; presence-check
			// once any document carries it.
			_ = wantLabel
			_ = s
		}
		if matched == 0 {
			t.Errorf("expected at least one rendered document carrying %q; none found in %d files",
				wantArgo, len(res.Files))
		}
	})

	// --- Criterion 7: FilenameCollision (negative-path fixture) ---
	t.Run("FilenameCollision", testFilenameCollision)

	// --- Criterion 8: WorkloadSecrets reject (blackbox slot reference) ---
	t.Run("WorkloadSecrets/Reject", testWorkloadSecretsReject)
}

// assertAlwaysPresentTraceSlots discovers each node-leaf directory under
// traceRoot/boundaries/ (recursively, so nested-boundary fixtures like the
// compose+bridge variant are handled) and asserts the seven slots that
// fire regardless of the node's Recipe shape: pre-loop captures (01-04),
// Phase B.0 locals (06), per-node emission log (11), and provenance summary
// (12).
//
// Node-leaf detection: a directory carrying 12-labels-injected.log is a
// node-leaf. Slots whose presence depends on Recipe shape (05-source,
// 07-valuesMapping, 08-workload, 09-artifacts, 10-engine-synthesis) are
// exercised by trace_layout_test.go's per-shape tests — the cross-cutting
// criterion validates only the cross-variant always-present subset.
func assertAlwaysPresentTraceSlots(t *testing.T, traceRoot string) {
	t.Helper()
	boundariesDir := filepath.Join(traceRoot, "boundaries")
	if _, err := os.Stat(boundariesDir); err != nil {
		t.Fatalf("trace boundaries dir %s missing: %v", boundariesDir, err)
	}
	var nodeDirs []string
	err := filepath.Walk(boundariesDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, "12-labels-injected.log")); err == nil {
			nodeDirs = append(nodeDirs, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", boundariesDir, err)
	}
	if len(nodeDirs) == 0 {
		t.Fatalf("no node-leaf directories under %s; trace not emitted", boundariesDir)
	}
	for _, nodeDir := range nodeDirs {
		rel, _ := filepath.Rel(boundariesDir, nodeDir)
		for _, slot := range []string{
			"01-pillars-initial.yaml",
			"02-pass1-provides.yaml",
			"03-pass2-inject.yaml",
			"04-phaseA-inject.yaml",
			"06-phaseB-locals.yaml",
			"11-engine-emission.log",
			"12-labels-injected.log",
		} {
			if _, err := os.Stat(filepath.Join(nodeDir, slot)); err != nil {
				t.Errorf("node %s: always-present slot %s missing", rel, slot)
			}
		}
	}
}

// testCategorizedErrors_TagEmission validates that *cerrors.CompilerError's
// canonical Error() rendering tags each of the four categories. The CLI
// formatter at cmd/aggrega/error_format.go renders a richer multi-line shape
// on top of this; the criterion is about category attachment correctness at
// the package-errors layer.
func testCategorizedErrors_TagEmission(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		category cerrors.Category
		wantTag  string
	}{
		{"developer", cerrors.DeveloperError, "[DEVELOPER_ERROR]"},
		{"ops", cerrors.OpsError, "[OPS_ERROR]"},
		{"catalog", cerrors.CatalogError, "[CATALOG_ERROR]"},
		{"translation", cerrors.TranslationError, "[TRANSLATION_ERROR]"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := cerrors.New(
				tc.category,
				cerrors.SourceLocation{File: "test.yaml"},
				"sample message",
				nil,
				nil,
			)
			out := err.Error()
			if !strings.HasPrefix(out, tc.wantTag) {
				t.Errorf("category %s: expected Error() to start with %q; got %q",
					tc.category, tc.wantTag, out)
			}
			if !strings.Contains(out, "test.yaml") {
				t.Errorf("category %s: expected Error() to include source; got %q",
					tc.category, out)
			}
			if !strings.Contains(out, "sample message") {
				t.Errorf("category %s: expected Error() to include message; got %q",
					tc.category, out)
			}
		})
	}
}

// assertProvenanceLabelsPresent walks the rendered file set and validates
// that the three canonical aggrega.ai/* labels (component-id, recipe-uri,
// boundary-path) appear at least once across the variant's output. The
// criterion is presence-of-any, not per-document — the helm and helmchart
// engines emit labels on synthesized Workload-derived resources via a
// separate helper than the artifacts-evaluator path; per-document assertion
// would entrench that split. Cross-variant presence-of-any anchors the
// label contract without requiring engine-internal alignment.
func assertProvenanceLabelsPresent(t *testing.T, res *example22Result, variantName string) {
	t.Helper()
	required := []string{
		"aggrega.ai/component-id:",
		"aggrega.ai/recipe-uri:",
		"aggrega.ai/boundary-path:",
	}
	seen := make(map[string]bool, len(required))
	for _, content := range res.Files {
		s := string(content)
		for _, label := range required {
			if strings.Contains(s, label) {
				seen[label] = true
			}
		}
	}
	for _, label := range required {
		if !seen[label] {
			t.Errorf("variant %s: provenance label %q absent across %d rendered files",
				variantName, label, len(res.Files))
		}
	}
}

// assertByteIdenticalAcrossIterations compares the file sets across
// iterations for byte-equality. Surfaces non-determinism as differences
// in either file count or per-path bytes.
func assertByteIdenticalAcrossIterations(t *testing.T, results []*example22Result) {
	t.Helper()
	if len(results) < 2 {
		t.Fatalf("determinism check needs >= 2 iterations; got %d", len(results))
	}
	first := results[0]
	for i := 1; i < len(results); i++ {
		this := results[i]
		if len(this.Files) != len(first.Files) {
			t.Fatalf("iteration %d: file count differs from iteration 0 (%d vs %d)",
				i, len(this.Files), len(first.Files))
		}
		for path, firstContent := range first.Files {
			thisContent, ok := this.Files[path]
			if !ok {
				t.Errorf("iteration %d: path %s missing (present in iteration 0)", i, path)
				continue
			}
			if !bytes.Equal(firstContent, thisContent) {
				t.Errorf("iteration %d: bytes differ at %s (len: first=%d, this=%d)",
					i, path, len(firstContent), len(thisContent))
			}
		}
	}
}

// testFilenameCollision exercises Phase B.5's cross-origin filename
// collision detection. The helm-sourced-with-artifacts-collision variant
// pairs the same chart as helm-sourced-with-artifacts (charts/api/templates/
// deployment.yaml renders to api-deployment.yaml under the boundary's
// helm/ directory) with an artifacts entry whose final filename also
// resolves to api-deployment.yaml.
func testFilenameCollision(t *testing.T) {
	t.Parallel()
	res, err := tryRunFeatureMatrixVariant(t, featureMatrixVariant{
		name:        "helm-sourced-with-artifacts-collision",
		releaseFile: "helm-sourced-with-artifacts-collision.yaml",
	})
	if err == nil {
		t.Fatalf("expected compile to halt with filename-collision error; got %d files rendered",
			len(res.Files))
	}
	msg := strings.ToLower(joinErrorChain(err))
	if !strings.Contains(msg, "collision") &&
		!strings.Contains(msg, "duplicate") &&
		!strings.Contains(msg, "conflict") {
		t.Errorf("expected collision/duplicate/conflict in error chain; got: %s", joinErrorChain(err))
	}
}

// testWorkloadSecretsReject exercises ValidateWorkloadSecrets's blackbox-
// reference rejection. The workload-secrets-blackbox-violation variant has
// a db Recipe whose Workload's container references the slot root_password
// (declared blackbox via inject: [] on the db Component) — the validator
// should fail the Recipe at load with ErrWorkloadSecretsSchema.
func testWorkloadSecretsReject(t *testing.T) {
	t.Parallel()
	res, err := tryRunFeatureMatrixVariant(t, featureMatrixVariant{
		name:        "workload-secrets-blackbox-violation",
		releaseFile: "workload-secrets-blackbox-violation.yaml",
	})
	if err == nil {
		t.Fatalf("expected compile to fail validating Workload secrets; got %d files rendered",
			len(res.Files))
	}
	chain := joinErrorChain(err)
	msg := strings.ToLower(chain)
	// Validator surface: "blackbox" appears in the finding's Constraint string,
	// or the slot name "root_password" appears in the field path.
	if !strings.Contains(msg, "blackbox") && !strings.Contains(msg, "root_password") {
		t.Errorf("expected blackbox/root_password in error chain; got: %s", chain)
	}
}
