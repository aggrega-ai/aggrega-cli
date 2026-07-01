// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// publishSignedPluginEntity publishes a REAL kind:Plugin entity (the .wasm
// fixture + the stamped entity) to the in-process registry through the
// production push path with keyed signing requested. The production push signs
// both the .wasm artifact and the entity manifest with the one signer, so each
// lands with a verifiable signature referrer. It returns the .wasm content
// digest the closure pins. It is the signed counterpart of publishPluginEntity.
func publishSignedPluginEntity(t *testing.T, host, socketName, version, pluginPath string, signer signing.Signer) string {
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
  name: `+socketName+`
  version: "`+version+`"
spec:
  plugin: "oci://`+host+`/wasm/`+socketName+`:`+version+`"
  pluginTypes:
    - frp_step
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`)
	sign := true
	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   host,
		Sign:       &sign,
		Signer:     signer,
	}); err != nil {
		t.Fatalf("publish signed plugin entity %s:%s: %v", socketName, version, err)
	}
	return contentDigest
}

// writePipelinePluginConsumerWorkspace writes a controlled fixture catalog (a
// helm-provider Component whose Recipe carries a one-step pipeline referencing
// the named plugin by its bare identity) and a consumer workspace wired to build
// that Component over the registry at host. The workspace trust policy REQUIRES
// signatures and pins pinnedPubPEM as the keyed public key. It returns the
// workspace path (not yet chdir'd into). Because the fixture catalog entities
// resolve under their host-less catalog identities, only the host-carrying .wasm
// plugin artifact is subject to the signature-requiring policy — the axis a real
// pull's verify pass reaches.
func writePipelinePluginConsumerWorkspace(t *testing.T, host, pluginURI string, pinnedPubPEM []byte) string {
	t.Helper()
	cat := t.TempDir()
	writeFixtureEntity(t, cat, "provider/helm/1.0.0/provider.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  baseSchema: |
    type: object
    properties:
      image: { type: string }
      replicas: { type: integer, default: 1 }
    additionalProperties: true
`)
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
`)
	writeFixtureEntity(t, cat, "recipe/helm/svc/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  provider: helm
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
	pushCatalog(t, host, cat)

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
	b.WriteString("    requires_signatures: true\n")
	b.WriteString("    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(pinnedPubPEM), "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	return b.String()
}

// TestIntegration_PullCommandVerifiesSignedPlugin_Keyed is the keyed
// publish→pull→verify capstone driven through the PRODUCTION pull command. A
// Recipe references a pipeline plugin by its bare identity; the plugin is
// published SIGNED (the .wasm artifact and its Plugin entity both carry a keyed
// signature referrer). A consumer workspace whose trust policy REQUIRES
// signatures and pins the publisher's key runs the real pullRun. The pull walks
// the closure — the two-pass plugin descent records the host-carrying .wasm
// artifact URI (oci://host/wasm/...@sha256:...) — and its verify pass verifies
// that artifact's signature against the pinned key, folding the verified fact
// onto the lockfile. This exercises the whole spine (resolution → closure
// descent → buildLockfile → verifyClosureSignatures → pullverify.Verify) through
// the exact code path production runs: no hand-built closure, no hand-set
// digest, no skip flag.
//
// The .wasm artifact URI is the axis a real pull's verify pass reaches, because
// the closure records catalog entities under their host-less catalog identities
// while a plugin artifact is pinned by its content-addressed oci:// URI that
// carries the registry host.
//
// The negative arm shares the identical production spine and changes only the
// pinned key, so the rejection is attributable to the trust policy and nothing
// else: a wrong pinned key → the pull HALTS with ErrSignatureVerificationFailed.
func TestIntegration_PullCommandVerifiesSignedPlugin_Keyed(t *testing.T) {
	// Resolve the committed .wasm fixture BEFORE any chdir (fixture discovery
	// walks up from cwd); a clean skip if the toolchain is absent.
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	const (
		pluginSocket = "pipeline"
		pluginName   = "patch-server"
		version      = "1.0.0"
	)
	pluginURI := pluginSocket + "/" + pluginName + ":" + version

	t.Run("positive_signed_plugin_verifies_through_pull_command", func(t *testing.T) {
		host := startRegistry(t, nil)
		wasmContentDg := publishSignedPluginEntity(t, host, pluginSocket+"/"+pluginName, version, pluginPath, signer)
		artifactURI := "oci://" + host + "/wasm/" + pluginSocket + "/" + pluginName + "@" + wasmContentDg
		ws := writePipelinePluginConsumerWorkspace(t, host, pluginURI, pubPEM)
		t.Chdir(ws)

		if perr := pullRun(pullConfig{ReleasePath: "release.yaml"}); perr != nil {
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
		if artifactRow.Signature == nil {
			t.Fatal("no verified signature fact recorded on the .wasm artifact row; the verify pass did not fire through the pull")
		}

		// Build reads the recorded facts and completes: signing changes the
		// supply-chain trust, not the buildability.
		if berr := dispatchBuild(nil, "release.yaml", nil, false, os.Stderr, os.Stdout); berr != nil {
			t.Fatalf("build after verified pull: %v", berr)
		}
	})

	t.Run("negative_wrong_pinned_key_halts_pull", func(t *testing.T) {
		host := startRegistry(t, nil)
		publishSignedPluginEntity(t, host, pluginSocket+"/"+pluginName, version, pluginPath, signer)

		// Pin a DIFFERENT key: the signature verifies against the publisher's key
		// but not this one, so the require-signatures policy halts the pull.
		_, wrongPubPEM, gerr := signing.GenerateKeyedPEM()
		if gerr != nil {
			t.Fatalf("GenerateKeyedPEM (wrong): %v", gerr)
		}
		ws := writePipelinePluginConsumerWorkspace(t, host, pluginURI, wrongPubPEM)
		t.Chdir(ws)

		perr := pullRun(pullConfig{ReleasePath: "release.yaml"})
		if !errors.Is(perr, signing.ErrSignatureVerificationFailed) {
			t.Fatalf("a wrong pinned key must HALT the pull with ErrSignatureVerificationFailed; got: %v", perr)
		}
	})

	t.Run("negative_unsigned_plugin_halts_under_require_signatures", func(t *testing.T) {
		host := startRegistry(t, nil)
		// Publish the plugin UNSIGNED, then pull under a require-signatures policy:
		// the missing signature on the host-carrying .wasm artifact is a Loud
		// Guardrail.
		publishPluginEntity(t, host, pluginSocket+"/"+pluginName, version, pluginPath)
		ws := writePipelinePluginConsumerWorkspace(t, host, pluginURI, pubPEM)
		t.Chdir(ws)

		perr := pullRun(pullConfig{ReleasePath: "release.yaml"})
		if !errors.Is(perr, signing.ErrNoSignatureFound) {
			t.Fatalf("an unsigned plugin under a require-signatures policy must HALT with ErrNoSignatureFound; got: %v", perr)
		}
	})
}
