// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// bridgeMarkerKey is the compose extension key the compose-bridge FeatureRecipe's
// valuesMapping forwards: it carries the boundary's edges (as toJson-projected
// data) plus the portBase/providesField knobs. The engine consumes it to
// generate the gateway's stream config and strips it from the output.
const bridgeMarkerKey = "x-aggrega-bridge"

// bridgeConfigName is the top-level compose config that carries the gateway's
// generated nginx stream config; bridgeConfigTarget is where it mounts inside
// the gateway container (nginx:alpine's default CMD loads this path).
const (
	bridgeConfigName   = "aggrega-gw-nginx"
	bridgeConfigTarget = "/etc/nginx/nginx.conf"
)

// injectBridgeGateway consumes the bridge marker on a rendered compose fragment,
// generates the nginx stream config for the bridged (cross-network) edges, and
// delivers it as a mounted compose config: a top-level `configs:` entry with
// inline content, referenced from the aggrega-gw service's `configs:` so it
// mounts at /etc/nginx/nginx.conf. The marker is stripped. No-op when absent
// (every non-bridge render). It shares resolveBridge with the per-consumer
// env-override producer, so the gateway's `listen` ports match the consumers'
// rewritten ports per edge — including portBase-reassigned collisions — by
// construction.
func injectBridgeGateway(merged map[string]any) error {
	raw, ok := merged[bridgeMarkerKey]
	if !ok {
		return nil
	}
	delete(merged, bridgeMarkerKey)
	marker, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("compose bridge: %s must be a mapping, got %T", bridgeMarkerKey, raw)
	}

	hostField := defaultUpstreamHostField
	if s, ok := marker["providesField"].(string); ok && s != "" {
		hostField = s
	}
	portBase := defaultBridgePortBase
	if pb, ok := bridgeMarkerInt(marker["portBase"]); ok {
		portBase = pb
	}

	edgesRaw, _ := marker["edges"].([]any)
	specs := make([]bridgeSpec, 0, len(edgesRaw))
	for _, er := range edgesRaw {
		if em, ok := er.(map[string]any); ok {
			specs = append(specs, bridgeSpecFromMap(em, hostField))
		}
	}
	resolved := resolveBridge(specs, portBase)
	if len(resolved) == 0 {
		// Opt-in: no cross-network edge -> no gateway config (Invariant 4).
		return nil
	}

	conf := renderStreamConfig(resolved)
	svc, err := bridgeGatewayServiceMap(merged)
	if err != nil {
		return err
	}
	// Deliver the generated config as a top-level compose config with inline
	// content, mounted into the gateway as a readable file. nginx:alpine's
	// default CMD loads /etc/nginx/nginx.conf, so no command override is needed.
	// Inline content requires Docker Compose >= 2.23.1.
	//
	// Docker Compose runs $-interpolation over the whole compose file, including
	// configs.content — so an nginx `$uN` stream variable would be read as an
	// (unset) compose variable and substituted blank, corrupting the mounted
	// config. Escape every `$` as `$$` so Compose emits a literal `$` and nginx
	// receives its variables intact.
	configsBlock, _ := merged["configs"].(map[string]any)
	if configsBlock == nil {
		configsBlock = map[string]any{}
		merged["configs"] = configsBlock
	}
	configsBlock[bridgeConfigName] = map[string]any{"content": strings.ReplaceAll(conf, "$", "$$")}
	svc["configs"] = []any{
		map[string]any{"source": bridgeConfigName, "target": bridgeConfigTarget},
	}

	// Attach the gateway to every network its bridged edges span, so it can
	// reach both the consumer (who resolves aggrega-gw:<port>) and the upstream
	// (proxy_pass <target>). The network names resolve through the SAME
	// deriveNodeNetworkName the assembler uses to place the components, so they
	// match the components' actual networks — without this the gateway has two
	// networks that don't relay. Sorted for byte-deterministic output; the
	// assembler appends the gateway's own network on top (appendUnique).
	netSet := map[string]bool{}
	for _, r := range resolved {
		netSet[deriveNodeNetworkName(r.Spec.SrcNS, r.Spec.SrcPath)] = true
		netSet[deriveNodeNetworkName(r.Spec.TgtNS, r.Spec.TgtPath)] = true
	}
	netNames := make([]string, 0, len(netSet))
	for n := range netSet {
		netNames = append(netNames, n)
	}
	sort.Strings(netNames)
	nets := make([]any, len(netNames))
	for i, n := range netNames {
		nets[i] = n
	}
	svc["networks"] = nets
	return nil
}

// renderStreamConfig builds the nginx stream config: one server block per
// bridged edge (deterministic order, from resolveBridge), each listening on the
// assigned port and proxying to the edge-derived upstream host:port. The
// upstream is a compile-time compose service pointer; compose DNS resolves it at
// runtime (Invariant 2).
//
// A `resolver` directive plus per-server `set $uN <host>:<port>; proxy_pass
// $uN;` defers upstream resolution to connection time. A bare `proxy_pass
// <host>` resolves once at startup, so the gateway would die ("host not found
// in upstream") when it starts before the target — the variable form routes
// each connection through the resolver instead. 127.0.0.11 is Docker's embedded
// DNS. Each server's variable name is unique and edge-ordered for determinism.
func renderStreamConfig(resolved []bridgeResolved) string {
	var b strings.Builder
	b.WriteString("events {}\n")
	b.WriteString("stream {\n")
	b.WriteString("  resolver 127.0.0.11 valid=10s;\n")
	for i, r := range resolved {
		b.WriteString("  server {\n")
		fmt.Fprintf(&b, "    listen %d;\n", r.ListenPort)
		fmt.Fprintf(&b, "    set $u%d %s:%d;\n", i, r.Spec.UpstreamHost, r.Spec.TargetPort)
		fmt.Fprintf(&b, "    proxy_pass $u%d;\n", i)
		b.WriteString("  }\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func bridgeGatewayServiceMap(merged map[string]any) (map[string]any, error) {
	services, ok := merged["services"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("compose bridge: no services block to attach the gateway command to")
	}
	svc, ok := services[bridgeGatewayService].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("compose bridge: gateway service %q not present in the rendered fragment", bridgeGatewayService)
	}
	return svc, nil
}

// bridgeSpecFromMap normalizes a toJson-round-tripped edge (a map[string]any
// keyed by the EnrichedEdge json tags) into a bridgeSpec. It reads upstream
// host/port by field NAME — never by iterating the round-tripped maps, whose
// intra-map order the JSON detour does not preserve.
func bridgeSpecFromMap(m map[string]any, hostField string) bridgeSpec {
	s := bridgeSpec{
		Source:  bridgeStr(m["source"]),
		Target:  bridgeStr(m["target"]),
		Socket:  bridgeStr(m["socket"]),
		SrcNS:   bridgeStr(m["source_namespace"]),
		SrcPath: bridgeStr(m["source_boundary_path"]),
		TgtNS:   bridgeStr(m["target_namespace"]),
		TgtPath: bridgeStr(m["target_boundary_path"]),
	}
	if prov, ok := m["provides"].(map[string]any); ok {
		s.UpstreamHost = bridgeStr(prov[hostField])
		s.TargetPort, _ = strconv.Atoi(bridgeStr(prov["port"]))
	}
	return s
}

func bridgeStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func bridgeMarkerInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		if p, err := strconv.Atoi(n); err == nil {
			return p, true
		}
	case interface{ Int64() (int64, error) }: // json.Number
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}
