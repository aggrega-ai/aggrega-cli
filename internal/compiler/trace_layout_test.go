// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestTrace_FullLayout_OnHelmSourcedWithArtifacts exercises the full per-node
// trace layout against a fixture whose Recipes carry source: + valuesMapping:
// + artifacts: (plus the always-present pre-loop captures and Phase A flow).
// Asserts the documented layout entries land at the variant's evaluated path.
//
// Slots present per the helm-sourced-with-artifacts shape:
//   - 01-pillars-initial.yaml (pre-loop)
//   - 02-pass1-provides.yaml (pre-loop)
//   - 03-pass2-inject.yaml (pre-loop)
//   - 04-phaseA-inject.yaml (Phase A)
//   - 05-phaseB-source.yaml (B.0 source)
//   - 06-phaseB-locals.yaml (B.0 locals)
//   - 07-phaseB-valuesMapping.yaml (B.1)
//   - 09-phaseB-artifacts/<filename>.yaml (B.3 per-artifact)
//   - 11-engine-emission.log (post-B.6 per-node emission log)
//   - 12-labels-injected.log (provenance-label summary)
//
// Slots absent for this fixture (no workload-mode Recipes):
//   - 08-phaseB-workload.yaml
//   - 10-engine-synthesis/<filename>.yaml
func TestTrace_FullLayout_OnHelmSourcedWithArtifacts(t *testing.T) {
	t.Parallel()
	traceRoot := t.TempDir()
	res := runExample22Variant(t, variantFixture{
		name:      "helm-sourced-with-artifacts",
		traceRoot: traceRoot,
	})
	if len(res.Files) == 0 {
		t.Fatalf("renderer produced no files; trace assertion vacuous")
	}

	// Expected slots present per node. Three nodes: frontend, backend, data.main-db.
	for _, nodeID := range []string{"frontend", "backend", "data.main-db"} {
		// boundary path differs per node; discover at runtime via dir-walk.
		nodeDir := findTraceNodeDir(t, traceRoot, nodeID)
		assertTraceFilePresent(t, nodeDir, "01-pillars-initial.yaml")
		assertTraceFilePresent(t, nodeDir, "02-pass1-provides.yaml")
		assertTraceFilePresent(t, nodeDir, "03-pass2-inject.yaml")
		assertTraceFilePresent(t, nodeDir, "04-phaseA-inject.yaml")
		assertTraceFilePresent(t, nodeDir, "05-phaseB-source.yaml")
		assertTraceFilePresent(t, nodeDir, "06-phaseB-locals.yaml")
		assertTraceFilePresent(t, nodeDir, "07-phaseB-valuesMapping.yaml")
		assertTraceFilePresent(t, nodeDir, "11-engine-emission.log")
		assertTraceFilePresent(t, nodeDir, "12-labels-injected.log")
		// workload-mode and synthesis-per-file slots absent for sourced shape:
		assertTraceFileAbsent(t, nodeDir, "08-phaseB-workload.yaml")
		assertTraceDirAbsent(t, nodeDir, "10-engine-synthesis")
	}

	// Per-artifact granularity at slot 09: each component's Recipe has
	// one artifacts entry; assert at least one file under
	// <node>/09-phaseB-artifacts/.
	for _, nodeID := range []string{"frontend", "backend", "data.main-db"} {
		nodeDir := findTraceNodeDir(t, traceRoot, nodeID)
		artifactDir := filepath.Join(nodeDir, "09-phaseB-artifacts")
		entries, err := os.ReadDir(artifactDir)
		if err != nil {
			t.Errorf("node %s: 09-phaseB-artifacts dir not found: %v", nodeID, err)
			continue
		}
		if len(entries) == 0 {
			t.Errorf("node %s: 09-phaseB-artifacts is empty; expected per-artifact entries", nodeID)
		}
	}
}

// TestTrace_HelmSynthesized_OmitsSourceAndValuesMappingSlots exercises the
// negative-presence assertions for slots that should be absent when the
// Recipe is workload-only. Slots 05 (source) and 07 (valuesMapping) are absent
// for helm-synthesized; slot 08 (workload) and 10 (per-synthesised-file) ARE
// present. Pre-loop (01/02/03/04), 06 (locals — phaseB0 always emits), 11, 12
// always present.
func TestTrace_HelmSynthesized_OmitsSourceAndValuesMappingSlots(t *testing.T) {
	t.Parallel()
	traceRoot := t.TempDir()
	res := runExample22Variant(t, variantFixture{
		name:      "helm-synthesized",
		traceRoot: traceRoot,
	})
	if len(res.Files) == 0 {
		t.Fatalf("renderer produced no files; trace assertion vacuous")
	}

	for _, nodeID := range []string{"frontend", "backend", "data.main-db"} {
		nodeDir := findTraceNodeDir(t, traceRoot, nodeID)
		// Always-present slots:
		assertTraceFilePresent(t, nodeDir, "01-pillars-initial.yaml")
		assertTraceFilePresent(t, nodeDir, "02-pass1-provides.yaml")
		assertTraceFilePresent(t, nodeDir, "03-pass2-inject.yaml")
		assertTraceFilePresent(t, nodeDir, "04-phaseA-inject.yaml")
		assertTraceFilePresent(t, nodeDir, "08-phaseB-workload.yaml")
		assertTraceFilePresent(t, nodeDir, "11-engine-emission.log")
		assertTraceFilePresent(t, nodeDir, "12-labels-injected.log")
		// Absent slots for sourceless + no-valuesMapping:
		assertTraceFileAbsent(t, nodeDir, "05-phaseB-source.yaml")
		assertTraceFileAbsent(t, nodeDir, "07-phaseB-valuesMapping.yaml")
		// Slot 10 (per-synthesised-file): present because Recipe has workload:.
		// Each component synthesises >= 1 file via WorkloadSynthesizer.
		synthDir := filepath.Join(nodeDir, "10-engine-synthesis")
		entries, err := os.ReadDir(synthDir)
		if err != nil {
			t.Errorf("node %s: 10-engine-synthesis dir not found: %v", nodeID, err)
			continue
		}
		if len(entries) == 0 {
			t.Errorf("node %s: 10-engine-synthesis is empty; expected per-synthesised-file entries", nodeID)
		}
	}
}

// TestTrace_ComposeWithArtifacts_LayoutPresent asserts the trace wiring works
// for the compose engine (not just helm). The variant uses placeholder workload
// + artifacts; asserts pre-loop slots, B.0/B.2 slots, slot 09 (per-artifact),
// slot 10 (per-synthesised-file), and slot 11 (emission) all land for compose.
func TestTrace_ComposeWithArtifacts_LayoutPresent(t *testing.T) {
	t.Parallel()
	traceRoot := t.TempDir()
	res := runExample22Variant(t, variantFixture{
		name:          "compose-with-artifacts",
		aggregatePath: "testdata/example22/aggregates/compose-with-artifacts.yaml",
		releasePath:   "testdata/example22/releases/compose-with-artifacts.yaml",
		manifestPath:  "testdata/example22/manifests/compose-with-artifacts.yaml",
		traceRoot:     traceRoot,
	})
	if len(res.Files) == 0 {
		t.Fatalf("renderer produced no files; trace assertion vacuous")
	}

	nodeDir := findTraceNodeDir(t, traceRoot, "overlays")
	assertTraceFilePresent(t, nodeDir, "01-pillars-initial.yaml")
	assertTraceFilePresent(t, nodeDir, "02-pass1-provides.yaml")
	assertTraceFilePresent(t, nodeDir, "03-pass2-inject.yaml")
	assertTraceFilePresent(t, nodeDir, "08-phaseB-workload.yaml")
	assertTraceFilePresent(t, nodeDir, "11-engine-emission.log")
	assertTraceFilePresent(t, nodeDir, "12-labels-injected.log")
	// Slots 09 and 10: ComposeArtifacts has both placeholder workload AND
	// artifacts, so both per-file slot directories should appear.
	for _, slotDir := range []string{"09-phaseB-artifacts", "10-engine-synthesis"} {
		full := filepath.Join(nodeDir, slotDir)
		entries, err := os.ReadDir(full)
		if err != nil {
			t.Errorf("%s dir not found at %s: %v", slotDir, full, err)
			continue
		}
		if len(entries) == 0 {
			t.Errorf("%s is empty; expected per-file entries", slotDir)
		}
	}
}

// findTraceNodeDir locates the trace directory for a given nodeID under
// traceRoot/boundaries/<boundary>/<nodeID>/. Returns the discovered path or
// fails the test. The boundary segment is unknown to the test (boundaries
// vary per fixture); discovery is by directory walk.
func findTraceNodeDir(t *testing.T, traceRoot, nodeID string) string {
	t.Helper()
	boundariesDir := filepath.Join(traceRoot, "boundaries")
	var found string
	err := filepath.Walk(boundariesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if filepath.Base(path) == nodeID && strings.HasPrefix(path, boundariesDir+string(filepath.Separator)) {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", boundariesDir, err)
	}
	if found == "" {
		t.Fatalf("trace node dir for %q not found under %s; existing entries: %v", nodeID, boundariesDir, listTraceContents(boundariesDir))
	}
	return found
}

// assertTraceFilePresent verifies a single file exists at nodeDir/name.
func assertTraceFilePresent(t *testing.T, nodeDir, name string) {
	t.Helper()
	full := filepath.Join(nodeDir, name)
	if _, err := os.Stat(full); err != nil {
		t.Errorf("expected trace file %s missing: %v", full, err)
	}
}

// assertTraceFileAbsent verifies a file does NOT exist at nodeDir/name.
func assertTraceFileAbsent(t *testing.T, nodeDir, name string) {
	t.Helper()
	full := filepath.Join(nodeDir, name)
	if _, err := os.Stat(full); err == nil {
		t.Errorf("trace file %s present but expected absent for this fixture's input kinds", full)
	}
}

// assertTraceDirAbsent verifies a directory does NOT exist at nodeDir/name.
func assertTraceDirAbsent(t *testing.T, nodeDir, name string) {
	t.Helper()
	full := filepath.Join(nodeDir, name)
	if info, err := os.Stat(full); err == nil && info.IsDir() {
		t.Errorf("trace dir %s present but expected absent for this fixture's input kinds", full)
	}
}

// listTraceContents returns sorted dir entries for diagnostic messages.
func listTraceContents(root string) []string {
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		out = append(out, path)
		return nil
	})
	sort.Strings(out)
	return out
}
