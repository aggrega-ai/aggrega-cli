// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
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
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
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
// operates=patch, scope=own, no custom host functions). spec.plugin is a
// placeholder reference that publish stamps to the .wasm content digest.
const agreeingPluginEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: frp/patch
  version: "1.0.0"
spec:
  plugin: "oci://hub.aggrega.ai/wasm/frp/patch:1.0.0"
  pluginTypes:
    - frp_step
  abiVersion: v1alpha1
  compatibleProviders:
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
// lands (tagged by metadata.version, referencing the .wasm by content-digest
// pin in spec.plugin). Both are resolvable in the registry double after publish.
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
	// mediatype, with spec.plugin stamped to the .wasm content digest.
	entityPull, err := oci.Pull(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("pull entity: %v", err)
	}
	if entityPull.MediaType != oci.MediaTypePlugin {
		t.Errorf("entity mediatype: got %q want %q", entityPull.MediaType, oci.MediaTypePlugin)
	}
	wantPin := "oci://hub.aggrega.ai/wasm/frp/patch@" + contentDigest
	if !strings.Contains(string(entityPull.Bytes), wantPin) {
		t.Errorf("published entity spec.plugin not stamped to %q; got:\n%s", wantPin, entityPull.Bytes)
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
  plugin: "oci://hub.aggrega.ai/wasm/frp/patch:1.0.0"
  pluginTypes:
    - frp_step
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

// TestPushPluginPrePinnedMismatchHalts proves the digest-stamp guard: an
// on-disk spec.plugin already pinning a DIFFERENT @sha256: digest than the
// published .wasm halts loudly rather than silently overwriting the pin.
func TestPushPluginPrePinnedMismatchHalts(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "frp-step-patch.yaml")
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: frp/patch
  version: "1.0.0"
spec:
  plugin: "oci://hub.aggrega.ai/wasm/frp/patch@sha256:0000000000000000000000000000000000000000000000000000000000000000"
  pluginTypes:
    - frp_step
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`)

	store := memory.New()
	err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	})
	if !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Fatalf("want ErrFetchIntegrity for a pre-pinned mismatch, got %v", err)
	}
	if _, exists, _ := oci.Resolve(context.Background(), store, "1.0.0"); exists {
		t.Error("entity tag must not exist after a halted pre-pinned-mismatch publish")
	}
}

// TestPushPluginPrePinnedMatchSucceeds proves an on-disk spec.plugin
// already pinned to the EXACT published .wasm content digest is accepted (the
// pin agrees with the bytes) and publishes without re-stamping.
func TestPushPluginPrePinnedMatchSucceeds(t *testing.T) {
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
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: frp/patch
  version: "1.0.0"
spec:
  plugin: "oci://hub.aggrega.ai/wasm/frp/patch@`+contentDigest+`"
  pluginTypes:
    - frp_step
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`)

	store := memory.New()
	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("pushRun with a matching pre-pin should succeed; got %v", err)
	}
	if _, exists, _ := oci.Resolve(ctx, store, "1.0.0"); !exists {
		t.Error("entity tag should exist after a successful matching-pre-pin publish")
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

// TestPushPluginSignedAndVerifiable proves the entity is signed at publish
// (the entity manifest digest is signed and attached as a referrer) and the
// signature verifies against the published entity digest with the held key —
// reusing the same verify path the catalog-kind signing uses.
func TestPushPluginSignedAndVerifiable(t *testing.T) {
	ctx := context.Background()
	entityPath, pluginPath, _ := writeAgreeingPlugin(t)

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

// TestPublishPullRoundTripPlugin proves the consume-side gap is closed: a
// Plugin published via the real push path (the .wasm tagged by its content
// digest, the entity spec.plugin content-pinned) resolves end-to-end through
// ResolvePlugin — the pull fetches the .wasm by its content-digest tag,
// content-digest-verifies it, caches it, re-resolves from cache, and a tampered
// cache slot halts with ErrFetchIntegrity.
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

	// Read the artifact URI from the PUBLISHED entity's stamped spec.plugin rather
	// than hand-spelling it — the pull resolves exactly what publish stamped.
	entityPull, err := oci.Pull(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("pull entity: %v", err)
	}
	artifactURI := "oci://hub.aggrega.ai/wasm/frp/patch@" + contentDigest
	if !strings.Contains(string(entityPull.Bytes), artifactURI) {
		t.Fatalf("published entity spec.plugin not content-pinned to %q:\n%s", artifactURI, entityPull.Bytes)
	}

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
