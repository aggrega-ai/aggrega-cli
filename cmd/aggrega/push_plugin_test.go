// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
// production remote-repository adapter pulls. For a plugin manifest oci.Pull
// returns the inline .wasm layer as the content bytes and the manifest digest,
// which is what resolveVersionedPluginArtifact expects.
type memStorePuller struct{ store *memory.Store }

func (p *memStorePuller) Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error) {
	return oci.Pull(ctx, p.store, tag)
}

// RegistryHost lets the puller bind a bare plugin identity to a location during
// resolution. The single in-memory store keys on the tag alone, so the fetch
// ignores the reference — the host only needs to be a well-formed registry.
func (p *memStorePuller) RegistryHost() string { return "hub.aggrega.ai" }

// agreeingPluginEntity is a Plugin entity whose authored export claim
// agrees with the frp-step-go fixture's describe() (export "step",
// operates=patch, scope=own, no custom host functions). Its bare identity
// (metadata.name) is its artifact reference; the entity carries no separate
// artifact field.
const agreeingPluginEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: patch
  pluginType: pipeline-step
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  capabilities:
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

// TestPushPluginSingleManifestPublish proves the single-manifest publish: the
// entity and its .wasm land as ONE manifest tagged by metadata.version — the
// entity YAML in the config blob (its identity is metadata.name; no separate
// artifact field), the .wasm as an inline layer. FetchPluginManifest reads both
// back from the one version tag, and the inline layer content-verifies to the pin.
func TestPushPluginSingleManifestPublish(t *testing.T) {
	ctx := context.Background()
	entityPath, pluginPath, contentDigest := writeAgreeingPlugin(t)
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	store := memory.New()

	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	entityYAML, wasm, manifestDigest, err := oci.FetchPluginManifest(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("FetchPluginManifest: %v", err)
	}
	if manifestDigest == "" {
		t.Error("manifest digest should be non-empty")
	}

	// The config blob is the entity; its identity is metadata.name and it carries
	// no separate plugin-artifact field.
	if !strings.Contains(string(entityYAML), "name: patch") {
		t.Errorf("published entity should carry its bare identity metadata.name; got:\n%s", entityYAML)
	}
	if strings.Contains(string(entityYAML), "@sha256:") {
		t.Errorf("published entity must carry no @sha256: digest; got:\n%s", entityYAML)
	}

	// The inline .wasm layer is the pushed bytes; its content digest is the pin.
	if !bytes.Equal(wasm, wasmBytes) {
		t.Error("inline .wasm layer bytes should equal the pushed .wasm bytes")
	}
	if got := digest.ContentDigest(wasm); got != contentDigest {
		t.Errorf(".wasm content digest: got %q want %q", got, contentDigest)
	}
}

// TestPushPluginPublishedEntityReloadsClean proves the published Plugin entity
// is schema-valid on reload: the entity config blob the manifest carries passes
// envelope validation with no error, and carries no @sha256: digest.
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

	entityYAML, _, _, err := oci.FetchPluginManifest(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("FetchPluginManifest: %v", err)
	}
	if _, err := envelope.ValidateBytes(envelope.KindPlugin, entityYAML, "reloaded.yaml"); err != nil {
		t.Fatalf("published entity should reload schema-clean; got %v", err)
	}
	if strings.Contains(string(entityYAML), "@sha256:") {
		t.Errorf("published entity must carry no @sha256: digest:\n%s", entityYAML)
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
// .wasm's describe() halts with ErrWasmContractMismatch and pushes NO bytes.
func TestPushPluginDescribeMismatchHaltsNoBytes(t *testing.T) {
	ctx := context.Background()
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "frp-step-patch.yaml")
	// scope=host disagrees with the fixture's describe() (scope=own).
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: patch
  pluginType: pipeline-step
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: host
`)

	store := memory.New()
	err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	})
	if !errors.Is(err, wasm.ErrWasmContractMismatch()) {
		t.Fatalf("want ErrWasmContractMismatch, got %v", err)
	}

	// No bytes landed: the version tag does not exist in the store.
	if _, exists, rerr := oci.Resolve(ctx, store, "1.0.0"); rerr != nil || exists {
		t.Errorf("version tag must not exist after a halted publish (exists=%v err=%v)", exists, rerr)
	}
}

// TestPushPluginPartialPushSafety proves the version tag is the atomic commit
// point of the single-manifest publish: a failure tagging the manifest leaves
// the config and layer blobs in the content store (unreferenced, harmless) and
// NO version tag — the consume side fails at pull on the absent tag rather than
// loading a broken plugin. Driven by a target that fails the version-tag write
// after the blobs have landed.
func TestPushPluginPartialPushSafety(t *testing.T) {
	ctx := context.Background()
	entityPath, pluginPath, _ := writeAgreeingPlugin(t)

	store := memory.New()
	target := &failVersionTagTarget{Target: store}

	err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: target,
	})
	if err == nil {
		t.Fatal("expected the manifest tag to fail; got nil")
	}

	// The version tag does NOT exist, so the consume side fails at pull rather
	// than loading a dangling plugin.
	pr, perr := oci.Pull(ctx, store, "1.0.0")
	if perr == nil {
		t.Fatalf("plugin must not be pullable after a failed manifest tag; pulled %q", pr.MediaType)
	}
	if !errors.Is(perr, oci.ErrFetchNotFound()) {
		t.Errorf("consume side should fail-at-pull with ErrFetchNotFound; got %v", perr)
	}
}

// TestPushPluginSignedAndVerifiable proves the single manifest is signed ONCE:
// the manifest digest is signed and attached as a lone referrer of the version
// tag, and it verifies against the published manifest digest under the held key,
// reusing the same verify path the catalog-kind signing uses. Because the digest
// commits to the inline .wasm layer, the one signature transitively covers the
// executable — there is no second referrer.
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

	// The published manifest digest is what the single signature commits to.
	_, _, manifestDigest, err := oci.FetchPluginManifest(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("FetchPluginManifest: %v", err)
	}

	refs, err := oci.ListReferrers(ctx, store, "1.0.0", signing.BundleMediaType)
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("want exactly one signature referrer on the manifest, got %d", len(refs))
	}
	bundle, err := oci.FetchReferrerPayload(ctx, store, refs[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload: %v", err)
	}
	if err := signing.VerifyKeyedBundle(bundle, signer.PublicKeyPEM(), manifestDigest); err != nil {
		t.Fatalf("signature should verify against the published manifest digest; got %v", err)
	}
}

// failVersionTagTarget wraps an oras target and fails tagging the entity version
// ("1.0.0"), simulating the manifest tag — the atomic commit point of the
// single-manifest publish — failing after its blobs have landed in the content
// store. Every other operation delegates to the embedded target.
type failVersionTagTarget struct {
	oras.Target
}

func (f *failVersionTagTarget) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	if reference == "1.0.0" {
		return errors.New("simulated manifest tag failure")
	}
	return f.Target.Tag(ctx, desc, reference)
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

// largeGuestURI returns the embedded URI of the helm provider guest — the same
// guest largeGuestBytes returns bytes for. It is the key embeddedGuestVersion
// reads the guest's describe() self-report version from, so a Plugin entity's
// authored version can be aligned to what the guest actually announces.
func largeGuestURI(t *testing.T) string {
	t.Helper()
	for _, uri := range embedded.URIs() {
		if strings.HasSuffix(uri, "/helm") {
			return uri
		}
	}
	t.Skip("embedded helm provider guest not built into the test binary")
	return ""
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
	version := embeddedGuestVersion(t, largeGuestURI(t))
	writeEntityFixture(t, entityPath, fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-renderer
  pluginType: provider
  version: %q
spec:
  plugin: "oci://hub.aggrega.ai/plugin/provider/helm"
  abiVersion: v1alpha1
  capabilities:
    - helm
  exports:
    - name: render_node
`, version)+runtimeBlock)
	// Record the machine-owned host-function set before publishing, mirroring the
	// author workflow: publish is verify-read-only and halts on an out-of-sync
	// record.
	if err := stampPluginFile(&bytes.Buffer{}, entityPath, pluginPath); err != nil {
		t.Fatalf("record host functions: %v", err)
	}
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
	if errors.Is(err, wasm.ErrWasmContractMismatch()) {
		t.Fatalf("failure should be a load error, not a capability mismatch; got %v", err)
	}
}

// writeProviderPluginWithContract writes the embedded helm provider guest and
// a Plugin entity carrying the given trailing spec block (a plugin-level
// capabilities block plus a runtime cap) into a fresh tempdir, chdirs into it,
// and returns the entity and .wasm paths. Unlike writeLargeGuestPlugin, the
// entity authors a plugin-level capability claim, so a publish exercises the full
// capabilities cross-check — not only the per-export claim. Skips cleanly when
// the embedded guests are not built into the test binary.
func writeProviderPluginWithContract(t *testing.T, specTail string) (entityPath, pluginPath string) {
	t.Helper()
	wasmBytes := largeGuestBytes(t)
	root := t.TempDir()
	t.Chdir(root)
	pluginPath = filepath.Join(root, "helm-provider.wasm")
	if err := os.WriteFile(pluginPath, wasmBytes, 0o644); err != nil {
		t.Fatalf("write provider guest .wasm: %v", err)
	}
	entityPath = filepath.Join(root, "helm-plugin.yaml")
	version := embeddedGuestVersion(t, largeGuestURI(t))
	writeEntityFixture(t, entityPath, fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-renderer
  pluginType: provider
  version: %q
spec:
  plugin: "oci://hub.aggrega.ai/plugin/provider/helm"
  abiVersion: v1alpha1
  capabilities:
    - helm
  exports:
    - name: render_node
`, version)+specTail)
	// Record the machine-owned host-function set before publishing, mirroring the
	// author workflow: publish is verify-read-only and halts on an out-of-sync
	// record.
	if err := stampPluginFile(&bytes.Buffer{}, entityPath, pluginPath); err != nil {
		t.Fatalf("record host functions: %v", err)
	}
	return entityPath, pluginPath
}

// TestPushPluginProviderReusableInstanceCrossCheck proves the publish
// describe()-cross-check carries the full authored plugin-level capability claim —
// including reusable_instance — into the comparison. The embedded helm provider
// self-reports reusable_instance true; a provider entity authoring
// reusableInstance: true (exactly as the standard helm-renderer entity does) must
// therefore cross-check clean at publish. A mapping that dropped the field would
// compare the authored true as false against the guest's true and halt with
// ErrWasmContractMismatch, blocking publication of every reusable provider.
func TestPushPluginProviderReusableInstanceCrossCheck(t *testing.T) {
	entityPath, pluginPath := writeProviderPluginWithContract(t, `  provider:
    contract:
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
// halts with ErrWasmContractMismatch, and the message names the field. It
// proves the publish cross-check genuinely compares the reusable-instance claim
// (and so that the mapping carries it), rather than silently ignoring it — the
// two tests together bracket the field's round-trip through the publish path.
func TestPushPluginProviderReusableInstanceMismatchHalts(t *testing.T) {
	entityPath, pluginPath := writeProviderPluginWithContract(t, `  provider:
    contract:
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
	if !errors.Is(err, wasm.ErrWasmContractMismatch()) {
		t.Fatalf("want ErrWasmContractMismatch for a reusable-instance disagreement; got %v", err)
	}
	if !strings.Contains(err.Error(), "reusable_instance") {
		t.Errorf("the rejection should name the reusable_instance field; got %v", err)
	}
}

// TestPublishPullRoundTripPlugin proves the single-manifest consume path end to
// end: a Plugin published via the real push path (one manifest tagged by
// metadata.version, the entity YAML in the config blob, the .wasm as an inline
// layer) resolves through ResolvePlugin using the entity's BARE identity
// (metadata.name) plus its metadata.version. The pull takes the .wasm from the inline
// layer, content-digest-verifies it, caches it; a second resolve reads it from
// the cache slot the build re-reads; and a tampered cache slot halts with
// oci.ErrFetchIntegrity. The .wasm is never a separately-tagged artifact — it is
// intrinsic to the version-tagged manifest.
func TestPublishPullRoundTripPlugin(t *testing.T) {
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

	// The resolve inputs are exactly what a consumer holds: the entity's
	// bare identity (metadata.name) and its metadata.version.
	const uri = "frp/patch"
	const version = "1.0.0"

	root := t.TempDir()
	cat, err := catalog.LoadAll("", root)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Pull: resolve the inline .wasm layer from the version-tagged manifest,
	// content-verify, cache.
	lookup := cat.ClosureLookup(&memStorePuller{store: store}, root, ".aggrega/cache")
	res, err := lookup.ResolvePlugin(uri, version)
	if err != nil {
		t.Fatalf("ResolvePlugin (pull): %v", err)
	}
	if res.ResolutionPath != "tier-3-pull" {
		t.Errorf("ResolutionPath = %q; want tier-3-pull", res.ResolutionPath)
	}
	if res.Digest != contentDigest {
		t.Errorf("Digest = %q; want the inline .wasm content digest %q", res.Digest, contentDigest)
	}
	if res.ManifestDigest == "" {
		t.Error("ManifestDigest should be set to the plugin manifest digest")
	}

	// The .wasm bytes are in the cache slot the build re-reads (keyed by the pin).
	cached, err := oci.ReadCached(root, ".aggrega/cache", uri, contentDigest)
	if err != nil {
		t.Fatalf("expected .wasm cached after pull: %v", err)
	}
	if got := digest.ContentDigest(cached); got != contentDigest {
		t.Errorf("cached .wasm digest = %q; want %q", got, contentDigest)
	}

	// Re-resolve with no puller. The versioned .wasm slot is addressed by the
	// content digest, which the build learns from the lockfile pin — so the cache
	// read is reached through a prior pin, exactly as the offline build does.
	pins := map[string]catalog.Pin{
		uri: {ResolvedVersion: contentDigest, Digest: contentDigest, ManifestDigest: res.ManifestDigest},
	}
	cachedLookup := cat.ClosureLookupWithReuse(nil, root, ".aggrega/cache", nil, nil,
		&catalog.ReuseVerification{PriorPins: pins})
	res2, err := cachedLookup.ResolvePlugin(uri, version)
	if err != nil {
		t.Fatalf("ResolvePlugin (cache): %v", err)
	}
	if res2.NotFound || res2.Digest != contentDigest {
		t.Errorf("cache-hit resolution = %+v; want digest %q", res2, contentDigest)
	}

	// Tamper the cached blob: the content check against the manifest halts with
	// ErrFetchIntegrity rather than serving the corrupted .wasm.
	slot := oci.CachePath(root, ".aggrega/cache", uri, contentDigest)
	if err := os.WriteFile(filepath.Join(slot, "blob"), []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tamper write: %v", err)
	}
	if _, err := cachedLookup.ResolvePlugin(uri, version); !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("tampered cache: err = %v; want ErrFetchIntegrity", err)
	}
}

// TestVersionTagPushPullParity pins the single-source tag contract of the
// single-manifest model: the tag the push publishes the manifest under and the
// tag the consume side resolves the .wasm at are one value — the entity's
// metadata.version. It asserts both ends against that single value: the push
// landed the manifest at metadata.version, and ResolvePlugin at the identical
// metadata.version locates the inline .wasm. A divergence would mean a published
// plugin the consumer silently cannot find. (There is no content-digest-tagged
// .wasm in this model — the executable rides the version-tagged manifest, so the
// tag that is single-sourced is the version itself.)
func TestVersionTagPushPullParity(t *testing.T) {
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

	// metadata.version in agreeingPluginEntity. The push MUST have tagged the
	// manifest under exactly this value.
	const version = "1.0.0"
	if _, exists, rerr := oci.Resolve(ctx, store, version); rerr != nil || !exists {
		t.Fatalf("push must land the manifest at metadata.version %q (exists=%v err=%v)", version, exists, rerr)
	}

	// The consume side resolves the .wasm at the identical value.
	const uri = "frp/patch"
	root := t.TempDir()
	cat, err := catalog.LoadAll("", root)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	lookup := cat.ClosureLookup(&memStorePuller{store: store}, root, ".aggrega/cache")
	res, err := lookup.ResolvePlugin(uri, version)
	if err != nil {
		t.Fatalf("the .wasm is not resolvable at the published metadata.version %q: %v", version, err)
	}
	if res.Digest != contentDigest {
		t.Errorf("resolved .wasm content digest = %q; want %q", res.Digest, contentDigest)
	}
}
