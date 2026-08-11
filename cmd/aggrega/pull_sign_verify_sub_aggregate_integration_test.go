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

// writeSubAggregateConsumerWorkspace publishes a signed sub-aggregate (and the
// signed Component / Recipe it needs) to the registry at host, then
// writes a consumer workspace whose LOCAL parent aggregate imports that
// sub-aggregate by bare identity. The sub-aggregate is absent from the workspace
// catalog, so it resolves purely through the AggregatePuller tier — the registry
// pull that binds the bare identity to host/aggregate/<name> and pins the fetched
// body's metadata.version. The workspace trust policy REQUIRES signatures and
// pins pinnedPubPEM. It chdir's into the consumer workspace and returns that
// workspace (so the caller can declare it as the workspace it resolves), the
// sub-aggregate's bare closure URI (the lockfile row key the verify fact lands
// on) and the component's bare closure URI.
func writeSubAggregateConsumerWorkspace(t *testing.T, host string, pinnedPubPEM []byte, push func(t *testing.T, host, entityPath string)) (wsRoot, subURI, componentURI string) {
	t.Helper()
	cat := t.TempDir()

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
`)
	// The sub-aggregate: a published Aggregate artifact addressed by its bare
	// identity, importing the registry Component. Its per-version subdirectory
	// (1.0.0) matches metadata.version, satisfying the publish-path guard.
	subAggPath := filepath.Join(cat, "aggregate/mysub/1.0.0/aggregate.yaml")
	writeFixture(t, subAggPath, `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mysub
  version: "1.0.0"
spec:
  components:
    svc:
      type: test/svc
`)

	// Push order mirrors a dependency-safe publish: component, recipe,
	// then the sub-aggregate that references the component.
	push(t, host, componentPath)
	push(t, host, recipePath)
	push(t, host, subAggPath)

	ws := t.TempDir()
	// A LOCAL parent aggregate imports the sub-aggregate by bare identity. The
	// parent is not registry-resolved (it is a ./file), so it carries no
	// signature; the sub-aggregate it names is registry-resolved and must verify.
	writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: top
  version: "1.0.0"
spec:
  aggregates:
    mysub:
      type: mysub
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
        mysub.svc:
          infra:
            replicas: 1
`)
	writeWorkspaceConfig(t, ws, signedPluginWorkspaceConfigBody(host, pinnedPubPEM))
	t.Chdir(ws)
	return ws, "mysub", "test/svc"
}

// TestIntegration_PullCommandVerifiesSignedSubAggregate_Keyed is the
// sub-aggregate publish→pull→verify capstone driven through the PRODUCTION pull
// command. A LOCAL parent aggregate imports a sub-aggregate by bare identity; the
// sub-aggregate is published SIGNED as an Aggregate artifact through the real
// registry harness. A consumer workspace whose trust policy REQUIRES signatures
// and pins the publisher's key runs the real pullRun. The pull walks the closure
// — the sub-aggregate descent records the bare-identity sub-aggregate resolved
// via the AggregatePuller tier — and its verify pass verifies that Aggregate's
// signature against the pinned key (its OCI manifest digest is the verify
// subject), folding the verified fact onto the lockfile.
//
// This closes the coverage gap where only a Component was ever proven to verify
// through pull: a sub-aggregate resolves via a distinct tier (AggregatePuller,
// not the core entity tier), so its verify path — bind mysub -> host/aggregate/
// mysub, discover referrers against the fetched manifest digest, verify the
// bundle — needs its own end-to-end proof. It exercises the whole spine
// (resolution -> sub-aggregate descent -> buildLockfile ->
// verifyClosureSignatures -> pullverify.Verify) through the exact code path
// production runs: no hand-built closure, no hand-set digest, no skip flag.
func TestIntegration_PullCommandVerifiesSignedSubAggregate_Keyed(t *testing.T) {
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

	t.Run("positive_signed_sub_aggregate_verifies_through_pull_command", func(t *testing.T) {
		host := startRegistry(t, nil)
		wsRoot, subURI, _ := writeSubAggregateConsumerWorkspace(t, host, pubPEM, signedPush)

		if perr := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}), ReleasePath: "release.yaml"}); perr != nil {
			t.Fatalf("a signed sub-aggregate under a matching pinned key must PULL and VERIFY; got: %v", perr)
		}

		lf, _, lerr := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
		if lerr != nil {
			t.Fatalf("load lockfile after signed pull: %v", lerr)
		}

		// The verify pass ran through the pull and recorded a verified signature
		// fact on the sub-aggregate's bare-identity closure row — the AggregatePuller
		// tier's counterpart of the core-entity proof.
		var subRow *lockfile.Entity
		for i := range lf.Entities {
			if lf.Entities[i].URI == subURI {
				subRow = &lf.Entities[i]
				break
			}
		}
		if subRow == nil {
			uris := make([]string, 0, len(lf.Entities))
			for i := range lf.Entities {
				uris = append(uris, lf.Entities[i].URI)
			}
			t.Fatalf("the sub-aggregate %q is absent from the written lockfile entities (have %v)", subURI, uris)
		}
		if subRow.Signature == nil {
			t.Fatalf("no verified signature fact recorded on the sub-aggregate row %q; the verify pass did not fire for a sub-aggregate", subURI)
		}
		if subRow.Signature.Mode != "keyed" {
			t.Fatalf("sub-aggregate %q: verified signature mode = %q, want keyed", subURI, subRow.Signature.Mode)
		}
		if subRow.ManifestDigest == "" || subRow.Signature.BundleDigest == "" {
			t.Fatalf("sub-aggregate %q: signature fact is incomplete (manifest_digest=%q bundle_digest=%q)",
				subURI, subRow.ManifestDigest, subRow.Signature.BundleDigest)
		}
	})

	t.Run("negative_wrong_pinned_key_halts_pull", func(t *testing.T) {
		host := startRegistry(t, nil)
		// Pin a DIFFERENT key: the sub-aggregate's signature verifies against the
		// publisher's key but not this one, so the require-signatures policy halts
		// the pull on the sub-aggregate.
		_, wrongPubPEM, gerr := signing.GenerateKeyedPEM()
		if gerr != nil {
			t.Fatalf("GenerateKeyedPEM (wrong): %v", gerr)
		}
		wsRoot, _, _ := writeSubAggregateConsumerWorkspace(t, host, wrongPubPEM, signedPush)

		perr := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}), ReleasePath: "release.yaml"})
		if !errors.Is(perr, signing.ErrSignatureVerificationFailed()) {
			t.Fatalf("a wrong pinned key must HALT the pull with ErrSignatureVerificationFailed; got: %v", perr)
		}
	})
}
