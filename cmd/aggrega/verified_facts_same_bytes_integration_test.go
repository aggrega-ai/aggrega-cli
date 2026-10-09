// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

const (
	sameBytesComponent = `apiVersion: aggrega.ai/v1alpha1
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
`
	sameBytesRecipe = `apiVersion: aggrega.ai/v1alpha1
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
    replicas: {{ optional .infra ` + "`replicas`" + ` 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ optional .infra ` + "`port`" + ` 8080 }}
`
	sameBytesMarker = "superseded by test/svc 2"
)

// TestIntegration_EveryRowPinningVerifiedBytesRecordsTheVerification publishes one
// signed Component, its Recipe, and a signed deprecation marker on the Component to
// an in-process OCI registry, then pulls and builds a Release that references the
// Component twice — once without its version and once with it — in a workspace
// whose one registry entry requires a keyed signature and reads no local catalog,
// through the real command tree.
//
// The two references resolve to one registry entity, so the lockfile pins it under
// two rows that share their content digest and their manifest digest. The pull
// fetches the entity once, under whichever reference it reaches first, and serves
// the other from the cache slot that fetch wrote. Both rows must record what the
// pull established for those bytes: the keyed signature it verified, with the
// bundle cached where an offline re-check reads it, and the deprecation marker it
// discovered and verified. The build then reads that lockfile, fetching nothing,
// renders the Release, and reports the deprecation once per row that records it;
// `aggrega pull --verify` accepts the lockfile; and a second pull, which fetches
// nothing, rewrites nothing, so the facts survive a re-pull served wholly from the
// cache.
func TestIntegration_EveryRowPinningVerifiedBytesRecordsTheVerification(t *testing.T) {
	// Not parallel: every command runs from the workspace it builds, which moves the
	// process working directory, and the commands' notices are read from the process
	// stderr.
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	host := startRegistry(t, nil)

	pub := t.TempDir()
	publisher := resolveTempWorkspace(t, tempWorkspaceOptions{Root: pub})
	componentPath := filepath.Join(pub, "component/test/svc/1.0.0/component.yaml")
	writeFixture(t, componentPath, sameBytesComponent)
	recipePath := filepath.Join(pub, "recipe/helm/svc/1.0.0/recipe.yaml")
	writeFixture(t, recipePath, sameBytesRecipe)
	for _, p := range []string{componentPath, recipePath} {
		on := true
		if err := pushRun(t.Context(), pushConfig{Workspace: publisher, EntityPath: p, Registry: host, Sign: &on, Signer: signer}); err != nil {
			t.Fatalf("publish %s signed: %v", p, err)
		}
	}
	if err := deprecateRun(t.Context(), deprecateConfig{
		Workspace: publisher,
		Reference: host + "/component/test/svc:1.0.0",
		Severity:  "warning",
		Reason:    sameBytesMarker,
		Signer:    signer,
		EnvLookup: func(string) string { return "" },
		Out:       io.Discard,
	}); err != nil {
		t.Fatalf("attach a signed deprecation marker to test/svc:1.0.0: %v", err)
	}

	var trust strings.Builder
	trust.WriteString("workspace:\n  catalog_dirs: []\n")
	trust.WriteString("default_registry: \"" + host + "\"\noffline: false\nauto_pull: false\n")
	trust.WriteString("registries:\n  - host: \"" + host + "\"\n    signing:\n      required: true\n    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(pubPEM), "\n"), "\n") {
		trust.WriteString("      " + line + "\n")
	}

	rows := []struct {
		name string
		// aggregateType and manifestKey are the two places the Release writes the
		// Component; exactly one of them carries the version.
		aggregateType, manifestKey string
	}{
		{name: "the Aggregate writes it without its version and the Manifest with it", aggregateType: "test/svc", manifestKey: "test/svc:1.0.0"},
		{name: "the Aggregate writes it with its version and the Manifest without it", aggregateType: "test/svc:1.0.0", manifestKey: "test/svc"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := t.TempDir()
			writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: `+row.aggregateType+`
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
        `+row.manifestKey+`: helm/svc:1.0.0
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
			writeWorkspaceConfig(t, ws, trust.String())
			lockPath := lockfile.PathFor("release.yaml")
			t.Chdir(ws)

			if text, err := runFromTheCommandLine(t, ws, "pull", "--release", "release.yaml"); err != nil {
				t.Fatalf("aggrega pull of a signed publication, its key pinned and a signature required: %v\n%s", err, text)
			}
			lf, _, err := lockfile.LoadLockfile(lockPath)
			if err != nil {
				t.Fatalf("load the lockfile the pull wrote: %v", err)
			}
			byURI := map[string]lockfile.Entity{}
			for _, e := range lf.Entities {
				byURI[e.URI] = e
			}
			bare, hasBare := byURI["test/svc"]
			versioned, hasVersioned := byURI["test/svc:1.0.0"]
			if !hasBare || !hasVersioned {
				t.Fatalf("the lockfile does not pin the Component under both references it is written as; entities: %+v", lf.Entities)
			}
			if bare.Digest != versioned.Digest || bare.ManifestDigest == "" || bare.ManifestDigest != versioned.ManifestDigest {
				t.Fatalf("the two references no longer resolve to one registry entity, so this test proves nothing about it: "+
					"test/svc %s / %s, test/svc:1.0.0 %s / %s", bare.Digest, bare.ManifestDigest, versioned.Digest, versioned.ManifestDigest)
			}
			for _, e := range []lockfile.Entity{bare, versioned} {
				if e.Signature == nil || e.Signature.Mode != "keyed" || e.Signature.BundleDigest == "" {
					t.Errorf("%s pins bytes this pull verified under a required keyed signature and records no keyed signature with a cached bundle: %+v", e.URI, e.Signature)
					continue
				}
				if _, statErr := os.Stat(oci.SignatureBundlePath(ws, ".aggrega/cache", e.Signature.BundleDigest)); statErr != nil {
					t.Errorf("%s records a signature bundle that is not cached where an offline re-check reads it: %v", e.URI, statErr)
				}
				if e.Deprecation == nil || e.Deprecation.Message != sameBytesMarker || e.Deprecation.Severity != "warning" || e.Deprecation.Signer == nil {
					t.Errorf("%s pins bytes whose signed deprecation marker this pull discovered and records no verified marker: %+v", e.URI, e.Deprecation)
				}
			}
			if bare.Signature != nil && versioned.Signature != nil && *bare.Signature != *versioned.Signature {
				t.Errorf("the two rows pinning one manifest record different signature facts: %+v and %+v", *bare.Signature, *versioned.Signature)
			}
			first := mustReadFile(t, lockPath)

			text, err := runFromTheCommandLine(t, ws, "build", "--release", "release.yaml")
			if err != nil {
				t.Fatalf("aggrega build over the lockfile its pull wrote, a signature required: %v\n%s", err, text)
			}
			for _, uri := range []string{"test/svc", "test/svc:1.0.0"} {
				if !strings.Contains(text, uri+" is deprecated (warning)") {
					t.Errorf("the build did not report the deprecation the lockfile records for %s:\n%s", uri, text)
				}
			}
			if rendered := readDistTree(t, filepath.Join(ws, "dist")); !strings.Contains(rendered, "test-server:latest") {
				t.Errorf("the build did not render the Component; got:\n%s", rendered)
			}

			if text, err := runFromTheCommandLine(t, ws, "pull", "--release", "release.yaml", "--verify"); err != nil {
				t.Errorf("aggrega pull --verify exited non-zero over the lockfile the pull wrote: %v\n%s", err, text)
			}
			if text, err := runFromTheCommandLine(t, ws, "pull", "--release", "release.yaml"); err != nil {
				t.Fatalf("second aggrega pull exited non-zero: %v\n%s", err, text)
			}
			if second := mustReadFile(t, lockPath); string(second) != string(first) {
				t.Errorf("a second pull, served wholly from the cache, rewrote the lockfile\nfirst:\n%s\nsecond:\n%s", first, second)
			}
		})
	}
}
