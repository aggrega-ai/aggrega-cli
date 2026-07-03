// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package abtest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
)

// buildRenderInput projects the chart at chartDir and marshals a render_node
// payload carrying nodeID and the given values, returning the payload
// alongside the equivalent plain values map for the oracle render. The
// values decode through the same ordered-map JSON decode the guest performs
// off the wire, so both sides of the A/B receive the same value shape.
func buildRenderInput(t *testing.T, chartDir, nodeID, valuesJSON string) (payload []byte, plainValues map[string]any) {
	t.Helper()
	valuesOrdered := valuesFromJSON(t, valuesJSON)
	proj, err := helm.ProjectChartSource(chartDir)
	if err != nil {
		t.Fatalf("ProjectChartSource: %v", err)
	}
	input := renderer.RenderNodeInput{
		Node:             renderer.IRNodeProjection{ID: nodeID, Values: valuesOrdered},
		Context:          renderctx.RenderContext{},
		SourceProjection: proj,
	}
	b, err := input.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return b, orderedToPlain(&valuesOrdered)
}

// decodeCandidate decodes a render_node output payload into the
// path-to-bytes map the production host collector produces.
func decodeCandidate(t *testing.T, raw []byte) map[string][]byte {
	t.Helper()
	var out renderer.RenderNodeOutput
	if err := out.UnmarshalJSON(raw); err != nil {
		t.Fatalf("decode RenderNodeOutput: %v (raw: %s)", err, raw)
	}
	candidate := make(map[string][]byte, len(out.Files))
	for _, f := range out.Files {
		candidate[f.Path] = f.Content
	}
	return candidate
}

// TestHelmProviderABByteIdentitySchemaChart proves a schema-bearing chart —
// a values.schema.json with no remote references — renders byte-identical
// guest-vs-oracle through the production loader. The oracle validates the
// values through the Helm SDK's own validator; the guest validates them
// through the hermetic validator against the projected chart files. Because
// validation only gates and never transforms, accepted values render to the
// exact same bytes on both sides.
func TestHelmProviderABByteIdentitySchemaChart(t *testing.T) {
	ctx := context.Background()
	wasmPath := buildPluginWasm(t)

	repoTestdata := absPath(t, "../../../internal/compiler/renderer/helm/testdata")
	chartDir := filepath.Join(repoTestdata, "schema-chart")
	nodeID := "api"

	payload, values := buildRenderInput(t, chartDir, nodeID, `{"replicas":4,"message":"schema-ab"}`)

	oracle, err := (&helm.HelmEngine{}).Render(ctx, values, renderer.NodeRenderContext{NodeID: nodeID, Source: chartDir})
	if err != nil {
		t.Fatalf("oracle Render: %v", err)
	}

	rawOut := invokeGuest(t, ctx, wasmPath, payload)
	assertByteIdentical(t, oracle, decodeCandidate(t, rawOut))
}

// writeRemoteRefChart writes a chart whose values.schema.json is a single
// remote $ref to refURL, returning the chart directory.
func writeRemoteRefChart(t *testing.T, refURL string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"Chart.yaml":         "apiVersion: v2\nname: remoteref\nversion: 0.1.0\ndescription: Chart fixture whose values schema references a remote document.\n",
		"values.yaml":        "replicas: 1\n",
		"values.schema.json": fmt.Sprintf("{\n  \"$ref\": %q\n}\n", refURL),
		"templates/configmap.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n" +
			"  name: {{ .Release.Name }}-remote-ref\ndata:\n  replicas: {{ .Values.replicas | quote }}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// TestHelmProviderRemoteRefSchemaExternalInputs proves the remote-$ref
// values-schema path end to end against the pinned external-input channel,
// with a counting loopback server standing in for the remote schema host:
//
//  1. Frozen closure, nothing pinned: the guest render fails in-band, the
//     failure names the URL and the remedy, the plugin's warning channel
//     carries it too, and the network is never touched.
//  2. Acquisition enabled: the guest render succeeds, the URL is pinned with
//     the plugin's name as provenance, and the output is byte-identical to
//     the in-tree oracle rendering the same chart (whose SDK validator
//     resolves the reference live from the same server — the host engine's
//     existing behaviour, used deliberately so the two outputs compare).
//  3. Second run from the pin: a fresh frozen service seeded with the
//     recorded pin over the same store renders byte-identical again with
//     zero additional network hits.
func TestHelmProviderRemoteRefSchemaExternalInputs(t *testing.T) {
	ctx := context.Background()
	wasmPath := buildPluginWasm(t)

	var hits atomic.Int64
	const contract = `{"type":"object","properties":{"replicas":{"type":"integer","minimum":1}},"required":["replicas"]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(contract))
	}))
	defer srv.Close()
	refURL := srv.URL + "/values-contract.json"

	chartDir := writeRemoteRefChart(t, refURL)
	nodeID := "api"
	payload, values := buildRenderInput(t, chartDir, nodeID, `{"replicas":3}`)
	storeRoot := t.TempDir()

	// Scenario 1 — frozen closure, nothing pinned.
	frozen := externalinputs.NewService(storeRoot, false, nil, nil)
	frozenPlugin := loadGuest(t, ctx, wasmPath, frozen)
	if _, err := frozenPlugin.Call(ctx, "render_node", payload); err == nil {
		t.Fatal("frozen render succeeded; an unpinned remote schema reference must halt the render")
	} else {
		for _, want := range []string{refURL, "not pinned"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("frozen render error missing %q: %v", want, err)
			}
		}
	}
	warned := false
	for _, w := range frozenPlugin.Warnings() {
		if strings.Contains(w, refURL) {
			warned = true
		}
	}
	if !warned {
		t.Errorf("plugin warning channel does not name the unpinned URL; warnings: %v", frozenPlugin.Warnings())
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("frozen render reached the network: %d hits", got)
	}

	// Oracle — the in-tree engine with the SDK's live reference resolution.
	oracle, err := (&helm.HelmEngine{}).Render(ctx, values, renderer.NodeRenderContext{NodeID: nodeID, Source: chartDir})
	if err != nil {
		t.Fatalf("oracle Render: %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("oracle render did not resolve the remote reference (expected a live fetch)")
	}

	// Scenario 2 — acquisition enabled.
	acquire := externalinputs.NewService(storeRoot, true, nil, nil)
	acquirePlugin := loadGuest(t, ctx, wasmPath, acquire)
	rawOut, err := acquirePlugin.Call(ctx, "render_node", payload)
	if err != nil {
		t.Fatalf("acquisition render: %v", err)
	}
	assertByteIdentical(t, oracle, decodeCandidate(t, rawOut))
	recs := acquire.AcquiredRecords()
	if len(recs) != 1 {
		t.Fatalf("AcquiredRecords: want exactly one pin, got %d (%+v)", len(recs), recs)
	}
	if recs[0].URL != refURL {
		t.Errorf("pinned URL = %q, want %q", recs[0].URL, refURL)
	}
	if !slices.Contains(recs[0].RequiredBy, "helm") {
		t.Errorf("pin provenance %v missing the plugin name", recs[0].RequiredBy)
	}

	// Scenario 3 — second run from the pin, frozen, zero additional hits.
	before := hits.Load()
	pinned := externalinputs.NewService(storeRoot, false, nil, recs)
	pinnedPlugin := loadGuest(t, ctx, wasmPath, pinned)
	rawOut2, err := pinnedPlugin.Call(ctx, "render_node", payload)
	if err != nil {
		t.Fatalf("pinned re-render: %v", err)
	}
	assertByteIdentical(t, oracle, decodeCandidate(t, rawOut2))
	if got := hits.Load(); got != before {
		t.Fatalf("pinned re-render reached the network: %d additional hits", got-before)
	}
}
