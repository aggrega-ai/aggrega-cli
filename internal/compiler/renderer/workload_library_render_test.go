// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// The workload body carries a template expression (.values.tag) so the proof
// exercises the full resolve → EvalTemplate → parse path, not a static string.
const wlLibraryBody = `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:{{ .values.tag }}
`

// runWorkloadPhaseB runs B.0 → B.1 → B.2 for a recipe whose workload is w,
// against a context carrying values.tag, with the given WorkloadLibrary set, and
// returns the parsed workload. It exercises the production resolveWorkloadText →
// EvalTemplate → parse path phaseB2 drives.
func runWorkloadPhaseB(t *testing.T, w types.WorkloadTemplate, wlibs map[string]types.WorkloadLibrary) (*types.Workload, error) {
	t.Helper()
	ctx := minimalCtx()
	ctx.Values.Set("tag", "1.0")
	r := minimalRecipe("test")
	r.Spec.Workload = w
	state := NewPhaseBState(r, "test-node", "default", "recipe.yaml")
	state.WorkloadLibraries = wlibs
	emptyLocals := types.OrderedMap[string]{}
	emptySlots := types.OrderedMap[types.AttachmentSlot]{}
	if err := phaseB0(ctx, state, emptyLocals); err != nil {
		t.Fatalf("phaseB0: %v", err)
	}
	if err := phaseB1(ctx, state); err != nil {
		t.Fatalf("phaseB1: %v", err)
	}
	if err := phaseB2(ctx, state, emptySlots); err != nil {
		return nil, err
	}
	return state.ParsedWorkload, nil
}

// TestWorkloadLibraryImport_ByteIdenticalToInline is the load-bearing proof (R5,
// Invariant 8): a library-imported workload body renders byte-identically to the
// equivalent inline body. The library indirection collapses in phaseB2 — the
// resolver returns the entry body verbatim, then the SAME EvalTemplate + parse
// runs — so equal parsed workloads guarantee byte-identical synthesised output
// (phaseB4 has no library-specific path). The proof compares both the deep struct
// and its YAML serialisation.
func TestWorkloadLibraryImport_ByteIdenticalToInline(t *testing.T) {
	t.Parallel()

	inlineW, err := runWorkloadPhaseB(t, types.WorkloadTemplate{Inline: wlLibraryBody}, nil)
	if err != nil {
		t.Fatalf("inline recipe phaseB: %v", err)
	}

	wlibs := map[string]types.WorkloadLibrary{
		"BaseWorkloads": workloadLibraryFixture("BaseWorkloads", map[string]string{"Deployment": wlLibraryBody}),
	}
	libraryW, err := runWorkloadPhaseB(t, types.WorkloadTemplate{Import: "BaseWorkloads#/workloads/Deployment"}, wlibs)
	if err != nil {
		t.Fatalf("library-import recipe phaseB: %v", err)
	}

	inlineBytes, err := yaml.Marshal(inlineW)
	if err != nil {
		t.Fatalf("marshal inline workload: %v", err)
	}
	libraryBytes, err := yaml.Marshal(libraryW)
	if err != nil {
		t.Fatalf("marshal library workload: %v", err)
	}
	if string(inlineBytes) != string(libraryBytes) {
		t.Errorf("library-imported workload is not byte-identical to inline:\n--- inline ---\n%s\n--- library ---\n%s", inlineBytes, libraryBytes)
	}
	// Sanity: the template actually evaluated (`.values.tag` → 1.0), so the
	// byte-identity is over rendered content, not two empty structs.
	main, ok := libraryW.Containers.Get("main")
	if !ok || main.Image != "app:1.0" {
		t.Errorf("library workload container main = %+v; want image app:1.0", main)
	}
}

// TestWorkloadLibraryImport_ChainByteIdentical proves the chain case: a
// WorkloadLibrary entry whose body is itself a field-root $import of another
// entry resolves to the terminal body and renders byte-identically to inline.
func TestWorkloadLibraryImport_ChainByteIdentical(t *testing.T) {
	t.Parallel()

	inlineW, err := runWorkloadPhaseB(t, types.WorkloadTemplate{Inline: wlLibraryBody}, nil)
	if err != nil {
		t.Fatalf("inline recipe phaseB: %v", err)
	}

	wlibs := map[string]types.WorkloadLibrary{
		"BaseWorkloads": workloadLibraryFixture("BaseWorkloads", map[string]string{
			"Deployment": "$import: Real#/workloads/Deployment",
		}),
		"Real": workloadLibraryFixture("Real", map[string]string{"Deployment": wlLibraryBody}),
	}
	chainW, err := runWorkloadPhaseB(t, types.WorkloadTemplate{Import: "BaseWorkloads#/workloads/Deployment"}, wlibs)
	if err != nil {
		t.Fatalf("chain library-import recipe phaseB: %v", err)
	}

	inlineBytes, _ := yaml.Marshal(inlineW)
	chainBytes, _ := yaml.Marshal(chainW)
	if string(inlineBytes) != string(chainBytes) {
		t.Errorf("chained library import is not byte-identical to inline:\n--- inline ---\n%s\n--- chain ---\n%s", inlineBytes, chainBytes)
	}
}

// TestWorkloadLibraryImport_LoudFailures proves the error paths through the real
// phaseB2 path: a host-bearing oci:// reference, an unknown library, and an
// unknown entry each halt loudly with the mirrored sentinel rather than silently
// rendering nothing.
func TestWorkloadLibraryImport_LoudFailures(t *testing.T) {
	t.Parallel()

	wlibs := map[string]types.WorkloadLibrary{
		"BaseWorkloads": workloadLibraryFixture("BaseWorkloads", map[string]string{"Deployment": wlLibraryBody}),
	}
	cases := []struct {
		name string
		imp  string
		want error
	}{
		{"oci host rejected", "oci://hub/BaseWorkloads#/workloads/Deployment", ErrHostInLibraryImport()},
		{"unknown library", "Absent#/workloads/Deployment", ErrWorkloadLibraryNotFound()},
		{"unknown entry", "BaseWorkloads#/workloads/Missing", ErrWorkloadEntryNotFound()},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := runWorkloadPhaseB(t, types.WorkloadTemplate{Import: tc.imp}, wlibs)
			if err == nil {
				t.Fatalf("expected a loud failure for %q; got nil", tc.imp)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v; want %v", err, tc.want)
			}
		})
	}
}

// TestWorkloadLibraryImport_CycleHaltsThroughPhaseB proves the cycle guard on the
// real phaseB2 path: a library↔library import cycle halts with ErrCircularImport.
func TestWorkloadLibraryImport_CycleHaltsThroughPhaseB(t *testing.T) {
	t.Parallel()

	wlibs := map[string]types.WorkloadLibrary{
		"A": workloadLibraryFixture("A", map[string]string{"X": "$import: B#/workloads/Y"}),
		"B": workloadLibraryFixture("B", map[string]string{"Y": "$import: A#/workloads/X"}),
	}
	_, err := runWorkloadPhaseB(t, types.WorkloadTemplate{Import: "A#/workloads/X"}, wlibs)
	if err == nil {
		t.Fatal("expected a cycle halt; got nil")
	}
	if !errors.Is(err, ErrCircularImport()) {
		t.Errorf("error = %v; want ErrCircularImport", err)
	}
}
