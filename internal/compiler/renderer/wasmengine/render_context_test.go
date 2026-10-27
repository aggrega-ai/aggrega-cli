// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestRenderContextIsTheGuestContextWithTheIndentation pins what the provider
// engine hands its guest on every export carrying a context: the node's guest
// context exactly as the renderer built it — every bucket, the parameters and
// the boundary — with the render-wide indentation width added and nothing else
// changed. It also requires the node's own context to be left as it was, since
// the same value is what a pipeline step of that node is handed.
func TestRenderContextIsTheGuestContextWithTheIndentation(t *testing.T) {
	t.Parallel()

	ordered := func(pairs ...string) types.OrderedMap[string] {
		var m types.OrderedMap[string]
		for i := 0; i+1 < len(pairs); i += 2 {
			m.Set(pairs[i], pairs[i+1])
		}
		return m
	}
	var params types.OrderedMap[any]
	params.Set("region", "eu-west-1")
	guest := renderer.RenderContext{
		Parameters: params,
		Annotations: renderer.RenderAnnotations{
			Aggregate: ordered("team.example/owner", "billing"),
			Release:   ordered("aggrega.ai/namespace", "payments"),
			Catalog:   ordered("tier.example/class", "gold"),
		},
		Boundary: renderer.BoundaryContext{
			Name:      "payments",
			Path:      []string{"aws-prod", "payments"},
			SyncWave:  1,
			Directory: "dist/prod",
		},
	}
	nodeCtx := renderer.NodeRenderContext{GuestContext: guest, YAMLIndent: 4}

	got, err := json.Marshal((&Engine{}).renderContext(nodeCtx))
	if err != nil {
		t.Fatalf("marshal the handed context: %v", err)
	}
	want := guest
	want.YAMLIndent = 4
	wantBytes, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal the expected context: %v", err)
	}
	if !bytes.Equal(got, wantBytes) {
		t.Errorf("the guest is handed a context other than the node's with the indentation:\n got: %s\nwant: %s", got, wantBytes)
	}
	if nodeCtx.GuestContext.YAMLIndent != 0 {
		t.Errorf("handing the context over changed the node's own copy: yaml_indent = %d", nodeCtx.GuestContext.YAMLIndent)
	}
}
