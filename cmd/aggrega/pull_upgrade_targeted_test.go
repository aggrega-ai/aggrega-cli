// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// twoEntityFixture writes a Release whose closure pins two distinct catalog
// entities — a Component ("server") and a Recipe ("server-recipe") — so a
// targeted upgrade of one can be observed to leave the other untouched. It
// returns the Release path and the catalog directory.
func twoEntityFixture(t *testing.T, root string) (string, string) {
	t.Helper()
	cat := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    api:
      type: server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: smoke-rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        server: server-recipe
`)
	writeFixture(t, filepath.Join(cat, "components", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeFixture(t, filepath.Join(cat, "recipes", "server-recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      server:
        image: test-server:latest
        ports:
          web: 8080
    services:
      svc:
        type: ClusterIP
        ports:
          web: 8080
`)
	writeHelmRendererPlugin(t, filepath.Join(cat, "providers"))
	return filepath.Join(root, "release.yaml"), cat
}

func entityByURI(t *testing.T, lockfilePath, uri string) (lockfile.Entity, bool) {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockfilePath)
	if err != nil {
		t.Fatalf("LoadLockfile %s: %v", lockfilePath, err)
	}
	for _, e := range lf.Entities {
		if e.URI == uri {
			return e, true
		}
	}
	return lockfile.Entity{}, false
}

// TestPullTargetedUpgradePreservesLockfileAndHolds proves the targeted-upgrade
// path does NOT discard the prior pin set: a named-target upgrade holds every
// other pin and carries forward the recorded external inputs (in contrast to the
// bare full-refresh upgrade, which drops both). The distinguishing behavior is
// that the targeted path reads the prior lockfile, so the carry-forward finds the
// prior external-input pin.
func TestPullTargetedUpgradePreservesLockfileAndHolds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rel, cat := twoEntityFixture(t, root)
	lockPath := filepath.Join(root, "release.lock")

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: rel, CatalogDirs: []string{cat}}); err != nil {
		t.Fatalf("initial pull: %v", err)
	}
	// A prior render-time external-input pin the targeted upgrade must preserve.
	want := seedExternalInput(t, lockPath)
	compBefore, ok := entityByURI(t, lockPath, "server")
	if !ok {
		t.Fatalf("precondition: server component pin absent")
	}

	// Targeted upgrade of the recipe only.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: rel, CatalogDirs: []string{cat}, Upgrade: true, UpgradeTargets: []string{"server-recipe"}}); err != nil {
		t.Fatalf("targeted upgrade: %v", err)
	}

	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load lockfile after targeted upgrade: %v", err)
	}
	// External input carried forward (lockfile not removed).
	found := false
	for _, x := range lf.ExternalInputs {
		if x.URL == want.URL {
			found = true
		}
	}
	if !found {
		t.Errorf("targeted upgrade must carry forward the prior external-input pin %q; got %+v", want.URL, lf.ExternalInputs)
	}
	// The non-targeted component pin is unchanged.
	compAfter, ok := entityByURI(t, lockPath, "server")
	if !ok {
		t.Fatalf("server component pin dropped by targeted upgrade")
	}
	if compAfter.ResolvedVersion != compBefore.ResolvedVersion || compAfter.Digest != compBefore.Digest {
		t.Errorf("targeted upgrade of the recipe moved the untargeted component pin: before %s/%s after %s/%s",
			compBefore.ResolvedVersion, compBefore.Digest, compAfter.ResolvedVersion, compAfter.Digest)
	}
	// The targeted entity is still pinned.
	if _, ok := entityByURI(t, lockPath, "server-recipe"); !ok {
		t.Errorf("targeted entity server-recipe should remain pinned after re-resolve")
	}
}

// TestPullTargetedUpgradeNoMatchHalts proves a targeted upgrade whose named URI
// matches no recorded pin halts loudly rather than silently doing nothing.
func TestPullTargetedUpgradeNoMatchHalts(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	rel, cat := twoEntityFixture(t, root)

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: rel, CatalogDirs: []string{cat}}); err != nil {
		t.Fatalf("initial pull: %v", err)
	}
	err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: rel, CatalogDirs: []string{cat}, Upgrade: true, UpgradeTargets: []string{"core/nonexistent"}})
	if err == nil {
		t.Fatal("expected a loud halt for an unmatched upgrade target; got nil")
	}
	if !strings.Contains(err.Error(), "is not pinned in the lockfile") {
		t.Errorf("halt should name the not-pinned target; got %v", err)
	}
}

// TestPullTargetedUpgradeNoPriorLockfileHalts proves a targeted upgrade with no
// prior lockfile halts loudly — there is no pin set to reduce.
func TestPullTargetedUpgradeNoPriorLockfileHalts(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	rel, cat := twoEntityFixture(t, root)
	// Deliberately do NOT create a lockfile first.

	err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: rel, CatalogDirs: []string{cat}, Upgrade: true, UpgradeTargets: []string{"server-recipe"}})
	if err == nil {
		t.Fatal("expected a loud halt for a targeted upgrade with no prior lockfile; got nil")
	}
	if !strings.Contains(err.Error(), "no lockfile to upgrade") {
		t.Errorf("halt should name the missing lockfile; got %v", err)
	}
}

// TestPullPositionalArgsWithoutUpgradeIsUsageError proves positional arguments
// (upgrade targets) require --upgrade: supplying them on a plain pull is a loud
// usage error, not a silent no-op. The check lives in the cobra RunE wrapper, so
// this exercises the command-execution path.
func TestPullPositionalArgsWithoutUpgradeIsUsageError(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	rel, cat := twoEntityFixture(t, root)

	cmd := newPullCmd()
	cmd.SetArgs([]string{"--release", rel, "--catalog", cat, "server-recipe"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a usage error for positional args without --upgrade; got nil")
	}
	if !strings.Contains(err.Error(), "require --upgrade") {
		t.Errorf("usage error should name the --upgrade requirement; got %v", err)
	}
}
