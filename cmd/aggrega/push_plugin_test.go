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
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

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

// TestPushPluginSingleManifestPublish proves the single-manifest publish: the
// entity and its .wasm land as ONE manifest tagged by metadata.version — the
// entity YAML in the config blob (spec.plugin left as the authored digest-free
// identity), the .wasm as an inline layer. FetchPluginManifest reads both back
// from the one version tag, and the inline layer content-verifies to the pin.
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

	// The config blob is the entity, with spec.plugin left as the digest-free
	// identity.
	wantIdentity := "oci://hub.aggrega.ai/plugin/frp/patch"
	if !strings.Contains(string(entityYAML), wantIdentity) {
		t.Errorf("published entity spec.plugin not the digest-free identity %q; got:\n%s", wantIdentity, entityYAML)
	}
	if strings.Contains(string(entityYAML), "@sha256:") {
		t.Errorf("published entity must carry no @sha256: digest in spec.plugin; got:\n%s", entityYAML)
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
// envelope validation with no error (the digest-free spec.plugin the schema
// requires), and carries no @sha256: digest.
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
// .wasm's describe() halts with ErrWasmCapabilityMismatch and pushes NO bytes.
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
	err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	})
	if !errors.Is(err, wasm.ErrWasmCapabilityMismatch) {
		t.Fatalf("want ErrWasmCapabilityMismatch, got %v", err)
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
	if !errors.Is(perr, oci.ErrFetchNotFound) {
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
