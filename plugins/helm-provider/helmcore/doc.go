// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package helmcore holds the helm provider's translation core: the functions
// that turn a node's render input into flat rendered Kubernetes YAML for GitOps
// consumers such as ArgoCD and Flux.
//
// The core carries no build tag, so it compiles for the host and for the wasip1
// guest alike. It sits beside the guest that consumes it because it is guest
// code: the host drives the render without linking any of it.
//
// # The two input kinds
//
//   - Sourced. The Recipe references a Helm chart via source:. The chart is
//     rendered through the Helm Go SDK with the Recipe's evaluated values.
//   - Synthesised. The Recipe declares a workload: block, which is translated
//     into a Deployment and an optional Service.
//
// A Recipe that declares neither source: nor workload: produces no compute.
// Nothing is added on the author's behalf.
//
// This is not the helm-chart provider, which emits a chart scaffold rather than
// rendered manifests; that translation lives in
// plugins/helmchart-provider/helmchartcore.
package helmcore
