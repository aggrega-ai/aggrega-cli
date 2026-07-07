// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// envOverrideStubEngine is a test-only provider engine implementing the generic
// BoundaryEnvOverrider channel. It returns a fixed per-consumer override map,
// proving the channel carries opaque string values with zero domain specifics
// (no compose, no ports, no gateway).
type envOverrideStubEngine struct {
	overrides map[string]map[string]string
}

func (e envOverrideStubEngine) Render(context.Context, map[string]any, NodeRenderContext) (map[string][]byte, error) {
	return nil, nil
}

func (e envOverrideStubEngine) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (e envOverrideStubEngine) BoundaryEnvOverrides(context.Context, []types.EnrichedEdge, types.OrderedMap[any]) (map[string]map[string]string, error) {
	return e.overrides, nil
}

// TestApplyConsumerEnvOverrides_NonDisturbance exercises the apply-site contract:
// the override is a targeted last-wins set on exactly its keys, leaving every
// other Env key, the File/Arg pipes, and the key order byte-identical, and it
// rebuilds .All. (Obligations 1-4 from the channel design.)
func TestApplyConsumerEnvOverrides_NonDisturbance(t *testing.T) {
	t.Parallel()

	inj := &types.InjectPipe{}
	inj.Env.Set("ALPHA", "from-other-feature") // another feature's injectMapping output
	inj.Env.Set("FOO_ADDR", "svc-x:original")  // a uses-injected target, pre-override
	inj.File.Set("conf", "/etc/app/conf")
	inj.Arg.Set("flag", "--verbose")
	inj.All = buildInjectAll(*inj)
	fileBefore := inj.File
	argBefore := inj.Arg

	applyConsumerEnvOverrides(inj, map[string]string{"FOO_ADDR": "svc-x:1234"})

	// Obligation 2: the named key is overridden; the other feature's key is not.
	if v, _ := inj.Env.Get("FOO_ADDR"); v != "svc-x:1234" {
		t.Errorf("FOO_ADDR = %q; want svc-x:1234 (override not applied)", v)
	}
	if v, _ := inj.Env.Get("ALPHA"); v != "from-other-feature" {
		t.Errorf("ALPHA = %q; want from-other-feature (other feature's key disturbed)", v)
	}
	// Order preserved (FOO_ADDR updated in place, no reordering / no new key).
	if got := inj.Env.Keys(); !reflect.DeepEqual(got, []string{"ALPHA", "FOO_ADDR"}) {
		t.Errorf("Env key order = %v; want [ALPHA FOO_ADDR] (order disturbed)", got)
	}
	// Obligation 3: File / Arg untouched.
	if !reflect.DeepEqual(inj.File, fileBefore) {
		t.Errorf("File pipe disturbed: %+v", inj.File)
	}
	if !reflect.DeepEqual(inj.Arg, argBefore) {
		t.Errorf("Arg pipe disturbed: %+v", inj.Arg)
	}
	// .All rebuilt: consistent with the post-override pipe and carrying the new value.
	if !reflect.DeepEqual(inj.All, buildInjectAll(*inj)) {
		t.Errorf(".All not rebuilt to match the post-override pipe")
	}
	if v, _ := inj.All.Get("FOO_ADDR"); v != "svc-x:1234" {
		t.Errorf(".All[FOO_ADDR] = %q; want svc-x:1234 (stale .All)", v)
	}
}

// TestApplyConsumerEnvOverrides_NilIsNoOp pins obligation 1's mechanism: a
// consumer with no override entry (nil/empty map) is left byte-identical — the
// orchestrator gates the apply on map membership, so non-named consumers never
// change.
func TestApplyConsumerEnvOverrides_NilIsNoOp(t *testing.T) {
	t.Parallel()
	inj := &types.InjectPipe{}
	inj.Env.Set("ALPHA", "x")
	inj.All = buildInjectAll(*inj)
	before := *inj
	applyConsumerEnvOverrides(inj, nil)
	if !reflect.DeepEqual(*inj, before) {
		t.Errorf("nil override disturbed the pipe: %+v", *inj)
	}
}

// TestBoundaryConsumerEnvChannel_PopulatesViaEngineHook proves the generic
// contract: a boundary-scope FR whose provider engine implements
// BoundaryEnvOverrider has its returned map stored in
// state.ConsumerEnvOverrides during the pre-loop, keyed by the producer's
// consumer-nodeID; a non-named consumer gets no entry. No compose specifics.
func TestBoundaryConsumerEnvChannel_PopulatesViaEngineHook(t *testing.T) {
	t.Parallel()
	fr := boundaryScopeProvidesFR()
	lookup := dbProvidesLookup(fr)

	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})
	infra := types.OrderedMap[any]{}
	infra.Set("host", "db.internal")
	inner := types.OrderedMap[types.IRBoundaryFeature]{}
	inner.Set("db", types.IRBoundaryFeature{Type: "core/db:1.0", Infra: infra})
	graph.Spec.BoundaryFeatures.Set("cluster", inner)

	release := &types.Release{}
	release.Spec.Boundaries.Set("cluster", types.Boundary{Description: "cluster"})

	state := NewBoundaryFRState()
	factory := func(string) (Engine, error) {
		return envOverrideStubEngine{overrides: map[string]map[string]string{
			"api": {"FOO_ADDR": "svc-x:1234"},
		}}, nil
	}
	if err := PrepopulateBoundaryScopeProvides(graph, release, nil, lookup, nil, factory, state); err != nil {
		t.Fatalf("PrepopulateBoundaryScopeProvides: %v", err)
	}

	got, ok := state.ConsumerEnvOverrides["api"]
	if !ok || got["FOO_ADDR"] != "svc-x:1234" {
		t.Errorf("ConsumerEnvOverrides[api] = %v (ok=%v); want {FOO_ADDR: svc-x:1234}", got, ok)
	}
	if _, ok := state.ConsumerEnvOverrides["other"]; ok {
		t.Error("non-named consumer 'other' must have no override entry")
	}
}
