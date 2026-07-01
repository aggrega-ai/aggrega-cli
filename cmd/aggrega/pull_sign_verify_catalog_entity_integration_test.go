// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// pushCatalogEntitySigned publishes a single publishable catalog entity (its
// YAML at a per-version subdirectory) to the in-process registry through the
// production push path with keyed signing requested. The production push signs
// the entity manifest against its OCI manifest digest and attaches the bundle as
// a referrer. It is the catalog-entity counterpart of publishSignedPluginEntity.
func pushCatalogEntitySigned(t *testing.T, host, entityPath string, signer signing.Signer) {
	t.Helper()
	sign := true
	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		Registry:   host,
		Sign:       &sign,
		Signer:     signer,
	}); err != nil {
		t.Fatalf("publish signed catalog entity %s: %v", entityPath, err)
	}
}

// writeCatalogEntityConsumerWorkspace writes a controlled fixture catalog (a
// Provider, a Component addressed by its bare identity, and a Recipe that
// implements it) and pushes each entity to the registry at host through the
// supplied push helper, then writes a consumer workspace wired to build the
// Component over that registry. The consumer workspace carries NO local catalog
// directory, so every catalog entity resolves purely through a Tier-3
// registry pull that binds the bare identity to the registry host from
// default_registry — the axis the pre-fix verify pass skipped. The workspace
// trust policy REQUIRES signatures and pins pinnedPubPEM as the keyed public
// key. It chdir's into the consumer workspace and returns the Component's bare
// closure URI (the lockfile row key the verify fact lands on).
func writeCatalogEntityConsumerWorkspace(t *testing.T, host string, pinnedPubPEM []byte, push func(t *testing.T, host, entityPath string)) string {
	t.Helper()
	cat := t.TempDir()

	providerPath := filepath.Join(cat, "provider/helm/1.0.0/provider.yaml")
	writeFixture(t, providerPath, `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  plugin: "builtin:helm"
`)
	componentPath := filepath.Join(cat, "component/test/svc/1.0.0/component.yaml")
	writeFixture(t, componentPath, `apiVersion: aggrega.ai/v1alpha1
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
	recipePath := filepath.Join(cat, "recipe/helm/svc/1.0.0/recipe.yaml")
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1alpha1
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
`)
	// Push order mirrors a dependency-safe publish: provider, component, recipe.
	push(t, host, providerPath)
	push(t, host, componentPath)
	push(t, host, recipePath)

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
	t.Chdir(ws)
	return "test/svc"
}

// TestIntegration_PullCommandVerifiesSignedCatalogEntity_Keyed is the catalog-entity
// publish→pull→verify capstone driven through the PRODUCTION pull command. It
// closes the authenticity gap where a signed catalog entity (Component / Recipe /
// Provider), recorded in the closure under its host-less bare identity, was never
// signature-verified on pull even under a require-signatures policy: the pre-fix
// verify pass gated on the bare identity's leading path segment ("test") looking
// like a registry host, which it never does, so every catalog entity was silently
// skipped and only the host-carrying .wasm plugin artifact was ever verified.
//
// A Component is addressed by its bare identity from the Aggregate; the closure
// resolves it via a Tier-3 registry pull that binds the identity to the workspace
// registry from default_registry. The three arms drive the whole spine
// (resolution → closure descent → buildLockfile → verifyClosureSignatures →
// pullverify.Verify) through the exact code path production runs: no hand-built
// closure, no hand-set digest, no skip flag.
//
// PRE-FIX FAILURE: without the gate fix, the positive arm's assertion
// (a recorded Signature fact on the Component's lockfile row) fails because the
// verify pass never reaches the catalog entity — it collects zero verifiable
// items from cl.Entities — and the two negative arms do not halt at all, because
// there is no catalog-entity signature to be absent or to mismatch. Each arm's
// expectation is therefore exactly the behaviour the fix introduces.
func TestIntegration_PullCommandVerifiesSignedCatalogEntity_Keyed(t *testing.T) {
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	signedPush := func(t *testing.T, host, entityPath string) {
		pushCatalogEntitySigned(t, host, entityPath, signer)
	}
	unsignedPush := func(t *testing.T, host, entityPath string) {
		if perr := pushRun(pushConfig{EntityPath: entityPath, Registry: host}); perr != nil {
			t.Fatalf("publish unsigned catalog entity %s: %v", entityPath, perr)
		}
	}

	t.Run("positive_signed_entity_verifies_through_pull_command", func(t *testing.T) {
		host := startRegistry(t, nil)
		componentURI := writeCatalogEntityConsumerWorkspace(t, host, pubPEM, signedPush)

		if perr := pullRun(pullConfig{ReleasePath: "release.yaml"}); perr != nil {
			t.Fatalf("a signed catalog entity under a matching pinned key must PULL and VERIFY; got: %v", perr)
		}

		lf, _, lerr := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
		if lerr != nil {
			t.Fatalf("load lockfile after signed pull: %v", lerr)
		}

		// The verify pass ran through the pull and recorded a verified signature
		// fact on the Component's bare-identity closure row. This is the exact
		// thing the pre-fix code never did: a catalog entity's signature verified
		// and its fact folded into the lockfile.
		var componentRow *lockfile.Entity
		for i := range lf.Entities {
			if lf.Entities[i].URI == componentURI {
				componentRow = &lf.Entities[i]
				break
			}
		}
		if componentRow == nil {
			uris := make([]string, 0, len(lf.Entities))
			for i := range lf.Entities {
				uris = append(uris, lf.Entities[i].URI)
			}
			t.Fatalf("the Component %q is absent from the written lockfile entities (have %v)", componentURI, uris)
		}
		if componentRow.Signature == nil {
			t.Fatalf("no verified signature fact recorded on the catalog entity row %q; the verify pass did not fire for a catalog entity", componentURI)
		}
		if componentRow.Signature.Mode != "keyed" {
			t.Fatalf("catalog entity %q: verified signature mode = %q, want keyed", componentURI, componentRow.Signature.Mode)
		}
		if componentRow.ManifestDigest == "" || componentRow.Signature.BundleDigest == "" {
			t.Fatalf("catalog entity %q: signature fact is incomplete (manifest_digest=%q bundle_digest=%q)",
				componentURI, componentRow.ManifestDigest, componentRow.Signature.BundleDigest)
		}
	})

	t.Run("negative_wrong_pinned_key_halts_pull", func(t *testing.T) {
		host := startRegistry(t, nil)
		// Pin a DIFFERENT key: the signature verifies against the publisher's key
		// but not this one, so the require-signatures policy halts the pull on the
		// catalog entity.
		_, wrongPubPEM, gerr := signing.GenerateKeyedPEM()
		if gerr != nil {
			t.Fatalf("GenerateKeyedPEM (wrong): %v", gerr)
		}
		writeCatalogEntityConsumerWorkspace(t, host, wrongPubPEM, signedPush)

		perr := pullRun(pullConfig{ReleasePath: "release.yaml"})
		if !errors.Is(perr, signing.ErrSignatureVerificationFailed) {
			t.Fatalf("a wrong pinned key must HALT the pull with ErrSignatureVerificationFailed; got: %v", perr)
		}
	})

	t.Run("negative_unsigned_entity_halts_under_require_signatures", func(t *testing.T) {
		host := startRegistry(t, nil)
		// Publish the catalog entities UNSIGNED, then pull under a require-signatures
		// policy: the missing signature on a Tier-3-resolved catalog entity is a
		// Loud Guardrail.
		writeCatalogEntityConsumerWorkspace(t, host, pubPEM, unsignedPush)

		perr := pullRun(pullConfig{ReleasePath: "release.yaml"})
		if !errors.Is(perr, signing.ErrNoSignatureFound) {
			t.Fatalf("an unsigned catalog entity under a require-signatures policy must HALT with ErrNoSignatureFound; got: %v", perr)
		}
	})
}
