// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package cue exposes embedded CUE schema sources for Aggrega's platform-owned
// kinds. These are go:embed'd into the aggrega-cli binary and consumed at
// runtime for strict validation (Phase B.2 Workload parsing, etc.).
package cue

import _ "embed"

// WorkloadCUE is the CUE source for the aggrega.ai/v1alpha1 Workload kind.
// Mirrors workload-v1alpha1.schema.json in the aggrega-schemas repo. Consumers compile
// and unify this against parsed Workload values during Phase B.2.
//
//go:embed workload.cue
var WorkloadCUE string
