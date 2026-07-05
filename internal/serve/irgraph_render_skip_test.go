// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestResolveRenderDiagnosticsPrecedence pins the flag-over-config
// precedence: an explicit flag (non-nil) wins over the config-file key;
// an unset flag (nil) falls through to the config value.
func TestResolveRenderDiagnosticsPrecedence(t *testing.T) {
	t.Parallel()
	tru := true
	fls := false
	cases := []struct {
		name     string
		flag     *bool
		cfgValue bool
		want     bool
	}{
		{"flag-unset,config-false", nil, false, false},
		{"flag-unset,config-true", nil, true, true},
		{"flag-true-overrides-config-false", &tru, false, true},
		{"flag-false-overrides-config-true", &fls, true, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveRenderDiagnostics(c.flag, c.cfgValue); got != c.want {
				t.Errorf("resolveRenderDiagnostics(%v, %v) = %v, want %v", c.flag, c.cfgValue, got, c.want)
			}
		})
	}
}

// irGraphWithDial drives one irGraph request to completion at pool=1 with
// the render-diagnostics dial set to renderDiag, returning the response's
// graph JSON. Fails the test if the response carries an error envelope.
func irGraphWithDial(t *testing.T, ws, releaseRel string, renderDiag bool) json.RawMessage {
	t.Helper()
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	srv.renderDiagnostics = renderDiag
	in.Write(framedRequest(1, "aggrega/irGraph", irGraphParams{Release: releaseRel}))
	srv.in = bufio.NewReader(&in)
	srv.out = &out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &req, &out)
	graph := extractGraph(t, &out)
	if graph == nil {
		t.Fatalf("irGraph (renderDiagnostics=%v) produced no graph", renderDiag)
	}
	return graph
}

// TestServeIrGraphByteIdenticalRenderDialOnVsOff proves the irGraph
// response graph is byte-identical whether the render-diagnostics dial is
// off (render skipped) or on (render runs). Render never mutates the IR
// graph, so the dial changes only whether render runs — never the answer.
func TestServeIrGraphByteIdenticalRenderDialOnVsOff(t *testing.T) {
	t.Parallel()
	ws, releaseA, _ := pinIsolationWorkspace(t)
	rel := filepath.Base(releaseA)

	off := irGraphWithDial(t, ws, rel, false)
	on := irGraphWithDial(t, ws, rel, true)

	if !bytes.Equal(off, on) {
		t.Errorf("irGraph graph differs between dial off and on:\n off: %s\n on:  %s", off, on)
	}
}

// TestServeIrGraphPipelineOptionsReflectsDial proves the handler maps the
// resolved render-diagnostics dial onto the pipeline's SkipRender option:
// dial off (the default) requests render-skip; dial on requests render. The
// pipeline-level proof (that SkipRender skips render and loads no rendering
// plugin) closes the chain: dial off => SkipRender true => no render/no
// plugin load; dial on => SkipRender false => render runs.
func TestServeIrGraphPipelineOptionsReflectsDial(t *testing.T) {
	t.Parallel()
	srv := &Server{}

	srv.renderDiagnostics = false
	if opts := srv.irGraphPipelineOptions(); !opts.SkipRender {
		t.Error("dial off (default): SkipRender should be true so render is skipped")
	}

	srv.renderDiagnostics = true
	if opts := srv.irGraphPipelineOptions(); opts.SkipRender {
		t.Error("dial on: SkipRender should be false so render runs on the irGraph path")
	}
}
