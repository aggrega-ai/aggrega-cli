// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestDiscoverWorkloadLibrariesTransitive asserts the discovery fixpoint on the
// workload axis: a recipe workload `$import:`s LibA, LibA's own entry body
// `$import:`s LibB, so both are discovered and resolved — discovery descends a
// resolved library's body, not just the consumer workloads.
func TestDiscoverWorkloadLibrariesTransitive(t *testing.T) {
	t.Parallel()

	lookup := &fakeLookup{
		workloadLibs: map[string]closure.LibraryResolution{
			"LibA": resolvedLib("1.0.0", "sha256:aaa"),
			"LibB": resolvedLib("1.0.0", "sha256:bbb"),
		},
	}
	consumers := []closure.WorkloadConsumer{
		{URI: "consumer", Template: "$import: LibA#/workloads/Foo"},
	}
	body := func(lib closure.ResolvedLibrary) ([]string, bool, error) {
		if lib.URI == "LibA" {
			return []string{"$import: LibB#/workloads/Bar"}, true, nil
		}
		return nil, true, nil
	}

	var c closure.Closure
	if err := c.DiscoverWorkloadLibraries(context.Background(), lookup, consumers, body); err != nil {
		t.Fatalf("DiscoverWorkloadLibraries: %v", err)
	}

	got := map[string]bool{}
	for _, lib := range c.WorkloadLibraries {
		got[lib.URI] = true
	}
	if !got["LibA"] || !got["LibB"] {
		t.Errorf("expected transitive discovery of LibA and LibB; got %+v", c.WorkloadLibraries)
	}
}

// TestDiscoverWorkloadLibrariesCycleTerminates asserts the visited-set terminates
// a library↔library reference cycle at discovery on the workload axis: LibA's body
// `$import:`s LibB and LibB's body `$import:`s LibA. Discovery descends each
// library once and returns without error or unbounded recursion.
func TestDiscoverWorkloadLibrariesCycleTerminates(t *testing.T) {
	t.Parallel()

	lookup := &fakeLookup{
		workloadLibs: map[string]closure.LibraryResolution{
			"LibA": resolvedLib("1.0.0", "sha256:aaa"),
			"LibB": resolvedLib("1.0.0", "sha256:bbb"),
		},
	}
	consumers := []closure.WorkloadConsumer{
		{URI: "consumer", Template: "$import: LibA#/workloads/Foo"},
	}
	body := func(lib closure.ResolvedLibrary) ([]string, bool, error) {
		switch lib.URI {
		case "LibA":
			return []string{"$import: LibB#/workloads/Bar"}, true, nil
		case "LibB":
			return []string{"$import: LibA#/workloads/Foo"}, true, nil
		}
		return nil, true, nil
	}

	var c closure.Closure
	if err := c.DiscoverWorkloadLibraries(context.Background(), lookup, consumers, body); err != nil {
		t.Fatalf("cycle should terminate cleanly at discovery; got %v", err)
	}

	got := map[string]bool{}
	for _, lib := range c.WorkloadLibraries {
		got[lib.URI] = true
	}
	if !got["LibA"] || !got["LibB"] {
		t.Errorf("expected both cycle members discovered; got %+v", c.WorkloadLibraries)
	}
}

// TestDiscoverWorkloadLibrariesIdentityGate asserts the walker records only
// host-less identity `#/workloads/` references: a local-file import and a
// host-bearing `oci://…` form are not cross-library edges and yield an empty
// WorkloadLibrary closure.
func TestDiscoverWorkloadLibrariesIdentityGate(t *testing.T) {
	t.Parallel()

	lookup := &fakeLookup{
		workloadLibs: map[string]closure.LibraryResolution{
			"LibA": resolvedLib("1.0.0", "sha256:aaa"),
		},
	}
	noBody := func(closure.ResolvedLibrary) ([]string, bool, error) { return nil, false, nil }

	cases := map[string]string{
		"local-file":   "$import: ./workloads/service.yaml",
		"host-bearing": "$import: oci://hub.aggrega.ai/LibA#/workloads/Foo",
		"inline":       "apiVersion: aggrega.ai/v1beta1\nkind: Workload\n",
	}
	for name, tmpl := range cases {
		tmpl := tmpl
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var c closure.Closure
			if err := c.DiscoverWorkloadLibraries(context.Background(), lookup,
				[]closure.WorkloadConsumer{{URI: "consumer", Template: tmpl}}, noBody); err != nil {
				t.Fatalf("DiscoverWorkloadLibraries: %v", err)
			}
			if len(c.WorkloadLibraries) != 0 {
				t.Errorf("expected no workload-library edge for %q; got %+v", tmpl, c.WorkloadLibraries)
			}
		})
	}
}
