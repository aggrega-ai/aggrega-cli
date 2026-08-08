// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline_test

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
	entityCascade := pluginpath.EntityLookup(func(uri string) (string, *pluginpath.PluginContractClaim, *pluginpath.PluginRuntimeLimits, *[]pluginpath.PluginCommandMirror, bool) {
		if catalogLookup.Plugin == nil {
			return "", nil, nil, nil, false
		}
		entity, ok := catalogLookup.Plugin(uri)
		if !ok {
			return "", nil, nil, nil, false
		}
		return entity.ArtifactReference(), resolver.PluginContractClaim(entity), resolver.PluginRuntimeLimits(entity), resolver.PluginCommandsMirror(entity), true
	}, pluginpath.NewResolver(pluginpath.ResolutionContext{}, pluginpath.PinPostureNone, pluginpath.PinMaps{}).Resolve, pluginpath.SurfaceEntityOnly)
	return func(uri string) (pluginpath.ResolvedPlugin, error) {
		if b, ok := bytesByURI[uri]; ok {
			return pluginpath.ResolvedPlugin{Bytes: b, URI: uri}, nil
		}
		return entityCascade(uri)
	}
}

// proofInjectAnnotationKey is the registered annotation key the
// annotation-injector proof guest injects; proofInjectFeature is the Feature it
// binds to; proofInjectMarker is the value that flows through binding expansion
// into the synthetic Feature's infra and out into rendered output.
const (
	proofInjectAnnotationKey = "feature.proof/inject"
	proofInjectFeature       = "core/proof-injector"
	proofInjectMarker        = "annotation-transform-was-here"
	proofInjectUnboundKey    = "proof.aggrega/unbound"
	injectedByAnnotation     = "aggrega.ai/injected-by"
)

// loadFullstackWithProofBinding loads the shared fullstack fixture like
// loadFullstackForSockets, but registers the proof-injector annotation binding in
// the Manifest in-memory (the shared manifest.yaml stays untouched, so the
// golden-path and other socket tests are unaffected). The binding maps the
// annotation key to the catalog's core/proof-injector Feature through a single
// infra path, so a scalar injection routes unambiguously and expands into a
// visible Feature attachment.
func loadFullstackWithProofBinding(t *testing.T) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	rel, err := resolver.LoadRelease(fullstackReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(fullstackManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if manifest.Spec.Mappings.Features == nil {
		t.Fatalf("fullstack manifest has no features mapping tier; the proof binding cannot attach")
	}
	manifest.Spec.Mappings.Features.Type.Set(proofInjectFeature, "proof-injector-patcher:1.0.0")
	bindings := types.OrderedMap[types.AnnotationBinding]{}
	bindings.Set(proofInjectAnnotationKey, types.AnnotationBinding{
		Feature: proofInjectFeature,
		Maps:    types.AnnotationBindingMaps{Infra: []string{"marker"}},
	})
	manifest.Spec.Mappings.Features.Annotations = &bindings

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

// TestAnnotationTransformSocketInjectsFeatureE2E proves the pre-expansion
// annotation-transform governance role end-to-end: a guest attached at the role
// injects a bound annotation that expands into a synthetic Feature attachment
// VISIBLE in rendered output, carries the host-stamped aggrega.ai/injected-by
// provenance marker, is byte-identical across two builds, and injects an unbound
// annotation that behaves exactly as an authored unbound annotation — it rides
// through as a plain annotation and produces no Feature (no silent compute,
// Invariant 4).
func TestAnnotationTransformSocketInjectsFeatureE2E(t *testing.T) {
	ctx := context.Background()
	const pluginURI = "proof://annotation-injector"

	build := func() *pipeline.Result {
		inputs, buildLookup, recipeLookup, subAgg := loadFullstackWithProofBinding(t)
		inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{URI: pluginURI, Role: plugintype.RoleAnnotationTransform}}
		inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{pluginURI: "annotation-injector-go.wasm"})
		res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
		if err != nil {
			t.Fatalf("RunPipeline with annotation-transform: %v", err)
		}
		return res
	}

	res := build()

	// The bound injection expanded into a synthetic Feature attachment.
	var foundNode string
	var entry types.IRFeatureEntry
	for nodeID, feats := range res.IRGraph.Spec.FeatureValuesMapping.All() {
		if e, ok := feats.Get(proofInjectAnnotationKey); ok {
			foundNode, entry = nodeID, e
			break
		}
	}
	if foundNode == "" {
		t.Fatalf("no synthetic Feature attachment produced from the injected annotation %q", proofInjectAnnotationKey)
	}
	if entry.Origin != types.OriginAnnotation {
		t.Errorf("injected feature origin = %q, want %q (annotation-driven)", entry.Origin, types.OriginAnnotation)
	}
	if entry.Type != proofInjectFeature {
		t.Errorf("injected feature type = %q, want %q", entry.Type, proofInjectFeature)
	}
	if v, ok := entry.Infra.Get("marker"); !ok || v != proofInjectMarker {
		t.Errorf("injected feature infra.marker = %v (present=%v), want %q", v, ok, proofInjectMarker)
	}

	// The host-stamped injected-by provenance marker rides on the target node.
	node, ok := res.IRGraph.Spec.Nodes.Get(foundNode)
	if !ok {
		t.Fatalf("node %q missing from IR graph", foundNode)
	}
	if v, ok := node.ReleaseAnnotations.Get(injectedByAnnotation); !ok || v != pluginURI {
		t.Errorf("node %q %s = %q (present=%v), want %q", foundNode, injectedByAnnotation, v, ok, pluginURI)
	}

	// The unbound injected annotation produced no Feature anywhere...
	for nodeID, feats := range res.IRGraph.Spec.FeatureValuesMapping.All() {
		if _, ok := feats.Get(proofInjectUnboundKey); ok {
			t.Errorf("unbound injected annotation %q produced a Feature attachment on node %q (silent compute — Invariant 4 breach)", proofInjectUnboundKey, nodeID)
		}
	}
	// ...yet rode through as a plain annotation, exactly like an authored one.
	if v, ok := node.ReleaseAnnotations.Get(proofInjectUnboundKey); !ok || v != "no-binding-here" {
		t.Errorf("unbound injected annotation %q did not ride through as a plain annotation: got %q (present=%v)", proofInjectUnboundKey, v, ok)
	}

	// The synthetic Feature attachment is visible in real rendered output: its
	// FeatureRecipe injectMapping rendered the injected marker into the target
	// component's artifacts.
	visible := false
	for _, content := range res.Artifacts {
		if strings.Contains(string(content), proofInjectMarker) {
			visible = true
			break
		}
	}
	if !visible {
		t.Errorf("injected feature is not visible in rendered output (no artifact carries %q)", proofInjectMarker)
	}

	// Determinism: a second build of identical input yields byte-identical output.
	res2 := build()
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

// TestMutatorSocketInjectsLabelE2E proves the Step-2 mutator socket runs a
// configured plugin end-to-end: the injected label reaches the IR graph on
// every node, and two builds of the same input produce byte-identical
// artifacts (determinism holds with the socket in the pipeline).
func TestMutatorSocketInjectsLabelE2E(t *testing.T) {
	ctx := context.Background()

	build := func() *pipeline.Result {
		inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
		inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{URI: "proof://mutator", Role: plugintype.RoleLabelTransform}}
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
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{URI: "proof://policy-allow", Role: plugintype.RoleIRVerdict}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{"proof://policy-allow": "policy-allow-go.wasm"})

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
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{URI: "proof://policy", Role: plugintype.RoleIRVerdict}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{"proof://policy": "policy-go.wasm"})

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg,
		pipeline.Options{})
	if err == nil {
		t.Fatalf("RunPipeline with failing policy: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
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

// TestVerdictRoleConfinesMutateOnlyGuestE2E proves the verdict role is
// structurally non-mutating: a governance guest built only to transform (it
// exports mutate and emits label patches) cannot mutate when attached at a
// verdict role. The verdict dispatch path has no mutate/patch code path — it
// demands the role's validate export — so a mutate-only guest is rejected loudly
// at the verdict step rather than silently mutating the build. This is the
// structural replacement for the retired plugin-type-identity guard: confinement
// is a property of the role's host code, not of what the guest declares itself to
// be.
func TestVerdictRoleConfinesMutateOnlyGuestE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{URI: "proof://mutate-only-at-verdict", Role: plugintype.RoleIRVerdict}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{"proof://mutate-only-at-verdict": "mutator-proof-go.wasm"})

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg,
		pipeline.Options{})
	if err == nil {
		t.Fatalf("a mutate-only guest at a verdict role: expected a loud halt (the verdict role has no mutate path), got nil")
	}
	// The halt is the verdict step demanding its validate export — the guest's
	// mutate never ran, and no patch was applied — so the failure surfaces at the
	// IR-verdict step and names the offending attachment.
	if !strings.Contains(err.Error(), "ir-verdict") || !strings.Contains(err.Error(), "proof://mutate-only-at-verdict") {
		t.Errorf("verdict-role confinement halt should surface at the IR-verdict step and name the attachment:\n%v", err)
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
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-observe",
		Role: plugintype.RoleIRVerdict,
		With: &workspaceconfig.PluginRefWith{
			Sources:  []string{srcPath},
			Settings: map[string]any{"severity": severity},
		},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-observe": "policy-observe-go.wasm",
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
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
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
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-render",
		Role: plugintype.RoleRenderVerdict,
		With: &workspaceconfig.PluginRefWith{Settings: map[string]any{"severity": severity}},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-render": "policy-render-go.wasm",
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
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
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

// governanceMultiroleURI is the single URI the four-role proof attaches at every
// governance role; governanceMultiroleVerdictMsg is the substring the guest's
// verdict export writes, used to count verdict emissions per stage.
const (
	governanceMultiroleURI        = "proof://governance-multirole"
	governanceMultiroleVerdictMsg = "governance-multirole verdict"
)

// governanceMultiroleInputs attaches ONE governance guest at all four roles on a
// single build — annotation-transform, label-transform, ir-verdict,
// render-verdict — with the two verdict roles driven by their own per-attachment
// severity. It uses the proof annotation binding so the annotation facet's
// injection expands into a visible Feature. This is the "one engine, four roles"
// scenario: the same URI resolves to one guest binary, and the role on each entry
// fixes which facet the host dispatches it into.
func governanceMultiroleInputs(t *testing.T, irSeverity, renderSeverity string) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackWithProofBinding(t)
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{
		{URI: governanceMultiroleURI, Role: plugintype.RoleAnnotationTransform},
		{URI: governanceMultiroleURI, Role: plugintype.RoleLabelTransform},
		{URI: governanceMultiroleURI, Role: plugintype.RoleIRVerdict, With: &workspaceconfig.PluginRefWith{Settings: map[string]any{"severity": irSeverity}}},
		{URI: governanceMultiroleURI, Role: plugintype.RoleRenderVerdict, With: &workspaceconfig.PluginRefWith{Settings: map[string]any{"severity": renderSeverity}}},
	}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{governanceMultiroleURI: "governance-multirole-go.wasm"})
	return inputs, buildLookup, recipeLookup, subAgg
}

// TestGovernanceEngineAllFourRolesE2E is the four-role end-to-end proof: ONE
// governance engine attached at all four roles on ONE build. It also greens the
// positive half of the failing-first proof — a single guest serving both a
// transform and a verdict role, build succeeds — which the retired plugin-type
// identity guard used to make impossible. With both verdict roles at audit
// severity the build proceeds, and every role's effect is observed on the one
// build: the label-transform proof label is on every node; the
// annotation-transform injection expanded into a synthetic Feature visible in
// rendered output carrying the host-stamped injected-by provenance; and both the
// ir-verdict and render-verdict roles recorded their audit verdict on stderr (two
// emissions, one per verdict role). Output is byte-identical across two runs.
func TestGovernanceEngineAllFourRolesE2E(t *testing.T) {
	ctx := context.Background()

	var res *pipeline.Result
	stderr := captureStderr(t, func() {
		inputs, buildLookup, recipeLookup, subAgg := governanceMultiroleInputs(t, analyzer.SeverityAudit, analyzer.SeverityAudit)
		r, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
		if err != nil {
			t.Fatalf("one engine at all four roles (verdicts audit): unexpected halt: %v", err)
		}
		res = r
	})
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("four-role build produced no artifacts")
	}

	// label-transform role ran: the proof label is on every node.
	injected := 0
	for nodeID, node := range res.IRGraph.Spec.Nodes.All() {
		v, ok := node.Labels.Get(proofMutatorLabelKey)
		if !ok {
			t.Errorf("node %q missing the label-transform proof label %q", nodeID, proofMutatorLabelKey)
			continue
		}
		if v != proofMutatorLabelValue {
			t.Errorf("node %q label %q = %q, want %q", nodeID, proofMutatorLabelKey, v, proofMutatorLabelValue)
		}
		injected++
	}
	if injected == 0 {
		t.Fatalf("label-transform role stamped no labels")
	}

	// annotation-transform role ran: the bound injection expanded into a
	// synthetic Feature attachment carrying the injected marker.
	var foundNode string
	var entry types.IRFeatureEntry
	for nodeID, feats := range res.IRGraph.Spec.FeatureValuesMapping.All() {
		if e, ok := feats.Get(proofInjectAnnotationKey); ok {
			foundNode, entry = nodeID, e
			break
		}
	}
	if foundNode == "" {
		t.Fatalf("annotation-transform role produced no synthetic Feature from %q", proofInjectAnnotationKey)
	}
	if entry.Type != proofInjectFeature {
		t.Errorf("injected feature type = %q, want %q", entry.Type, proofInjectFeature)
	}
	node, ok := res.IRGraph.Spec.Nodes.Get(foundNode)
	if !ok {
		t.Fatalf("node %q missing from IR graph", foundNode)
	}
	if v, ok := node.ReleaseAnnotations.Get(injectedByAnnotation); !ok || v != governanceMultiroleURI {
		t.Errorf("node %q %s = %q (present=%v), want %q", foundNode, injectedByAnnotation, v, ok, governanceMultiroleURI)
	}

	// The synthetic Feature is visible in real rendered output.
	visible := false
	for _, content := range res.Artifacts {
		if strings.Contains(string(content), proofInjectMarker) {
			visible = true
			break
		}
	}
	if !visible {
		t.Errorf("injected feature not visible in rendered output (no artifact carries %q)", proofInjectMarker)
	}

	// Both verdict roles ran on this one build: the ir-verdict role's audit AND
	// the render-verdict role's audit each recorded the guest's message on
	// stderr — two emissions, one per verdict role.
	if got := strings.Count(stderr, governanceMultiroleVerdictMsg); got != 2 {
		t.Errorf("expected the verdict message on stderr twice (ir-verdict + render-verdict), got %d:\n%s", got, stderr)
	}

	// Determinism: a second build of identical input is byte-identical.
	inputs2, buildLookup2, recipeLookup2, subAgg2 := governanceMultiroleInputs(t, analyzer.SeverityAudit, analyzer.SeverityAudit)
	res2, err := pipeline.RunPipeline(ctx, inputs2, buildLookup2, recipeLookup2, subAgg2, pipeline.Options{})
	if err != nil {
		t.Fatalf("four-role build (second run): %v", err)
	}
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

// TestGovernanceEngineAllFourRolesIRVerdictHaltsE2E proves the ir-verdict facet
// of the four-role engine halts the build: with the ir-verdict attachment at deny
// the build halts at the IR stage with ErrPolicyViolation, before render — so the
// render-verdict role (at audit) never records its verdict on stderr.
func TestGovernanceEngineAllFourRolesIRVerdictHaltsE2E(t *testing.T) {
	ctx := context.Background()
	var err error
	stderr := captureStderr(t, func() {
		inputs, buildLookup, recipeLookup, subAgg := governanceMultiroleInputs(t, analyzer.SeverityDeny, analyzer.SeverityAudit)
		_, err = pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	})
	if err == nil {
		t.Fatalf("ir-verdict deny: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("ir-verdict deny not errors.Is ErrPolicyViolation: %v", err)
	}
	if !strings.Contains(err.Error(), governanceMultiroleVerdictMsg) {
		t.Errorf("ir-verdict halt does not carry the guest verdict message:\n%v", err)
	}
	// The halt is at the IR stage, before render: the render-verdict audit never
	// ran, so no verdict message reached stderr.
	if strings.Contains(stderr, governanceMultiroleVerdictMsg) {
		t.Errorf("ir-verdict deny should halt before render; render-verdict audit must not have run:\n%s", stderr)
	}
}

// TestGovernanceEngineAllFourRolesRenderVerdictHaltsE2E proves the render-verdict
// facet of the four-role engine halts the build: with the ir-verdict at audit
// (which lets the build proceed and records on stderr) and the render-verdict at
// deny, the build passes the IR stage and halts at render with ErrPolicyViolation
// — proving the render-verdict role, not the ir-verdict role, produced the halt.
func TestGovernanceEngineAllFourRolesRenderVerdictHaltsE2E(t *testing.T) {
	ctx := context.Background()
	var err error
	stderr := captureStderr(t, func() {
		inputs, buildLookup, recipeLookup, subAgg := governanceMultiroleInputs(t, analyzer.SeverityAudit, analyzer.SeverityDeny)
		_, err = pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	})
	if err == nil {
		t.Fatalf("render-verdict deny: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("render-verdict deny not errors.Is ErrPolicyViolation: %v", err)
	}
	if !strings.Contains(err.Error(), governanceMultiroleVerdictMsg) {
		t.Errorf("render-verdict halt does not carry the guest verdict message:\n%v", err)
	}
	// The build passed the IR stage (the ir-verdict audit recorded on stderr) and
	// halted at render — so the halt came from the render-verdict role.
	if !strings.Contains(stderr, governanceMultiroleVerdictMsg) {
		t.Errorf("render-verdict deny should run after a passing ir-verdict audit; the ir-verdict audit did not record:\n%s", stderr)
	}
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
