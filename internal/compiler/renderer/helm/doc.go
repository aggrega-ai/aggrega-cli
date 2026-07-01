// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package helm is the provider that produces flat rendered Kubernetes YAML
// for GitOps consumers (ArgoCD, Flux). Two input kinds:
//
//   - Sourced: Recipe references an on-disk Helm chart via `source`. We use the
//     Helm Go SDK to render it with the values dictionary produced by Phase B.
//   - Synthesised: Recipe declares a `workload:` block. The Workload
//     synthesizer materialises Deployment + optional Service; a Recipe that
//     declares neither source nor workload invents no compute (Invariant 4).
//
// This is NOT the `helm-chart` provider (which emits a chart scaffold); see
// `internal/compiler/renderer/helmchart` for that.
package helm
