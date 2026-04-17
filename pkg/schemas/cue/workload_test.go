// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cue

import (
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

// TestWorkloadCUECompiles verifies the embedded Workload CUE source compiles
// without error at test time. This catches syntax errors early rather than at
// user-runtime when the schema is first unified against a parsed Workload.
func TestWorkloadCUECompiles(t *testing.T) {
	t.Parallel()
	ctx := cuecontext.New()
	v := ctx.CompileString(WorkloadCUE)
	if err := v.Err(); err != nil {
		t.Fatalf("workload.cue compile error: %v", err)
	}
}

// TestWorkloadCUENonEmpty guards against accidental truncation of the embedded
// file via build mishaps — if embed fails silently the variable would be empty.
func TestWorkloadCUENonEmpty(t *testing.T) {
	t.Parallel()
	if !strings.Contains(WorkloadCUE, "#Workload:") {
		t.Fatalf("WorkloadCUE missing #Workload definition; embed failed?")
	}
	if !strings.Contains(WorkloadCUE, `apiVersion: "aggrega.ai/v1alpha1"`) {
		t.Fatalf("WorkloadCUE missing apiVersion constraint")
	}
}
