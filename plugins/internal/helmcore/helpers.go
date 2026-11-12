// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore

import (
	"strings"
)

// Shared helm-render helpers and Kubernetes API-version constants used by the
// sourced and synthesized (Workload) render paths. Workload synthesis itself
// lives in synth_core.go (SynthesizeWorkloadCore / Phase B.4); the engine never
// synthesizes compute from a bare values dict — that would invent compute the
// developer never declared (Invariant 4).

const (
	k8sDeploymentAPIVersion = "apps/v1"
	k8sServiceAPIVersion    = "v1"
)

func sanitizeContainerName(id string) string {
	r := strings.NewReplacer(".", "-", "_", "-")
	return r.Replace(id)
}

// nilIfEmpty returns nil when m is empty so yaml `omitempty` elides the
// metadata.annotations key entirely (preserves byte-identity for the
// default-0 sync-wave case across rendered fixtures).
func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}
