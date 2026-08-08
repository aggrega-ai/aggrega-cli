// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestClosureEntityFilesCoversEveryLibraryAxis pins the conventions walk to every
// library axis the closure declares, not a subset of them.
//
// The collection used to name two axes and stop, so a locally-authored library on
// either of the other two was never handed to the conventions rules: it was linted
// by nobody, and nothing reported the gap — a convention violation in such a
// library simply shipped. Asserting against the declared axis set rather than a
// hand-written list of axis names is what keeps that from recurring, because a new
// axis joins this assertion by being declared rather than by being remembered here.
func TestClosureEntityFilesCoversEveryLibraryAxis(t *testing.T) {
	ws := t.TempDir()

	// One locally-authored library per axis, each with a distinct name so the
	// returned path identifies which axis produced it.
	bodies := map[string]string{
		"schema.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: ProbeSchemas
  version: "1.0.0"
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`,
		"mapping.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: ProbeMappings
  version: "1.0.0"
spec:
  mappings:
    Simple: |
      key: value
`,
		"workload.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: WorkloadLibrary
metadata:
  name: ProbeWorkloads
  version: "1.0.0"
spec:
  workloads:
    Deploy: |
      kind: Deployment
`,
		"artifacts.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: ArtifactsLibrary
metadata:
  name: ProbeArtifacts
  version: "1.0.0"
spec:
  artifacts:
    Bundle:
      note.txt: |
        hello
`,
	}
	paths := map[string]string{}
	for file, body := range bodies {
		p := filepath.Join(ws, file)
		mustWriteFile(t, p, body)
		paths[file] = p
	}

	// The name index the collection joins on, one entry per authored library.
	index := map[string]string{
		"SchemaLibrary|ProbeSchemas":      paths["schema.yaml"],
		"MappingLibrary|ProbeMappings":    paths["mapping.yaml"],
		"WorkloadLibrary|ProbeWorkloads":  paths["workload.yaml"],
		"ArtifactsLibrary|ProbeArtifacts": paths["artifacts.yaml"],
	}

	cl := closure.Closure{
		SchemaLibraries:    []closure.ResolvedLibrary{{URI: "ProbeSchemas"}},
		MappingLibraries:   []closure.ResolvedLibrary{{URI: "ProbeMappings"}},
		WorkloadLibraries:  []closure.ResolvedLibrary{{URI: "ProbeWorkloads"}},
		ArtifactsLibraries: []closure.ResolvedLibrary{{URI: "ProbeArtifacts"}},
	}

	lazyView := catalog.NewLazyLookup(ws, "", []string{ws}, nil)
	got := closureEntityFiles(lazyView, cl, index)

	returned := map[string]bool{}
	for _, p := range got {
		returned[p] = true
	}

	// Non-vacuity: the fixture must actually reach the collection at all. If not
	// even the schema axis came back, the assertion below would be reporting a
	// broken fixture rather than a coverage gap.
	if !returned[paths["schema.yaml"]] {
		t.Fatalf("fixture did not reach the collection at all (got %v); the coverage assertion below would be vacuous", got)
	}

	for _, axis := range closure.LibraryAxes() {
		want := map[string]string{
			"SchemaLibraries":    paths["schema.yaml"],
			"MappingLibraries":   paths["mapping.yaml"],
			"WorkloadLibraries":  paths["workload.yaml"],
			"ArtifactsLibraries": paths["artifacts.yaml"],
		}[axis.Axis]
		if want == "" {
			continue
		}
		if !returned[want] {
			t.Errorf("the conventions collection dropped the %s axis: %s was never handed to the rules, so a convention violation in a locally-authored %s is linted by nobody",
				axis.Axis, want, axis.Kind)
		}
	}
}
