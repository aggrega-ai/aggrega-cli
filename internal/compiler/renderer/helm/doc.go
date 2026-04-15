// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package helm is the provider that produces flat rendered Kubernetes YAML
// for GitOps consumers (ArgoCD, Flux). Two modes:
//
//   - Sourced: Recipe references an on-disk Helm chart via `source`. We use the
//     Helm Go SDK to render it with the values dictionary produced by Phase B.
//   - Sourceless: Recipe has no `source`. The Provider's baseSchema validates the
//     values dictionary and we generate Deployment + optional Service directly.
//
// This is NOT the `helm-chart` provider (which emits a chart scaffold); see
// `internal/compiler/renderer/helmchart` for that.
package helm
