// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

// Workload service-entry type values: how far an entry's ports reach. The
// vocabulary is the Workload schema's enum, and each provider engine maps a
// value to its own native exposure or refuses one its target cannot express.
//
//   - ClusterIP and Headless keep the ports on the target's own network.
//   - NodePort and LoadBalancer publish them beyond it.
//   - Loopback publishes them on the host's own loopback address only.
const (
	WorkloadServiceTypeClusterIP    = "ClusterIP"
	WorkloadServiceTypeHeadless     = "Headless"
	WorkloadServiceTypeNodePort     = "NodePort"
	WorkloadServiceTypeLoadBalancer = "LoadBalancer"
	WorkloadServiceTypeLoopback     = "Loopback"
)
