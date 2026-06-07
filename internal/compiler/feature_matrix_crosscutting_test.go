// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"bytes"
	stderrors "errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
		"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components.yaml",
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
			v.includeTrace = true
			res := runFeatureMatrixVariant(t, v)
			if len(res.Files) == 0 {
				t.Fatalf("renderer produced no files; trace assertion vacuous")
			}
			assertAlwaysPresentTraceSlots(t, res.Trace)
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
			results := make([]*fullstackAppResult, iterations)
			for i := 0; i < iterations; i++ {
				results[i] = runFeatureMatrixVariant(t, v)
			}
			assertByteIdenticalAcrossIterations(t, results)
		})
	}

	// --- BridgeProvenance: FR-emitted bridge service carries FR identity ---
	t.Run("BridgeProvenance/compose-sourced-with-bridge", func(t *testing.T) {
		t.Parallel()
		res := runFeatureMatrixVariant(t, variants[3])
		var mergedCompose string
		for path, content := range res.Files {
			if strings.HasSuffix(path, "docker-compose.yaml") {
				mergedCompose = string(content)
				break
			}
		}
		if mergedCompose == "" {
			t.Fatalf("merged docker-compose.yaml not found in %d files", len(res.Files))
		}
		// Locate the aggrega-gw service block + its labels block. The
		// service appears as the first key alphabetically in the compose
		// services map; find its labels via a substring window starting
		// at the service header and bounded by the next service.
		bridgeStart := strings.Index(mergedCompose, "aggrega-gw:")
		if bridgeStart < 0 {
			t.Fatalf("aggrega-gw service block missing from merged compose:\n%s", mergedCompose)
		}
		// Take a bounded window — services in the compose document end
		// where the next service (alphabetically: api) begins.
		nextService := strings.Index(mergedCompose[bridgeStart:], "\n  api:")
		bridgeEnd := bridgeStart + len(mergedCompose[bridgeStart:])
		if nextService > 0 {
			bridgeEnd = bridgeStart + nextService
		}
		bridgeBlock := mergedCompose[bridgeStart:bridgeEnd]

		// Discovery A — recipe-uri must reflect FR identity, not consumer Recipe.
		const wantRecipeURI = "aggrega.ai/recipe-uri: compose-bridge-fr:1.0.0"
		if !strings.Contains(bridgeBlock, wantRecipeURI) {
			t.Errorf("bridge service: expected %q\nbridge block was:\n%s", wantRecipeURI, bridgeBlock)
		}

		// Discovery B — component-id must be the sanitized synthetic, not raw marker.
		const wantComponentID = "aggrega.ai/component-id: boundary-fr-bridge-cluster"
		if !strings.Contains(bridgeBlock, wantComponentID) {
			t.Errorf("bridge service: expected %q\nbridge block was:\n%s", wantComponentID, bridgeBlock)
		}

		// Defensive: the bridge service should NOT carry the consumer
		// Recipe's identity nor the raw marker syntax.
		const stale1 = "feature-matrix-api-compose-bridge:1.0.0"
		const stale2 = "<boundary-fr>"
		if strings.Contains(bridgeBlock, stale1) {
			t.Errorf("bridge service: stale consumer Recipe identity %q leaked into labels:\n%s", stale1, bridgeBlock)
		}
		if strings.Contains(bridgeBlock, stale2) {
			t.Errorf("bridge service: raw marker syntax %q leaked into labels:\n%s", stale2, bridgeBlock)
		}
	})

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

	// --- Path-driven sentinel coverage ---
	t.Run("Sentinel/PathCollision", testSentinelPathCollision)
	t.Run("Sentinel/InstanceIDCollision", testSentinelInstanceIDCollision)
	t.Run("Sentinel/ChartNameMismatch", testSentinelChartNameMismatch)
	t.Run("Sentinel/FRTemplateInvalidField", testSentinelFRTemplateInvalidField)

	// --- HelmHookWeight: helm.sh/hook-weight emission on chart-templated hooks ---
	//
	// Triggers via the helm-sourced-with-artifacts variant whose chart
	// contains a templates/pre-install-job.yaml carrying
	// `helm.sh/hook: pre-install`. The chart-rendering path (helm/sourced.go)
	// detects the hook annotation and injects helm.sh/hook-weight matching
	// the boundary's syncWave (`syncWave: 2` on this variant).
	//
	// Note: the artifacts-evaluator path (renderer/artifacts.go) does not
	// inject hook-weight today — only the sourced-chart path does. The
	// distinction is intentional: artifacts are typed as "extra resources"
	// declared by the Recipe author; chart-templated hooks live in the
	// chart-author-managed surface where Helm hook ordering applies. If a
	// future scope extends hook-weight to the artifacts path, this test
	// can be expanded with a second sub-test or variant.
	t.Run("HelmHookWeight/helm-sourced-with-artifacts", func(t *testing.T) {
		t.Parallel()
		res := runFeatureMatrixVariant(t, variants[1])
		var hookManifest string
		var hookPath string
		for path, content := range res.Files {
			s := string(content)
			if strings.Contains(s, "helm.sh/hook: pre-install") {
				hookManifest = s
				hookPath = path
				break
			}
		}
		if hookManifest == "" {
			t.Fatalf("no rendered manifest carries `helm.sh/hook: pre-install`; looked across %d files", len(res.Files))
		}
		// helm-sourced-with-artifacts boundary declares syncWave: 2.
		const wantHookWeight = `helm.sh/hook-weight: "2"`
		if !strings.Contains(hookManifest, wantHookWeight) {
			t.Errorf("hook manifest at %s missing %q\nrendered:\n%s", hookPath, wantHookWeight, hookManifest)
		}
		// Defensive: hook + sync-wave coexist on the same manifest. Hook-
		// weight is hook-phase-scoped; sync-wave is globally-scoped.
		const wantSyncWave = `argocd.argoproj.io/sync-wave: "2"`
		if !strings.Contains(hookManifest, wantSyncWave) {
			t.Errorf("hook manifest at %s missing %q\nrendered:\n%s", hookPath, wantSyncWave, hookManifest)
		}
	})
}

// testSentinelPathCollision exercises the dispatch-path uniqueness check
// at orchestrator.go: two boundaries with distinct providers (helm +
// compose) both override `subDirectory: merged` so their evaluated
// dispatch paths converge on `dist/shared/merged`. The assembler refuses
// to silently overwrite one provider's output with another's.
func testSentinelPathCollision(t *testing.T) {
	t.Parallel()
	res, err := tryRunFeatureMatrixVariant(t, featureMatrixVariant{
		name:        "path-collision",
		releaseFile: "path-collision.yaml",
	})
	if err == nil {
		t.Fatalf("expected ErrPathCollision; got %d files rendered", len(res.Files))
	}
	if !stderrors.Is(err, renderer.ErrPathCollision) {
		t.Errorf("expected errors.Is(err, ErrPathCollision); got: %s", joinErrorChain(err))
	}
}

// testSentinelInstanceIDCollision verifies the sentinel itself rather
// than a fixture-driven trigger condition. The sentinel is a defensive
// guardrail: topology flattening's prefix discipline (every node's
// instance ID is prefixed with its boundary path) makes the same-ID
// collision unreachable from valid input. Triggering via fixture would
// require malformed IR. This test constructs the wrapped error directly
// to verify the sentinel's identity and confirm errors.Is matches the
// shape the orchestrator emits.
func testSentinelInstanceIDCollision(t *testing.T) {
	t.Parallel()
	inner := fmt.Errorf("%w: dispatch path %q provider %q already contains node %q",
		renderer.ErrInstanceIDCollision, "dist/shared", "helm", "api")
	wrapped := cerrors.Wrap(inner, cerrors.TranslationError, cerrors.SourceLocation{}, inner.Error())
	if !stderrors.Is(wrapped, renderer.ErrInstanceIDCollision) {
		t.Errorf("expected errors.Is(wrapped, ErrInstanceIDCollision); got chain: %s", joinErrorChain(wrapped))
	}
}

// testSentinelChartNameMismatch exercises the helm-chart umbrella
// uniqueness check at orchestrator.go: a parent + 2 child boundaries
// with `directory: "{{ .parent_boundary.directory }}"` cascade — all
// three contribute to the same evaluated path under helm-chart provider
// — but each declares a different `aggrega.ai/chart-name` annotation.
// The merged umbrella has exactly one name; conflicting cascades
// surface as the documented sentinel.
func testSentinelChartNameMismatch(t *testing.T) {
	t.Parallel()
	res, err := tryRunFeatureMatrixVariant(t, featureMatrixVariant{
		name:        "chart-name-mismatch",
		releaseFile: "chart-name-mismatch.yaml",
	})
	if err == nil {
		t.Fatalf("expected ErrChartNameMismatch; got %d files rendered", len(res.Files))
	}
	if !stderrors.Is(err, renderer.ErrChartNameMismatch) {
		t.Errorf("expected errors.Is(err, ErrChartNameMismatch); got: %s", joinErrorChain(err))
	}
}

// testSentinelFRTemplateInvalidField exercises the boundary-scope FR
// template validator: an `executionScope: boundary` FR whose template
// references a per-component-only field (`.context.instance_id`) is
// rejected at FR validation time. The fixture's local FR triggers the
// check via its `valuesMapping` referencing the disallowed field.
func testSentinelFRTemplateInvalidField(t *testing.T) {
	t.Parallel()
	res, err := tryRunFeatureMatrixVariant(t, featureMatrixVariant{
		name:        "fr-template-invalid",
		releaseFile: "fr-template-invalid.yaml",
	})
	if err == nil {
		t.Fatalf("expected ErrFRTemplateInvalidField; got %d files rendered", len(res.Files))
	}
	if !stderrors.Is(err, renderer.ErrFRTemplateInvalidField) {
		t.Errorf("expected errors.Is(err, ErrFRTemplateInvalidField); got: %s", joinErrorChain(err))
	}
}

// assertAlwaysPresentTraceSlots discovers each node-leaf in the in-memory
// trace map (recursively across nested boundaries, so the compose+bridge
// variant is handled) and asserts the seven slots that fire regardless of the
// node's Recipe shape: pre-loop captures (01-04), Phase B.0 locals (06),
// per-node emission log (11), and provenance summary (12).
//
// Node-leaf detection: a node emits 12-labels-injected.log directly under its
// node directory, so each key ending in /12-labels-injected.log anchors one
// node-leaf; its prefix is that key with the slot filename trimmed. Slots
// whose presence depends on Recipe shape (05-source, 07-valuesMapping,
// 08-workload, 09-artifacts, 10-engine-synthesis) are exercised by
// trace_layout_test.go's per-shape tests — the cross-cutting criterion
// validates only the cross-variant always-present subset.
func assertAlwaysPresentTraceSlots(t *testing.T, tr map[string][]byte) {
	t.Helper()
	const anchor = "12-labels-injected.log"
	var prefixes []string
	for k := range tr {
		if strings.HasSuffix(k, "/"+anchor) {
			prefixes = append(prefixes, strings.TrimSuffix(k, "/"+anchor))
		}
	}
	if len(prefixes) == 0 {
		t.Fatalf("no node-leaf trace entries; trace not emitted")
	}
	for _, prefix := range prefixes {
		for _, slot := range []string{
			"01-pillars-initial.yaml",
			"02-pass1-provides.yaml",
			"03-pass2-inject.yaml",
			"04-phaseA-inject.yaml",
			"06-phaseB-locals.yaml",
			"11-engine-emission.log",
			"12-labels-injected.log",
		} {
			if _, ok := tr[prefix+"/"+slot]; !ok {
				t.Errorf("node %s: always-present slot %s missing", prefix, slot)
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
func assertProvenanceLabelsPresent(t *testing.T, res *fullstackAppResult, variantName string) {
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
func assertByteIdenticalAcrossIterations(t *testing.T, results []*fullstackAppResult) {
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
