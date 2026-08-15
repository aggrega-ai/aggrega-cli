// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestTerraformExamplesHermeticOfflineBuild proves the default Terraform build
// path is hermetic: the render is the embedded provider guest plus a local or
// inline module source, so a complete build needs no network at all.
//
// The proof runs each terraform example under an offline workspace. Offline is
// enforced by construction: the puller is nil (catalog resolution never dials a
// registry) and the build refuses to network, freezing against the lockfile.
// Under those conditions both examples resolve from the sibling catalog, render
// through the embedded guest, and emit their full dist tree — with no network
// reachable. The artifacts example carries an inline HCL body (no source at
// all); the sourced example vendors a local module (source acquired at build
// time from the local filesystem, never fetched). Neither contributes a remote
// source artifact, so there is nothing to fetch even under the default
// auto-pull configuration.
func TestTerraformExamplesHermeticOfflineBuild(t *testing.T) {
	catalogRoot := siblingCatalogRootForExamples(t)

	for _, name := range []string{"terraform-rds", "terraform-rds-module"} {
		t.Run(name, func(t *testing.T) {
			src := workedExampleDir(t, name)
			ws := t.TempDir()
			copyExampleInputs(t, src, ws)
			// Offline workspace: no registry dial on resolve, no network on build.
			if err := os.WriteFile(
				filepath.Join(ws, ".aggrega-config.yaml"),
				[]byte("default_registry: hub.aggrega.ai\nauto_pull: false\noffline: true\n"),
				0o644,
			); err != nil {
				t.Fatalf("write offline config: %v", err)
			}
			t.Chdir(ws)

			// Resolve + pin from the local catalog with the puller disabled by
			// offline — a network dial here would be a hard failure, not a
			// fallback.
			if err := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: []string{catalogRoot}}); err != nil {
				t.Fatalf("offline pull %q (a network dependency would fail here): %v", name, err)
			}

			releasePath := "release.yaml"
			catalogDirs := []string{catalogRoot}
			traceEnabled := false

			// The build refuses to network under offline; success proves the
			// render path (embedded guest + local/inline source) is hermetic.
			if err := dispatchBuild(resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), releasePath, catalogDirs, traceEnabled, io.Discard, io.Discard); err != nil {
				t.Fatalf("offline build %q (default terraform path must not network): %v", name, err)
			}

			tree := readExampleDistTree(t, ws)
			if _, ok := tree["infra/terraform/main.tf"]; !ok {
				t.Errorf("offline build %q emitted no main.tf; dist keys: %v", name, sortedKeys(tree))
			}
		})
	}
}
