// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// memStorePuller wraps an in-memory oras store so it satisfies catalog.OCIPuller.
// The reference is ignored — a single store holds every artifact in these
// single-registry round-trip tests — and the pull is by tag, exactly as the
// production remote-repository adapter pulls.
type memStorePuller struct{ store *memory.Store }

func (p *memStorePuller) Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error) {
	return oci.Pull(ctx, p.store, tag)
}

// agreeingPluginEntity is a Plugin entity whose authored export claim
// agrees with the frp-step-go fixture's describe() (export "step",
// operates=patch, scope=own, no custom host functions). spec.plugin is the
// digest-free artifact identity; publish leaves it unchanged.
const agreeingPluginEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: frp/patch
  version: "1.0.0"
spec:
  plugin: "oci://hub.aggrega.ai/plugin/frp/patch"
  pluginType: frp_step
  abiVersion: v1alpha1
  targets:
    - helm
  exports:
    - name: step
      operates: patch
      scope: own
`

// writeAgreeingPlugin builds the frp-step-go fixture, writes the agreeing
// entity into a fresh tempdir, chdirs into it, and returns the entity path,
// the .wasm path, and the .wasm content digest. Skips cleanly when the Wasm
// toolchain is absent.
func writeAgreeingPlugin(t *testing.T) (entityPath, pluginPath, contentDigest string) {
	t.Helper()
	pluginPath = wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	contentDigest = digest.ContentDigest(wasmBytes)

	root := t.TempDir()
	t.Chdir(root)
	entityPath = filepath.Join(root, "frp-step-patch.yaml")
	writeEntityFixture(t, entityPath, agreeingPluginEntity)
	return entityPath, pluginPath, contentDigest
}

// TestPushPluginOrderedPublish proves the ordered, two-artifact publish:
// the .wasm artifact lands (addressed by its content-digest tag) and the entity
// lands (tagged by metadata.version, referencing the .wasm by its digest-free
// identity in spec.plugin). Both are resolvable in the registry double after
// publish.
func TestPushPluginOrderedPublish(t *testing.T) {
	ctx := context.Background()
	entityPath, pluginPath, contentDigest := writeAgreeingPlugin(t)
	store := memory.New()

	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	// The entity is resolvable by its version tag and carries the entity
	// mediatype, with spec.plugin left as the authored digest-free identity.
	entityPull, err := oci.Pull(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("pull entity: %v", err)
	}
	if entityPull.MediaType != oci.MediaTypePlugin {
		t.Errorf("entity mediatype: got %q want %q", entityPull.MediaType, oci.MediaTypePlugin)
	}
	wantIdentity := "oci://hub.aggrega.ai/plugin/frp/patch"
	if !strings.Contains(string(entityPull.Bytes), wantIdentity) {
		t.Errorf("published entity spec.plugin not the digest-free identity %q; got:\n%s", wantIdentity, entityPull.Bytes)
	}
	if strings.Contains(string(entityPull.Bytes), "@sha256:") {
		t.Errorf("published entity must carry no @sha256: digest in spec.plugin; got:\n%s", entityPull.Bytes)
	}

	// The .wasm artifact is resolvable by its content-digest tag and carries
	// the .wasm mediatype; its bytes content-verify to the pin.
	wasmTag := strings.ReplaceAll(contentDigest, ":", "-")
	wasmPull, err := oci.Pull(ctx, store, wasmTag)
	if err != nil {
		t.Fatalf("pull .wasm by content-digest tag: %v", err)
	}
	if wasmPull.MediaType != oci.MediaTypeWasm {
		t.Errorf(".wasm mediatype: got %q want %q", wasmPull.MediaType, oci.MediaTypeWasm)
	}
	if got := digest.ContentDigest(wasmPull.Bytes); got != contentDigest {
		t.Errorf(".wasm content digest: got %q want %q", got, contentDigest)
	}
}

// TestPushPluginPublishedEntityReloadsClean proves the published Plugin entity
// is schema-valid on reload: the bytes push emits pass envelope validation with
// no error (the digest-free spec.plugin the schema requires), and carry no
// @sha256: digest.
func TestPushPluginPublishedEntityReloadsClean(t *testing.T) {
	ctx := context.Background()
	entityPath, pluginPath, _ := writeAgreeingPlugin(t)
	store := memory.New()

	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	entityPull, err := oci.Pull(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("pull entity: %v", err)
	}
	if _, err := envelope.ValidateBytes(envelope.KindPlugin, entityPull.Bytes, "reloaded.yaml"); err != nil {
		t.Fatalf("published entity should reload schema-clean; got %v", err)
	}
	if strings.Contains(string(entityPull.Bytes), "@sha256:") {
		t.Errorf("published entity must carry no @sha256: digest:\n%s", entityPull.Bytes)
	}
}

// TestPushPluginRequiresPluginFlag proves a Plugin push without
// --plugin halts before any bytes land.
func TestPushPluginRequiresPluginFlag(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "frp-step-patch.yaml")
	writeEntityFixture(t, entityPath, agreeingPluginEntity)
	store := memory.New()

	err := pushRun(pushConfig{
		EntityPath: entityPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	})
	if err == nil {
		t.Fatal("expected an error when --plugin is omitted for a Plugin; got nil")
	}
	if !strings.Contains(err.Error(), "--plugin") {
		t.Errorf("error should name the missing --plugin requirement; got %v", err)
	}
}

// TestPushPluginDescribeMismatchHaltsNoBytes proves the publish-time
// describe()-cross-check: an entity whose authored exports disagree with the
// .wasm's describe() halts with ErrWasmCapabilityMismatch and pushes NO bytes.
func TestPushPluginDescribeMismatchHaltsNoBytes(t *testing.T) {
	ctx := context.Background()
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	contentDigest := digest.ContentDigest(wasmBytes)

	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "frp-step-patch.yaml")
	// scope=host disagrees with the fixture's describe() (scope=own).
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: frp/patch
  version: "1.0.0"
spec:
  plugin: "oci://hub.aggrega.ai/plugin/frp/patch:1.0.0"
  pluginType: frp_step
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: host
`)

	store := memory.New()
	err = pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	})
	if !errors.Is(err, wasm.ErrWasmCapabilityMismatch) {
		t.Fatalf("want ErrWasmCapabilityMismatch, got %v", err)
	}

	// No bytes landed: neither the entity tag nor the .wasm content-digest tag
	// exists in the store.
	if _, exists, rerr := oci.Resolve(ctx, store, "1.0.0"); rerr != nil || exists {
		t.Errorf("entity tag must not exist after a halted publish (exists=%v err=%v)", exists, rerr)
	}
	wasmTag := strings.ReplaceAll(contentDigest, ":", "-")
	if _, exists, rerr := oci.Resolve(ctx, store, wasmTag); rerr != nil || exists {
		t.Errorf(".wasm tag must not exist after a halted publish (exists=%v err=%v)", exists, rerr)
	}
}

// TestPushPluginPartialPushSafety proves the ordering makes the safe
// interleaving the only one: the .wasm is pushed FIRST (addressed by content
// digest), so a failure before the entity push leaves an orphan .wasm
// (harmless, addressed by digest) and NO entity tag — the consume side fails at
// pull on the absent entity rather than loading a broken plugin. Driven by a
// target that fails the entity (second) push while letting the .wasm (first)
// push through.
func TestPushPluginPartialPushSafety(t *testing.T) {
	ctx := context.Background()
	entityPath, pluginPath, contentDigest := writeAgreeingPlugin(t)

	// failSecondPushTarget lets the first push (the .wasm) succeed and fails the
	// second (the entity), simulating a mid-sequence failure.
	store := memory.New()
	target := &failSecondPushTarget{Target: store}

	err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: target,
	})
	if err == nil {
		t.Fatal("expected the entity push to fail; got nil")
	}

	// The orphan .wasm is addressed by its content-digest tag (harmless).
	wasmTag := strings.ReplaceAll(contentDigest, ":", "-")
	if _, exists, _ := oci.Resolve(ctx, store, wasmTag); !exists {
		t.Error(".wasm artifact should have landed first (it is the harmless orphan)")
	}
	// The entity tag does NOT exist, so the consume side fails at pull rather
	// than loading a dangling-referenced plugin.
	pr, perr := oci.Pull(ctx, store, "1.0.0")
	if perr == nil {
		t.Fatalf("entity must not be pullable after a failed entity push; pulled %q", pr.MediaType)
	}
	if !errors.Is(perr, oci.ErrFetchNotFound) {
		t.Errorf("consume side should fail-at-pull with ErrFetchNotFound; got %v", perr)
	}
}

// TestPushPluginSignedAndVerifiable proves BOTH the entity and the .wasm
// artifact are signed at publish — each manifest digest is signed and attached
// as a referrer of its own artifact — and each verifies against its published
// manifest digest under the SAME held key, reusing the same verify path the
// catalog-kind signing uses. Two artifacts, two independent signature
// referrers, one signer.
func TestPushPluginSignedAndVerifiable(t *testing.T) {
	ctx := context.Background()
	entityPath, pluginPath, contentDigest := writeAgreeingPlugin(t)

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	store := memory.New()
	wantSign := true
	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
		Sign:       &wantSign,
		Signer:     signer,
	}); err != nil {
		t.Fatalf("pushRun --sign: %v", err)
	}

	// The published entity's manifest digest is what the signature commits to.
	entityPull, err := oci.Pull(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("pull entity: %v", err)
	}

	refs, err := oci.ListReferrers(ctx, store, "1.0.0", signing.BundleMediaType)
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("want exactly one signature referrer on the entity, got %d", len(refs))
	}
	bundle, err := oci.FetchReferrerPayload(ctx, store, refs[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload: %v", err)
	}
	if err := signing.VerifyKeyedBundle(bundle, signer.PublicKeyPEM(), entityPull.Digest); err != nil {
		t.Fatalf("entity signature should verify against the published entity digest; got %v", err)
	}

	// The .wasm artifact is signed independently: a signature referrer is
	// attached to the .wasm (addressed by its content-digest tag) and verifies
	// against the .wasm's published manifest digest under the same key.
	wasmTag := oci.ContentDigestTag(contentDigest)
	wasmPull, err := oci.Pull(ctx, store, wasmTag)
	if err != nil {
		t.Fatalf("pull .wasm: %v", err)
	}
	wasmRefs, err := oci.ListReferrers(ctx, store, wasmTag, signing.BundleMediaType)
	if err != nil {
		t.Fatalf("ListReferrers (.wasm): %v", err)
	}
	if len(wasmRefs) != 1 {
		t.Fatalf("want exactly one signature referrer on the .wasm, got %d", len(wasmRefs))
	}
	wasmBundle, err := oci.FetchReferrerPayload(ctx, store, wasmRefs[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload (.wasm): %v", err)
	}
	if err := signing.VerifyKeyedBundle(wasmBundle, signer.PublicKeyPEM(), wasmPull.Digest); err != nil {
		t.Fatalf(".wasm signature should verify against the published .wasm manifest digest; got %v", err)
	}
}

// failSecondPushTarget wraps an oras target and fails tagging the entity
// version ("1.0.0"), simulating the entity push failing after the .wasm (tagged
// by its content digest) has fully landed — the partial-push interleaving under
// test. Every other operation delegates to the embedded target.
type failSecondPushTarget struct {
	oras.Target
}

func (f *failSecondPushTarget) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	if reference == "1.0.0" {
		return errors.New("simulated entity push failure")
	}
	return f.Target.Tag(ctx, desc, reference)
}

// TestContentDigestTagPushPullParity pins the single-source tag contract: the
// .wasm-publishing tag the push derives and the tag the consume side derives to
// FETCH it both come from the one oci.ContentDigestTag function. A divergence
// here would mean the push tags the artifact under one tag and the pull looks
// under another — a published plugin that silently cannot be located.
func TestContentDigestTagPushPullParity(t *testing.T) {
	ctx := context.Background()
	entityPath, pluginPath, contentDigest := writeAgreeingPlugin(t)
	store := memory.New()

	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	// The single shared derivation: the tag the push published the .wasm under is
	// exactly the tag the consume side computes to fetch it.
	tag := oci.ContentDigestTag(contentDigest)
	if _, err := oci.Pull(ctx, store, tag); err != nil {
		t.Fatalf("the .wasm is not resolvable under oci.ContentDigestTag(%q)=%q: %v", contentDigest, tag, err)
	}
}

// largeGuestBytes returns the decompressed bytes of the largest embedded
// standard-Go provider guest (the helm provider). Its linear-memory working set
// exceeds the platform default cap, so it only instantiates for the publish
// describe()-cross-check when the load honors a raised memory cap. Skips cleanly
// when the embedded guests are not built into the test binary.
func largeGuestBytes(t *testing.T) []byte {
	t.Helper()
	for _, uri := range embedded.URIs() {
		if strings.HasSuffix(uri, "/helm") {
			if b, ok := embedded.Lookup(uri); ok {
				return b
			}
		}
	}
	t.Skip("embedded helm provider guest not built into the test binary")
	return nil
}

// writeLargeGuestPlugin writes the large guest .wasm and a Plugin entity (with
// the given runtime block) into a fresh tempdir, chdirs into it, and returns the
// entity path and the .wasm path. The entity's authored claim agrees with the
// guest's describe() (export render_node, no plugin-level capabilities claim) so
// the cross-check turns only on whether the guest loads under the resolved cap.
func writeLargeGuestPlugin(t *testing.T, runtimeBlock string) (entityPath, pluginPath string) {
	t.Helper()
	wasmBytes := largeGuestBytes(t)
	root := t.TempDir()
	t.Chdir(root)
	pluginPath = filepath.Join(root, "helm-provider.wasm")
	if err := os.WriteFile(pluginPath, wasmBytes, 0o644); err != nil {
		t.Fatalf("write large guest .wasm: %v", err)
	}
	entityPath = filepath.Join(root, "helm-plugin.yaml")
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-renderer
  version: "0.4.1"
spec:
  plugin: "oci://hub.aggrega.ai/plugin/provider/helm"
  pluginType: provider
  abiVersion: v1alpha1
  targets:
    - helm
  exports:
    - name: render_node
`+runtimeBlock)
	return entityPath, pluginPath
}

// TestPushPluginLargeGuestDryRunUnderDeclaredCap proves the publish
// describe()-cross-check loads a large standard-Go guest under the entity's
// declared memory cap, so a dry-run of a guest whose working set exceeds the
// platform default succeeds instead of failing to instantiate.
func TestPushPluginLargeGuestDryRunUnderDeclaredCap(t *testing.T) {
	entityPath, pluginPath := writeLargeGuestPlugin(t, "  runtime:\n    maxMemoryPages: 4096\n")

	var out bytes.Buffer
	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		DryRun:     true,
		Out:        &out,
	}); err != nil {
		t.Fatalf("dry-run of a large guest under its declared cap should succeed; got %v", err)
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Errorf("dry-run output should report the proposed reference; got:\n%s", out.String())
	}
}

// TestPushPluginLargeGuestDryRunFallsBackToDescribeFloor proves that when the
// entity declares no runtime memory cap, the cross-check load falls back to the
// describe-time headroom rather than the platform default, so a large guest with
// no authored cap still instantiates for the dry-run.
func TestPushPluginLargeGuestDryRunFallsBackToDescribeFloor(t *testing.T) {
	entityPath, pluginPath := writeLargeGuestPlugin(t, "")

	var out bytes.Buffer
	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		DryRun:     true,
		Out:        &out,
	}); err != nil {
		t.Fatalf("dry-run of a large guest with no declared cap should fall back to the describe floor and succeed; got %v", err)
	}
}

// TestPushPluginLargeGuestDefaultCapInsufficient pins the load-bearing half of
// the cap threading: a large guest loaded under the platform default cap (the
// pre-fix behavior) fails to instantiate for the cross-check, and the failure is
// a load error, not a capability mismatch — proving the guest genuinely needs a
// raised cap and that the declared cap is honored.
func TestPushPluginLargeGuestDefaultCapInsufficient(t *testing.T) {
	entityPath, pluginPath := writeLargeGuestPlugin(t, "  runtime:\n    maxMemoryPages: 1024\n")

	err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		DryRun:     true,
		Out:        &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("a large guest loaded under the platform default cap should fail to instantiate; got nil")
	}
	if errors.Is(err, wasm.ErrWasmCapabilityMismatch) {
		t.Fatalf("failure should be a load error, not a capability mismatch; got %v", err)
	}
}

// writeProviderPluginWithCapabilities writes the embedded helm provider guest and
// a Plugin entity carrying the given trailing spec block (a plugin-level
// capabilities block plus a runtime cap) into a fresh tempdir, chdirs into it,
// and returns the entity and .wasm paths. Unlike writeLargeGuestPlugin, the
// entity authors a plugin-level capability claim, so a publish exercises the full
// capabilities cross-check — not only the per-export claim. Skips cleanly when
// the embedded guests are not built into the test binary.
func writeProviderPluginWithCapabilities(t *testing.T, specTail string) (entityPath, pluginPath string) {
	t.Helper()
	wasmBytes := largeGuestBytes(t)
	root := t.TempDir()
	t.Chdir(root)
	pluginPath = filepath.Join(root, "helm-provider.wasm")
	if err := os.WriteFile(pluginPath, wasmBytes, 0o644); err != nil {
		t.Fatalf("write provider guest .wasm: %v", err)
	}
	entityPath = filepath.Join(root, "helm-plugin.yaml")
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-renderer
  version: "0.4.1"
spec:
  plugin: "oci://hub.aggrega.ai/plugin/provider/helm"
  pluginType: provider
  abiVersion: v1alpha1
  targets:
    - helm
  exports:
    - name: render_node
`+specTail)
	return entityPath, pluginPath
}

// TestPushPluginProviderReusableInstanceCrossCheck proves the publish
// describe()-cross-check carries the full authored plugin-level capability claim —
// including reusable_instance — into the comparison. The embedded helm provider
// self-reports reusable_instance true; a provider entity authoring
// reusableInstance: true (exactly as the standard helm-renderer entity does) must
// therefore cross-check clean at publish. A mapping that dropped the field would
// compare the authored true as false against the guest's true and halt with
// ErrWasmCapabilityMismatch, blocking publication of every reusable provider.
func TestPushPluginProviderReusableInstanceCrossCheck(t *testing.T) {
	entityPath, pluginPath := writeProviderPluginWithCapabilities(t, `  capabilities:
    inputKinds:
      sourced: true
      synthesized: true
      artifactsStandalone: true
    boundaryAssembly: none
    reusableInstance: true
  runtime:
    maxMemoryPages: 4096
`)

	var out bytes.Buffer
	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		DryRun:     true,
		Out:        &out,
	}); err != nil {
		t.Fatalf("a provider authoring reusableInstance: true (matching the guest's describe()) should cross-check clean at publish; got %v", err)
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Errorf("dry-run output should report the proposed reference; got:\n%s", out.String())
	}
}

// TestPushPluginProviderReusableInstanceMismatchHalts is the negative control:
// an entity authoring reusableInstance: false while the guest self-reports true
// halts with ErrWasmCapabilityMismatch, and the message names the field. It
// proves the publish cross-check genuinely compares the reusable-instance claim
// (and so that the mapping carries it), rather than silently ignoring it — the
// two tests together bracket the field's round-trip through the publish path.
func TestPushPluginProviderReusableInstanceMismatchHalts(t *testing.T) {
	entityPath, pluginPath := writeProviderPluginWithCapabilities(t, `  capabilities:
    inputKinds:
      sourced: true
      synthesized: true
      artifactsStandalone: true
    boundaryAssembly: none
    reusableInstance: false
  runtime:
    maxMemoryPages: 4096
`)

	err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		DryRun:     true,
		Out:        &bytes.Buffer{},
	})
	if !errors.Is(err, wasm.ErrWasmCapabilityMismatch) {
		t.Fatalf("want ErrWasmCapabilityMismatch for a reusable-instance disagreement; got %v", err)
	}
	if !strings.Contains(err.Error(), "reusable_instance") {
		t.Errorf("the rejection should name the reusable_instance field; got %v", err)
	}
}

// TestPublishPullRoundTripPlugin proves the consume-side gap is closed: a
// Plugin published via the real push path (the .wasm tagged by its content
// digest, the entity spec.plugin carrying the digest-free identity) resolves
// end-to-end through ResolvePlugin — the pull fetches the .wasm by its
// content-digest tag, content-digest-verifies it, caches it, re-resolves from
// cache, and a tampered cache slot halts with ErrFetchIntegrity.
func TestPublishPullRoundTripPlugin(t *testing.T) {
	ctx := context.Background()
	entityPath, pluginPath, contentDigest := writeAgreeingPlugin(t)
	store := memory.New()

	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	// The published entity's spec.plugin is the digest-free identity; the
	// artifactURI below is the resolved artifact reference a consumer forms
	// after obtaining the content digest at pull, not a value read from the
	// entity.
	entityPull, err := oci.Pull(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("pull entity: %v", err)
	}
	wantIdentity := "oci://hub.aggrega.ai/plugin/frp/patch"
	if !strings.Contains(string(entityPull.Bytes), wantIdentity) {
		t.Fatalf("published entity spec.plugin not the digest-free identity %q:\n%s", wantIdentity, entityPull.Bytes)
	}
	if strings.Contains(string(entityPull.Bytes), "@sha256:") {
		t.Fatalf("published entity must carry no @sha256: digest in spec.plugin:\n%s", entityPull.Bytes)
	}
	artifactURI := "oci://hub.aggrega.ai/plugin/frp/patch@" + contentDigest

	root := t.TempDir()
	cat, err := catalog.LoadAll("", root)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	lookup := cat.ClosureLookup(&memStorePuller{store: store}, root, ".aggrega/cache")

	// Pull: tier-3, content-digest-verified, cached.
	res, err := lookup.ResolvePlugin(artifactURI)
	if err != nil {
		t.Fatalf("ResolvePlugin (pull): %v", err)
	}
	if res.ResolutionPath != "tier-3-pull" {
		t.Errorf("ResolutionPath = %q; want tier-3-pull", res.ResolutionPath)
	}
	if res.Digest != contentDigest {
		t.Errorf("Digest = %q; want %q", res.Digest, contentDigest)
	}

	// The .wasm bytes are in the cache slot the build re-reads (keyed by the pin).
	cached, err := oci.ReadCached(root, ".aggrega/cache", artifactURI, contentDigest)
	if err != nil {
		t.Fatalf("expected .wasm cached after pull: %v", err)
	}
	if got := digest.ContentDigest(cached); got != contentDigest {
		t.Errorf("cached .wasm digest = %q; want %q", got, contentDigest)
	}

	// Second resolve with no puller: a cache hit (tier-2), proving lock-write slot
	// == cache-read slot.
	cachedLookup := cat.ClosureLookup(nil, root, ".aggrega/cache")
	res2, err := cachedLookup.ResolvePlugin(artifactURI)
	if err != nil {
		t.Fatalf("ResolvePlugin (cache): %v", err)
	}
	if res2.NotFound || res2.Digest != contentDigest {
		t.Errorf("cache-hit resolution = %+v; want digest %q", res2, contentDigest)
	}

	// Tamper the cached blob: the content-digest check halts with ErrFetchIntegrity.
	slot := oci.CachePath(root, ".aggrega/cache", artifactURI, contentDigest)
	if err := os.WriteFile(filepath.Join(slot, "blob"), []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tamper write: %v", err)
	}
	if _, err := cachedLookup.ResolvePlugin(artifactURI); !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Errorf("tampered cache: err = %v; want ErrFetchIntegrity", err)
	}
}
