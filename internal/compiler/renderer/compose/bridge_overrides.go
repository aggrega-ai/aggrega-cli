// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Derivation for the compose provider's boundary→consumer env channel. The
// bridge's cross-network derivation is pure and tag-free (bridge_ports.go,
// bridge_gateway.go); this file carries the compute entry point the compose
// provider guest drives on its boundary_env_overrides export. It compiles for
// both the host and the wasip1 guest — the guest calls it, the host projects the
// edge context to the guest.

package compose

import (
	"fmt"
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ComputeBridgeConsumerEnvOverrides rewrites each bridged consumer's
// host-carrying env var to "aggrega-gw:<assigned-port>", from a boundary's
// internal uses edges and the driving feature's resolved infra. All
// compose-domain logic (network identity, port rule) lives here in the provider
// package the compose guest runs. A boundary with no bridged edge yields a nil
// map.
func ComputeBridgeConsumerEnvOverrides(internalUsesEdges []types.EnrichedEdge, infra types.OrderedMap[any]) (map[string]map[string]string, error) {
	portBase := bridgePortBase(infra)
	hostField := bridgeHostField(infra)

	specs := make([]bridgeSpec, 0, len(internalUsesEdges))
	for _, edge := range internalUsesEdges {
		specs = append(specs, bridgeSpecFromEdge(edge, hostField))
	}
	resolved := resolveBridge(specs, portBase)
	if len(resolved) == 0 {
		return nil, nil
	}

	out := map[string]map[string]string{}
	for _, r := range resolved {
		for _, targetVar := range r.Spec.TargetVars {
			m := out[r.Spec.Source]
			if m == nil {
				m = map[string]string{}
				out[r.Spec.Source] = m
			}
			m[targetVar] = fmt.Sprintf("%s:%d", bridgeGatewayService, r.ListenPort)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// bridgeSpecFromEdge normalizes a structured boundary edge into a bridgeSpec.
// TargetVars are the env inject keys injected FROM the upstream host field — the
// vars the consumer uses to reach the target, hence the ones rewritten.
func bridgeSpecFromEdge(e types.EnrichedEdge, hostField string) bridgeSpec {
	host, _ := e.Provides.Get(hostField)
	port := 0
	if v, ok := e.Provides.Get("port"); ok {
		port, _ = strconv.Atoi(v)
	}
	var targetVars []string
	if envInject, ok := e.Inject.Get("env"); ok {
		for _, in := range envInject.Keys() {
			if from, _ := envInject.Get(in); from == hostField {
				targetVars = append(targetVars, in)
			}
		}
	}
	return bridgeSpec{
		Source:       e.Source,
		Target:       e.Target,
		Socket:       e.Socket,
		SrcNS:        e.SourceNamespace,
		SrcPath:      e.SourceBoundaryPath,
		TgtNS:        e.TargetNamespace,
		TgtPath:      e.TargetBoundaryPath,
		UpstreamHost: host,
		TargetPort:   port,
		TargetVars:   targetVars,
	}
}

// bridgePortBase reads the optional portBase from the bridge attachment's infra,
// defaulting to defaultBridgePortBase.
func bridgePortBase(infra types.OrderedMap[any]) int {
	v, ok := infra.Get("portBase")
	if !ok {
		return defaultBridgePortBase
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		if p, err := strconv.Atoi(n); err == nil {
			return p
		}
	case interface{ Int64() (int64, error) }: // json.Number
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	}
	return defaultBridgePortBase
}

// bridgeHostField reads the optional upstream host provides-field selector from
// the bridge attachment's infra, defaulting to defaultUpstreamHostField.
func bridgeHostField(infra types.OrderedMap[any]) string {
	if v, ok := infra.Get("providesField"); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return defaultUpstreamHostField
}
