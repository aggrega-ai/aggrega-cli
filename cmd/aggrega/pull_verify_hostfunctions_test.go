// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// stubPluginLoader serves one Plugin entity by URI for the host-function
// verify pass. The other RecipeLoader methods are unused by the pass.
type stubPluginLoader struct {
	byURI map[string]*types.Plugin
}

func (l *stubPluginLoader) LoadRecipe(string) (*types.Recipe, error)               { return nil, nil }
func (l *stubPluginLoader) LoadFeatureRecipe(string) (*types.FeatureRecipe, error) { return nil, nil }
func (l *stubPluginLoader) LoadPlugin(uri string) (*types.Plugin, error) {
	if wp, ok := l.byURI[uri]; ok {
		return wp, nil
	}
	return nil, errors.New("plugin not found: " + uri)
}

// cachePluginArtifact builds the frp-step-go fixture, writes its bytes into the
// (uri, contentDigest) cache slot the artifact descent would populate, and returns
// the workspace root, cache dir, the digest-free artifact URI, the content digest
// pin, and the derived (canonical) host-function set. The frp-step-go fixture
// imports no custom host functions, so the derived set is empty — the
// no-host-function scenario.
func cachePluginArtifact(t *testing.T) (workspaceRoot, cacheDir, artifactURI, contentDigest string, derived []string) {
	t.Helper()
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	contentDigest = digest.ContentDigest(wasmBytes)
	artifactURI = "frp/patch"

	workspaceRoot = t.TempDir()
	cacheDir = "cache"
	if err := oci.WriteCached(workspaceRoot, cacheDir, artifactURI, contentDigest, wasmBytes); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	d, err := wasm.DeriveHostFunctions("frp/patch", wasmBytes)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	return workspaceRoot, cacheDir, artifactURI, contentDigest, d
}

// pluginEntity builds a Plugin entity whose bare identity (metadata.name) is the
// artifact reference and whose stamped runtime.hostFunctions is the given set.
func pluginEntity(artifactURI string, stamped []string) *types.Plugin {
	var wp types.Plugin
	wp.Metadata.Name = artifactURI
	wp.Spec.Runtime = &types.PluginRuntime{HostFunctions: stamped}
	return &wp
}

// pluginArtifactClosure builds a closure carrying one plugin artifact pin and
// the referencing plugin entity URI, mirroring the artifact axis the descent
// populates. The content digest is the pin the host-function verify keys the
// cache read by.
func pluginArtifactClosure(artifactURI, entityURI, contentDigest string) *closure.Closure {
	return &closure.Closure{
		Plugins: []closure.ResolvedRef{
			{URI: artifactURI, Digest: contentDigest, ReferencedFrom: []string{entityURI}},
		},
	}
}

// TestVerifyPluginHostFunctions_StampMatchesBinary proves the verify pass
// passes when the stamped host-function record matches the set re-derived from
// the verified .wasm.
func TestVerifyPluginHostFunctions_StampMatchesBinary(t *testing.T) {
	t.Parallel()
	workspaceRoot, cacheDir, artifactURI, contentDigest, derived := cachePluginArtifact(t)
	const entityURI = "frp/patch:1.0.0"
	loader := &stubPluginLoader{byURI: map[string]*types.Plugin{
		entityURI: pluginEntity(artifactURI, derived),
	}}
	cl := pluginArtifactClosure(artifactURI, entityURI, contentDigest)

	if err := verifyPluginHostFunctions(context.Background(), cl, loader, workspaceRoot, cacheDir, os.Stderr); err != nil {
		t.Fatalf("a matching stamp should pass; got %v", err)
	}
}

// TestVerifyPluginHostFunctions_StampDivergesHalts proves the pull HALTS with
// ErrWasmHostFunctionsStampMismatch when the stamped record disagrees with the
// host functions re-derived from the verified .wasm — the non-aggrega-push /
// stamping-bug case the digest pin alone would not catch.
func TestVerifyPluginHostFunctions_StampDivergesHalts(t *testing.T) {
	t.Parallel()
	workspaceRoot, cacheDir, artifactURI, contentDigest, _ := cachePluginArtifact(t)
	const entityURI = "frp/patch:1.0.0"
	// Stamp a host function the binary does not import: the frp-step-go fixture
	// imports none, so a stamped [aggrega_log] is a divergence.
	loader := &stubPluginLoader{byURI: map[string]*types.Plugin{
		entityURI: pluginEntity(artifactURI, []string{"aggrega_log"}),
	}}
	cl := pluginArtifactClosure(artifactURI, entityURI, contentDigest)

	err := verifyPluginHostFunctions(context.Background(), cl, loader, workspaceRoot, cacheDir, os.Stderr)
	if !errors.Is(err, wasm.ErrWasmHostFunctionsStampMismatch) {
		t.Fatalf("a divergent stamp should halt with ErrWasmHostFunctionsStampMismatch; got %v", err)
	}
}

// TestVerifyPluginHostFunctions_NoCachedArtifactSkips proves a plugin whose
// artifact is not in the cache (offline / hermetic) is skipped rather than
// failing — there is nothing to re-derive against.
func TestVerifyPluginHostFunctions_NoCachedArtifactSkips(t *testing.T) {
	t.Parallel()
	const entityURI = "frp/patch:1.0.0"
	const artifactURI = "frp/patch"
	// A recorded content pin whose cache slot does not exist (offline / hermetic).
	const contentDigest = "sha256:" +
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	loader := &stubPluginLoader{byURI: map[string]*types.Plugin{
		entityURI: pluginEntity(artifactURI, []string{"aggrega_log"}),
	}}
	cl := pluginArtifactClosure(artifactURI, entityURI, contentDigest)

	if err := verifyPluginHostFunctions(context.Background(), cl, loader, t.TempDir(), "cache", os.Stderr); err != nil {
		t.Fatalf("an uncached artifact should be skipped, not fail; got %v", err)
	}
}
