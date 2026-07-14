// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// renderDialWorkspace builds a self-contained workspace whose single helm node
// resolves end-to-end: a cached+pinned Component, a manifest that maps the
// component type to a synthesised-mode helm Recipe, and a helm-renderer provider
// Plugin whose digest-free artifact identity resolves through the embed tier. So
// the irGraph render (dial on) runs the helm guest to completion — the render
// this test needs to actually execute to prove the graph is dial-invariant.
func renderDialWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	ws = t.TempDir()

	catalogDir := filepath.Join(ws, "catalog")
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "svc.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-server-synthesized
  version: "1.0.0"
spec:
  target: helm
  implements: demo/svc
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-server:latest
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  workloadSchema: |
    type: object
    additionalProperties: true
`)
	// The helm-renderer provider Plugin binds the helm provider to the embedded
	// helm rendering guest; its digest-free spec.plugin identity resolves through
	// the plugin-resolution cascade's embed tier with no sidecar, cache, or lockfile.
	writeFixtureFile(t, filepath.Join(catalogDir, "plugins", "helm-renderer.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: provider/helm
  version: "1.0.0"
spec:
  pluginType: provider
  abiVersion: v1alpha1
  targets:
    - helm
  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)

	writeFixtureFile(t, filepath.Join(ws, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/svc: demo-server-synthesized
`)

	writeFixtureFile(t, filepath.Join(ws, "app-agg.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app-agg
  version: "1.0.0"
spec:
  components:
    app:
      type: "demo/svc:1.0.0"
`)
	relPath := filepath.Join(ws, "app-release.yaml")
	writeFixtureFile(t, relPath, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: app-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: app-agg
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/app
      components:
        app: {}
`)
	// Serve requires a lockfile to exist; a Tier-1 (catalog-dir) component needs
	// no verify-on-read digest, so a minimal pin suffices. Any residual drift
	// from unpinned recipe/plugin entities surfaces as a Problems-panel warning,
	// not a halt, and applies identically to the dial-on and dial-off runs.
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./app-release.yaml",
		GeneratedAt: "2026-06-03T10:00:00Z",
		Entities:    []lockfile.Entity{{URI: "demo/svc:1.0.0", ResolvedVersion: "1.0.0"}},
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(relPath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	return ws, filepath.Base(relPath)
}

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
	ws, rel := renderDialWorkspace(t)

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
