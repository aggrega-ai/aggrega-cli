// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmfixture"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These tests drive a DELIBERATELY non-conforming provider guest into the host's
// refusals of a plugin contract violation, and assert that each refusal names a
// condition whose code reaches the emitted diagnostic.
//
// No such condition is reachable from any authored document — an author decides
// which provider renders a node, never what that provider returns — so the guest
// is what makes the proof possible at all. Its source is at
// internal/wasm/testdata/nonconforming-provider, and each violation sits on its
// own export or on its own crossing of one, so the input the host hands over is
// the whole selection.

const nonconformingProvider = "nonconforming"

// nonconformingProviderEngine builds the non-conforming provider guest and wraps
// it in a real engine. The build has no skip path, for the reason
// EnsureLocalGuestBuilt records: a silently skipped proof of an emitted code
// cannot be told apart from a proof that passed.
func nonconformingProviderEngine(t *testing.T) renderer.Provider {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "..", "wasm", "testdata", "nonconforming-provider"))
	if err != nil {
		t.Fatalf("resolve guest source: %v", err)
	}
	path := wasmfixture.EnsureLocalGuestBuilt(t, src, "nonconforming-provider.wasm")
	guestBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read guest %s: %v", path, err)
	}
	uri := "file://" + path
	lookup := func(u string) (pluginpath.ResolvedPlugin, error) {
		return pluginpath.ResolvedPlugin{
			Bytes:  guestBytes,
			URI:    u,
			Tier:   pluginpath.TierLocal,
			Limits: &pluginpath.PluginRuntimeLimits{MaxMemoryPages: 4096},
		}, nil
	}
	return wasmengine.New(lookup, uri, wasm.NewCachingLoader(nil))
}

// assertProviderRefusalEmitsCode asserts the error names the expected condition,
// carries that condition's short code, and reaches the emitted text with the
// registered sentence in it. It logs the diagnostic so the proof is readable in
// the test output rather than only asserted.
func assertProviderRefusalEmitsCode(t *testing.T, err error, condition error, wantShort string) {
	t.Helper()
	if err == nil {
		t.Fatal("the non-conforming guest produced no halt; the host must refuse the payload it returned")
	}
	if !errors.Is(err, condition) {
		t.Fatalf("expected %v in the error chain; got: %v", condition, err)
	}
	gotShort, ok := cerrors.ShortCodeOf(err)
	if !ok {
		t.Fatalf("the halt carries no short code, so a reader has nothing to quote; got: %v", err)
	}
	if gotShort != wantShort {
		t.Fatalf("short code = %q, want %q", gotShort, wantShort)
	}
	sentence, ok := cerrors.MessageOf(err)
	if !ok {
		t.Fatalf("the halt carries no registered sentence; got: %v", err)
	}
	emitted := cerrors.CauseChain(err)
	if !strings.Contains(emitted, sentence) {
		t.Fatalf("the emitted diagnostic does not carry the registered sentence.\nsentence: %s\nemitted: %s",
			sentence, emitted)
	}
	t.Logf("EMITTED [%s] %s", gotShort, emitted)
}

// TestNonconformingProvider_DuplicateOutputPath drives the guest's render into the
// duplicate-output-path refusal: two returned files under one path, which leaves
// the host with two values for one key and no way to choose between them.
func TestNonconformingProvider_DuplicateOutputPath(t *testing.T) {
	engine := nonconformingProviderEngine(t)

	// A sourced render enumerates its source directory, so the node needs one.
	// Its contents are irrelevant to the violation: the guest ignores the
	// listing and returns the same duplicate either way.
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "input.yaml"), []byte("kind: input\n"), 0o644); err != nil {
		t.Fatalf("write source input: %v", err)
	}

	_, err := engine.Render(context.Background(), map[string]any{}, renderer.NodeRenderContext{
		NodeID:       "dup-node",
		Source:       srcDir,
		ProviderName: nonconformingProvider,
	})
	assertProviderRefusalEmitsCode(t, err, renderer.ErrProviderRenderDuplicatePath(), "E0269")
}

// TestNonconformingProvider_RenderFactNotAList drives the guest's synthesis into the
// refusal of a render fact whose value is not a list, once for every kind of value
// that is not one. The guest reads the node's id to choose the kind it reports, so
// every row crosses the same export through the same real boundary and differs only
// in that one value. The halt names the fact, the node and the guest, and says what
// the value was.
//
// The control row asks for the conforming answer — a list — from the same guest,
// so a refusal on the other rows is attributable to the value and nothing else; it
// must render and hand its file back.
func TestNonconformingProvider_RenderFactNotAList(t *testing.T) {
	engine := nonconformingProviderEngine(t)
	synth, ok := engine.(renderer.WorkloadSynthesizer)
	if !ok {
		t.Fatalf("the provider adapter does not synthesize workloads (%T), so no synthesis crosses the guest", engine)
	}
	workload := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
	container := types.ContainerSpec{Image: "app:1.0"}
	workload.Containers.Set("main", container)

	for _, tc := range []struct {
		nodeID   string
		wantKind string // empty for the conforming control
	}{
		{nodeID: "object", wantKind: "an object"},
		{nodeID: "string", wantKind: "a string"},
		{nodeID: "number", wantKind: "a number"},
		{nodeID: "boolean", wantKind: "a boolean"},
		{nodeID: "null", wantKind: "null"},
		{nodeID: "control-list"},
	} {
		t.Run(tc.nodeID, func(t *testing.T) {
			var reported [][]renderer.RenderFact
			nodeCtx := renderer.NodeRenderContext{
				NodeID:         tc.nodeID,
				BoundaryPath:   "default",
				ProviderName:   nonconformingProvider,
				RenderFactSink: func(f []renderer.RenderFact) { reported = append(reported, f) },
			}
			files, err := synth.SynthesizeWorkload(context.Background(), nil, workload, nodeCtx)

			if tc.wantKind == "" {
				if err != nil {
					t.Fatalf("a guest reporting a list was refused: %v", err)
				}
				if len(files) != 1 || len(reported) != 1 || len(reported[0]) != 1 {
					t.Fatalf("the conforming synthesis handed back %d file(s) and %d report(s), want 1 and 1", len(files), len(reported))
				}
				if got := string(reported[0][0].Value); got != `[{"app":"api"}]` {
					t.Errorf("the conforming fact's value = %s, want the guest's own list", got)
				}
				return
			}

			assertProviderRefusalEmitsCode(t, err, renderer.ErrProviderRenderFactNotList(), "E0325")
			for _, want := range []string{`fact "pod_labels"`, `node "` + tc.nodeID + `"`, `provider "file://`, "its value is " + tc.wantKind} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %s: %v", want, err)
				}
			}
			if len(reported) != 0 {
				t.Errorf("a refused report reached the node's sink: %+v", reported)
			}
		})
	}
}

// TestNonconformingProvider_LinkPatchNamesAbsentFile drives the guest's publish
// half of a cross-boundary link into the refusal of wiring that names a file the
// dispatch group did not emit.
//
// Both endpoints are served by the same guest and sit at distinct dispatch paths,
// which is what makes the edge cross-boundary and routes it to the link exports
// at all. The guest's resolve half returns no wiring, so the halt is attributable
// to the file the publish half named.
func TestNonconformingProvider_LinkPatchNamesAbsentFile(t *testing.T) {
	engine := nonconformingProviderEngine(t)
	factory := func(string) (renderer.Provider, error) { return engine, nil }

	const emitted = "boundary.yaml"
	nodes := map[string]renderer.LinkPassNode{
		"consumer": {
			Path: "app", Provider: nonconformingProvider, Engine: engine,
			Files: map[string][]byte{emitted: []byte("kind: boundary\n")},
		},
		"producer": {
			Path: "data", Provider: nonconformingProvider, Engine: engine,
			Files: map[string][]byte{emitted: []byte("kind: boundary\n")},
		},
	}
	edge := types.IREdge{Source: "consumer", Target: "producer", Socket: "db"}

	_, _, err := renderer.RunCrossBoundaryLinkPassForTest(
		context.Background(), []types.IREdge{edge}, nodes, nil, factory, 2)
	assertProviderRefusalEmitsCode(t, err, renderer.ErrLinkPatchFileNotEmitted(), "E0270")
}
