// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"sort"
	"strings"
	"testing"
)

// TestTrace_FullLayout_OnHelmSourcedWithArtifacts exercises the full per-node
// trace layout against a fixture whose Recipes carry source: + valuesMapping:
// + artifacts: (plus the always-present pre-loop captures and the inject
// mapping flow). Asserts the documented layout entries land for each node.
// Trace is collected in-memory through the build-faithful harness
// (Input.IncludeTrace) and keyed by the same relative path the on-disk trace
// writer would use.
//
// Slots present per the helm-sourced-with-artifacts shape:
//   - 01-pillars-initial.yaml (pre-loop)
//   - 02-provides-evaluation.yaml (pre-loop)
//   - 03-inject-hydration.yaml (pre-loop)
//   - 04-inject-mapping.yaml
//   - 05-source-resolution.yaml (the source template)
//   - 06-source-locals.yaml (the locals source resolution publishes first)
//   - 07-values-mapping.yaml
//   - 09-artifact-evaluation/<filename>.yaml (per-artifact)
//   - 11-engine-emission.log (per-node emission log, after feature rendering)
//   - 12-labels-injected.log (provenance-label summary)
//
// Slots absent for this fixture (no workload-mode Recipes):
//   - 08-workload-synthesis.yaml
//   - 10-engine-synthesis/<filename>.yaml
func TestTrace_FullLayout_OnHelmSourcedWithArtifacts(t *testing.T) {
	t.Parallel()
	res := runFullstackAppVariant(t, variantFixture{
		name:         "helm-sourced-with-artifacts",
		includeTrace: true,
	})
	if len(res.Files) == 0 {
		t.Fatalf("renderer produced no files; trace assertion vacuous")
	}

	// Expected slots present per node. Three nodes: frontend, backend, data.main-db.
	for _, nodeID := range []string{"frontend", "backend", "data.main-db"} {
		prefix := traceNodePrefix(t, res.Trace, nodeID)
		assertTraceSlotPresent(t, res.Trace, prefix, "01-pillars-initial.yaml")
		assertTraceSlotPresent(t, res.Trace, prefix, "02-provides-evaluation.yaml")
		assertTraceSlotPresent(t, res.Trace, prefix, "03-inject-hydration.yaml")
		assertTraceSlotPresent(t, res.Trace, prefix, "04-inject-mapping.yaml")
		assertTraceSlotPresent(t, res.Trace, prefix, "05-source-resolution.yaml")
		assertTraceSlotPresent(t, res.Trace, prefix, "06-source-locals.yaml")
		assertTraceSlotPresent(t, res.Trace, prefix, "07-values-mapping.yaml")
		assertTraceSlotPresent(t, res.Trace, prefix, "11-engine-emission.log")
		assertTraceSlotPresent(t, res.Trace, prefix, "12-labels-injected.log")
		// workload-mode and synthesis-per-file slots absent for sourced shape:
		assertTraceSlotAbsent(t, res.Trace, prefix, "08-workload-synthesis.yaml")
		assertTraceSlotDirAbsent(t, res.Trace, prefix, "10-engine-synthesis")
	}

	// Per-artifact granularity at slot 09: each component's Recipe has
	// one artifacts entry; assert at least one file under
	// <node>/09-artifact-evaluation/.
	for _, nodeID := range []string{"frontend", "backend", "data.main-db"} {
		prefix := traceNodePrefix(t, res.Trace, nodeID)
		entries := traceSlotDirEntries(res.Trace, prefix, "09-artifact-evaluation")
		if len(entries) == 0 {
			t.Errorf("node %s: 09-artifact-evaluation is empty; expected per-artifact entries", nodeID)
		}
	}
}

// TestTrace_HelmSynthesized_OmitsSourceAndValuesMappingSlots exercises the
// negative-presence assertions for slots that should be absent when the
// Recipe is workload-only. Slots 05 (source) and 07 (valuesMapping) are absent
// for helm-synthesized; slot 08 (workload) and 10 (per-synthesized-file) ARE
// present. Pre-loop (01/02/03/04), 06 (locals — source resolution always emits
// it), 11, 12 always present.
func TestTrace_HelmSynthesized_OmitsSourceAndValuesMappingSlots(t *testing.T) {
	t.Parallel()
	res := runFullstackAppVariant(t, variantFixture{
		name:         "helm-synthesized",
		includeTrace: true,
	})
	if len(res.Files) == 0 {
		t.Fatalf("renderer produced no files; trace assertion vacuous")
	}

	for _, nodeID := range []string{"frontend", "backend", "data.main-db"} {
		prefix := traceNodePrefix(t, res.Trace, nodeID)
		// Always-present slots:
		assertTraceSlotPresent(t, res.Trace, prefix, "01-pillars-initial.yaml")
		assertTraceSlotPresent(t, res.Trace, prefix, "02-provides-evaluation.yaml")
		assertTraceSlotPresent(t, res.Trace, prefix, "03-inject-hydration.yaml")
		assertTraceSlotPresent(t, res.Trace, prefix, "04-inject-mapping.yaml")
		assertTraceSlotPresent(t, res.Trace, prefix, "08-workload-synthesis.yaml")
		assertTraceSlotPresent(t, res.Trace, prefix, "11-engine-emission.log")
		assertTraceSlotPresent(t, res.Trace, prefix, "12-labels-injected.log")
		// Absent slots for sourceless + no-valuesMapping:
		assertTraceSlotAbsent(t, res.Trace, prefix, "05-source-resolution.yaml")
		assertTraceSlotAbsent(t, res.Trace, prefix, "07-values-mapping.yaml")
		// Slot 10 (per-synthesized-file): present because Recipe has workload:.
		// Each component synthesizes >= 1 file via WorkloadSynthesizer.
		entries := traceSlotDirEntries(res.Trace, prefix, "10-engine-synthesis")
		if len(entries) == 0 {
			t.Errorf("node %s: 10-engine-synthesis is empty; expected per-synthesized-file entries", nodeID)
		}
	}
}

// TestTrace_ComposeWithArtifacts_LayoutPresent asserts the trace wiring works
// for the compose engine (not just helm). The variant uses placeholder workload
// + artifacts; asserts pre-loop slots, the source-resolution and
// workload-synthesis slots, slot 09 (per-artifact), slot 10
// (per-synthesized-file), and slot 11 (emission) all land for compose.
func TestTrace_ComposeWithArtifacts_LayoutPresent(t *testing.T) {
	t.Parallel()
	res := runFullstackAppVariant(t, variantFixture{
		name:         "compose-with-artifacts",
		releasePath:  "testdata/fullstack-app/releases/compose-with-artifacts.yaml",
		includeTrace: true,
	})
	if len(res.Files) == 0 {
		t.Fatalf("renderer produced no files; trace assertion vacuous")
	}

	prefix := traceNodePrefix(t, res.Trace, "overlays")
	assertTraceSlotPresent(t, res.Trace, prefix, "01-pillars-initial.yaml")
	assertTraceSlotPresent(t, res.Trace, prefix, "02-provides-evaluation.yaml")
	assertTraceSlotPresent(t, res.Trace, prefix, "03-inject-hydration.yaml")
	assertTraceSlotPresent(t, res.Trace, prefix, "08-workload-synthesis.yaml")
	assertTraceSlotPresent(t, res.Trace, prefix, "11-engine-emission.log")
	assertTraceSlotPresent(t, res.Trace, prefix, "12-labels-injected.log")
	// Slots 09 and 10: ComposeArtifacts has both placeholder workload AND
	// artifacts, so both per-file slot directories should appear.
	for _, slotDir := range []string{"09-artifact-evaluation", "10-engine-synthesis"} {
		if len(traceSlotDirEntries(res.Trace, prefix, slotDir)) == 0 {
			t.Errorf("%s is empty; expected per-file entries", slotDir)
		}
	}
}

// traceNodePrefix locates the trace key prefix for a given nodeID within the
// in-memory trace map. Every node emits the always-present slot
// 12-labels-injected.log directly under its node directory, so the key ending
// in /<nodeID>/12-labels-injected.log uniquely anchors the node; the prefix is
// that key with the slot filename trimmed. Returns the prefix or fails the test.
func traceNodePrefix(t *testing.T, tr map[string][]byte, nodeID string) string {
	t.Helper()
	const anchor = "12-labels-injected.log"
	suffix := "/" + nodeID + "/" + anchor
	for k := range tr {
		if strings.HasSuffix(k, suffix) {
			return strings.TrimSuffix(k, "/"+anchor)
		}
	}
	t.Fatalf("trace node prefix for %q not found; keys: %v", nodeID, sortedTraceKeys(tr))
	return ""
}

// assertTraceSlotPresent verifies a single slot file exists at prefix/name.
func assertTraceSlotPresent(t *testing.T, tr map[string][]byte, prefix, name string) {
	t.Helper()
	if _, ok := tr[prefix+"/"+name]; !ok {
		t.Errorf("expected trace slot %s/%s missing", prefix, name)
	}
}

// assertTraceSlotAbsent verifies a slot file does NOT exist at prefix/name.
func assertTraceSlotAbsent(t *testing.T, tr map[string][]byte, prefix, name string) {
	t.Helper()
	if _, ok := tr[prefix+"/"+name]; ok {
		t.Errorf("trace slot %s/%s present but expected absent for this fixture's input kinds", prefix, name)
	}
}

// assertTraceSlotDirAbsent verifies no per-file slot entries exist under
// prefix/dir/.
func assertTraceSlotDirAbsent(t *testing.T, tr map[string][]byte, prefix, dir string) {
	t.Helper()
	if entries := traceSlotDirEntries(tr, prefix, dir); len(entries) > 0 {
		t.Errorf("trace slot dir %s/%s present (%d entries) but expected absent for this fixture's input kinds", prefix, dir, len(entries))
	}
}

// traceSlotDirEntries returns the trace keys representing per-file entries
// under prefix/dir/ (e.g. the per-artifact or per-synthesized-file slots).
func traceSlotDirEntries(tr map[string][]byte, prefix, dir string) []string {
	want := prefix + "/" + dir + "/"
	var out []string
	for k := range tr {
		if strings.HasPrefix(k, want) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// sortedTraceKeys returns the trace map's keys sorted, for diagnostic messages.
func sortedTraceKeys(tr map[string][]byte) []string {
	out := make([]string, 0, len(tr))
	for k := range tr {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
