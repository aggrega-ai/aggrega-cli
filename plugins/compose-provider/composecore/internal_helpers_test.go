// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestPickPrimaryProbe_EveryBranch pins the probe-selection precedence:
// Live > Ready > Startup > nil. The function returns the first non-nil
// probe in that order, together with the authored name of the slot it came from.
func TestPickPrimaryProbe_EveryBranch(t *testing.T) {
	t.Parallel()
	live := &types.ProbeSpec{HTTP: "/live"}
	ready := &types.ProbeSpec{HTTP: "/ready"}
	startup := &types.ProbeSpec{HTTP: "/startup"}

	tests := []struct {
		scenario string
		health   types.HealthSpec
		want     *types.ProbeSpec
		wantSlot string
	}{
		{"all three slots set: live wins", types.HealthSpec{Live: live, Ready: ready, Startup: startup}, live, "live"},
		{"live absent, ready present: ready wins", types.HealthSpec{Ready: ready, Startup: startup}, ready, "ready"},
		{"only startup set: startup wins", types.HealthSpec{Startup: startup}, startup, "startup"},
		{"no slot set: no probe and no slot name", types.HealthSpec{}, nil, ""},
	}

	for _, tc := range tests {
		t.Run(tc.scenario, func(t *testing.T) {
			t.Parallel()
			got, gotSlot := pickPrimaryProbe(&tc.health)
			if got != tc.want {
				t.Errorf("probe: got %+v; want %+v", got, tc.want)
			}
			if gotSlot != tc.wantSlot {
				t.Errorf("slot name: got %q; want %q", gotSlot, tc.wantSlot)
			}
		})
	}
}

// TestPickMainContainerName_EveryBranch pins the main-container
// selection: a container literally named "main" is main; otherwise
// the first container in author order is main; empty Containers
// returns (zero, false).
func TestPickMainContainerName_EveryBranch(t *testing.T) {
	t.Parallel()
	// Empty containers.
	emptyW := &types.Workload{}
	if name, ok := pickMainContainerName(emptyW); ok || name != "" {
		t.Errorf("empty containers: got (%q, %v); want (\"\", false)", name, ok)
	}
	// "main" explicit.
	containers := types.OrderedMap[types.ContainerSpec]{}
	containers.Set("sidecar", types.ContainerSpec{Image: "side"})
	containers.Set("main", types.ContainerSpec{Image: "primary"})
	w := &types.Workload{Containers: containers}
	if name, ok := pickMainContainerName(w); !ok || name != "main" {
		t.Errorf("explicit main: got (%q, %v); want (main, true)", name, ok)
	}
	// First container is main when no "main" named.
	c2 := types.OrderedMap[types.ContainerSpec]{}
	c2.Set("worker", types.ContainerSpec{Image: "w"})
	c2.Set("sidecar", types.ContainerSpec{Image: "s"})
	w2 := &types.Workload{Containers: c2}
	if name, ok := pickMainContainerName(w2); !ok || name != "worker" {
		t.Errorf("no explicit main: got (%q, %v); want (worker, true)", name, ok)
	}
}

// TestAppendUnique_AddsNewSkipsDuplicate pins the dedup helper: a
// new entry appends; an entry already in the list returns the list
// unchanged.
func TestAppendUnique_AddsNewSkipsDuplicate(t *testing.T) {
	t.Parallel()
	got := appendUnique([]string{"a", "b"}, "c")
	if len(got) != 3 || got[2] != "c" {
		t.Errorf("new entry: expected [a b c]; got %v", got)
	}
	got = appendUnique([]string{"a", "b"}, "a")
	if len(got) != 2 {
		t.Errorf("duplicate: expected [a b]; got %v", got)
	}
	got = appendUnique(nil, "x")
	if len(got) != 1 || got[0] != "x" {
		t.Errorf("nil base: expected [x]; got %v", got)
	}
}
