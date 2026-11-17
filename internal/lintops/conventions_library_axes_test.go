// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
// A collection naming only some axes never hands a locally-authored library on
// another axis to the conventions rules: it is linted by nobody, and nothing
// reports the gap. Asserting against the declared axis set rather than a
// hand-written list of axis names keeps the coverage a property of the axis set: a
// new axis joins this assertion by being declared rather than by being remembered
// here.
//
// The closure names each library at the version its resolution recorded, and the
// index is the one the release pass builds from the catalog walk, so each library
// is looked up exactly as a Release's lint looks it up.
func TestClosureEntityFilesCoversEveryLibraryAxis(t *testing.T) {
	ws := t.TempDir()

	// One locally-authored library per axis, each with a distinct name so the
	// returned path identifies which axis produced it.
	bodies := map[string]string{
		"schema.yaml": `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: ProbeSchemas
  version: "1.0.0"
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`,
		"mapping.yaml": `apiVersion: aggrega.ai/v1beta1
kind: MappingLibrary
metadata:
  name: ProbeMappings
  version: "1.0.0"
spec:
  mappings:
    Simple: |
      key: value
`,
		"workload.yaml": `apiVersion: aggrega.ai/v1beta1
kind: WorkloadLibrary
metadata:
  name: ProbeWorkloads
  version: "1.0.0"
spec:
  workloads:
    Deploy: |
      kind: Deployment
`,
		"artifacts.yaml": `apiVersion: aggrega.ai/v1beta1
kind: ArtifactLibrary
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

	// The index the collection joins on, built from the catalog walk as the release
	// pass builds it.
	files, _, err := walkCatalogEntityFiles(t.Context(), ws, []string{ws}, nil, "")
	if err != nil {
		t.Fatalf("walk catalog: %v", err)
	}

	resolved := func(name string) []closure.ResolvedLibrary {
		return []closure.ResolvedLibrary{{URI: name, ResolvedVersion: "1.0.0"}}
	}
	cl := closure.Closure{
		SchemaLibraries:   resolved("ProbeSchemas"),
		MappingLibraries:  resolved("ProbeMappings"),
		WorkloadLibraries: resolved("ProbeWorkloads"),
		ArtifactLibraries: resolved("ProbeArtifacts"),
	}

	lazyView := catalog.NewLazyLookup(ws, "", []string{ws}, nil)
	got := closureEntityFiles(lazyView, cl, entityFileIndex(files))

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
			"SchemaLibraries":   paths["schema.yaml"],
			"MappingLibraries":  paths["mapping.yaml"],
			"WorkloadLibraries": paths["workload.yaml"],
			"ArtifactLibraries": paths["artifacts.yaml"],
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
