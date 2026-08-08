// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// producerInjectorChainInput names the producer-injector fixture's happy path: a
// helm-chart-sourced host component with a component-scope FeatureRecipe attached
// whose pipeline chains three steps in one per-node walk — a producer that EMITs
// a secret-reference document into its own set and RETURNS the secret's name, a
// secret-injector that reads that name through the inter-step channel
// (.steps.ref.outputs.secretName) and PATCHes the host workload to reference it,
// and a sidecar-injector that appends a companion container to the host workload.
// Each step references a Plugin ENTITY URI; the entity's .wasm artifact is built
// on demand from the Go PDK example and staged co-located beside its Plugin entity
// in the catalog so the build's entity-front resolves it. When the wasm toolchain
// is absent the staging build skips the calling test.
func producerInjectorChainInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	catalogPaths := stageColocatedCatalog(t, []string{
		"testdata/producer-injector/catalog/base",
		"testdata/producer-injector/catalog/recipes/helm-chart",
	}, map[string]string{
		"frp_step/secret-ref-producer": "secret-ref-producer-go.wasm",
		"frp_step/secret-injector":     "secret-injector-go.wasm",
		"frp_step/sidecar-injector":    "sidecar-injector-go.wasm",
	})
	return pipelinetest.Input{
		ReleasePath:  "testdata/producer-injector/release-chain.yaml",
		CatalogPaths: catalogPaths,
	}
}

// TestProducerInjector_Chain compiles the happy-path fixture and asserts the
// producer-to-injector chain ran end-to-end: the secret name the producer
// returned as .steps.ref.outputs.secretName reaches the host workload through the
// injector's PATCH, the sidecar-injector appended its container, the producer's
// emitted secret-reference document rendered, and the surgical host edits left
// the pre-existing chart content intact.
func TestProducerInjector_Chain(t *testing.T) {
	in := producerInjectorChainInput(t)
	t.Parallel()

	result := pipelinetest.Build(t, in)

	values := findArtifactBySuffix(t, result.Artifacts, "charts/api/values.yaml")
	got := string(values)
	t.Logf("rendered host values.yaml after producer->injector chain:\n%s", got)

	// The chain proof: the value the injector wrote into the host workload's
	// environment block is the secret name the PRODUCER returned as its output —
	// only the producer emits this string, so its presence here proves the value
	// flowed producer output -> .steps.ref.outputs.secretName -> injector `with`
	// -> host PATCH.
	if !strings.Contains(got, "INJECTED_SECRET_NAME:") {
		t.Errorf("host values.yaml missing the injected secret-name key; got:\n%s", got)
	}
	if !strings.Contains(got, "guardrail-managed-secret") {
		t.Errorf("host values.yaml missing the producer's secret name (the chain did not carry the producer output into the host PATCH); got:\n%s", got)
	}
	// The sidecar-injector added its companion container under the containers map.
	if !strings.Contains(got, "guardrail-sidecar") {
		t.Errorf("host values.yaml missing the added sidecar container; got:\n%s", got)
	}
	if !strings.Contains(got, "guardrail-agent:latest") {
		t.Errorf("host values.yaml missing the sidecar container image; got:\n%s", got)
	}
	// Byte-faithful node-span surgery: the leading comment and pre-existing
	// env var (with its single-quoted scalar style) survive verbatim.
	if !strings.Contains(got, "# Chart values for the api-server host component.") {
		t.Errorf("host values.yaml lost its leading comment after PATCH; got:\n%s", got)
	}
	if !strings.Contains(got, "EXISTING_VAR: 'keep-me'") {
		t.Errorf("host values.yaml lost a pre-existing env var (or its scalar style) after PATCH; got:\n%s", got)
	}

	// The producer's own-scope EMIT surfaced as a rendered artifact, and it names
	// the same secret the injector wired in — the two ends of the chain agree.
	ref := findArtifactBySuffix(t, result.Artifacts, "secret-ref.yaml")
	if !strings.Contains(string(ref), "secretName: guardrail-managed-secret") {
		t.Errorf("emitted secret-reference document missing the expected secret name; got:\n%s", ref)
	}

	// Content selection: Chart.yaml is not values.yaml, so neither injector
	// patched it.
	chart := findArtifactBySuffix(t, result.Artifacts, "charts/api/Chart.yaml")
	if strings.Contains(string(chart), "INJECTED_SECRET_NAME") || strings.Contains(string(chart), "guardrail-sidecar") {
		t.Errorf("Chart.yaml should carry no injected content; got:\n%s", chart)
	}
}

// TestProducerInjector_Deterministic compiles the happy-path fixture repeatedly
// and asserts byte-identical output across runs — the producer output stringify,
// the inter-step channel, and the two host PATCHes are order-preserving
// end-to-end.
func TestProducerInjector_Deterministic(t *testing.T) {
	in := producerInjectorChainInput(t)
	t.Parallel()
	pipelinetest.AssertDeterministic(t, in)
}

// TestProducerInjector_HaltOnBadInput compiles a variant whose injector step
// supplies a wrong-typed secretName (an integer where the export's declared input
// contract requires a string). The build must halt with the typed-I/O
// contract-violation guardrail before the guest runs.
func TestProducerInjector_HaltOnBadInput(t *testing.T) {
	catalogPaths := stageColocatedCatalog(t, []string{
		"testdata/producer-injector/catalog/base",
		"testdata/producer-injector/catalog/recipes/helm-chart",
	}, map[string]string{
		"frp_step/secret-injector": "secret-injector-go.wasm",
	})
	in := pipelinetest.Input{
		ReleasePath:  "testdata/producer-injector/release-badinput.yaml",
		CatalogPaths: catalogPaths,
	}
	t.Parallel()

	_, err := pipelinetest.BuildE(t, in)
	if err == nil {
		t.Fatal("expected the build to halt on the input-contract violation; got no error")
	}
	if !errors.Is(err, frp.ErrPipelineContractViolation()) {
		t.Errorf("expected ErrPipelineContractViolation in the error chain; got: %v", err)
	}
	// The user-facing top-level renders a single-line summary; the contract
	// detail lives one unwrap deeper. Read the deepest single-wrap layer for the
	// full diagnostic.
	detail := deepestWrapped(err)
	if !strings.Contains(detail.Error(), "does not satisfy the declared input contract") {
		t.Errorf("halt message does not name the input-contract violation; got: %v", detail)
	}
	// Surface the verbatim halt messages so the diagnostic a real build produces
	// is visible in the test log.
	t.Logf("HALT A (input contract) top-level: %v", err)
	t.Logf("HALT A (input contract) detail: %v", detail)
}

// TestProducerInjector_HaltOnBadOutput compiles a variant whose producer step
// returns outputs that violate the export's declared output contract (a number
// where a string secretName is required). The build must halt with the typed-I/O
// contract-violation guardrail after the guest runs.
func TestProducerInjector_HaltOnBadOutput(t *testing.T) {
	catalogPaths := stageColocatedCatalog(t, []string{
		"testdata/producer-injector/catalog/base",
		"testdata/producer-injector/catalog/recipes/helm-chart",
	}, map[string]string{
		"frp_step/secret-ref-producer-badoutput": "secret-ref-producer-badoutput-go.wasm",
	})
	in := pipelinetest.Input{
		ReleasePath:  "testdata/producer-injector/release-badoutput.yaml",
		CatalogPaths: catalogPaths,
	}
	t.Parallel()

	_, err := pipelinetest.BuildE(t, in)
	if err == nil {
		t.Fatal("expected the build to halt on the output-contract violation; got no error")
	}
	if !errors.Is(err, frp.ErrPipelineContractViolation()) {
		t.Errorf("expected ErrPipelineContractViolation in the error chain; got: %v", err)
	}
	detail := deepestWrapped(err)
	if !strings.Contains(detail.Error(), "do not satisfy the declared output contract") {
		t.Errorf("halt message does not name the output-contract violation; got: %v", detail)
	}
	t.Logf("HALT B (output contract) top-level: %v", err)
	t.Logf("HALT B (output contract) detail: %v", detail)
}

// deepestWrapped follows the single-wrap chain to the last layer errors.Unwrap
// can reach. The renderer's category wrappers render a single-line summary in
// their own Error(); the fuller diagnostic (the step's typed-I/O contract
// message) lives on the walk error they wrap, which is the deepest layer a
// single-error unwrap reaches.
func deepestWrapped(err error) error {
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err
		}
		err = u
	}
}
