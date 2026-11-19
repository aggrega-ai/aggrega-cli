// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginfetchverify"
	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestIntegration_PluginNoticesAndSBOM_RoundTripOverHTTP publishes a signed Plugin
// with its notices and bill of materials to an in-process registry served over
// HTTP, so referrers come from the registry's Referrers API, then reads it back
// as a consumer does: the manifest carries [wasm, notices], exactly one
// application/spdx+json referrer carries the file, listing by the signature type
// returns the signature alone, and the closure signature pass that pull runs
// verifies the publication under the pinned key.
func TestIntegration_PluginNoticesAndSBOM_RoundTrip_OverHTTP(t *testing.T) {
	ctx := context.Background()
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	host := startRegistry(t, nil)
	notices, sbom := writeNoticesFiles(t)

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatal(err)
	}

	const name, version = "patch-server", "1.0.0"
	entityDir := t.TempDir()
	entityPath := filepath.Join(entityDir, "plugin.yaml")
	writeFixture(t, entityPath, pluginEntityYAML(host, name, version))
	wantSign := true
	if err := pushRun(ctx, pushConfig{
		Workspace:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: entityDir}),
		EntityPath:        entityPath,
		PluginPath:        pluginPath,
		PluginNoticesPath: notices,
		PluginSBOMPath:    sbom,
		Registry:          host,
		Sign:              &wantSign,
		Signer:            signer,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	loc := catalog.OCILocation("Plugin", pipelineStepPluginType, name)
	repo, err := oci.NewRemoteRepository(host+"/"+loc, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}

	// The manifest's layers, read over HTTP.
	desc, err := repo.Resolve(ctx, version)
	if err != nil {
		t.Fatal(err)
	}
	layerTypes := fetchLayerTypes(t, repo, desc)
	if len(layerTypes) != 2 || layerTypes[0] != oci.MediaTypeWasm || layerTypes[1] != oci.MediaTypePluginNotices {
		t.Fatalf("manifest layers = %v, want [wasm, notices]", layerTypes)
	}

	boms, err := oci.ListReferrers(ctx, repo, version, oci.MediaTypeSPDXJSON)
	if err != nil || len(boms) != 1 {
		t.Fatalf("spdx referrers = %d, err %v; want exactly one", len(boms), err)
	}
	payload, err := oci.FetchReferrerPayload(ctx, repo, boms[0])
	if err != nil || string(payload) != sbomFixture {
		t.Fatalf("referrer payload = %q, err %v; want the file", payload, err)
	}
	sigs, err := oci.ListReferrers(ctx, repo, version, signing.BundleMediaType)
	if err != nil || len(sigs) != 1 {
		t.Fatalf("signature referrers = %d, err %v; want exactly the signature", len(sigs), err)
	}

	// The verification pull runs, over the published closure.
	p := fetchPublishedPlugin(t, host, name, version)
	cl, lf := closureAndLockfileFor(p)
	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: host, Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(signer.PublicKeyPEM())},
		},
	}
	if verr := pullops.VerifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), ".aggrega/cache", nil, pluginfetchverify.RemoteTargetFor(&http.Client{})); verr != nil {
		t.Fatalf("the signature must verify under the pinned key; got: %v", verr)
	}
	if lf.Entities[0].Signature == nil || lf.Plugins[0].Signature == nil {
		t.Fatal("the verified signature fact is not recorded on both rows")
	}
}
