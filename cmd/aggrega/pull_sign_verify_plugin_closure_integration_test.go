// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// signedPluginPublication captures the coordinates of a Plugin published
// SIGNED to a real in-process registry: the entity and .wasm closure URIs, and
// the real content + manifest digests of each, recovered by pulling them back.
type signedPluginPublication struct {
	host             string
	entityURI        string
	wasmURI          string
	entityContentDg  string
	entityManifestDg string
	wasmContentDg    string
	wasmManifestDg   string
}

// publishSignedPluginToRegistry publishes a real Plugin entity + .wasm fixture,
// signed with the given keyed signer, to a fresh in-process registry, and
// recovers the real digests the production resolution records. The .wasm and
// the entity each carry their own signature referrer.
func publishSignedPluginToRegistry(t *testing.T, signer signing.Signer) signedPluginPublication {
	t.Helper()
	ctx := context.Background()

	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	host := startRegistry(t, nil)

	const socket = "pipeline"
	const name = "patch-server"
	const version = "1.0.0"
	socketName := socket + "/" + name

	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read .wasm: %v", err)
	}
	wasmContentDg := digest.ContentDigest(wasmBytes)

	entityDir := t.TempDir()
	entityPath := filepath.Join(entityDir, "plugin.yaml")
	writeFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: `+socketName+`
  version: "`+version+`"
spec:
  plugin: "oci://`+host+`/wasm/`+socketName+`:`+version+`"
  pluginType: frp_step
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`)

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

	// Recover the real manifest digests the production resolution records, by
	// pulling each artifact back from the registry.
	entityRepo, err := oci.NewRemoteRepository(host + "/wasm/" + socketName)
	if err != nil {
		t.Fatalf("entity repo: %v", err)
	}
	entityPull, err := oci.Pull(ctx, entityRepo, version)
	if err != nil {
		t.Fatalf("pull entity: %v", err)
	}
	wasmPull, err := oci.Pull(ctx, entityRepo, oci.ContentDigestTag(wasmContentDg))
	if err != nil {
		t.Fatalf("pull .wasm: %v", err)
	}

	return signedPluginPublication{
		host:             host,
		entityURI:        host + "/wasm/" + socketName,
		wasmURI:          "oci://" + host + "/wasm/" + socketName + "@" + wasmContentDg,
		entityContentDg:  digest.ContentDigest(entityPull.Bytes),
		entityManifestDg: entityPull.Digest,
		wasmContentDg:    wasmContentDg,
		wasmManifestDg:   wasmPull.Digest,
	}
}

// closureAndLockfileFor builds the two-row closure + lockfile the production
// verify pass consumes: the Plugin ENTITY row (entities axis) and the .wasm
// ARTIFACT row (plugins axis, referenced from the entity). Content and manifest
// digests are kept distinct on each row, matching the production resolution.
func closureAndLockfileFor(p signedPluginPublication) (closure.Closure, *lockfile.Lockfile) {
	cl := closure.Closure{
		Entities: []closure.ResolvedRef{
			{URI: p.entityURI, Digest: p.entityContentDg, ManifestDigest: p.entityManifestDg, ResolutionPath: "tier-3-pull"},
		},
		Plugins: []closure.ResolvedRef{
			{URI: p.wasmURI, Digest: p.wasmContentDg, ManifestDigest: p.wasmManifestDg, ResolutionPath: "tier-3-pull", ReferencedFrom: []string{p.entityURI}},
		},
	}
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{URI: p.entityURI, Digest: p.entityContentDg, ManifestDigest: p.entityManifestDg},
		},
		Plugins: []lockfile.Plugin{
			{URI: p.wasmURI, Digest: p.wasmContentDg, ManifestDigest: p.wasmManifestDg, ReferencedFrom: []string{p.entityURI}},
		},
	}
	return cl, lf
}

// TestIntegration_PluginSignedClosure_BothVerifyAndSameSigner is proof (a) +
// the same-signer pass: a real SIGNED publish of a Plugin records BOTH manifest
// digests and verifies BOTH signatures (entity + .wasm) at pull through the
// production verify pass, and the same-signer check passes because both were
// signed by the one signer. Each row's content digest and manifest digest are
// distinct — the anti-conflation guard.
func TestIntegration_PluginSignedClosure_BothVerifyAndSameSigner(t *testing.T) {
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

	// Anti-conflation: content digest != manifest digest on BOTH rows, so a
	// verify bound to the content digest could not accidentally succeed.
	if p.entityContentDg == p.entityManifestDg {
		t.Fatalf("entity content digest equals manifest digest; the test cannot distinguish the verify subject")
	}
	if p.wasmContentDg == p.wasmManifestDg {
		t.Fatalf(".wasm content digest equals manifest digest; the test cannot distinguish the verify subject")
	}

	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: p.host, RequiresSignatures: true, PublicKey: string(signer.PublicKeyPEM())},
		},
	}

	if verr := verifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), nil, remoteTargetFor); verr != nil {
		t.Fatalf("a signed plugin should verify BOTH signatures and pass same-signer; got: %v", verr)
	}
	if lf.Entities[0].Signature == nil {
		t.Fatal("entity signature fact not recorded on the entities row")
	}
	if lf.Plugins[0].Signature == nil {
		t.Fatal(".wasm signature fact not recorded on the plugins row")
	}
	// Both signers match: keyed fingerprints over the one pinned key.
	if lf.Entities[0].Signature.Identity != lf.Plugins[0].Signature.Identity {
		t.Errorf("entity signer %q != .wasm signer %q; the same-signer check should have matched them",
			lf.Entities[0].Signature.Identity, lf.Plugins[0].Signature.Identity)
	}
}

// publishPluginWithSelectiveSignatures publishes a Plugin UNSIGNED, then
// attaches a keyed signature referrer to ONLY the requested artifacts (entity
// and/or .wasm). It gives a test an asymmetric publish — one artifact signed,
// the other not — which the production pushPlugin (sign-both-or-neither) never
// produces, so the per-artifact policy halt can be exercised on each leg.
func publishPluginWithSelectiveSignatures(t *testing.T, signer signing.Signer, signEntity, signWasm bool) signedPluginPublication {
	t.Helper()
	ctx := context.Background()

	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	host := startRegistry(t, nil)

	const socketName = "pipeline/patch-server"
	const version = "1.0.0"

	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read .wasm: %v", err)
	}
	wasmContentDg := digest.ContentDigest(wasmBytes)

	entityDir := t.TempDir()
	entityPath := filepath.Join(entityDir, "plugin.yaml")
	writeFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: `+socketName+`
  version: "`+version+`"
spec:
  plugin: "oci://`+host+`/wasm/`+socketName+`:`+version+`"
  pluginType: frp_step
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`)
	if perr := pushRun(pushConfig{EntityPath: entityPath, PluginPath: pluginPath, Registry: host}); perr != nil {
		t.Fatalf("publish unsigned plugin: %v", perr)
	}

	repo, err := oci.NewRemoteRepository(host + "/wasm/" + socketName)
	if err != nil {
		t.Fatalf("entity repo: %v", err)
	}
	entityPull, err := oci.Pull(ctx, repo, version)
	if err != nil {
		t.Fatalf("pull entity: %v", err)
	}
	wasmTag := oci.ContentDigestTag(wasmContentDg)
	wasmPull, err := oci.Pull(ctx, repo, wasmTag)
	if err != nil {
		t.Fatalf("pull .wasm: %v", err)
	}

	if signEntity {
		bundle, artifactType, serr := signer.Sign(ctx, entityPull.Digest)
		if serr != nil {
			t.Fatalf("sign entity: %v", serr)
		}
		if _, aerr := oci.AttachReferrer(ctx, repo, version, artifactType, bundle); aerr != nil {
			t.Fatalf("attach entity sig: %v", aerr)
		}
	}
	if signWasm {
		bundle, artifactType, serr := signer.Sign(ctx, wasmPull.Digest)
		if serr != nil {
			t.Fatalf("sign .wasm: %v", serr)
		}
		if _, aerr := oci.AttachReferrer(ctx, repo, wasmTag, artifactType, bundle); aerr != nil {
			t.Fatalf("attach .wasm sig: %v", aerr)
		}
	}

	return signedPluginPublication{
		host:             host,
		entityURI:        host + "/wasm/" + socketName,
		wasmURI:          "oci://" + host + "/wasm/" + socketName + "@" + wasmContentDg,
		entityContentDg:  digest.ContentDigest(entityPull.Bytes),
		entityManifestDg: entityPull.Digest,
		wasmContentDg:    wasmContentDg,
		wasmManifestDg:   wasmPull.Digest,
	}
}

// TestIntegration_PluginSignedClosure_PolicyHaltsOnMissingSig is proof (b),
// SYMMETRIC: a signature-requiring policy HALTS when EITHER required signature
// is missing — the .wasm unsigned (entity signed), and the entity unsigned
// (.wasm signed). Each leg proves the per-registry RequiresSignatures governs
// both axes.
func TestIntegration_PluginSignedClosure_PolicyHaltsOnMissingSig(t *testing.T) {
	ctx := context.Background()

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	cases := []struct {
		name       string
		signEntity bool
		signWasm   bool
	}{
		{"wasm_signature_missing", true, false},
		{"entity_signature_missing", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := publishPluginWithSelectiveSignatures(t, signer, tc.signEntity, tc.signWasm)
			cl, lf := closureAndLockfileFor(p)
			wsCfg := workspaceconfig.Config{
				Registries: []workspaceconfig.RegistryConfig{
					{Host: p.host, RequiresSignatures: true, PublicKey: string(signer.PublicKeyPEM())},
				},
			}
			verr := verifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), nil, remoteTargetFor)
			if !errors.Is(verr, signing.ErrNoSignatureFound) {
				t.Fatalf("a signature-requiring policy must HALT with ErrNoSignatureFound when a required signature is missing; got: %v", verr)
			}
		})
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
			{Host: p.host, RequiresSignatures: true, PublicKey: string(wrongPub)},
		},
	}

	verr := verifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), nil, remoteTargetFor)
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

	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	host := startRegistry(t, nil)

	const socketName = "pipeline/patch-server"
	const version = "1.0.0"

	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read .wasm: %v", err)
	}
	wasmContentDg := digest.ContentDigest(wasmBytes)

	entityDir := t.TempDir()
	entityPath := filepath.Join(entityDir, "plugin.yaml")
	writeFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: `+socketName+`
  version: "`+version+`"
spec:
  plugin: "oci://`+host+`/wasm/`+socketName+`:`+version+`"
  pluginType: frp_step
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`)
	// Publish UNSIGNED.
	if perr := pushRun(pushConfig{EntityPath: entityPath, PluginPath: pluginPath, Registry: host}); perr != nil {
		t.Fatalf("publish unsigned plugin: %v", perr)
	}

	entityRepo, err := oci.NewRemoteRepository(host + "/wasm/" + socketName)
	if err != nil {
		t.Fatalf("entity repo: %v", err)
	}
	entityPull, err := oci.Pull(ctx, entityRepo, version)
	if err != nil {
		t.Fatalf("pull entity: %v", err)
	}
	wasmPull, err := oci.Pull(ctx, entityRepo, oci.ContentDigestTag(wasmContentDg))
	if err != nil {
		t.Fatalf("pull .wasm: %v", err)
	}

	entityURI := host + "/wasm/" + socketName
	wasmURI := "oci://" + host + "/wasm/" + socketName + "@" + wasmContentDg
	cl := closure.Closure{
		Entities: []closure.ResolvedRef{
			{URI: entityURI, Digest: digest.ContentDigest(entityPull.Bytes), ManifestDigest: entityPull.Digest, ResolutionPath: "tier-3-pull"},
		},
		Plugins: []closure.ResolvedRef{
			{URI: wasmURI, Digest: wasmContentDg, ManifestDigest: wasmPull.Digest, ResolutionPath: "tier-3-pull", ReferencedFrom: []string{entityURI}},
		},
	}
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{{URI: entityURI, Digest: digest.ContentDigest(entityPull.Bytes), ManifestDigest: entityPull.Digest}},
		Plugins:  []lockfile.Plugin{{URI: wasmURI, Digest: wasmContentDg, ManifestDigest: wasmPull.Digest, ReferencedFrom: []string{entityURI}}},
	}

	// A registry with RequiresSignatures:false (verify-if-present default).
	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: host, RequiresSignatures: false, PublicKey: ""},
		},
	}
	if verr := verifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), nil, remoteTargetFor); verr != nil {
		t.Fatalf("a non-requiring policy must NOT halt on an unsigned plugin; got: %v", verr)
	}
	if lf.Entities[0].Signature != nil || lf.Plugins[0].Signature != nil {
		t.Fatal("no signature fact should be recorded for an unsigned plugin under a non-requiring policy")
	}
}
