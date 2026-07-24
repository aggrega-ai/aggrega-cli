// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"encoding/json"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestResourceListYAMLRetainsExtended proves a yaml.Unmarshal of a resourceList
// with an extended key retains cpu, memory, and the extended quantity, and that a
// yaml round-trip re-emits all three.
func TestResourceListYAMLRetainsExtended(t *testing.T) {
	src := "cpu: \"2\"\nmemory: 8Gi\nnvidia.com/gpu: \"1\"\n"
	var rl ResourceList
	if err := yaml.Unmarshal([]byte(src), &rl); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if rl.CPU != "2" || rl.Memory != "8Gi" {
		t.Fatalf("cpu/memory lost: cpu=%q memory=%q", rl.CPU, rl.Memory)
	}
	got, ok := rl.Extended.Get("nvidia.com/gpu")
	if !ok || got != "1" {
		t.Fatalf("extended key lost: ok=%v got=%q", ok, got)
	}
	out, err := yaml.Marshal(rl)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	var rl2 ResourceList
	if err := yaml.Unmarshal(out, &rl2); err != nil {
		t.Fatalf("yaml round-trip: %v", err)
	}
	if g, _ := rl2.Extended.Get("nvidia.com/gpu"); g != "1" {
		t.Fatalf("extended key dropped on yaml round-trip: %q", g)
	}
}

// TestResourceListJSONRetainsExtended proves the render-payload json crossing
// keeps the extended key — this is the load-bearing path (a yaml-inline map would
// be dropped by encoding/json here).
func TestResourceListJSONRetainsExtended(t *testing.T) {
	var rl ResourceList
	rl.CPU = "2"
	rl.Memory = "8Gi"
	rl.Extended.Set("nvidia.com/gpu", "1")

	data, err := json.Marshal(rl)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(data) != `{"cpu":"2","memory":"8Gi","nvidia.com/gpu":"1"}` {
		t.Fatalf("unexpected json emission: %s", data)
	}
	var rl2 ResourceList
	if err := json.Unmarshal(data, &rl2); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if rl2.CPU != "2" || rl2.Memory != "8Gi" {
		t.Fatalf("cpu/memory lost on json round-trip")
	}
	if g, ok := rl2.Extended.Get("nvidia.com/gpu"); !ok || g != "1" {
		t.Fatalf("extended key dropped on json round-trip: ok=%v got=%q", ok, g)
	}
}

// TestResourceListNoExtendedByteIdentical proves a resourceList carrying only
// cpu/memory emits exactly as before (cpu then memory), so existing renders do not
// regress.
func TestResourceListNoExtendedByteIdentical(t *testing.T) {
	rl := ResourceList{CPU: "500m", Memory: "128Mi"}
	data, err := json.Marshal(rl)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(data) != `{"cpu":"500m","memory":"128Mi"}` {
		t.Fatalf("json regression: %s", data)
	}
	empty, err := json.Marshal(ResourceList{})
	if err != nil {
		t.Fatalf("json.Marshal empty: %v", err)
	}
	if string(empty) != `{}` {
		t.Fatalf("empty json regression: %s", empty)
	}
}

// TestWorkloadRuntimeClassNameRoundTrip proves runtimeClassName parses and
// re-marshals on both surfaces.
func TestWorkloadRuntimeClassNameRoundTrip(t *testing.T) {
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\nruntimeClassName: nvidia\n"
	var w Workload
	if err := yaml.Unmarshal([]byte(src), &w); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if w.RuntimeClassName != "nvidia" {
		t.Fatalf("runtimeClassName lost: %q", w.RuntimeClassName)
	}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var w2 Workload
	if err := json.Unmarshal(data, &w2); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if w2.RuntimeClassName != "nvidia" {
		t.Fatalf("runtimeClassName dropped on json round-trip: %q", w2.RuntimeClassName)
	}
}
