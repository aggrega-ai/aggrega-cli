// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"encoding/json"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// A Provider guest receives its per-node payload as JSON and decodes it through
// the generated WorkloadProjection codec. The Workload's opaque pass-through
// fields (scheduling affinity, tolerations, topology spread constraints,
// lifecycle httpGet, volume items) are typed `any`, so a plain reflective JSON
// decode would widen every integer literal to float64. A float64 re-serializes
// a round number in exponential form (1000000 becomes 1e+06) and silently drops
// precision beyond 2^53. The guest input codec therefore decodes opaque values
// with a number-preserving decoder so integers survive as json.Number.
//
// Host-side Workload-to-manifest synthesis runs inside the Provider Wasm guest
// and is not wired in this package, so these tests drive the decode directly and
// use yaml serialization as the faithful proxy for the guest's emit — it is the
// exact serializer stage where the float64 widening becomes an observable
// 1e+06.

func TestWorkloadProjection_OpaqueIntDecodesAsNumber(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"workload":{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","metadata":{"name":"web"},"scheduling":{"affinity":{"weight":1000000}}}}`)

	var wp WorkloadProjection
	if err := wp.UnmarshalJSON(payload); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}

	if wp.Workload.Scheduling == nil {
		t.Fatalf("scheduling: decoded to nil")
	}
	aff, ok := wp.Workload.Scheduling.Affinity.(map[string]any)
	if !ok {
		t.Fatalf("affinity: expected map[string]any, got %T", wp.Workload.Scheduling.Affinity)
	}
	num, ok := aff["weight"].(json.Number)
	if !ok {
		t.Fatalf("affinity.weight: expected json.Number, got %T (%v)", aff["weight"], aff["weight"])
	}
	if num != json.Number("1000000") {
		t.Errorf("affinity.weight: got %q, want %q", num, "1000000")
	}
}

// A large integer beyond float64's exact-integer range (2^53) is the sharpest
// proof: a float64 decode truncates 9007199254740993 to ...992, an
// unrecoverable loss. The number-preserving decode keeps the exact digits.
func TestWorkloadProjection_OpaqueIntYAMLKeepsFidelity(t *testing.T) {
	t.Parallel()
	const bigInt = "9007199254740993" // 2^53 + 1
	payload := []byte(`{"workload":{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","metadata":{"name":"web"},"scheduling":{"affinity":{"weight":1000000},"tolerations":[{"tolerationSeconds":` + bigInt + `}]}}}`)

	var wp WorkloadProjection
	if err := wp.UnmarshalJSON(payload); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}

	// Guard: the decoded shape is a real types.Workload, not a bare map.
	if _, ok := any(wp.Workload).(types.Workload); !ok {
		t.Fatalf("workload: unexpected decoded type %T", wp.Workload)
	}

	out, err := yaml.Marshal(wp.Workload)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	got := string(out)

	if strings.Contains(got, "1e+06") {
		t.Errorf("workload YAML widened 1000000 to exponential form:\n%s", got)
	}
	if !strings.Contains(got, "1000000") {
		t.Errorf("workload YAML lost the integer 1000000:\n%s", got)
	}
	if !strings.Contains(got, bigInt) {
		t.Errorf("workload YAML lost >2^53 integer precision (want %s):\n%s", bigInt, got)
	}
	if strings.Contains(got, "9.007199254740992e+15") {
		t.Errorf("workload YAML truncated >2^53 integer to float64:\n%s", got)
	}
}
