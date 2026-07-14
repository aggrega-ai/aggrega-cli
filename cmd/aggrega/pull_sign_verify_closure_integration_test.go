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

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// signedCatalogComponentBody is a minimal Component the publish flow can sign
// and push without a recipe / .wasm. Its bytes drive a real content digest that
// differs from the manifest digest the registry mints.
const signedCatalogComponentBody = "apiVersion: aggrega.ai/v1alpha1\n" +
	"kind: Component\n" +
	"metadata:\n  name: core/api-server\n  version: 1.0.0\n" +
	"spec:\n  schema:\n    infra:\n      port: { type: integer, default: 8080 }\n"

// TestIntegration_EntitySignatureVerifiesAgainstManifestDigest is the F1
// regression guard. A catalog entity is SIGNED at publish against its OCI
// manifest digest and pulled through the production verify pass against a
// signature-requiring keyed policy. The entity's signature commits to the
// MANIFEST digest, while the closure row's content digest is a DIFFERENT value
// (the portable cache/integrity pin). The fix binds verification to the
// recorded manifest digest; binding it to the content digest — the prior
// behaviour — rejects a legitimately-signed artifact.
//
// The two arms prove both directions through the SAME production code:
//   - verify against the manifest digest PASSES (the fix);
//   - verify against the content digest HALTS with
//     ErrSignatureVerificationFailed (the F1 bug, reproduced by feeding the
//     content digest as the subject the prior code used).
func TestIntegration_EntitySignatureVerifiesAgainstManifestDigest(t *testing.T) {
	host := startRegistry(t, nil)
	ctx := context.Background()

	// A keyed signer, used for both the publish and the pinned verify policy.
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	// Publish the Component SIGNED through the real push path: the entity's
	// manifest digest is signed and attached as an OCI referrer on the real
	// in-process registry.
	// The publish path requires the entity's per-version subdirectory to match
	// metadata.version, so place the file under a 1.0.0/ directory.
	entityDir := filepath.Join(t.TempDir(), "1.0.0")
	if werr := os.MkdirAll(entityDir, 0o755); werr != nil {
		t.Fatalf("mkdir: %v", werr)
	}
	entityPath := filepath.Join(entityDir, "component.yaml")
	if werr := os.WriteFile(entityPath, []byte(signedCatalogComponentBody), 0o644); werr != nil {
		t.Fatalf("write component: %v", werr)
	}
	wantSign := true
	if perr := pushRun(pushConfig{
		EntityPath: entityPath,
		Registry:   host,
		Sign:       &wantSign,
		Signer:     signer,
	}); perr != nil {
		t.Fatalf("pushRun --sign: %v", perr)
	}

	// Recover the REAL digests the production resolution would record: the
	// manifest digest from the registry pull (what oci.Pull returns and the
	// closure now captures), and the content digest from the pulled bytes (the
	// cache/integrity pin). They MUST differ — that difference is exactly what
	// the F1 bug papered over.
	repo, err := oci.NewRemoteRepository(host + "/component/core/api-server")
	if err != nil {
		t.Fatalf("NewRemoteRepository: %v", err)
	}
	pulled, err := oci.Pull(ctx, repo, "1.0.0")
	if err != nil {
		t.Fatalf("oci.Pull: %v", err)
	}
	manifestDigest := pulled.Digest
	contentDg := digest.ContentDigest(pulled.Bytes)
	if manifestDigest == contentDg {
		t.Fatalf("manifest digest %s equals content digest; the test cannot distinguish the verify subject", manifestDigest)
	}

	// The host-carrying closure URI so the verify pass collects it and
	// remoteTargetFor resolves the right repo.
	entityURI := host + "/component/core/api-server"

	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: host, Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(signer.PublicKeyPEM())},
		},
	}

	// ARM 1 (the fix): the closure row carries the content digest as Digest and
	// the manifest digest as ManifestDigest. The production verify entrypoint
	// (verifyClosureSignatures → remoteTargetFor) discovers the real referrer and
	// verifies the entity signature against ManifestDigest — it PASSES.
	cl := closure.Closure{
		Entities: []closure.ResolvedRef{
			{URI: entityURI, Digest: contentDg, ManifestDigest: manifestDigest, ResolutionPath: "tier-3-pull"},
		},
	}
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{{URI: entityURI, Digest: contentDg, ManifestDigest: manifestDigest}},
	}
	if verr := verifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), nil, remoteTargetFor); verr != nil {
		t.Fatalf("verify against the recorded manifest digest should PASS, got: %v", verr)
	}
	if lf.Entities[0].Signature == nil {
		t.Fatal("a verified signature fact was not recorded on the entity row")
	}

	// ARM 2 (the F1 bug, reproduced precisely): discover the real signature
	// referrer (correct subject = manifest digest), fetch the actual bundle, and
	// verify it against the CONTENT digest — exactly what the pre-fix code did
	// when it passed the row's content Digest as the crypto subject. It must
	// HALT, because the signature commits to the manifest digest, not the content
	// digest. This isolates the crypto-subject from referrer discovery and proves
	// the fix is load-bearing: were the two digests conflated, this would pass.
	refs, err := oci.ListReferrers(ctx, repo, manifestDigest, signing.BundleMediaType)
	if err != nil {
		t.Fatalf("ListReferrers on the signed entity: %v", err)
	}
	if len(refs) == 0 {
		t.Fatal("the signed entity carries no signature referrer")
	}
	bundle, err := oci.FetchReferrerPayload(ctx, repo, refs[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload: %v", err)
	}
	// The fix: verifying against the manifest digest succeeds.
	if verr := signing.VerifyKeyedBundle(bundle, signer.PublicKeyPEM(), manifestDigest); verr != nil {
		t.Fatalf("the real bundle should verify against the manifest digest, got: %v", verr)
	}
	// The F1 behaviour: verifying the SAME bundle against the content digest halts.
	bugErr := signing.VerifyKeyedBundle(bundle, signer.PublicKeyPEM(), contentDg)
	if !errors.Is(bugErr, signing.ErrSignatureVerificationFailed) {
		t.Fatalf("verify against the content digest (the F1 behaviour) must HALT with ErrSignatureVerificationFailed, got: %v", bugErr)
	}
}
