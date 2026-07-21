// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// mixedScopeComposeFixtureInput names the mixed-scope-compose fixture: a single
// compose-synthesized host with a component-scope FeatureRecipe attached whose
// pipeline carries BOTH an own-scope PATCH step and a host-scope PATCH step, and
// whose own provider is compose (an assembler). Compose merges per-node
// fragments into one docker-compose.yaml only at assembly, so the host's
// documents are pre-assembly fragments and the whole pipeline defers to the
// post-assembly phase. The host-scope step replays fine against the merged
// document, but the FeatureRecipe's own output is emitted as discrete fragment
// files and is never assembled — the deferred own-scope step has no merged own
// document to patch, so the build halts. Each step references a distinct Plugin
// ENTITY URI; both entities' spec.plugin .wasm artifacts are built on demand from
// the Rust PDK examples and staged co-located beside their Plugin entities in the
// catalog, so the build's entity-front dispatches each step to its own plugin
// through the co-located tier. When the wasm toolchain is absent the staging build
// skips the calling test.
func mixedScopeComposeFixtureInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	catalogPaths := stageColocatedCatalog(t, []string{
		"testdata/mixed-scope-compose/catalog/base",
		"testdata/mixed-scope-compose/catalog/recipes/compose",
	}, map[string]string{
		"frp_step/own-patch-compose":  "frp-step-own-patch-compose-rust.wasm",
		"frp_step/host-patch-compose": "frp-step-host-patch-compose-rust.wasm",
	})
	return pipelinetest.Input{
		ReleasePath:  "testdata/mixed-scope-compose/release.yaml",
		CatalogPaths: catalogPaths,
	}
}

// TestMixedScopeCompose_OwnStepHaltsWithClearDiagnostic compiles the
// mixed-scope-compose fixture and asserts the build halts with the scope-
// specific own-on-assembler diagnostic rather than the bare provider-
// incompatibility report. A component-scope FeatureRecipe's own output is
// emitted as discrete fragment files and is never assembled, so its deferred
// own-scope pipeline step has no merged own document to patch after assembly —
// a Loud Guardrail that must name the real cause and the remedies in user
// terms, not surface an empty-provider compatibility claim.
func TestMixedScopeCompose_OwnStepHaltsWithClearDiagnostic(t *testing.T) {
	in := mixedScopeComposeFixtureInput(t)
	t.Parallel()

	_, err := pipelinetest.BuildE(t, in)
	if err == nil {
		t.Fatalf("expected the mixed own+host FeatureRecipe pipeline on a compose host to halt; got no error")
	}

	// The halt reuses the assembler-fragment sentinel: the own target would be
	// an unassembled assembler fragment.
	if !errors.Is(err, renderer.ErrPipelinePatchPreAssembly) {
		t.Errorf("expected the halt to match ErrPipelinePatchPreAssembly; got:\n%v", err)
	}

	// The message names the real cause (FeatureRecipe own output emitted as
	// discrete fragment files and never assembled) and a remedy. cerrors.Wrap
	// renders only the outer frame in Error(); the inner cause surfaces to the
	// user through the CLI's chain renderer, so the assertion walks the chain.
	if !chainContains(err, "own-scope pipeline step") {
		t.Errorf("halt message does not name the own-scope pipeline step; got:\n%v", err)
	}
	if !chainContains(err, "discrete fragment files and is never assembled") {
		t.Errorf("halt message does not name the FR-own-unassembled cause; got:\n%v", err)
	}
	if !chainContains(err, "Remedy:") {
		t.Errorf("halt message does not offer a remedy; got:\n%v", err)
	}

	// It must NOT surface the bare provider-incompatibility report whose empty
	// provider name reads as a malformed claim.
	if errors.Is(err, renderer.ErrPluginProviderIncompatible) {
		t.Errorf("halt should not be the generic provider-incompatibility report; got:\n%v", err)
	}
	if chainContains(err, `does not list provider ""`) {
		t.Errorf("halt message still surfaces the bare empty-provider compatibility claim; got:\n%v", err)
	}

	// And it must NOT be the generic absent-target diagnostic — that path is
	// unreachable for this corner (the pre-walk compatibility gate fires first).
	if errors.Is(err, renderer.ErrPipelineTargetNotEmitted) {
		t.Errorf("halt should not be the generic ErrPipelineTargetNotEmitted; got:\n%v", err)
	}
}

// chainContains walks err via errors.Unwrap and returns true when any link's
// Error() contains needle. cerrors.Wrap renders only the outer frame in
// Error(); the CLI surfaces the inner cause through its chain renderer, so
// verifying inner-cause text in a test requires the same chain traversal.
func chainContains(err error, needle string) bool {
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if strings.Contains(cause.Error(), needle) {
			return true
		}
	}
	return false
}
