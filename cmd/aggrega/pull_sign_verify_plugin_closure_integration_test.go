// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// signedPluginPublication captures the coordinates of a Plugin published to a
// real in-process registry under the single-manifest model: the entity and .wasm
// closure URIs, the real content digest of each blob, and the ONE manifest digest
// both axes commit to. The entity YAML (config blob) and the .wasm (inline layer)
// ride one OCI manifest tagged by metadata.version, so there is a single manifest
// digest — not a per-artifact pair.
type signedPluginPublication struct {
	host            string
	entityURI       string
	wasmURI         string
	entityContentDg string
	wasmContentDg   string
	manifestDg      string
}

// pluginEntityYAML renders a Plugin entity fixture with the canonical BARE
// spec.plugin identity (no ":version", no "@sha256:"); the version comes from
// metadata.version and the .wasm rides inline in the single manifest.
func pluginEntityYAML(host, name, version string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: ` + name + `
  pluginType: ` + frpStepPluginType + `
  version: "` + version + `"
spec:
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`
}

// fetchPublishedPlugin recovers the single-manifest coordinates the production
// resolution records, by reading the one manifest back from the registry: the
// entity YAML (config blob) and .wasm (inline layer) content digests, and the
// manifest digest both axes commit to.
func fetchPublishedPlugin(t *testing.T, host, name, version string) signedPluginPublication {
	t.Helper()
	loc := catalog.OCILocation("Plugin", frpStepPluginType, name)
	repo, err := oci.NewRemoteRepository(host+"/"+loc, &http.Client{})
	if err != nil {
		t.Fatalf("entity repo: %v", err)
	}
	entityYAML, wasm, manifestDg, err := oci.FetchPluginManifest(context.Background(), repo, version)
	if err != nil {
		t.Fatalf("fetch single plugin manifest: %v", err)
	}
	return signedPluginPublication{
		host:            host,
		entityURI:       host + "/" + loc,
		wasmURI:         "oci://" + host + "/" + loc,
		entityContentDg: digest.ContentDigest(entityYAML),
		wasmContentDg:   digest.ContentDigest(wasm),
		manifestDg:      manifestDg,
	}
}

// publishSignedPluginToRegistry publishes a real Plugin entity + .wasm fixture,
// signed ONCE with the given keyed signer, to a fresh in-process registry, and
// recovers the real digests the production resolution records. Under the
// single-manifest model the entity YAML and the inline .wasm ride one manifest,
// signed once — the single signature transitively covers the .wasm.
func publishSignedPluginToRegistry(t *testing.T, signer signing.Signer) signedPluginPublication {
	t.Helper()
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	host := startRegistry(t, nil)

	const name = "patch-server"
	const version = "1.0.0"

	entityDir := t.TempDir()
	entityPath := filepath.Join(entityDir, "plugin.yaml")
	writeFixture(t, entityPath, pluginEntityYAML(host, name, version))

	wantSign := true
	if perr := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   host,
		Sign:       &wantSign,
		Signer:     signer,
	}); perr != nil {
		t.Fatalf("publish signed plugin: %v", perr)
	}
	return fetchPublishedPlugin(t, host, name, version)
}

// publishUnsignedPluginToRegistry publishes a real Plugin entity + .wasm fixture
// UNSIGNED to a fresh in-process registry (one manifest, no signature referrer),
// and recovers the single-manifest coordinates. It is the unsigned counterpart of
// publishSignedPluginToRegistry.
func publishUnsignedPluginToRegistry(t *testing.T) signedPluginPublication {
	t.Helper()
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	host := startRegistry(t, nil)

	const name = "patch-server"
	const version = "1.0.0"

	entityDir := t.TempDir()
	entityPath := filepath.Join(entityDir, "plugin.yaml")
	writeFixture(t, entityPath, pluginEntityYAML(host, name, version))

	if perr := pushRun(pushConfig{EntityPath: entityPath, PluginPath: pluginPath, Registry: host}); perr != nil {
		t.Fatalf("publish unsigned plugin: %v", perr)
	}
	return fetchPublishedPlugin(t, host, name, version)
}

// closureAndLockfileFor builds the two-row closure + lockfile the production
// verify pass consumes: the Plugin ENTITY row (entities axis) and the .wasm
// ARTIFACT row (plugins axis, referenced from the entity). Under the
// single-manifest model both rows commit to the ONE manifest digest — the
// pairing is intrinsic — while each row keeps its own blob content digest,
// distinct from the manifest digest (the anti-conflation guard).
func closureAndLockfileFor(p signedPluginPublication) (closure.Closure, *lockfile.Lockfile) {
	cl := closure.Closure{
		Entities: []closure.ResolvedRef{
			{URI: p.entityURI, Digest: p.entityContentDg, ManifestDigest: p.manifestDg, ResolutionPath: "tier-3-pull"},
		},
		Plugins: []closure.ResolvedRef{
			{URI: p.wasmURI, Digest: p.wasmContentDg, ManifestDigest: p.manifestDg, ResolutionPath: "tier-3-pull", ReferencedFrom: []string{p.entityURI}},
		},
	}
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{URI: p.entityURI, Digest: p.entityContentDg, ManifestDigest: p.manifestDg},
		},
		Plugins: []lockfile.Plugin{
			{URI: p.wasmURI, Digest: p.wasmContentDg, ManifestDigest: p.manifestDg, ReferencedFrom: []string{p.entityURI}},
		},
	}
	return cl, lf
}

// TestIntegration_PluginSignedClosure_ManifestSignatureVerifies is proof (a): a
// real SIGNED publish of a Plugin records the ONE manifest digest on both the
// entity and the .wasm-artifact closure rows and verifies the single manifest
// signature at pull through the production verify pass. Under the single-manifest
// model the manifest commits to the inline .wasm layer, so the entity↔wasm
// pairing is intrinsic — one signature transitively covers both axes and there is
// no second signer to reconcile. Each row's blob content digest is distinct from
// the manifest digest (the anti-conflation guard), and both rows share the one
// manifest digest (the pairing witness).
func TestIntegration_PluginSignedClosure_ManifestSignatureVerifies(t *testing.T) {
	ctx := context.Background()

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	p := publishSignedPluginToRegistry(t, signer)
	cl, lf := closureAndLockfileFor(p)

	// Anti-conflation: each row's blob content digest differs from the manifest
	// digest, so a verify bound to the content digest could not accidentally succeed.
	if p.entityContentDg == p.manifestDg {
		t.Fatalf("entity content digest equals manifest digest; the test cannot distinguish the verify subject")
	}
	if p.wasmContentDg == p.manifestDg {
		t.Fatalf(".wasm content digest equals manifest digest; the test cannot distinguish the verify subject")
	}
	// Pairing witness: one manifest covers both axes, so both rows commit to the
	// same manifest digest — the intrinsic pairing the single manifest guarantees.
	if lf.Entities[0].ManifestDigest != lf.Plugins[0].ManifestDigest {
		t.Fatalf("entity manifest digest %q != .wasm manifest digest %q; the single manifest must cover both axes",
			lf.Entities[0].ManifestDigest, lf.Plugins[0].ManifestDigest)
	}

	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: p.host, Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(signer.PublicKeyPEM())},
		},
	}

	if verr := verifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), nil, remoteTargetForWith(&http.Client{})); verr != nil {
		t.Fatalf("the single manifest signature must verify under the pinned key; got: %v", verr)
	}
	if lf.Entities[0].Signature == nil {
		t.Fatal("entity signature fact not recorded on the entities row")
	}
	if lf.Plugins[0].Signature == nil {
		t.Fatal(".wasm signature fact not recorded on the plugins row")
	}
}

// TestIntegration_PluginSignedClosure_PolicyHaltsOnMissingSig is proof (b): a
// signature-requiring policy HALTS when the single manifest carries no signature.
// Under the single-manifest model a plugin has ONE signature covering both the
// entity and the inline .wasm, so there is one missing-signature case — an
// unsigned publish — not a per-axis asymmetry.
func TestIntegration_PluginSignedClosure_PolicyHaltsOnMissingSig(t *testing.T) {
	ctx := context.Background()

	// A pinned public key makes the require-signatures policy well-formed (keyed
	// mode); the manifest carries no signature, so the halt is the missing
	// signature, not a key mismatch.
	_, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}

	p := publishUnsignedPluginToRegistry(t)
	cl, lf := closureAndLockfileFor(p)
	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: p.host, Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(pubPEM)},
		},
	}
	verr := verifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), nil, remoteTargetForWith(&http.Client{}))
	if !errors.Is(verr, signing.ErrNoSignatureFound) {
		t.Fatalf("a signature-requiring policy must HALT with ErrNoSignatureFound when the single manifest carries no signature; got: %v", verr)
	}
}

// TestIntegration_PluginSignedClosure_PolicyHaltsOnWrongKey is proof (b),
// crypto-failure leg: a signature-requiring policy pinning the WRONG key halts
// because neither signature verifies against it.
func TestIntegration_PluginSignedClosure_PolicyHaltsOnWrongKey(t *testing.T) {
	ctx := context.Background()

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	p := publishSignedPluginToRegistry(t, signer)
	cl, lf := closureAndLockfileFor(p)

	// A DIFFERENT pinned key: each signature verifies against the publisher's
	// key but not this one, so the requiring policy halts.
	_, wrongPub, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM (wrong): %v", err)
	}
	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: p.host, Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(wrongPub)},
		},
	}

	verr := verifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), nil, remoteTargetForWith(&http.Client{}))
	if !errors.Is(verr, signing.ErrSignatureVerificationFailed) {
		t.Fatalf("a wrong pinned key must HALT with ErrSignatureVerificationFailed; got: %v", verr)
	}
}

// TestIntegration_PluginUnsignedClosure_NonRequiringNoHalt is proof (d): an
// UNSIGNED plugin pulled under a policy that does NOT require signatures does
// not halt and records no signature fact. This proves the policy gate, not a
// hardcoded-always requirement.
func TestIntegration_PluginUnsignedClosure_NonRequiringNoHalt(t *testing.T) {
	ctx := context.Background()

	p := publishUnsignedPluginToRegistry(t)
	cl, lf := closureAndLockfileFor(p)

	// A registry with signing.required:false (verify-if-present default).
	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: p.host, Signing: workspaceconfig.SigningConfig{Required: false}, PublicKey: ""},
		},
	}
	if verr := verifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), nil, remoteTargetForWith(&http.Client{})); verr != nil {
		t.Fatalf("a non-requiring policy must NOT halt on an unsigned plugin; got: %v", verr)
	}
	if lf.Entities[0].Signature != nil || lf.Plugins[0].Signature != nil {
		t.Fatal("no signature fact should be recorded for an unsigned plugin under a non-requiring policy")
	}
}
