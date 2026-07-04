// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
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
// to its built fixture's bytes and delegates every other URI to the bare
// cascade (embed + local tiers) — a harmless fallback since the fullstack build
// renders through Go engines and asks the lookup for nothing else.
func fixtureSocketLookup(t *testing.T, uriToArtifact map[string]string) pluginpath.Lookup {
	t.Helper()
	bytesByURI := make(map[string][]byte, len(uriToArtifact))
	for uri, artifact := range uriToArtifact {
		bytesByURI[uri] = wasmtest.EnsureFixtureBytes(t, artifact)
	}
	bare := (pluginpath.Resolver{}).Resolve
	return func(uri string) (pluginpath.ResolvedPlugin, error) {
		if b, ok := bytesByURI[uri]; ok {
			return pluginpath.ResolvedPlugin{Bytes: b, URI: uri}, nil
		}
		return bare(uri)
	}
}

// TestMutatorSocketInjectsLabelE2E proves the Step-2 mutator socket runs a
// configured plugin end-to-end: the injected label reaches the IR graph on
// every node, and two builds of the same input produce byte-identical
// artifacts (determinism holds with the socket in the pipeline).
func TestMutatorSocketInjectsLabelE2E(t *testing.T) {
	ctx := context.Background()
	lookup := fixtureSocketLookup(t, map[string]string{"proof://mutator": "mutator-proof-go.wasm"})

	build := func() *pipeline.Result {
		inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
		inputs.WorkspaceConfig.Plugins.Mutator = []workspaceconfig.PluginRef{{URI: "proof://mutator"}}
		inputs.PluginPathLookup = lookup
		res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg,
			productionEngineFactory, pipeline.Options{})
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
	inputs.PluginPathLookup = fixtureSocketLookup(t, map[string]string{"proof://policy-allow": "policy-validator-allow-go.wasm"})

	res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg,
		productionEngineFactory, pipeline.Options{})
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
	inputs.PluginPathLookup = fixtureSocketLookup(t, map[string]string{"proof://policy": "policy-validator-go.wasm"})

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg,
		productionEngineFactory, pipeline.Options{})
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
}

// TestSocketsEmptyConfigNoOp is the regression guard: with no plugin
// attachments, neither socket runs — the build succeeds and no node carries the
// mutator's proof label. This is the invariant the whole existing example suite
// relies on to stay non-regressed.
func TestSocketsEmptyConfigNoOp(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	// A lookup that errors on every URI: proves neither socket resolves a
	// plugin when unconfigured (any call would fail the build).
	inputs.PluginPathLookup = func(uri string) (pluginpath.ResolvedPlugin, error) {
		return pluginpath.ResolvedPlugin{}, fmt.Errorf("no socket should resolve %q under empty config", uri)
	}

	res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg,
		productionEngineFactory, pipeline.Options{})
	if err != nil {
		t.Fatalf("empty-config build halted: %v", err)
	}
	for nodeID, node := range res.IRGraph.Spec.Nodes.All() {
		if _, ok := node.Labels.Get(proofMutatorLabelKey); ok {
			t.Errorf("node %q carries the mutator proof label under empty config (socket ran when it should not have)", nodeID)
		}
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
