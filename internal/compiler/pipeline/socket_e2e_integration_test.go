// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// proofMutatorLabelKey is the label the mutator-proof fixture injects on every
// node; proofMutatorLabelValue is its value. They mirror the fixture's
// constants so the assertion tracks what the guest actually writes.
const (
	proofMutatorLabelKey   = "proof.aggrega/touched"
	proofMutatorLabelValue = "mutator-proof"
)

// loadFullstackForSockets loads the shared fullstack-app fixture exactly as the
// golden-path test does, returning the inputs, lookups, and loaders RunPipeline
// consumes. The socket tests clone the returned inputs and set the plugin
// attachments + lookup per scenario.
func loadFullstackForSockets(t *testing.T) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	rel, err := resolver.LoadRelease(fullstackReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(fullstackManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggBytes, err := os.ReadFile(fullstackAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.LoadAll("", fullstackCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	inputs := pipeline.Inputs{
		Release:           rel,
		Manifest:          manifest,
		AggregateRawBytes: aggBytes,
		AggregatePath:     fullstackAggregatePath,
		WorkspaceConfig:   workspaceconfig.Default(),
		CompilerVersion:   fixtureCompilerVersion,
	}
	return inputs, cat.CatalogLookup(), cat.RecipeLookupWithManifest(manifest), cat.SubAggregateLoader()
}

// fixtureSocketLookup returns a plugin lookup that resolves each configured URI
// to its built fixture's bytes and delegates every other URI to the
// entity-fronted embedded cascade — the same Plugin-entity translation
// (entity URI → spec.plugin artifact reference + capability claim) the build's
// own lookup applies, so a provider entity URI the render resolves reaches its
// embedded guest bytes and its authored capability claim. A bare resolver would
// consult the embedded tier but skip the entity translation, so a provider
// guest would resolve without its capability claim and the render-stage
// compatibility check would reject it; the socket tests render through the Wasm
// providers, so they need the same entity front the build uses.
func fixtureSocketLookup(t *testing.T, catalogLookup resolver.CatalogLookup, uriToArtifact map[string]string) pluginpath.Lookup {
	t.Helper()
	bytesByURI := make(map[string][]byte, len(uriToArtifact))
	for uri, artifact := range uriToArtifact {
		bytesByURI[uri] = wasmtest.EnsureFixtureBytes(t, artifact)
	}
	entityCascade := pluginpath.EntityLookup(func(uri string) (string, *pluginpath.PluginCapabilityClaim, *pluginpath.PluginRuntimeLimits, bool) {
		if catalogLookup.Plugin == nil {
			return "", nil, nil, false
		}
		entity, ok := catalogLookup.Plugin(uri)
		if !ok {
			return "", nil, nil, false
		}
		return entity.Spec.Plugin, resolver.PluginCapabilityClaim(entity), resolver.PluginRuntimeLimits(entity), true
	}, (pluginpath.Resolver{}).Resolve)
	return func(uri string) (pluginpath.ResolvedPlugin, error) {
		if b, ok := bytesByURI[uri]; ok {
			return pluginpath.ResolvedPlugin{Bytes: b, URI: uri}, nil
		}
		return entityCascade(uri)
	}
}

// TestMutatorSocketInjectsLabelE2E proves the Step-2 mutator socket runs a
// configured plugin end-to-end: the injected label reaches the IR graph on
// every node, and two builds of the same input produce byte-identical
// artifacts (determinism holds with the socket in the pipeline).
func TestMutatorSocketInjectsLabelE2E(t *testing.T) {
	ctx := context.Background()

	build := func() *pipeline.Result {
		inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
		inputs.WorkspaceConfig.Plugins.Mutator = []workspaceconfig.PluginRef{{URI: "proof://mutator"}}
		inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{"proof://mutator": "mutator-proof-go.wasm"})
		res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg,
			pipeline.Options{})
		if err != nil {
			t.Fatalf("RunPipeline with mutator: %v", err)
		}
		return res
	}

	res := build()
	if res.IRGraph == nil || res.IRGraph.Spec.Nodes.Len() == 0 {
		t.Fatalf("build produced no IR nodes; assertion would be vacuous")
	}
	injected := 0
	for nodeID, node := range res.IRGraph.Spec.Nodes.All() {
		v, ok := node.Labels.Get(proofMutatorLabelKey)
		if !ok {
			t.Errorf("node %q missing injected label %q", nodeID, proofMutatorLabelKey)
			continue
		}
		if v != proofMutatorLabelValue {
			t.Errorf("node %q label %q = %q, want %q", nodeID, proofMutatorLabelKey, v, proofMutatorLabelValue)
		}
		injected++
	}
	if injected == 0 {
		t.Fatalf("mutator socket injected no labels")
	}

	// Determinism: a second build of identical input yields byte-identical
	// artifacts with the socket active.
	res2 := build()
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

// TestPolicySocketAllowE2E proves the Step-4 policy socket lets a build succeed
// when the configured plugin passes every rule.
func TestPolicySocketAllowE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	inputs.WorkspaceConfig.Plugins.Policy = []workspaceconfig.PluginRef{{URI: "proof://policy-allow"}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{"proof://policy-allow": "policy-validator-allow-go.wasm"})

	res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg,
		pipeline.Options{})
	if err != nil {
		t.Fatalf("RunPipeline with all-pass policy: unexpected halt: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Fatalf("all-pass policy build produced no artifacts")
	}
}

// TestPolicySocketHaltE2E proves the Step-4 policy socket halts the build when
// the configured plugin fails a rule, and that the halt carries the plugin's
// message and the ErrPolicyViolation sentinel.
func TestPolicySocketHaltE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	inputs.WorkspaceConfig.Plugins.Policy = []workspaceconfig.PluginRef{{URI: "proof://policy"}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{"proof://policy": "policy-validator-go.wasm"})

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg,
		pipeline.Options{})
	if err == nil {
		t.Fatalf("RunPipeline with failing policy: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation) {
		t.Fatalf("policy halt not errors.Is ErrPolicyViolation: %v", err)
	}
	const wantMsg = "fixture rule B: deliberate fail for end-to-end coverage"
	if !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("policy halt error does not carry the plugin message %q:\n%v", wantMsg, err)
	}
	// The rejected verdict surfaces at the CLI boundary as a categorised
	// CompilerError carrying POLICY_ERROR — a governance verdict, distinct
	// from the DeveloperError a selector/appliesTo violation would carry.
	var ce *cerrors.CompilerError
	if !errors.As(err, &ce) {
		t.Fatalf("policy halt does not surface a *CompilerError: %v", err)
	}
	if ce.Category != cerrors.PolicyError {
		t.Errorf("policy halt category = %q, want %q", ce.Category, cerrors.PolicyError)
	}
}

// TestPolicySocketWrongPluginTypeE2E proves attaching a mutator plugin at the
// policy socket fails loudly with a plugin_type-mismatch message rather than
// through the cryptic missing-export error the wrong export name would raise.
func TestPolicySocketWrongPluginTypeE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	inputs.WorkspaceConfig.Plugins.Policy = []workspaceconfig.PluginRef{{URI: "proof://mutator-at-policy"}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{"proof://mutator-at-policy": "mutator-proof-go.wasm"})

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg,
		pipeline.Options{})
	if err == nil {
		t.Fatalf("RunPipeline with a mutator plugin at the policy socket: expected a halt, got nil")
	}
	for _, want := range []string{"plugin_type", "mutator", "policy-validator"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("wrong-socket halt error missing %q:\n%v", want, err)
		}
	}
}

// TestSocketsEmptyConfigNoOp is the regression guard: with no plugin
// attachments, neither socket runs — the build succeeds and no node carries the
// mutator's proof label. This is the invariant the whole existing example suite
// relies on to stay non-regressed.
func TestSocketsEmptyConfigNoOp(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	// The render resolves the provider guests through the lookup, so the lookup
	// resolves provider entity URIs via the entity-fronted embedded cascade — but
	// any proof:// socket URI is a hard failure, since no socket is attached and
	// so neither socket should ever ask the lookup to resolve one. A proof:// call
	// halts the build and fails the test, proving the sockets stayed no-ops.
	provLookup := fixtureSocketLookup(t, buildLookup, nil)
	inputs.PluginPathLookup = func(uri string) (pluginpath.ResolvedPlugin, error) {
		if strings.HasPrefix(uri, "proof://") {
			return pluginpath.ResolvedPlugin{}, fmt.Errorf("no socket should resolve %q under empty config", uri)
		}
		return provLookup(uri)
	}

	res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg,
		pipeline.Options{})
	if err != nil {
		t.Fatalf("empty-config build halted: %v", err)
	}
	for nodeID, node := range res.IRGraph.Spec.Nodes.All() {
		if _, ok := node.Labels.Get(proofMutatorLabelKey); ok {
			t.Errorf("node %q carries the mutator proof label under empty config (socket ran when it should not have)", nodeID)
		}
	}
}

// captureStderr redirects os.Stderr for the duration of fn and returns
// everything written to it. The socket runners emit warn / audit diagnostics to
// os.Stderr, so a proof that a warn "flags" or an audit "records" reads the
// captured text. It mutates a process global, so its callers do not run in
// parallel.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return <-done
}

// observePolicyInputs clones the fullstack inputs and attaches the observing
// policy fixture at the policy socket with the given settings severity and a
// single on-disk source file. It returns the inputs and lookups RunPipeline
// consumes, the source file's absolute path, and its content so a caller can
// assert the guest echoed the right base name and content length.
func observePolicyInputs(t *testing.T, severity string) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader, string, string) {
	t.Helper()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "policy.def")
	srcContent := "definition body for severity " + severity + "\n"
	if err := os.WriteFile(srcPath, []byte(srcContent), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	inputs.WorkspaceConfig.Plugins.Policy = []workspaceconfig.PluginRef{{
		URI: "proof://policy-observe",
		With: &workspaceconfig.PluginRefWith{
			Sources:  []string{srcPath},
			Settings: map[string]any{"severity": severity},
		},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-observe": "policy-validator-observe-go.wasm",
	})
	return inputs, buildLookup, recipeLookup, subAgg, srcPath, srcContent
}

// TestPolicySocketSeverityDenyE2E proves a deny verdict halts the build loudly
// (carrying ErrPolicyViolation) AND that the attachment's sources reached the
// guest as base names: the halt message carries the base-name path and the
// content length the guest observed, and never the absolute host path.
func TestPolicySocketSeverityDenyE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg, srcPath, srcContent := observePolicyInputs(t, analyzer.SeverityDeny)

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err == nil {
		t.Fatalf("deny verdict: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation) {
		t.Fatalf("deny halt not errors.Is ErrPolicyViolation: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, `path="policy.def"`) {
		t.Errorf("deny halt does not carry the base-name source path:\n%v", msg)
	}
	if want := "content-length=" + strconv.Itoa(len(srcContent)); !strings.Contains(msg, want) {
		t.Errorf("deny halt does not carry the observed content length %q:\n%v", want, msg)
	}
	if strings.Contains(msg, srcPath) {
		t.Errorf("deny halt leaked the absolute host source path %q (paths must be base names):\n%v", srcPath, msg)
	}
}

// TestPolicySocketSeverityWarnE2E proves a warn verdict flags on stderr and lets
// the build proceed to produce artifacts, and that two builds are byte-identical
// (a warn does not perturb output).
func TestPolicySocketSeverityWarnE2E(t *testing.T) {
	ctx := context.Background()

	var res *pipeline.Result
	stderr := captureStderr(t, func() {
		inputs, buildLookup, recipeLookup, subAgg, _, _ := observePolicyInputs(t, analyzer.SeverityWarn)
		r, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
		if err != nil {
			t.Fatalf("warn verdict: unexpected halt: %v", err)
		}
		res = r
	})
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("warn build produced no artifacts")
	}
	if !strings.Contains(stderr, "warning:") || !strings.Contains(stderr, "policy plugin") {
		t.Errorf("warn verdict did not flag on stderr:\n%s", stderr)
	}

	// Determinism: a second build of identical input yields byte-identical output.
	inputs2, buildLookup2, recipeLookup2, subAgg2, _, _ := observePolicyInputs(t, analyzer.SeverityWarn)
	res2, err := pipeline.RunPipeline(ctx, inputs2, buildLookup2, recipeLookup2, subAgg2, pipeline.Options{})
	if err != nil {
		t.Fatalf("warn verdict (second build): %v", err)
	}
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

// TestPolicySocketSeverityAuditE2E proves an audit verdict records under a
// distinct "audit:" label, lets the build proceed, and does not perturb output.
func TestPolicySocketSeverityAuditE2E(t *testing.T) {
	ctx := context.Background()

	var res *pipeline.Result
	stderr := captureStderr(t, func() {
		inputs, buildLookup, recipeLookup, subAgg, _, _ := observePolicyInputs(t, analyzer.SeverityAudit)
		r, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
		if err != nil {
			t.Fatalf("audit verdict: unexpected halt: %v", err)
		}
		res = r
	})
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("audit build produced no artifacts")
	}
	if !strings.Contains(stderr, "audit:") {
		t.Errorf("audit verdict did not record under the distinct audit label:\n%s", stderr)
	}

	inputs2, buildLookup2, recipeLookup2, subAgg2, _, _ := observePolicyInputs(t, analyzer.SeverityAudit)
	res2, err := pipeline.RunPipeline(ctx, inputs2, buildLookup2, recipeLookup2, subAgg2, pipeline.Options{})
	if err != nil {
		t.Fatalf("audit verdict (second build): %v", err)
	}
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

// renderPolicyInputs clones the fullstack inputs and attaches the render-stage
// policy fixture with the given settings severity, returning the inputs and the
// lookups RunPipeline consumes.
func renderPolicyInputs(t *testing.T, severity string) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	inputs.WorkspaceConfig.Plugins.Policy = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-render",
		With: &workspaceconfig.PluginRefWith{Settings: map[string]any{"severity": severity}},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-render": "policy-validator-render-go.wasm",
	})
	return inputs, buildLookup, recipeLookup, subAgg
}

// TestRenderStagePolicySocketDenyE2E proves a render-stage policy plugin runs
// AFTER render over the artifacts payload and can halt the build: its deny
// verdict halts loudly with ErrPolicyViolation, and the message proves the guest
// saw the rendered artifacts (a non-zero artifact count).
func TestRenderStagePolicySocketDenyE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := renderPolicyInputs(t, analyzer.SeverityDeny)

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err == nil {
		t.Fatalf("render-stage deny: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation) {
		t.Fatalf("render-stage deny not errors.Is ErrPolicyViolation: %v", err)
	}
	if !strings.Contains(err.Error(), "render stage saw") {
		t.Errorf("render-stage deny message does not prove the guest saw artifacts:\n%v", err)
	}
	// The guest must have seen at least one artifact.
	if strings.Contains(err.Error(), "render stage saw 0 artifact(s)") {
		t.Errorf("render-stage guest saw no artifacts; render payload was empty:\n%v", err)
	}
}

// TestRenderStagePolicySocketAuditE2E proves a render-stage audit verdict records
// and the build proceeds to produce artifacts, with byte-identical output.
func TestRenderStagePolicySocketAuditE2E(t *testing.T) {
	ctx := context.Background()

	var res *pipeline.Result
	stderr := captureStderr(t, func() {
		inputs, buildLookup, recipeLookup, subAgg := renderPolicyInputs(t, analyzer.SeverityAudit)
		r, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
		if err != nil {
			t.Fatalf("render-stage audit: unexpected halt: %v", err)
		}
		res = r
	})
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("render-stage audit build produced no artifacts")
	}
	if !strings.Contains(stderr, "audit:") || !strings.Contains(stderr, "render stage saw") {
		t.Errorf("render-stage audit did not record on stderr:\n%s", stderr)
	}

	inputs2, buildLookup2, recipeLookup2, subAgg2 := renderPolicyInputs(t, analyzer.SeverityAudit)
	res2, err := pipeline.RunPipeline(ctx, inputs2, buildLookup2, recipeLookup2, subAgg2, pipeline.Options{})
	if err != nil {
		t.Fatalf("render-stage audit (second build): %v", err)
	}
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

// assertArtifactsByteIdentical fails unless a and b carry the same path set and
// byte-identical content for every path.
func assertArtifactsByteIdentical(t *testing.T, a, b map[string][]byte) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("artifact count differs: %d vs %d", len(a), len(b))
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			t.Errorf("second build missing artifact %q", k)
			continue
		}
		if string(va) != string(vb) {
			t.Errorf("artifact %q differs between runs (non-deterministic)", k)
		}
	}
}
