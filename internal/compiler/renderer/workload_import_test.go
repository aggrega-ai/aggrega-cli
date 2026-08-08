// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// workloadLibraryFixture builds a WorkloadLibrary from entry-name → template-body
// pairs, inserting in sorted-name order for stable iteration.
func workloadLibraryFixture(name string, entries map[string]string) types.WorkloadLibrary {
	wl := types.WorkloadLibrary{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "WorkloadLibrary",
		Metadata:   types.WorkloadLibraryMetadata{Name: name, Version: "1.0.0"},
	}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		wl.Spec.Workloads.Set(k, types.WorkloadEntry{Body: entries[k]})
	}
	return wl
}

// TestResolveWorkloadLibraryImport_SingleLevel pins the base case: a bare-name
// reference resolves to its entry's template fragment verbatim.
func TestResolveWorkloadLibraryImport_SingleLevel(t *testing.T) {
	t.Parallel()
	libs := map[string]types.WorkloadLibrary{
		"BaseWorkloads": workloadLibraryFixture("BaseWorkloads", map[string]string{
			"Deployment": "kind: Deployment\nspec: {{ .values.spec }}",
		}),
	}
	got, err := resolveWorkloadLibraryImport("BaseWorkloads#/workloads/Deployment", libs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "kind: Deployment\nspec: {{ .values.spec }}"; got != want {
		t.Errorf("fragment = %q; want %q", got, want)
	}
}

// TestResolveWorkloadLibraryImport_VersionFloorStripped pins that a `:version`
// floor in the reference is stripped — the version is supplied by the resolved
// set (keyed by bare name).
func TestResolveWorkloadLibraryImport_VersionFloorStripped(t *testing.T) {
	t.Parallel()
	libs := map[string]types.WorkloadLibrary{
		"BaseWorkloads": workloadLibraryFixture("BaseWorkloads", map[string]string{"Deployment": "BODY"}),
	}
	got, err := resolveWorkloadLibraryImport("BaseWorkloads:1.2.0#/workloads/Deployment", libs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "BODY" {
		t.Errorf("fragment = %q; want BODY", got)
	}
}

// TestResolveWorkloadLibraryImport_Recursive pins the recursive case: an entry
// whose whole body is a field-root $import of another library entry resolves to
// that target's terminal fragment.
func TestResolveWorkloadLibraryImport_Recursive(t *testing.T) {
	t.Parallel()
	libs := map[string]types.WorkloadLibrary{
		"BaseWorkloads": workloadLibraryFixture("BaseWorkloads", map[string]string{
			"Deployment": "$import: Real#/workloads/Deployment",
		}),
		"Real": workloadLibraryFixture("Real", map[string]string{
			"Deployment": "kind: Deployment",
		}),
	}
	got, err := resolveWorkloadLibraryImport("BaseWorkloads#/workloads/Deployment", libs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "kind: Deployment"; got != want {
		t.Errorf("fragment = %q; want the recursively-resolved terminal %q", got, want)
	}
}

// TestResolveWorkloadLibraryImport_CycleHalts pins the cycle guard: a
// library↔library import cycle halts with ErrCircularImport (the shared
// refcycle.ErrCycle sentinel), not unbounded recursion.
func TestResolveWorkloadLibraryImport_CycleHalts(t *testing.T) {
	t.Parallel()
	libs := map[string]types.WorkloadLibrary{
		"A": workloadLibraryFixture("A", map[string]string{"X": "$import: B#/workloads/Y"}),
		"B": workloadLibraryFixture("B", map[string]string{"Y": "$import: A#/workloads/X"}),
	}
	_, err := resolveWorkloadLibraryImport("A#/workloads/X", libs, nil)
	if err == nil {
		t.Fatal("expected a cycle error; got nil")
	}
	if !errors.Is(err, ErrCircularImport()) {
		t.Errorf("error = %v; want ErrCircularImport", err)
	}
	if !errors.Is(err, refcycle.ErrCycle()) {
		t.Errorf("error = %v; want the shared refcycle.ErrCycle identity", err)
	}
}

// TestResolveWorkloadLibraryImport_Errors pins the resolution-failure surface:
// unknown library, unknown entry, host-bearing reference, and malformed shape.
func TestResolveWorkloadLibraryImport_Errors(t *testing.T) {
	t.Parallel()
	libs := map[string]types.WorkloadLibrary{
		"BaseWorkloads": workloadLibraryFixture("BaseWorkloads", map[string]string{"Deployment": "BODY"}),
	}
	cases := []struct {
		name string
		ref  string
		want error
	}{
		{"unknown library", "Absent#/workloads/Deployment", ErrWorkloadLibraryNotFound()},
		{"unknown entry", "BaseWorkloads#/workloads/Missing", ErrWorkloadEntryNotFound()},
		{"host-bearing", "oci://hub/BaseWorkloads:1.0.0#/workloads/Deployment", ErrHostInLibraryImport()},
		{"stale @ version separator", "BaseWorkloads@1.0.0#/workloads/Deployment", ErrWorkloadImportShape()},
		{"non-workloads fragment", "BaseWorkloads#/mappings/Deployment", ErrWorkloadImportShape()},
		{"no fragment", "BaseWorkloads", ErrWorkloadImportShape()},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveWorkloadLibraryImport(tc.ref, libs, nil)
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v; want %v", err, tc.want)
			}
		})
	}
}

// TestWorkloadTemplate_IsLibraryImport_HasOCIPrefix pins the type-level
// discriminators mirror the ValuesMapping equivalents: a `#/workloads/`
// bare-name identity is a library import; a local path is not; an `oci://`
// reference is a host, not a library import.
func TestWorkloadTemplate_IsLibraryImport_HasOCIPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		imp        string
		isLibrary  bool
		hasOCIHost bool
	}{
		{"library identity", "BaseWorkloads#/workloads/Deployment", true, false},
		{"library identity with version floor", "BaseWorkloads:1.0.0#/workloads/Deployment", true, false},
		{"local path", "./workload.yaml", false, false},
		{"oci host", "oci://hub/BaseWorkloads#/workloads/Deployment", false, true},
		{"mappings fragment is not a workload library import", "BaseWorkloads#/mappings/Deployment", false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := types.WorkloadTemplate{Import: tc.imp}
			if got := w.IsLibraryImport(); got != tc.isLibrary {
				t.Errorf("IsLibraryImport(%q) = %v; want %v", tc.imp, got, tc.isLibrary)
			}
			if got := w.HasOCIPrefix(); got != tc.hasOCIHost {
				t.Errorf("HasOCIPrefix(%q) = %v; want %v", tc.imp, got, tc.hasOCIHost)
			}
		})
	}
}
