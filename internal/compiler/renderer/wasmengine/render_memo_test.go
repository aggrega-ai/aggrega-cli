// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine_test

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// nonReusableGuestLookup builds a testdata render fixture whose describe() omits
// the capabilities block — so it declares no reuse claim — and returns a lookup
// resolving its bytes plus the URI to address it by. It asserts, through a
// throwaway describe, that the fixture is genuinely non-reusable, so the
// fresh-per-node guard rests on a verified premise rather than an assumption. The
// fixture builds on demand; a missing toolchain skips the test cleanly.
func nonReusableGuestLookup(t *testing.T) (pluginpath.Lookup, string) {
	t.Helper()
	path := wasmtest.EnsureFixtureBuilt(t, "render-node-rust.wasm")
	guestBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read render fixture %q: %v", path, err)
	}
	const uri = "oci://testdata/render-node-rust"
	lookup := pluginpath.Lookup(func(string) (pluginpath.ResolvedPlugin, error) {
		return pluginpath.ResolvedPlugin{
			Bytes:  guestBytes,
			URI:    uri,
			Tier:   pluginpath.TierEmbed,
			Limits: &pluginpath.PluginRuntimeLimits{MaxMemoryPages: 4096},
		}, nil
	})

	probe := wasm.NewCachingLoader(nil)
	p, desc, err := probe.LoadAndDescribe(context.Background(), resolver.PluginSpec(uri, mustResolve(t, lookup, uri)))
	if err != nil {
		t.Fatalf("describe render fixture: %v", err)
	}
	_ = p.Close(context.Background())
	if desc.Contract != nil && desc.Contract.ReusableInstance {
		t.Fatalf("render-node-rust fixture unexpectedly declares reusable_instance; the fresh-per-node guard needs a guest that does not")
	}
	return lookup, uri
}

// mustResolve resolves uri through lookup, failing the test on error. It keeps the
// nonReusableGuestLookup probe a one-liner.
func mustResolve(t *testing.T, lookup pluginpath.Lookup, uri string) pluginpath.ResolvedPlugin {
	t.Helper()
	resolved, err := lookup(uri)
	if err != nil {
		t.Fatalf("resolve %q: %v", uri, err)
	}
	return resolved
}

// minimalSynthWorkload builds a single-container Workload exposing one ClusterIP
// service — the smallest end-to-end synthesis case, enough to drive the
// helm-provider guest's render_node export over the workload projection.
func minimalSynthWorkload() *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", types.PortSpec{Port: 8080})
	w.Containers.Set("main", c)
	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)
	return w
}

// synthNodeCtx returns a minimal NodeRenderContext for a workload-synthesis node.
func synthNodeCtx(id string) renderer.NodeRenderContext {
	return renderer.NodeRenderContext{
		NodeID:       id,
		BoundaryPath: "default",
		Provenance: renderer.ProvenanceContext{
			CompilerVersion:  "test",
			AggregateName:    "test-agg",
			ReleaseName:      "test-rel",
			Environment:      "test-env",
			AggregateVersion: "1.0.0",
		},
	}
}

// TestRenderPathDescribesOncePerProvider proves the render path memoizes the
// guest descriptor: across many per-node crossings of one provider, the describe
// gate (describe() call, descriptor validation, load-gate cross-checks) runs
// exactly once, not once per node. It counts the loader's describe-gate firings
// through OnDescribe while driving several workload-synthesis crossings through a
// single Engine. Before the render path read the memo, each crossing ran the full
// describe gate, so this count would equal the crossing count; the memo drops it
// to one per provider.
func TestRenderPathDescribesOncePerProvider(t *testing.T) {
	var loads, describes int64
	loader := wasm.NewCachingLoader(nil)
	loader.OnLoad = func(string) { atomic.AddInt64(&loads, 1) }
	loader.OnDescribe = func(string) { atomic.AddInt64(&describes, 1) }

	helmURI := embeddedGuestURI(t, "helm")
	e := wasmengine.New(embeddedGuestLookup, helmURI, loader)

	const nodes = 5
	for i := 0; i < nodes; i++ {
		if _, err := e.SynthesizeWorkload(context.Background(), nil, minimalSynthWorkload(), synthNodeCtx("node")); err != nil {
			t.Fatalf("SynthesizeWorkload node %d: %v", i, err)
		}
	}

	if got := atomic.LoadInt64(&describes); got != 1 {
		t.Errorf("describe gate ran %d times across %d nodes; want exactly 1 (describe once per provider)", got, nodes)
	}
	// The helm-provider guest declares reusable_instance, so the render path loads
	// twice regardless of node count: one load backing the one-time describe gate,
	// and one reused instance every crossing shares.
	if got := atomic.LoadInt64(&loads); got != 2 {
		t.Errorf("plugin loaded %d times across %d nodes; want 2 (the one describe-gate load plus the single reused instance)", got, nodes)
	}
}

// TestRenderPathReusesInstanceForReusableGuest proves the render path reuses one
// plugin instance across a build's crossings for a guest that declares
// reusable_instance. It pre-warms the descriptor memo (so the one-time describe
// gate is separated out), then asserts that the FIRST further crossing loads the
// one reused instance and every crossing after it loads nothing — the load count
// tracks the number of providers, not the number of nodes. The isolation default
// (fresh instance per node) is proven separately for a guest that makes no reuse
// claim by TestRenderPathLoadsFreshInstancePerNodeForNonReusableGuest.
func TestRenderPathReusesInstanceForReusableGuest(t *testing.T) {
	var loads, describes int64
	loader := wasm.NewCachingLoader(nil)
	loader.OnLoad = func(string) { atomic.AddInt64(&loads, 1) }
	loader.OnDescribe = func(string) { atomic.AddInt64(&describes, 1) }

	helmURI := embeddedGuestURI(t, "helm")
	e := wasmengine.New(embeddedGuestLookup, helmURI, loader)

	// Warm the descriptor memo through the same path the input-kind gate uses,
	// so the one-time describe gate is not counted against the per-node loads.
	if caps := e.Contract(); !caps.Synthesized {
		t.Fatalf("helm-provider guest does not report the synthesized input kind: %+v", caps)
	}
	if got := atomic.LoadInt64(&describes); got != 1 {
		t.Fatalf("descriptor warm ran the describe gate %d times; want 1", got)
	}
	atomic.StoreInt64(&loads, 0)
	atomic.StoreInt64(&describes, 0)

	const nodes = 4
	for i := 0; i < nodes; i++ {
		if _, err := e.SynthesizeWorkload(context.Background(), nil, minimalSynthWorkload(), synthNodeCtx("node")); err != nil {
			t.Fatalf("SynthesizeWorkload node %d: %v", i, err)
		}
	}

	if got := atomic.LoadInt64(&loads); got != 1 {
		t.Errorf("render path loaded %d instances for %d nodes; want 1 (one reused instance for the whole build)", got, nodes)
	}
	if got := atomic.LoadInt64(&describes); got != 0 {
		t.Errorf("render path ran the describe gate %d times after warm; want 0 (descriptor is memoized)", got)
	}
	// The reused instance is released at build end.
	if err := e.Close(context.Background()); err != nil {
		t.Fatalf("Close reused instance: %v", err)
	}
}

// TestRenderPathLoadsFreshInstancePerNodeForNonReusableGuest is the isolation
// guard for a guest that does NOT declare reusable_instance: each render-path
// crossing loads and closes its own plugin instance, so no guest state carries
// across nodes by construction. It drives a testdata render fixture whose
// describe() omits the reuse claim, pre-warms the descriptor memo, then asserts
// that N further crossings produce exactly N fresh loads. A regression that reused
// an instance for a guest that made no reuse claim — trading the isolation default
// for reuse — would drop the load count below N and trip this test.
func TestRenderPathLoadsFreshInstancePerNodeForNonReusableGuest(t *testing.T) {
	lookup, uri := nonReusableGuestLookup(t)

	var loads, describes int64
	loader := wasm.NewCachingLoader(nil)
	loader.OnLoad = func(string) { atomic.AddInt64(&loads, 1) }
	loader.OnDescribe = func(string) { atomic.AddInt64(&describes, 1) }

	e := wasmengine.New(lookup, uri, loader)

	// Warm the descriptor memo so the one-time describe gate is not counted against
	// the per-node loads. The fixture declares no capabilities block (the helper
	// asserts it declares no reuse claim), so the render path takes the
	// fresh-instance-per-crossing default.
	_ = e.Contract()
	if got := atomic.LoadInt64(&describes); got != 1 {
		t.Fatalf("descriptor warm ran the describe gate %d times; want 1", got)
	}
	atomic.StoreInt64(&loads, 0)
	atomic.StoreInt64(&describes, 0)

	const nodes = 4
	for i := 0; i < nodes; i++ {
		if _, err := e.SynthesizeWorkload(context.Background(), nil, minimalSynthWorkload(), synthNodeCtx("node")); err != nil {
			t.Fatalf("SynthesizeWorkload node %d: %v", i, err)
		}
	}

	if got := atomic.LoadInt64(&loads); got != nodes {
		t.Errorf("render path loaded %d instances for %d nodes; want %d (one fresh instance per node)", got, nodes, nodes)
	}
	if got := atomic.LoadInt64(&describes); got != 0 {
		t.Errorf("render path ran the describe gate %d times after warm; want 0 (descriptor is memoized)", got)
	}
}

// benchSynthEngine constructs a helm-provider Engine over a warm shared loader for
// the per-node cost benchmarks; the shared compilation cache is warmed once so the
// benchmarks measure instantiation and invocation, not one-time compilation.
func benchSynthEngine(b *testing.B) (*wasmengine.Engine, wasm.PluginSpec, *wasm.Loader) {
	b.Helper()
	loader := wasm.NewCachingLoader(nil)
	helmURI := ""
	for _, uri := range embedded.URIs() {
		if strings.HasSuffix(uri, "/helm") {
			helmURI = uri
			break
		}
	}
	if helmURI == "" {
		b.Fatal("no embedded helm-provider guest URI")
	}
	resolved, err := embeddedGuestLookup(helmURI)
	if err != nil {
		b.Fatalf("resolve embedded helm-provider: %v", err)
	}
	spec := resolver.PluginSpec(helmURI, resolved)

	// Warm the compilation cache so timed iterations measure instantiate/invoke,
	// not the guest's one-time compilation (which the disk-backed cache amortizes).
	p, _, err := loader.LoadAndDescribe(context.Background(), spec)
	if err != nil {
		b.Fatalf("warm load: %v", err)
	}
	_ = p.Close(context.Background())

	e := wasmengine.New(embeddedGuestLookup, helmURI, loader)
	return e, spec, loader
}

// BenchmarkPerNode_LoadAndDescribe_Close measures the pre-memo per-node crossing
// overhead: a full LoadAndDescribe (instantiate + describe() + validation +
// cross-checks) plus Close, once per node. The delta against
// BenchmarkPerNode_Load_Close is the describe-gate cost the descriptor memo saves
// per node.
func BenchmarkPerNode_LoadAndDescribe_Close(b *testing.B) {
	_, spec, loader := benchSynthEngine(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p, _, err := loader.LoadAndDescribe(ctx, spec)
		if err != nil {
			b.Fatalf("LoadAndDescribe: %v", err)
		}
		if err := p.Close(ctx); err != nil {
			b.Fatalf("Close: %v", err)
		}
	}
}

// BenchmarkPerNode_Load_Close measures the post-memo per-node instantiation cost:
// a plain Load (fresh, isolated instance; no describe) plus Close, once per node.
// This is the residual per-node cost after the descriptor memo — the cost a
// hypothetical instance-reuse model would eliminate by loading once per provider.
func BenchmarkPerNode_Load_Close(b *testing.B) {
	_, spec, loader := benchSynthEngine(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p, err := loader.Load(ctx, spec)
		if err != nil {
			b.Fatalf("Load: %v", err)
		}
		if err := p.Close(ctx); err != nil {
			b.Fatalf("Close: %v", err)
		}
	}
}

// BenchmarkPerNode_SynthesizeWorkload measures the full post-memo per-node render
// crossing: memoized-descriptor read, fresh Load, render_node invocation over the
// workload projection, decode, and Close. Subtracting BenchmarkPerNode_Load_Close
// isolates the render_node invocation itself — the irreducible work every model
// (fresh-per-node or reuse) pays once per node. Comparing that invocation cost
// against the Load_Close instantiation cost answers whether instantiation is the
// per-node hot path.
func BenchmarkPerNode_SynthesizeWorkload(b *testing.B) {
	e, _, _ := benchSynthEngine(b)
	ctx := context.Background()
	w := minimalSynthWorkload()
	nodeCtx := synthNodeCtx("bench-node")
	// Warm the descriptor memo before timing so the first iteration is not charged
	// the one-time describe gate.
	if _, err := e.SynthesizeWorkload(ctx, nil, w, nodeCtx); err != nil {
		b.Fatalf("warm synth: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.SynthesizeWorkload(ctx, nil, w, nodeCtx); err != nil {
			b.Fatalf("SynthesizeWorkload: %v", err)
		}
	}
}
