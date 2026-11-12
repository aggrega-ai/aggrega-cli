// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// signedRealLinterStore publishes the REAL built publishing-linter
// .wasm bytes to an in-memory store, signs the manifest digest with a
// fresh held key, attaches the signature bundle as a referrer, and
// returns the store, the manifest digest, the pinned public-key PEM,
// and the artifact bytes. Unlike signedPluginStore (which uses a
// stand-in Wasm header), this seeds the actual artifact so the same
// bytes can be loaded and run after verification.
func signedRealLinterStore(t *testing.T) (store *memory.Store, manifestDigest, contentDigest string, pubPEM []byte, artifact []byte) {
	t.Helper()
	ctx := context.Background()

	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")
	artifact = readArtifactBytes(t, pluginPath)
	contentDigest = digest.ContentDigest(artifact)

	store = memory.New()
	dg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, artifact, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	bundle, artifactType, err := signer.Sign(ctx, dg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := oci.AttachReferrer(ctx, store, "1.0.0", artifactType, bundle); err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}
	return store, dg, contentDigest, signer.PublicKeyPEM(), artifact
}

// readArtifactBytes reads a built .wasm artifact from disk for the
// signing round-trip.
func readArtifactBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact %s: %v", path, err)
	}
	return b
}

// writeTempWasm writes the verified artifact bytes to a temp file so
// the loader (which reads from a path) can load the exact bytes that
// passed signature verification.
func writeTempWasm(t *testing.T, artifact []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "verified.wasm")
	if err := os.WriteFile(path, artifact, 0o644); err != nil {
		t.Fatalf("write temp wasm: %v", err)
	}
	return path
}

// TestSignVerifyRunRealLinter proves the bytes that pass signature
// verification are the bytes that run: the real publishing-linter
// artifact is signed, verified through the same pull-verify pass the
// catalog uses, then loaded through the describe() gate and invoked.
// The finding it returns is derived from the verified bytes — closing
// the loop between supply-chain trust and execution.
func TestSignVerifyRunRealLinter(t *testing.T) {
	store, dg, contentDg, pubPEM, artifact := signedRealLinterStore(t)

	const uri = "keyed.example.com/plugin/linter/publishing-linter"
	cl := closure.Closure{
		Plugins: []closure.ResolvedRef{
			{URI: uri, Digest: contentDg, ManifestDigest: dg, ResolutionPath: "tier-3-pull"},
		},
	}
	lf := &lockfile.Lockfile{
		Plugins: []lockfile.Plugin{{URI: uri, Digest: contentDg, ManifestDigest: dg}},
	}
	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "keyed.example.com", Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(pubPEM)},
		},
	}

	if err := VerifyClosureSignatures(context.Background(), lf, cl, wsCfg, t.TempDir(), ".aggrega/cache", nil, pluginTargetFor(store, "1.0.0")); err != nil {
		t.Fatalf("VerifyClosureSignatures: %v", err)
	}
	if lf.Plugins[0].Signature == nil {
		t.Fatal("plugin signature fact was not recorded on the lockfile row")
	}

	// Same verified bytes, now loaded and run through the describe()
	// gate. Write them to a temp path the loader reads.
	wasmPath := writeTempWasm(t, artifact)
	loader := &wasm.Loader{}
	ctx := context.Background()
	plugin, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{
		Name: "publishing-linter",
		Path: wasmPath,
	})
	if err != nil {
		t.Fatalf("LoadAndDescribe verified bytes: %v", err)
	}
	defer plugin.Close(ctx)

	if desc.PluginType != string(wasm.PluginTypeLinter) {
		t.Errorf("plugin_type: got %q want %q", desc.PluginType, wasm.PluginTypeLinter)
	}

	// The linter socket carries canonical CBOR in both directions, so the input
	// is encoded with the generated encoder and the answer read through the
	// socket's strict binary decode.
	payload, err := abi.LinterInput{
		RuleID:     "name-shape",
		EntityKind: "Component",
		Proposed:   json.RawMessage(`{"kind":"Component","metadata":{"name":"BadName"}}`),
	}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the linter input: %v", err)
	}
	out, err := wasm.InvokeExport(ctx, plugin, desc, wasm.LintRulesExport, payload)
	if err != nil {
		t.Fatalf("InvokeExport(lint_rules) on verified bytes: %v", err)
	}
	var parsed abi.LinterOutput
	if err := wasm.DecodeGuestOutputCBOR(wasm.LintRulesExport.Name(), out, &parsed); err != nil {
		t.Fatalf("decode output: %v (raw: %x)", err, out)
	}
	if len(parsed.Findings) != 1 || parsed.Findings[0].RuleID != "name-shape" {
		t.Fatalf("verified bytes did not run to the expected finding; got %+v", parsed.Findings)
	}
}

// TestSignVerifyRealLinterWrongKeyHalts proves a signature-requiring
// registry pinning the wrong key halts the pull for the real artifact —
// the same Loud Guardrail the entity path produces.
func TestSignVerifyRealLinterWrongKeyHalts(t *testing.T) {
	store, dg, contentDg, _, _ := signedRealLinterStore(t)
	_, _, _, otherPub, _ := signedRealLinterStore(t)

	const uri = "keyed.example.com/plugin/linter/publishing-linter"
	cl := closure.Closure{
		Plugins: []closure.ResolvedRef{{URI: uri, Digest: contentDg, ManifestDigest: dg, ResolutionPath: "tier-3-pull"}},
	}
	lf := &lockfile.Lockfile{Plugins: []lockfile.Plugin{{URI: uri, Digest: contentDg, ManifestDigest: dg}}}
	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "keyed.example.com", Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(otherPub)},
		},
	}

	err := VerifyClosureSignatures(context.Background(), lf, cl, wsCfg, t.TempDir(), ".aggrega/cache", nil, pluginTargetFor(store, "1.0.0"))
	if !errors.Is(err, signing.ErrSignatureVerificationFailed()) {
		t.Fatalf("want ErrSignatureVerificationFailed, got %v", err)
	}
}
