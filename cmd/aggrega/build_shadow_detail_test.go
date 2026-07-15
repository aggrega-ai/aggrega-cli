// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildDetail_ShadowReportMatchesRenderedWinner proves the --detail catalog
// shadow diagnostic tells the truth: the winner directory it names for a
// duplicated Component is the same directory whose Component body actually flows
// into the rendered output. The report is derived from the eager first-win merge
// while the render resolves through the lazy filesystem index; both now fold
// cross-occurrence precedence through the shared foldFirstWin primitive, so the
// reported winner and the rendered winner agree. Before the lazy index was made
// first-win the two disagreed — the report named dir A while the render emitted
// dir B's body.
func TestBuildDetail_ShadowReportMatchesRenderedWinner(t *testing.T) {
	root := t.TempDir()

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: shadow-smoke
  version: "1.0.0"
spec:
  components:
    only:
      type: core/server
`)

	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: shadow-smoke-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        only:
          infra:
            replicas: 1
`)

	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: shadow-smoke-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)

	// The distinguishing marker is the Component's schema default for `image`,
	// which the recipe emits verbatim. Whichever directory's Component wins,
	// its image string is what renders.
	component := func(marker string) string {
		return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      image: { type: string, default: "` + marker + `" }
`
	}

	dirA := filepath.Join(root, "cat-a")
	dirB := filepath.Join(root, "cat-b")
	writeFixture(t, filepath.Join(dirA, "components", "server.yaml"), component("from-dir-a"))
	writeFixture(t, filepath.Join(dirB, "components", "server.yaml"), component("from-dir-b"))

	// Recipe + provider live only in dir A so they resolve unambiguously.
	writeFixture(t, filepath.Join(dirA, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  plugin: "builtin:helm"
`)
	writeHelmRendererPlugin(t, filepath.Join(dirA, "providers"))
	writeFixture(t, filepath.Join(dirA, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      server:
        image: {{ .values.image }}
`)

	prevRelease, prevCatalog, prevTrace, prevDetail := releasePath, catalogDirs, traceEnabled, buildDetail
	t.Cleanup(func() {
		releasePath, catalogDirs, traceEnabled, buildDetail = prevRelease, prevCatalog, prevTrace, prevDetail
	})
	releasePath = filepath.Join(root, "release.yaml")
	catalogDirs = []string{dirA, dirB}
	traceEnabled = false
	buildDetail = true

	// The build writes artifacts under a relative `dist/` in the process working
	// directory; anchor it at root so the output is hermetic to this test.
	ws := resolveTempWorkspace(t)
	t.Chdir(root)

	// Build is read-only: prime the lockfile with an explicit pull, its output routed
	// to a throwaway buffer so only the build's own shadow report lands in diag.
	var pullOut bytes.Buffer
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catalogDirs, ws: ws, DiagOut: &pullOut, Out: &pullOut, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}

	var diag bytes.Buffer
	if err := dispatchBuild(ws, releasePath, catalogDirs, traceEnabled, &diag, os.Stdout); err != nil {
		t.Fatalf("build failed: %v\n--- diag ---\n%s", err, diag.String())
	}

	// Which directory did the render actually pick? Read the emitted output.
	rendered := readRenderedOutput(t, filepath.Join(root, "dist"))
	var renderedWinner string
	switch {
	case strings.Contains(rendered, "from-dir-a"):
		renderedWinner = dirA
	case strings.Contains(rendered, "from-dir-b"):
		renderedWinner = dirB
	default:
		t.Fatalf("neither marker found in rendered output:\n%s", rendered)
	}

	// Which directory did --detail report as the winner? The line reads
	// `... is shadowed by <WinnerDir> (first-win precedence)`.
	reportedWinner := parseShadowWinner(t, diag.String())

	if reportedWinner != renderedWinner {
		t.Errorf("--detail lies: reported winner %q != rendered winner %q\n--- diag ---\n%s\n--- rendered ---\n%s",
			reportedWinner, renderedWinner, diag.String(), rendered)
	}
	if renderedWinner != dirA {
		t.Errorf("first-win expected dir A to render; got %q", renderedWinner)
	}
}

// readRenderedOutput concatenates every file emitted under the boundary output
// directory so a marker can be searched regardless of the emitted layout.
func readRenderedOutput(t *testing.T, dir string) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		sb.Write(data)
		sb.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatalf("read rendered output under %s: %v", dir, err)
	}
	return sb.String()
}

// parseShadowWinner extracts the winner directory from the reportCatalogShadows
// diagnostic line for the duplicated Component.
func parseShadowWinner(t *testing.T, diag string) string {
	t.Helper()
	const marker = "is shadowed by "
	for _, line := range strings.Split(diag, "\n") {
		if !strings.Contains(line, "Component") || !strings.Contains(line, marker) {
			continue
		}
		rest := line[strings.Index(line, marker)+len(marker):]
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "(first-win precedence)"))
	}
	t.Fatalf("no Component shadow line found in --detail output:\n%s", diag)
	return ""
}
