// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A boundary places a component on either of two axes: an aggregates: entry
// routing a whole sub-module in, or a components: entry naming one component by
// its qualified dotted path. A Release may use both at once — routing one
// sub-module while addressing another's components individually — and the two
// axes are two spellings of where a component belongs rather than two
// behaviours.
//
// This drives that claim through the full build path against the sibling
// catalog: the same developer intent, placed three ways, must emit the same
// artifacts at the same paths. It complements the placement assertions over the
// routed form, which read what each artifact records about itself; this reads
// whether the authored spelling reaches the output at all.
//
// The routed form is the committed worked example itself rather than a copy
// declared here, so that leg cannot drift away from the document the language
// reference teaches.

// mixedAxisRelease places the metrics module by routing and the documents
// module's components by dotted path, in one Release. The routing entry carries
// the same per-component sizing and Feature attachment the worked example
// declares, so the only difference from the committed form is which axis places
// the documents module.
const mixedAxisRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: k8s-helm-synth
  environment: production
  version: "1.0.0"
spec:
  aggregate: aggregate
  manifest: manifest
  boundaries:
    observability:
      directory: observability
      annotations:
        aggrega.ai/namespace: observability
      aggregates:
        metrics:
          components:
            query:
              infra:
                compute:
                  cpu: 500m
                  memory: 1Gi
              features:
                scrape:
                  type: core/prometheus-exporter:1.0.0
                  infra:
                    interval: 30s
                    port: http
                    path: /metrics
    data:
      directory: data
      annotations:
        aggrega.ai/namespace: data
      components:
        documents.backend: {}
        documents.proxy: {}
`

// dottedPathRelease places every component of both modules by dotted path,
// naming each one the Release wants placed. A component the Aggregate's closure
// contains and no boundary names is not placed by this axis, which is why all
// five are listed.
const dottedPathRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: k8s-helm-synth
  environment: production
  version: "1.0.0"
spec:
  aggregate: aggregate
  manifest: manifest
  boundaries:
    observability:
      directory: observability
      annotations:
        aggrega.ai/namespace: observability
      components:
        metrics.objstore: {}
        metrics.store: {}
        metrics.query:
          infra:
            compute:
              cpu: 500m
              memory: 1Gi
          features:
            scrape:
              type: core/prometheus-exporter:1.0.0
              infra:
                interval: 30s
                port: http
                path: /metrics
    data:
      directory: data
      annotations:
        aggrega.ai/namespace: data
      components:
        documents.backend: {}
        documents.proxy: {}
`

// exampleSrcWithRelease materialises a copy of an example's committed inputs
// with its Release replaced, returning the directory. The copy routes through
// the same helper the example builds use, so a change to what counts as an
// example input reaches this path too, and the build itself is left to
// buildExampleToDist rather than reimplemented here.
func exampleSrcWithRelease(t *testing.T, src, releaseYAML string) string {
	t.Helper()
	dir := t.TempDir()
	copyExampleInputs(t, src, dir)
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte(releaseYAML), 0o644); err != nil {
		t.Fatalf("write substituted release into %s: %v", dir, err)
	}
	return dir
}

// TestModuleBoundaryPlacementAxesEmitIdenticalArtifacts builds one developer
// intent under three Releases that place its components differently — both
// modules routed, one routed and one addressed by dotted path, and every
// component addressed by dotted path — and asserts the three emitted trees hold
// the same paths carrying the same bytes.
//
// Both halves of that matter and they fail independently: an emitted key derives
// from the resource's own identity, so a path can move while every file's
// content stays equal. assertByteIdenticalTrees compares the file sets and the
// bytes, so a move on either axis is reported.
func TestModuleBoundaryPlacementAxesEmitIdenticalArtifacts(t *testing.T) {
	t.Parallel()
	catalogRoot := siblingCatalogRootForExamples(t)
	routedSrc := workedExampleDir(t, "module-boundary-routing")

	// The three Releases must actually differ, or the comparison below holds
	// three copies of one document against each other and proves nothing. The
	// routed form is read from disk because it is the committed example.
	routedRelease, err := os.ReadFile(filepath.Join(routedSrc, "release.yaml"))
	if err != nil {
		t.Fatalf("read the worked example's Release: %v", err)
	}
	forms := map[string]string{
		"routed":     string(routedRelease),
		"mixed":      mixedAxisRelease,
		"dottedPath": dottedPathRelease,
	}
	for a, ra := range forms {
		for b, rb := range forms {
			if a < b && ra == rb {
				t.Fatalf("the %s and %s Releases are identical, so comparing what they emit asserts nothing", a, b)
			}
		}
	}

	// One overlay across every build, so all three see byte-identical inputs and
	// any difference in the output is the placement axis rather than the catalog.
	stack := exampleCatalogStack(t, catalogRoot, "provider/helm")

	routed := buildExampleToDist(t, routedSrc, stack)
	mixed := buildExampleToDist(t, exampleSrcWithRelease(t, routedSrc, mixedAxisRelease), stack)
	dotted := buildExampleToDist(t, exampleSrcWithRelease(t, routedSrc, dottedPathRelease), stack)

	// Against both single-axis forms, which is what "either axis alone" asks for:
	// a mixed Release must emit what the fully routed one does and what the fully
	// dotted one does.
	t.Run("mixed matches the routed form", func(t *testing.T) {
		assertByteIdenticalTrees(t, mixed, routed)
	})
	t.Run("mixed matches the dotted-path form", func(t *testing.T) {
		assertByteIdenticalTrees(t, mixed, dotted)
	})
}
