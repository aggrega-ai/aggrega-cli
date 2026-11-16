// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validateops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// probeArtifactLibrary is an ArtifactLibrary carrying one bundle of one file,
// whose content holds a marker the rendered output is searched for.
const probeArtifactLibrary = `apiVersion: aggrega.ai/v1beta1
kind: ArtifactLibrary
metadata:
  name: ProbeArtifacts
  version: "1.0.0"
spec:
  artifacts:
    Support:
      support.yaml: |
        apiVersion: v1
        kind: ConfigMap
        metadata:
          name: probe-support
        data:
          marker: imported-artifact-bundle
`

// probeWorkloadLibrary is a WorkloadLibrary carrying one workload for the probe
// Component, whose image holds a marker the rendered output is searched for.
const probeWorkloadLibrary = `apiVersion: aggrega.ai/v1beta1
kind: WorkloadLibrary
metadata:
  name: ProbeWorkloads
  version: "1.0.0"
spec:
  workloads:
    Server: |
      apiVersion: aggrega.ai/v1beta1
      kind: Workload
      replicas: 1
      containers:
        main:
          image: "imported-workload-image:{{ .infra.tag }}"
          ports:
            http: 8080
      services:
        default:
          type: ClusterIP
          ports:
            http: 8080
`

// probeRecipeHeader is the probe Recipe up to its workload, which each row
// writes itself.
const probeRecipeHeader = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: probe
  version: "1.0.0"
spec:
  capability: helm
  implements: example/compute/probe
`

// artifactImport is an artifacts block importing the bundle library names.
func artifactImport(library string) string {
	return "  artifacts:\n    support:\n      $import: \"" + library + "#/artifacts/Support\"\n"
}

// TestValidateRecipeRendersWhatItImportsFromTheCatalogsLibraries drives Recipes
// that import from a library sitting in the catalog directory the check is
// handed, through the one single-entity check.
//
// The isolated render resolves a Recipe's library imports against the libraries
// that catalog holds, as a build resolves them against its own: a Recipe whose
// workload or artifacts entry is imported from such a library renders, and what
// the library carries is in the output. A render handed no libraries stops at
// the first import as a library the catalog does not hold, while the census of
// the same Recipe's reads resolves it — so each rendered row fails on its own if
// that set stops reaching the render.
//
// The last row is the control on the refusal itself: an import naming a library
// no catalog directory holds still stops the render, naming the condition. None
// of these Recipes reads what a build of one cannot supply, so no stop is set
// aside as not attempted.
func TestValidateRecipeRendersWhatItImportsFromTheCatalogsLibraries(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	inlineWorkload, _ := buildOfOneRecipe("{{ .infra.image }}:{{ .infra.tag }}", false, false)
	cases := []struct {
		name   string
		recipe string
		// marker is text the rendered artifacts must carry; empty for a row that
		// must fail.
		marker string
		// wantCondition is the condition a failing row's error must name.
		wantCondition error
	}{
		{
			name:   "an artifacts entry imported from an ArtifactLibrary renders",
			recipe: inlineWorkload + artifactImport("ProbeArtifacts"),
			marker: "imported-artifact-bundle",
		},
		{
			name:   "a workload imported from a WorkloadLibrary renders",
			recipe: probeRecipeHeader + "  workload:\n    $import: \"ProbeWorkloads#/workloads/Server\"\n",
			marker: "imported-workload-image:1.0",
		},
		{
			name:          "an import naming a library no catalog directory holds still fails",
			recipe:        inlineWorkload + artifactImport("AbsentArtifacts"),
			wantCondition: renderer.ErrArtifactLibraryNotFound(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, recipe := writeProbeCatalog(t, tc.recipe)
			libraries := map[string]string{
				"artifactlibrary/probe-artifacts/1.0.0/library.yaml": probeArtifactLibrary,
				"workloadlibrary/probe-workloads/1.0.0/library.yaml": probeWorkloadLibrary,
			}
			for rel, content := range libraries {
				path := filepath.Join(dir, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("create %s: %v", filepath.Dir(path), err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatalf("write %s: %v", rel, err)
				}
			}
			root := filepath.Dir(dir)
			res, err := ValidateEntity(context.Background(), root, filepath.Join(root, "cache"), []string{dir}, nil, recipe, false)
			report := strings.Join(res.Diagnostics, "\n")
			for _, line := range res.Diagnostics {
				if strings.HasPrefix(line, "Render: not attempted") {
					t.Errorf("the render was set aside, so it was never judged:\n%s", report)
				}
			}

			if tc.marker == "" {
				if err == nil {
					t.Fatalf("ValidateEntity returned nil; want a render stop\nreport:\n%s", report)
				}
				if !errors.Is(err, tc.wantCondition) {
					t.Errorf("the failure does not name the condition %q:\n%v", tc.wantCondition, err)
				}
				if res.Valid {
					t.Errorf("Valid = true with error %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("ValidateEntity: %v\nreport:\n%s", err, report)
			}
			if !res.Valid {
				t.Errorf("Valid = false with no error\nreport:\n%s", report)
			}
			found := false
			for _, a := range res.Artifacts {
				if strings.Contains(string(a.Content), tc.marker) {
					found = true
				}
			}
			if !found {
				t.Errorf("no rendered artifact carries %q, which the imported library holds; %d artifact(s)\nreport:\n%s",
					tc.marker, len(res.Artifacts), report)
			}
		})
	}
}
