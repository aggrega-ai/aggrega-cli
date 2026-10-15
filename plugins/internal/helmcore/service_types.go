// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// checkServiceTypes refuses a service entry whose type the rendered Kubernetes
// Service cannot express, and halts on the first one in author order.
//
// A Loopback entry asks for its ports to be reachable from the host's own
// loopback address and from nowhere else. A Kubernetes Service has no type that
// binds to a node's loopback address — which node addresses a NodePort listens
// on is a setting of the whole cluster, not of one Service — so the entry is
// refused by name rather than rendered as a ClusterIP, which would drop the
// published reach, or as a NodePort, which would publish the ports on every
// node address the cluster allows.
//
// The refusal reports the value-not-representable condition: the manifests do
// carry a Service type, and this value has no counterpart among the ones they
// accept, so the value is what the author changes. The sentence names the node,
// the service's authored path and the value. The value is one of the schema's
// enumerated types, so quoting it carries nothing an author declared.
func checkServiceTypes(w *types.Workload, nodeID string) error {
	for name, svc := range w.Services.All() {
		if svc.Type != rendercore.WorkloadServiceTypeLoopback {
			continue
		}
		return cerrors.Wrap(wasm.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("helm: node %q: services.%s: type %q publishes the service's ports on the host's loopback address only, "+
				"and a Kubernetes Service has no type that does; declare ClusterIP to keep the ports inside the cluster, "+
				"or NodePort or LoadBalancer to publish them outside it", nodeID, name, svc.Type))
	}
	return nil
}
