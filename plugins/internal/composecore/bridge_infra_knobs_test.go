// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// bridgeTestEdgeInjectingFrom builds a cross-network edge whose target publishes
// the given fields and whose consumer injects each named env var FROM the field
// paired with it — so a test can vary which published field a var is wired to.
func bridgeTestEdgeInjectingFrom(source, target, socket, srcNS, tgtNS string, publishes map[string]string, injects map[string]string) types.EnrichedEdge {
	provides := types.OrderedMap[string]{}
	// Set in a fixed order so the edge is byte-identical across runs.
	for _, k := range []string{"host", "fqdn", "port"} {
		if v, ok := publishes[k]; ok {
			provides.Set(k, v)
		}
	}
	env := types.OrderedMap[string]{}
	for _, k := range []string{"DB_HOST", "DB_FQDN"} {
		if from, ok := injects[k]; ok {
			env.Set(k, from)
		}
	}
	inj := types.OrderedMap[types.OrderedMap[string]]{}
	inj.Set("env", env)
	return types.EnrichedEdge{
		Source:          source,
		Target:          target,
		Socket:          socket,
		SourceNamespace: srcNS,
		TargetNamespace: tgtNS,
		Provides:        provides,
		Inject:          inj,
	}
}

// bridgeInfra builds the driving attachment's resolved infra from one key.
func bridgeInfra(key string, value any) types.OrderedMap[any] {
	infra := types.OrderedMap[any]{}
	if key != "" {
		infra.Set(key, value)
	}
	return infra
}

// TestComputeBridgeConsumerEnvOverrides_PortBaseKnobAcceptsEveryFormItCanArriveIn
// asserts the operator's collision-fallback knob is honoured on the consumer half
// of the bridge, in every form the resolved attachment configuration can carry a
// number. A value that is not a number falls back to the built-in base rather
// than rewriting a consumer to a port nothing listens on.
func TestComputeBridgeConsumerEnvOverrides_PortBaseKnobAcceptsEveryFormItCanArriveIn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		portBase     any
		wantFallback int
	}{
		{"plain-integer", 20000, 20000},
		{"64-bit-integer", int64(20000), 20000},
		{"floating-point-number", float64(20000), 20000},
		{"decimal-string", "20000", 20000},
		{"deferred-json-number", json.Number("20000"), 20000},
		{"not-a-number", []string{"20000"}, defaultBridgePortBase},
		{"unparseable-string", "twenty-thousand", defaultBridgePortBase},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Two cross-network edges declaring one port: the later in stable key
			// order is reassigned from the base.
			edges := []types.EnrichedEdge{
				bridgeTestEdge("web", "db", "backend", "frontend", "backend", "db", "5432", "DB_HOST"),
				bridgeTestEdge("web", "cache", "caches", "frontend", "backend", "cache", "5432", "CACHE_HOST"),
			}
			got, err := ComputeBridgeConsumerEnvOverrides(edges, bridgeInfra("portBase", tc.portBase))
			if err != nil {
				t.Fatalf("ComputeBridgeConsumerEnvOverrides: %v", err)
			}
			want := map[string]map[string]string{"web": {
				"CACHE_HOST": "aggrega-gw:5432",
				"DB_HOST":    "aggrega-gw:" + strconv.Itoa(tc.wantFallback),
			}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("overrides = %v; want %v", got, want)
			}
		})
	}
}

// TestComputeBridgeConsumerEnvOverrides_ProvidesFieldKnobSelectsWhichVarsAreRewritten
// asserts what the upstream-host selector actually decides on the consumer half:
// the vars rewritten to the gateway are exactly those injected FROM the selected
// field. A target publishing two addresses therefore has one of its consumer's
// vars redirected through the gateway and the other left alone — changing the
// knob moves the rewrite from one var to the other rather than adding to it.
func TestComputeBridgeConsumerEnvOverrides_ProvidesFieldKnobSelectsWhichVarsAreRewritten(t *testing.T) {
	t.Parallel()
	publishes := map[string]string{"host": "db", "fqdn": "db.backend.internal", "port": "5432"}
	injects := map[string]string{"DB_HOST": "host", "DB_FQDN": "fqdn"}
	for _, tc := range []struct {
		name  string
		knob  any
		unset bool
		want  map[string]map[string]string
	}{
		{
			name:  "default-rewrites-the-var-injected-from-the-host-field",
			unset: true,
			want:  map[string]map[string]string{"web": {"DB_HOST": "aggrega-gw:5432"}},
		},
		{
			name: "selecting-another-field-moves-the-rewrite-to-its-var",
			knob: "fqdn",
			want: map[string]map[string]string{"web": {"DB_FQDN": "aggrega-gw:5432"}},
		},
		{
			name: "an-empty-selector-falls-back-to-the-host-field",
			knob: "",
			want: map[string]map[string]string{"web": {"DB_HOST": "aggrega-gw:5432"}},
		},
		{
			name: "a-non-string-selector-falls-back-to-the-host-field",
			knob: 7,
			want: map[string]map[string]string{"web": {"DB_HOST": "aggrega-gw:5432"}},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			infra := types.OrderedMap[any]{}
			if !tc.unset {
				infra.Set("providesField", tc.knob)
			}
			edges := []types.EnrichedEdge{
				bridgeTestEdgeInjectingFrom("web", "db", "backend", "frontend", "backend", publishes, injects),
			}
			got, err := ComputeBridgeConsumerEnvOverrides(edges, infra)
			if err != nil {
				t.Fatalf("ComputeBridgeConsumerEnvOverrides: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("overrides = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestComputeBridgeConsumerEnvOverrides_NoVarWiredToTheSelectedFieldYieldsNoRewrite
// asserts the empty result is nil rather than an empty consumer entry: a bridged
// edge whose consumer injects nothing from the selected field has nothing to
// redirect, and an empty map would still read as "this consumer was rewritten".
func TestComputeBridgeConsumerEnvOverrides_NoVarWiredToTheSelectedFieldYieldsNoRewrite(t *testing.T) {
	t.Parallel()
	edges := []types.EnrichedEdge{
		bridgeTestEdgeInjectingFrom("web", "db", "backend", "frontend", "backend",
			map[string]string{"host": "db", "fqdn": "db.backend.internal", "port": "5432"},
			map[string]string{"DB_HOST": "host"}),
	}
	got, err := ComputeBridgeConsumerEnvOverrides(edges, bridgeInfra("providesField", "fqdn"))
	if err != nil {
		t.Fatalf("ComputeBridgeConsumerEnvOverrides: %v", err)
	}
	if got != nil {
		t.Errorf("expected no overrides when no var is injected from the selected field; got %v", got)
	}
}
