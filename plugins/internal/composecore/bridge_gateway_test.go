// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// bridgeGatewayFragment is the shape of the compose fragment the bridge's
// FeatureRecipe renders: the gateway service and nothing else. The generated
// stream config and the gateway's network membership are the engine's to add.
const bridgeGatewayFragment = "services:\n  aggrega-gw:\n    image: nginx:alpine\n"

// bridgeMarkerEdge builds one entry of the marker's `edges` list in the shape a
// forwarded edge actually arrives in: a map keyed by the edge's own field names,
// with the target's provides as string values (the projection carries them as
// strings). A field left empty is OMITTED rather than set empty, matching a
// projection that drops an absent boundary path.
func bridgeMarkerEdge(source, target, socket, srcNS, tgtNS string, provides map[string]string) map[string]any {
	e := map[string]any{"source": source, "target": target, "socket": socket}
	for k, v := range map[string]string{"source_namespace": srcNS, "target_namespace": tgtNS} {
		if v != "" {
			e[k] = v
		}
	}
	p := map[string]any{}
	for k, v := range provides {
		p[k] = v
	}
	e["provides"] = p
	return e
}

// bridgeMarkerFor assembles the marker the FeatureRecipe forwards. knobs are
// merged over the edges, so a test sets portBase / providesField by name.
func bridgeMarkerFor(edges []map[string]any, knobs map[string]any) map[string]any {
	list := make([]any, len(edges))
	for i, e := range edges {
		list[i] = e
	}
	m := map[string]any{"edges": list}
	for k, v := range knobs {
		m[k] = v
	}
	return m
}

// bridgeGatewayMerged is the deep-merge result the render core hands the bridge
// injection: the gateway fragment plus the marker the values mapping produced.
func bridgeGatewayMerged(marker map[string]any) map[string]any {
	return map[string]any{
		"services": map[string]any{
			bridgeGatewayService: map[string]any{"image": "nginx:alpine"},
		},
		bridgeMarkerKey: marker,
	}
}

// generatedGatewayConfig returns the inline content of the gateway's mounted
// config, failing the test when the injection produced no config at all.
func generatedGatewayConfig(t *testing.T, merged map[string]any) string {
	t.Helper()
	configs, ok := merged["configs"].(map[string]any)
	if !ok {
		t.Fatalf("no top-level configs block; merged = %v", merged)
	}
	entry, ok := configs[bridgeConfigName].(map[string]any)
	if !ok {
		t.Fatalf("no %q config entry; configs = %v", bridgeConfigName, configs)
	}
	content, ok := entry["content"].(string)
	if !ok {
		t.Fatalf("config entry carries no inline content; entry = %v", entry)
	}
	return content
}

// gatewayListenPortByUpstream reads the generated stream config and returns each
// server block's upstream host mapped to the port that block listens on. It
// parses the emitted directives rather than re-deriving anything, so it can be
// compared against the consumer-side rewrite without sharing an implementation
// with either half.
func gatewayListenPortByUpstream(t *testing.T, content string) map[string]int {
	t.Helper()
	out := map[string]int{}
	listen := 0
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "listen "):
			p, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(line, "listen "), ";"))
			if err != nil {
				t.Fatalf("unparseable listen directive %q: %v", line, err)
			}
			listen = p
		case strings.HasPrefix(line, "set "):
			fields := strings.Fields(strings.TrimSuffix(line, ";"))
			if len(fields) != 3 {
				t.Fatalf("unexpected set directive %q", line)
			}
			colon := strings.LastIndex(fields[2], ":")
			if colon < 0 {
				t.Fatalf("set directive carries no host:port upstream: %q", line)
			}
			out[fields[2][:colon]] = listen
		}
	}
	return out
}

// TestRenderSourcedCore_BridgeDeliversItsGeneratedConfigAsAMountedComposeConfig
// drives the whole documented delivery on the render path the provider guest
// runs. For a cross-network edge the engine generates the gateway's nginx stream
// config and delivers it as a top-level compose config with inline content,
// referenced from the gateway service and mounted where nginx loads it; the
// marker it consumed does not survive into the rendered fragment.
func TestRenderSourcedCore_BridgeDeliversItsGeneratedConfigAsAMountedComposeConfig(t *testing.T) {
	t.Parallel()
	const nodeID = "gateway"
	marker := bridgeMarkerFor([]map[string]any{
		bridgeMarkerEdge("web", "db", "backend", "frontend", "backend",
			map[string]string{"host": "db", "port": "5432"}),
	}, nil)

	out, err := RenderSourcedCore([]byte(bridgeGatewayFragment), "bridge-fragment.yaml",
		map[string]any{bridgeMarkerKey: marker}, nodeCtxFor(nodeID))
	if err != nil {
		t.Fatalf("RenderSourcedCore: %v", err)
	}
	body, ok := out[nodeID+".compose-fragment.yaml"]
	if !ok {
		t.Fatalf("no compose fragment emitted; got %d file(s)", len(out))
	}

	// The marker is an engine input, not output: it must not reach the rendered
	// compose document, where compose itself would carry it as an extension key.
	if strings.Contains(string(body), bridgeMarkerKey) {
		t.Errorf("the bridge marker survived into the rendered fragment:\n%s", body)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse emitted fragment: %v\n%s", err, body)
	}
	content := generatedGatewayConfig(t, doc)

	// The documented shape of the generated config: a stream block, a resolver so
	// each upstream resolves at connection time, and one server block per bridged
	// edge listening on the edge's assigned port and proxying through that
	// block's own variable.
	for _, want := range []string{
		"stream {",
		"resolver 127.0.0.11 valid=10s;",
		"listen 5432;",
		"set $$u0 db:5432;",
		"proxy_pass $$u0;",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("generated config is missing %q:\n%s", want, content)
		}
	}
	if n := strings.Count(content, "server {"); n != 1 {
		t.Errorf("got %d server block(s) for 1 bridged edge; want 1:\n%s", n, content)
	}
	// Compose interpolates `$` over the whole document, including inline config
	// content, so every nginx variable is emitted doubled and compose hands nginx
	// a single literal `$`. A bare `$` here would reach nginx blanked.
	if strings.Contains(strings.ReplaceAll(content, "$$", ""), "$") {
		t.Errorf("an unescaped $ would be consumed by compose interpolation:\n%s", content)
	}

	// The config reaches nginx as a mounted file rather than an authored command.
	svc, ok := doc["services"].(map[string]any)[bridgeGatewayService].(map[string]any)
	if !ok {
		t.Fatalf("no %q service in the rendered fragment; doc = %v", bridgeGatewayService, doc)
	}
	refs, ok := svc["configs"].([]any)
	if !ok || len(refs) != 1 {
		t.Fatalf("gateway does not reference exactly one config; got %v", svc["configs"])
	}
	ref, _ := refs[0].(map[string]any)
	if ref["source"] != bridgeConfigName || ref["target"] != bridgeConfigTarget {
		t.Errorf("config reference = %v; want source %q mounted at %q", ref, bridgeConfigName, bridgeConfigTarget)
	}
}

// TestInjectBridgeGateway_NoCrossNetworkEdgeYieldsNoGateway asserts the opt-in:
// the gateway exists only where a declared edge actually crosses a network seam.
// An attachment whose edges all land on one network gets no generated config, no
// config reference and no network list — nothing the author did not ask for —
// while the marker is still consumed.
func TestInjectBridgeGateway_NoCrossNetworkEdgeYieldsNoGateway(t *testing.T) {
	t.Parallel()
	merged := bridgeGatewayMerged(bridgeMarkerFor([]map[string]any{
		bridgeMarkerEdge("web", "db", "backend", "app", "app",
			map[string]string{"host": "db", "port": "5432"}),
	}, nil))

	if err := injectBridgeGateway(merged); err != nil {
		t.Fatalf("injectBridgeGateway: %v", err)
	}
	if _, present := merged[bridgeMarkerKey]; present {
		t.Errorf("the marker was not consumed: %v", merged[bridgeMarkerKey])
	}
	if _, present := merged["configs"]; present {
		t.Errorf("a gateway config was generated for a same-network edge: %v", merged["configs"])
	}
	svc := merged["services"].(map[string]any)[bridgeGatewayService].(map[string]any)
	for _, key := range []string{"configs", "networks"} {
		if _, present := svc[key]; present {
			t.Errorf("gateway gained %q with no bridged edge: %v", key, svc[key])
		}
	}
}

// TestInjectBridgeGateway_GatewayJoinsEveryNetworkItsBridgedEdgesSpan asserts
// the relay requirement: the gateway joins the consumer's network AND the
// target's for every bridged edge, so it can be reached from one side and proxy
// to the other. The names are resolved through the same derivation that places
// the components — a namespace carrying characters compose forbids appears in
// the gateway's list exactly as sanitised for the component — and the list is
// ordered so identical input renders identical bytes.
func TestInjectBridgeGateway_GatewayJoinsEveryNetworkItsBridgedEdgesSpan(t *testing.T) {
	t.Parallel()
	merged := bridgeGatewayMerged(bridgeMarkerFor([]map[string]any{
		bridgeMarkerEdge("web", "db", "backend", "Front End", "backend",
			map[string]string{"host": "db", "port": "5432"}),
		bridgeMarkerEdge("web", "queue", "events", "Front End", "messaging",
			map[string]string{"host": "queue", "port": "5672"}),
	}, nil))

	if err := injectBridgeGateway(merged); err != nil {
		t.Fatalf("injectBridgeGateway: %v", err)
	}
	svc := merged["services"].(map[string]any)[bridgeGatewayService].(map[string]any)
	nets, ok := svc["networks"].([]any)
	if !ok {
		t.Fatalf("gateway joined no networks; service = %v", svc)
	}
	got := make([]string, len(nets))
	for i, n := range nets {
		got[i], _ = n.(string)
	}
	// "Front End" resolves to the same sanitised name the component placement
	// uses; the three spanned networks appear once each, in sorted order.
	want := []string{"backend", "front_end", "messaging"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("gateway networks = %v; want %v", got, want)
	}
	if resolved := deriveNodeNetworkName("Front End", ""); resolved != "front_end" {
		t.Errorf("the gateway's network name %q is not what the component placement derives (%q)", "front_end", resolved)
	}
}

// TestInjectBridgeGateway_MalformedMarkerOrAbsentGatewayHalts asserts each way
// the injection can find its input unusable is a named halt rather than a
// partially-wired gateway. Each case carries exactly one fault.
func TestInjectBridgeGateway_MalformedMarkerOrAbsentGatewayHalts(t *testing.T) {
	t.Parallel()
	bridgedEdge := []map[string]any{
		bridgeMarkerEdge("web", "db", "backend", "frontend", "backend",
			map[string]string{"host": "db", "port": "5432"}),
	}
	for _, tc := range []struct {
		name    string
		merged  map[string]any
		wantErr string
	}{
		{
			name: "marker-is-not-a-mapping",
			merged: map[string]any{
				"services":      map[string]any{bridgeGatewayService: map[string]any{}},
				bridgeMarkerKey: "portBase=10000",
			},
			wantErr: "must be a mapping",
		},
		{
			name:    "fragment-declares-no-services",
			merged:  map[string]any{bridgeMarkerKey: bridgeMarkerFor(bridgedEdge, nil)},
			wantErr: "no services block",
		},
		{
			name: "gateway-service-absent-from-the-fragment",
			merged: map[string]any{
				"services":      map[string]any{"web": map[string]any{"image": "nginx"}},
				bridgeMarkerKey: bridgeMarkerFor(bridgedEdge, nil),
			},
			wantErr: "not present in the rendered fragment",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := injectBridgeGateway(tc.merged)
			if err == nil {
				t.Fatalf("expected a halt for %s; got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error does not state the fault %q: %v", tc.wantErr, err)
			}
			if _, present := tc.merged["configs"]; present {
				t.Errorf("a halt left a partially-wired gateway config behind: %v", tc.merged["configs"])
			}
		})
	}
}

// TestBridge_GatewayListenPortsAgreeWithTheConsumerRewrites is the cross-check
// on the claim that both halves of the bridge read one derivation: the port the
// gateway listens on for an edge is the port that edge's consumer is rewritten
// to, collisions included. The two halves are driven from their own real inputs
// — the forwarded marker for the gateway, the structured boundary edges for the
// consumer channel — and their outputs are compared, so neither side is checked
// against a re-derivation of itself.
func TestBridge_GatewayListenPortsAgreeWithTheConsumerRewrites(t *testing.T) {
	t.Parallel()
	// Two cross-network edges declaring the same target port: one keeps it, the
	// other is reassigned. Whichever way the rule resolves that, both halves must
	// resolve it the same way.
	const collidingPort = "5432"
	upstreamOfVar := map[string]string{"DB_HOST": "db", "CACHE_HOST": "cache"}

	merged := bridgeGatewayMerged(bridgeMarkerFor([]map[string]any{
		bridgeMarkerEdge("web", "db", "backend", "frontend", "backend",
			map[string]string{"host": "db", "port": collidingPort}),
		bridgeMarkerEdge("web", "cache", "caches", "frontend", "backend",
			map[string]string{"host": "cache", "port": collidingPort}),
	}, nil))
	if err := injectBridgeGateway(merged); err != nil {
		t.Fatalf("injectBridgeGateway: %v", err)
	}
	listenByUpstream := gatewayListenPortByUpstream(t, generatedGatewayConfig(t, merged))

	overrides, err := ComputeBridgeConsumerEnvOverrides([]types.EnrichedEdge{
		bridgeTestEdge("web", "db", "backend", "frontend", "backend", "db", collidingPort, "DB_HOST"),
		bridgeTestEdge("web", "cache", "caches", "frontend", "backend", "cache", collidingPort, "CACHE_HOST"),
	}, types.OrderedMap[any]{})
	if err != nil {
		t.Fatalf("ComputeBridgeConsumerEnvOverrides: %v", err)
	}

	if len(listenByUpstream) != len(upstreamOfVar) {
		t.Fatalf("gateway generated %d server block(s) for %d bridged edge(s): %v", len(listenByUpstream), len(upstreamOfVar), listenByUpstream)
	}
	for envVar, upstream := range upstreamOfVar {
		rewritten, present := overrides["web"][envVar]
		if !present {
			t.Errorf("consumer var %q was not rewritten; overrides = %v", envVar, overrides)
			continue
		}
		listen, present := listenByUpstream[upstream]
		if !present {
			t.Errorf("no gateway server block proxies to %q; blocks = %v", upstream, listenByUpstream)
			continue
		}
		if want := bridgeGatewayService + ":" + strconv.Itoa(listen); rewritten != want {
			t.Errorf("edge to %q: consumer reaches %q but the gateway listens on %d (expected %q)", upstream, rewritten, listen, want)
		}
	}
	// A collision really was forced, so the agreement above is not the trivial
	// case of both halves defaulting to the declared target port.
	if listenByUpstream["db"] == listenByUpstream["cache"] {
		t.Fatalf("both edges were assigned the same listen port, so no collision was exercised: %v", listenByUpstream)
	}
}

// TestInjectBridgeGateway_PortBaseKnobAcceptsEveryFormItCanArriveIn asserts the
// collision-fallback knob is honoured whichever numeric form it reaches the
// engine in — the knob travels through a template that renders it as a plain
// scalar and then through a projection, so an integer, a floating-point number,
// a decimal string and a deferred JSON number all describe the same base. A
// value that is not a number at all falls back to the built-in base rather than
// to a port the gateway cannot listen on.
func TestInjectBridgeGateway_PortBaseKnobAcceptsEveryFormItCanArriveIn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		portBase   any
		wantListen int
	}{
		{"plain-integer", 20000, 20000},
		{"floating-point-number", float64(20000), 20000},
		{"deferred-json-number", json.Number("20000"), 20000},
		{"decimal-string", "20000", 20000},
		{"absent", nil, defaultBridgePortBase},
		{"not-a-number", true, defaultBridgePortBase},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			knobs := map[string]any{}
			if tc.portBase != nil {
				knobs["portBase"] = tc.portBase
			}
			// Two edges declaring one port: the second is reassigned from the base.
			merged := bridgeGatewayMerged(bridgeMarkerFor([]map[string]any{
				bridgeMarkerEdge("web", "db", "backend", "frontend", "backend",
					map[string]string{"host": "db", "port": "5432"}),
				bridgeMarkerEdge("web", "cache", "caches", "frontend", "backend",
					map[string]string{"host": "cache", "port": "5432"}),
			}, knobs))
			if err := injectBridgeGateway(merged); err != nil {
				t.Fatalf("injectBridgeGateway: %v", err)
			}
			listenByUpstream := gatewayListenPortByUpstream(t, generatedGatewayConfig(t, merged))
			// "cache" sorts before "db", so "db" is the reassigned one.
			if got := listenByUpstream["db"]; got != tc.wantListen {
				t.Errorf("reassigned edge listens on %d; want %d (blocks = %v)", got, tc.wantListen, listenByUpstream)
			}
			if got := listenByUpstream["cache"]; got != 5432 {
				t.Errorf("uncollided edge listens on %d; want its declared target port 5432", got)
			}
		})
	}
}

// TestInjectBridgeGateway_ProvidesFieldKnobSelectsTheUpstreamHost asserts the
// knob that names WHICH of the target's provided fields is the address the
// gateway proxies to. The default is the plain host field; naming another field
// redirects the upstream to that field's value, which is what lets a target
// publish more than one address.
func TestInjectBridgeGateway_ProvidesFieldKnobSelectsTheUpstreamHost(t *testing.T) {
	t.Parallel()
	provides := map[string]string{"host": "db", "fqdn": "db.backend.internal", "port": "5432"}
	for _, tc := range []struct {
		name         string
		knobs        map[string]any
		wantUpstream string
	}{
		{"default-is-the-host-field", nil, "db"},
		{"knob-selects-another-published-field", map[string]any{"providesField": "fqdn"}, "db.backend.internal"},
		{"empty-knob-falls-back-to-the-host-field", map[string]any{"providesField": ""}, "db"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			merged := bridgeGatewayMerged(bridgeMarkerFor([]map[string]any{
				bridgeMarkerEdge("web", "db", "backend", "frontend", "backend", provides),
			}, tc.knobs))
			if err := injectBridgeGateway(merged); err != nil {
				t.Fatalf("injectBridgeGateway: %v", err)
			}
			listenByUpstream := gatewayListenPortByUpstream(t, generatedGatewayConfig(t, merged))
			if _, present := listenByUpstream[tc.wantUpstream]; !present {
				t.Errorf("no server block proxies to %q; blocks = %v", tc.wantUpstream, listenByUpstream)
			}
			if len(listenByUpstream) != 1 {
				t.Errorf("expected exactly one server block; got %v", listenByUpstream)
			}
		})
	}
}
