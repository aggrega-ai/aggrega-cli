// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// oneNameRegistryRef is the reference a Release's Aggregate names its Component by
// and its Manifest names the Recipe by: one name, one version, two kinds.
const oneNameRegistryRef = "compose/app:1.0.0"

const oneNameRegistryComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: compose/app
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
spec:
  schema:
    values:
      message:
        type: string
        default: "hello"
`

const oneNameRegistryRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: app
  version: 1.0.0
spec:
  capability: compose
  implements: compose/app:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: "app:1"
        variables:
          MESSAGE: "{{ .values.message }}"
`

// TestAComponentAndARecipeOfOneNameServedByARegistryEachPinTheirOwnDocument drives a
// cold pull, a build and a re-pull of a Release whose Component and Recipe are both
// served by a registry under one reference and version.
//
// The two are cached in two entries, each kept under its own kind, so neither is
// read for the other: each kind's lockfile row records the digest of that kind's own
// published document on the cold pull and again on the re-pull, and the build
// accepts what the pull recorded. With one entry addressed by reference and version
// alone, the second of the two to resolve would read the first one's bytes.
func TestAComponentAndARecipeOfOneNameServedByARegistryEachPinTheirOwnDocument(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "catalog"), 0o755); err != nil {
		t.Fatalf("mkdir catalog: %v", err)
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: one-name
  version: "1.0.0"
spec:
  components:
    app:
      type: `+oneNameRegistryRef+`
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: one-name-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        `+oneNameRegistryRef+`: `+oneNameRegistryRef+`
`)
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: one-name-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: one-name
  manifest: one-name-manifest
  components:
    app: {}
`)
	writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs: [catalog]\ndefault_registry: hub.aggrega.ai\n"+fakeRegistryTrust)
	registry := newLibraryChainRegistry()
	registry.entities["Component compose/app"] = oneNameRegistryComponent
	registry.entities["Recipe compose/app"] = oneNameRegistryRecipe

	want := map[string]string{}
	for kind, body := range map[string]string{closure.KindComponent: oneNameRegistryComponent, closure.KindRecipe: oneNameRegistryRecipe} {
		d, err := oci.PublishedDigest([]byte(body))
		if err != nil {
			t.Fatalf("digest the %s: %v", kind, err)
		}
		want[kind] = d
	}
	if want[closure.KindComponent] == want[closure.KindRecipe] {
		t.Fatalf("the fixture's two documents digest alike; the measurement cannot tell them apart")
	}

	pull := func(label string) {
		t.Helper()
		var diag, out bytes.Buffer
		if err := pullRun(context.Background(), pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath: releasePath,
			DiagOut:     &diag,
			Out:         &out,
			diag:        buildDiagOptions{quiet: true},
			puller:      registry,
		}); err != nil {
			t.Fatalf("%s: %v\n%s%s", label, err, diag.String(), out.String())
		}
		lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
		if err != nil {
			t.Fatalf("%s: load the lockfile: %v", label, err)
		}
		got := map[string]string{}
		for _, e := range lf.Entities {
			if e.URI == oneNameRegistryRef {
				got[e.Kind] = e.Digest
			}
		}
		t.Logf("%s: lockfile rows of %s: %v; each document's own digest: %v", label, oneNameRegistryRef, got, want)
		for kind, d := range want {
			if got[kind] != d {
				t.Errorf("%s: the %s row of %s records %q; want the digest of the %s's own document, %q",
					label, kind, oneNameRegistryRef, got[kind], kind, d)
			}
		}
	}

	pull("the cold pull")
	var buildDiag, buildOut bytes.Buffer
	if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		releasePath, nil, false, &buildDiag, &buildOut); err != nil {
		t.Errorf("the build refused what its pull resolved: %v\n%s%s", err, buildDiag.String(), buildOut.String())
	}
	pull("the re-pull")
}
