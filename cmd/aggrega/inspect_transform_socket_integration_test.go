// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's transform-socket coverage for the INSPECT surface. A
// transform-role governance guest shapes the graph, so the picture inspect draws
// is the graph a build of the same documents renders only if inspect resolves and
// runs the same guest. That makes a transform attachment's resolution part of
// inspect's own contract rather than a build-only concern.
//
// The two arms differ in exactly one thing: where the guest's bytes come from. A
// co-located guest sits beside its Plugin entity in a catalog directory and is
// reachable by walking that directory. A registry-published guest is acquired
// into the content-addressed cache, which no catalog directory walk reaches, so
// it is resolvable only through a cache-backed read. Everything else — the
// component, the recipe, the workspace, the attachment, the acquisition pass — is
// identical between them, so an arm that fails while the other passes localises
// the failure to that one difference.
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// inspectTransformGuestLabel is the node label the transform fixture guest
// injects. The guest adds it to every node, so its presence in the graph inspect
// writes is an observable witness that the guest was resolved, loaded AND run —
// which a clean exit alone would not establish.
const inspectTransformGuestLabel = "proof.aggrega/touched"

// writeInspectTransformCatalog lays a local catalog serving the compute the
// inspect run resolves: one component and the recipe implementing it. The
// transform guest is deliberately NOT written here — each arm supplies it from
// the origin under test.
func writeInspectTransformCatalog(t *testing.T, catalogDir string) {
	t.Helper()
	writeFixtureEntity(t, catalogDir, "component/test/svc/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`)
	writeFixtureEntity(t, catalogDir, "recipe/compose/svc/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: compose
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: busybox:1
`)
}

// writeInspectTransformWorkspace lays the aggregate / manifest / release the
// inspect run is pointed at, alongside a workspace configuration attaching one
// transform-role governance plugin. registryHost is written as the default
// registry so an arm whose guest is published can acquire it; an arm whose guest
// is co-located leaves it empty and is written offline, so that arm resolves
// everything from the catalog directory and reaches no network at all.
func writeInspectTransformWorkspace(t *testing.T, root, registryHost, pluginsBlock string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: compose/svc:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
        app: {}
`)
	cfg := "workspace: {}\noffline: true\n"
	if registryHost != "" {
		cfg = "default_registry: \"" + registryHost + "\"\nauto_pull: true\noffline: false\n"
	}
	writeWorkspaceConfig(t, root, cfg+pluginsBlock)
}

// inspectGraphOf drives the real inspect dispatch over a Release and returns the
// IR graph document it writes. The graph leads inspect's output stream, so the
// captured bytes carry the resolved node set — including whatever a transform
// attachment contributed to it.
//
// stdCatalog is passed in rather than resolved here. Resolving it walks up from
// the working directory, and every caller has already changed into its temporary
// workspace — from which that walk finds nothing and skips the test. A skip reads
// as a pass at the package level, so deriving it here would make this proof
// incapable of failing.
func inspectGraphOf(t *testing.T, root, stdCatalog string) (string, error) {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("resolve workspace at %s: %v", root, err)
	}
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.irGraph = true
	derr := dispatchInspect(context.Background(), rw, filepath.Join(root, "release.yaml"),
		[]string{filepath.Join(root, "catalog"), stdCatalog}, opts)
	return buf.String(), derr
}

// TestIntegration_InspectResolvesATransformGuestFromEitherOrigin holds that the
// inspect surface resolves a transform-role governance guest whether that guest
// sits beside its catalog entity or was acquired from a registry into the
// content cache.
//
// The co-located arm is the CONTROL. It passes on a catalog-directory walk alone,
// so on its own it proves nothing about the cache-backed read the registry arm
// needs — its job is to show that everything else in the fixture (the component,
// the recipe, the attachment, the acquisition, the inspect dispatch) is sound, so
// a registry-arm failure is attributable to the guest's origin and to nothing
// else.
func TestIntegration_InspectResolvesATransformGuestFromEitherOrigin(t *testing.T) {
	// Resolved before any chdir: umbrella-root discovery walks up from the working
	// directory, and both arms change into their workspace.
	guestPath := wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm")
	guestBytes, err := os.ReadFile(guestPath)
	if err != nil {
		t.Fatalf("read transform guest fixture %s: %v", guestPath, err)
	}
	std := standardCatalogDir(t)
	const (
		guestName   = "tag-injector"
		guestSocket = "governance/" + guestName
		attachment  = "plugins:\n  governance:\n    - uri: " + guestSocket + ":1.0.0\n      role: label-transform\n"
	)

	t.Run("colocated", func(t *testing.T) {
		root := t.TempDir()
		catalogDir := filepath.Join(root, "catalog")
		writeInspectTransformCatalog(t, catalogDir)
		writeColocatedGovernanceGuest(t, catalogDir, guestName, "mutate", guestBytes)
		writeInspectTransformWorkspace(t, root, "", attachment)
		t.Chdir(root)

		if err := pullRun(context.Background(), pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath: "release.yaml",
			CatalogDirs: []string{catalogDir, std},
			DiagOut:     os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true},
		}); err != nil {
			t.Fatalf("acquisition with a co-located transform guest: %v", err)
		}

		graph, err := inspectGraphOf(t, root, std)
		if err != nil {
			t.Fatalf("inspect with a co-located transform guest: %v", err)
		}
		if !strings.Contains(graph, inspectTransformGuestLabel) {
			t.Fatalf("the graph inspect wrote carries no %q label, so the co-located guest did not run; the registry arm's comparison would be against nothing:\n%s",
				inspectTransformGuestLabel, graph)
		}
	})

	t.Run("registry_published", func(t *testing.T) {
		host := startRegistry(t, nil)
		publishSocketPluginEntity(t, host, guestSocket, "1.0.0", guestPath, "governance", "mutate")

		root := t.TempDir()
		catalogDir := filepath.Join(root, "catalog")
		writeInspectTransformCatalog(t, catalogDir)
		writeInspectTransformWorkspace(t, root, host, attachment)
		t.Chdir(root)

		if err := pullRun(context.Background(), pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath: "release.yaml",
			CatalogDirs: []string{catalogDir, std},
			DiagOut:     os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true},
		}); err != nil {
			t.Fatalf("acquisition with a registry-published transform guest: %v", err)
		}

		graph, err := inspectGraphOf(t, root, std)
		if err != nil {
			t.Fatalf("inspect with a registry-published transform guest: %v; the guest's bytes live in the content cache, which no catalog directory walk reaches, so inspect must read it from there", err)
		}
		if !strings.Contains(graph, inspectTransformGuestLabel) {
			t.Fatalf("the graph inspect wrote carries no %q label, so the registry-acquired guest resolved but never ran:\n%s",
				inspectTransformGuestLabel, graph)
		}
	})
}
