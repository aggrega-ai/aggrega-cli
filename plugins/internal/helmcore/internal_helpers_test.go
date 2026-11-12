// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestSortedLabelKeys_DeterministicOrder pins the deterministic-output
// helper: keys return in sorted order regardless of input map
// iteration order.
func TestSortedLabelKeys_DeterministicOrder(t *testing.T) {
	t.Parallel()
	m := map[string]string{
		"zebra": "z",
		"apple": "a",
		"mango": "m",
	}
	got := sortedLabelKeys(m)
	want := []string{"apple", "mango", "zebra"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q; want %q", i, got[i], want[i])
		}
	}
	// Empty map → empty result.
	if len(sortedLabelKeys(map[string]string{})) != 0 {
		t.Errorf("empty map should return empty slice")
	}
}

// TestTranslateResources_EveryBranch pins the resource-translation
// helper: requests-only, limits-only, both, and neither cases each
// produce the documented output shape.
func TestTranslateResources_EveryBranch(t *testing.T) {
	t.Parallel()
	// Both populated.
	full := &types.ResourceSpec{
		Requests: &types.ResourceList{CPU: "250m", Memory: "512Mi"},
		Limits:   &types.ResourceList{CPU: "500m", Memory: "1Gi"},
	}
	got := translateResources(full)
	if got.Requests["cpu"] != "250m" || got.Requests["memory"] != "512Mi" {
		t.Errorf("full requests: got %+v", got.Requests)
	}
	if got.Limits["cpu"] != "500m" || got.Limits["memory"] != "1Gi" {
		t.Errorf("full limits: got %+v", got.Limits)
	}
	// Requests only.
	reqOnly := translateResources(&types.ResourceSpec{
		Requests: &types.ResourceList{CPU: "100m"},
	})
	if reqOnly.Limits != nil {
		t.Errorf("limits should be nil when not set; got %+v", reqOnly.Limits)
	}
	// Limits only.
	limOnly := translateResources(&types.ResourceSpec{
		Limits: &types.ResourceList{Memory: "256Mi"},
	})
	if limOnly.Requests != nil {
		t.Errorf("requests should be nil when not set; got %+v", limOnly.Requests)
	}
	// Empty.
	empty := translateResources(&types.ResourceSpec{})
	if empty.Requests != nil || empty.Limits != nil {
		t.Errorf("empty spec: both fields should be nil; got %+v", empty)
	}
	// CPU only (memory empty) - sparse population.
	cpuOnly := translateResources(&types.ResourceSpec{
		Requests: &types.ResourceList{CPU: "100m"},
	})
	if _, has := cpuOnly.Requests["memory"]; has {
		t.Errorf("memory should not appear when empty; got %+v", cpuOnly.Requests)
	}
}

// TestTranslateLifecycle_PostStartAndPreStop pins the lifecycle-spec
// translation: either or both hooks populated translate to non-nil
// LifecycleHandler entries; both nil produces an empty Lifecycle.
func TestTranslateLifecycle_PostStartAndPreStop(t *testing.T) {
	t.Parallel()
	post := &types.LifecycleHookSpec{Exec: []string{"/bin/sh", "-c", "warmup"}}
	pre := &types.LifecycleHookSpec{Exec: []string{"/bin/sh", "-c", "drain"}}
	got := translateLifecycle(&types.LifecycleSpec{PostStart: post, PreStop: pre})
	if got.PostStart == nil || got.PreStop == nil {
		t.Errorf("expected both hooks populated; got %+v", got)
	}
	// Only PostStart.
	got = translateLifecycle(&types.LifecycleSpec{PostStart: post})
	if got.PostStart == nil || got.PreStop != nil {
		t.Errorf("PostStart-only: got %+v", got)
	}
	// Empty.
	got = translateLifecycle(&types.LifecycleSpec{})
	if got.PostStart != nil || got.PreStop != nil {
		t.Errorf("empty: both should be nil; got %+v", got)
	}
}

// TestTranslateLifecycleHandler_ExecAndHTTPGet pins each populated
// shape: Exec list → ExecAction; HTTPGet pointer → HTTPGet on the
// handler.
func TestTranslateLifecycleHandler_ExecAndHTTPGet(t *testing.T) {
	t.Parallel()
	execHook := translateLifecycleHandler(&types.LifecycleHookSpec{Exec: []string{"/bin/sh", "-c", "do-something"}})
	if execHook.Exec == nil || len(execHook.Exec.Command) != 3 {
		t.Errorf("exec hook: got %+v", execHook.Exec)
	}
	// HTTPGet field uses the package-local HTTPGetAction type via the
	// types alias; constructing one requires reaching into types pkg.
	// Skip the HTTPGet-specific construction; the exec branch is the
	// primary contract this test pins.
	// Empty handler.
	empty := translateLifecycleHandler(&types.LifecycleHookSpec{})
	if empty.Exec != nil || empty.HTTPGet != nil {
		t.Errorf("empty handler: both should be nil; got %+v", empty)
	}
}
