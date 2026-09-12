// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// publishSignedPluginEntity publishes a REAL kind:Plugin entity (the .wasm
// fixture + the stamped entity) to the in-process registry through the
// production push path with keyed signing requested. Under the single-manifest
// model the entity YAML (config blob) and the .wasm (inline layer) publish as ONE
// OCI manifest tagged by metadata.version, signed ONCE — the single manifest
// signature transitively covers the inline .wasm. It returns the .wasm content
// digest the closure pins. It is the signed counterpart of publishPluginEntity,
// mirroring its canonical BARE spec.plugin identity. name is the plugin's BARE
// metadata.name; the socket is pipelineStepPluginType.
func publishSignedPluginEntity(t *testing.T, host, name, version, pluginPath string, signer signing.Signer) string {
	t.Helper()
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin fixture %s: %v", pluginPath, err)
	}
	contentDigest := digest.ContentDigest(wasmBytes)

	entityDir := t.TempDir()
	entityPath := filepath.Join(entityDir, "plugin.yaml")
	writeFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: `+name+`
  pluginType: `+pipelineStepPluginType+`
  version: "`+version+`"
spec:
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`)
	sign := true
	if err := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: entityDir}),
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   host,
		Sign:       &sign,
		Signer:     signer,
	}); err != nil {
		t.Fatalf("publish signed plugin entity %s/%s:%s: %v", pipelineStepPluginType, name, version, err)
	}
	return contentDigest
}

// writePipelinePluginConsumerWorkspace writes a controlled fixture catalog (a
// helm-provider Component whose Recipe carries a one-step pipeline referencing
// the named plugin by its bare identity) and a consumer workspace wired to build
// that Component over the registry at host. The workspace trust policy REQUIRES
// signatures and pins pinnedPubPEM as the keyed public key. It returns the
// workspace path (not yet chdir'd into).
//
// catalogSigner controls how the fixture catalog entities are published: a
// non-nil signer publishes them SIGNED (the whole Tier-3-resolved closure, not
// just the host-carrying .wasm plugin, is subject to the signature-requiring
// policy), a nil signer publishes them unsigned. The .wasm plugin is published
// separately by the caller; this helper covers the catalog axis alongside it.
func writePipelinePluginConsumerWorkspace(t *testing.T, host, pluginURI string, pinnedPubPEM []byte, catalogSigner signing.Signer) string {
	t.Helper()
	cat := t.TempDir()
	writeFixtureEntity(t, cat, "component/test/svc/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
      replicas: { type: integer, default: 1 }
`)
	writeFixtureEntity(t, cat, "recipe/helm/svc/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ optional .infra `+"`replicas`"+` 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ optional .infra `+"`port`"+` 8080 }}
  pipeline:
    - id: patch
      plugin: `+pluginURI+`
      function: step
`)
	if catalogSigner != nil {
		for _, rel := range []string{
			"component/test/svc/1.0.0/component.yaml",
			"recipe/helm/svc/1.0.0/recipe.yaml",
		} {
			pushCatalogEntitySigned(t, host, filepath.Join(cat, rel), catalogSigner)
		}
	} else {
		pushCatalog(t, host, cat)
	}

	ws := t.TempDir()
	writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeFixture(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: helm/svc:1.0.0
`)
	writeFixture(t, filepath.Join(ws, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app:
          infra:
            replicas: 1
`)
	writeWorkspaceConfig(t, ws, signedPluginWorkspaceConfigBody(host, pinnedPubPEM))
	return ws
}

// signedPluginWorkspaceConfigBody renders a workspace config that pins host as a
// keyed, signature-requiring registry against pinnedPubPEM (emitted as a YAML
// block scalar).
func signedPluginWorkspaceConfigBody(host string, pinnedPubPEM []byte) string {
	var b strings.Builder
	b.WriteString("default_registry: \"" + host + "\"\n")
	b.WriteString("auto_pull: true\n")
	b.WriteString("offline: false\n")
	b.WriteString("registries:\n")
	b.WriteString("  - host: \"" + host + "\"\n")
	b.WriteString("    signing:\n      required: true\n")
	b.WriteString("    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(pinnedPubPEM), "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	return b.String()
}

// TestIntegration_PullCommandVerifiesSignedPlugin_Keyed is the keyed
// publish→pull→verify capstone driven through the PRODUCTION pull command. A
// Recipe references a pipeline plugin by its bare identity; the plugin is
// published SIGNED under the single-manifest model — the entity YAML and the
// inline .wasm layer ride ONE OCI manifest tagged by metadata.version, signed
// ONCE, so the single manifest signature transitively covers the .wasm. A
// consumer workspace whose trust policy REQUIRES signatures and pins the
// publisher's key runs the real pullRun. The pull walks the closure — the plugin
// descent records the .wasm ARTIFACT row at its identity coordinate
// <pluginType>/<name>, digest = the .wasm content digest,
// manifest digest = the single manifest digest — and its verify pass verifies
// that manifest's signature against the pinned key, folding the verified fact
// onto the lockfile. This exercises the whole spine (resolution → closure
// descent → buildLockfile → VerifyClosureSignatures → pullverify.Verify) through
// the exact code path production runs: no hand-built closure, no hand-set
// digest, no skip flag.
//
// This test focuses on the .wasm plugin axis (pinned by its bare host-carrying
// oci:// identity); the catalog-entity axis (host-less bare identities bound to
// the registry at pull time) is exercised by the sibling catalog-entity capstone.
// Both axes are under the require-signatures policy, so the fixture catalog
// entities are published signed by the same publisher.
//
// The negative arm shares the identical production spine and changes only the
// pinned key, so the rejection is attributable to the trust policy and nothing
// else: a wrong pinned key → the pull HALTS with ErrSignatureVerificationFailed.
func TestIntegration_PullCommandVerifiesSignedPlugin_Keyed(t *testing.T) {
	// Resolve the committed .wasm fixture BEFORE any chdir (fixture discovery
	// walks up from cwd); a clean skip if the toolchain is absent.
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	// The core helm PROVIDER-rendering plugin ships in the standard catalog with
	// its .wasm embedded in the binary; supply it locally (resolved before any
	// chdir) so the render provider resolves from the embedded core while the
	// registry-published pipeline plugin and fixture catalog resolve over the
	// wire and are signature-verified — the require-signatures policy governs the
	// Tier-3 closure, not the locally-supplied core provider.
	std := standardCatalogDir(t)

	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	const (
		pluginName = "patch-server"
		version    = "1.0.0"
	)
	pluginURI := types.PluginCoordinate(pipelineStepPluginType, pluginName) + ":" + version

	t.Run("positive_signed_plugin_verifies_through_pull_command", func(t *testing.T) {
		host := startRegistry(t, nil)
		wasmContentDg := publishSignedPluginEntity(t, host, pluginName, version, pluginPath, signer)
		// The .wasm ARTIFACT row is keyed by the entity's identity coordinate
		// <pluginType>/<name> under the single-manifest model — no @sha256:
		// content-digest suffix.
		artifactURI := types.PluginCoordinate(pipelineStepPluginType, pluginName)
		// The whole Tier-3-resolved closure is under the require-signatures policy,
		// so the catalog entities are signed by the same publisher too.
		ws := writePipelinePluginConsumerWorkspace(t, host, pluginURI, pubPEM, signer)
		t.Chdir(ws)

		if perr := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: []string{std}}); perr != nil {
			t.Fatalf("a signed plugin under a matching pinned key must PULL and VERIFY; got: %v", perr)
		}

		lf, _, lerr := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
		if lerr != nil {
			t.Fatalf("load lockfile after signed pull: %v", lerr)
		}
		// The verify pass ran through the pull and recorded a verified signature
		// fact on the host-carrying .wasm artifact row.
		var artifactRow *lockfile.Plugin
		for i := range lf.Plugins {
			if lf.Plugins[i].URI == artifactURI {
				artifactRow = &lf.Plugins[i]
				break
			}
		}
		if artifactRow == nil {
			t.Fatalf("the .wasm artifact %q is absent from the written lockfile plugins (have %v)", artifactURI, lf.Plugins)
		}
		// Single-manifest row shape: digest = the .wasm content digest; manifest
		// digest = the single manifest the .wasm rides in.
		if artifactRow.Digest != wasmContentDg {
			t.Errorf("artifact row digest = %q; want the .wasm content digest %q", artifactRow.Digest, wasmContentDg)
		}
		if artifactRow.ManifestDigest == "" {
			t.Error("artifact row pinned no manifest digest; the inline-layer .wasm must pin its single manifest's digest")
		}
		if artifactRow.Signature == nil {
			t.Fatal("no verified signature fact recorded on the .wasm artifact row; the verify pass did not fire through the pull")
		}

		// Build reads the recorded facts and completes: signing changes the
		// supply-chain trust, not the buildability.
		if berr := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout); berr != nil {
			t.Fatalf("build after verified pull: %v", berr)
		}
	})

	t.Run("negative_wrong_pinned_key_halts_pull", func(t *testing.T) {
		host := startRegistry(t, nil)
		publishSignedPluginEntity(t, host, pluginName, version, pluginPath, signer)

		// Pin a DIFFERENT key: the signature verifies against the publisher's key
		// but not this one, so the require-signatures policy halts the pull.
		_, wrongPubPEM, gerr := signing.GenerateKeyedPEM()
		if gerr != nil {
			t.Fatalf("GenerateKeyedPEM (wrong): %v", gerr)
		}
		// The catalog entities are signed by the publisher; only the pinned key
		// differs, so the halt is attributable to the trust-policy mismatch alone.
		ws := writePipelinePluginConsumerWorkspace(t, host, pluginURI, wrongPubPEM, signer)
		t.Chdir(ws)

		perr := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml"})
		if !errors.Is(perr, signing.ErrSignatureVerificationFailed()) {
			t.Fatalf("a wrong pinned key must HALT the pull with ErrSignatureVerificationFailed; got: %v", perr)
		}
	})

	t.Run("negative_unsigned_plugin_halts_under_require_signatures", func(t *testing.T) {
		host := startRegistry(t, nil)
		// Publish the plugin UNSIGNED, then pull under a require-signatures policy:
		// the missing signature on the host-carrying .wasm artifact is a Loud
		// Guardrail.
		publishPluginEntity(t, host, pluginName, version, pluginPath)
		ws := writePipelinePluginConsumerWorkspace(t, host, pluginURI, pubPEM, nil)
		t.Chdir(ws)

		perr := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml"})
		if !errors.Is(perr, signing.ErrNoSignatureFound()) {
			t.Fatalf("an unsigned plugin under a require-signatures policy must HALT with ErrNoSignatureFound; got: %v", perr)
		}
	})
}
