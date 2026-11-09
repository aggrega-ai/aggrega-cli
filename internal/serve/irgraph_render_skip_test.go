// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// renderDialWorkspace builds a self-contained workspace whose single helm node
// resolves end-to-end: a cached+pinned Component, a manifest that maps the
// component type to a synthesized-mode helm Recipe, and a helm-renderer provider
// Plugin whose digest-free artifact identity resolves through the embed tier. So
// the irGraph render (dial on) runs the helm guest to completion — the render
// this test needs to actually execute to prove the graph is dial-invariant.
func renderDialWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	ws = t.TempDir()

	catalogDir := filepath.Join(ws, "catalog")
	const svcComponent = `apiVersion: aggrega.ai/v1beta1
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
`
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "svc.yaml"), svcComponent)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: demo-server-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/svc
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-server:latest
`)
	// The helm-renderer provider Plugin binds the helm provider to the embedded
	// helm rendering guest; its digest-free spec.plugin identity resolves through
	// the plugin-resolution cascade's embed tier with no cache or lockfile.
	writeFixtureFile(t, filepath.Join(catalogDir, "plugins", "helm-renderer.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: helm
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1beta1
  provider:
    capabilities:
      - helm
  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)

	writeFixtureFile(t, filepath.Join(ws, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
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

	writeFixtureFile(t, filepath.Join(ws, "app-agg.yaml"), `apiVersion: aggrega.ai/v1beta1
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
	writeFixtureFile(t, relPath, `apiVersion: aggrega.ai/v1beta1
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
	// Serve requires a lockfile to exist; a minimal pin of the one component
	// suffices, recording the component's own content digest as a pull would
	// (the lockfile refuses a row without a well-formed one). A Tier-1
	// (catalog-dir) component is not verified on read against it. Any residual
	// drift from unpinned recipe/plugin entities surfaces as a Problems-panel
	// warning, not a halt, and applies identically to the dial-on and dial-off runs.
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./app-release.yaml",
		GeneratedAt: "2026-06-03T10:00:00Z",
		Entities: []lockfile.Entity{{
			Kind: "component", URI: "demo/svc:1.0.0", ResolvedVersion: "1.0.0", Digest: contentDigestHex([]byte(svcComponent)),
		}},
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(relPath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	return ws, filepath.Base(relPath)
}

// TestResolveBuildOnInspectPrecedence pins the flag-over-config
// precedence: an explicit flag (non-nil) wins over the config-file key;
// an unset flag (nil) falls through to the config value.
func TestResolveBuildOnInspectPrecedence(t *testing.T) {
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
			if got := resolveBuildOnInspect(c.flag, c.cfgValue); got != c.want {
				t.Errorf("resolveBuildOnInspect(%v, %v) = %v, want %v", c.flag, c.cfgValue, got, c.want)
			}
		})
	}
}

// irGraphWithDial drives one irGraph request to completion at pool=1 with
// the build-on-inspect dial set to buildOnInspect, returning the response's
// graph JSON. Fails the test if the response carries an error envelope.
func irGraphWithDial(t *testing.T, ws, releaseRel string, buildOnInspect bool) json.RawMessage {
	t.Helper()
	graph := irGraphWithDials(t, ws, releaseRel, func(srv *Server) { srv.buildOnInspect = buildOnInspect })
	if graph == nil {
		t.Fatalf("irGraph (buildOnInspect=%v) produced no graph", buildOnInspect)
	}
	return graph
}

// irGraphWithDials drives one irGraph request to completion at pool=1 over a
// server whose session dials configure has set, returning the response's graph
// JSON.
//
// The dials are set through a callback rather than through one parameter per
// dial, so a test about one dial names that dial alone and a test about a dial
// added later needs no change here.
func irGraphWithDials(t *testing.T, ws, releaseRel string, configure func(*Server)) json.RawMessage {
	t.Helper()
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	configure(srv)
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
	return extractGraph(t, &out)
}

// TestServeIrGraphByteIdenticalRenderDialOnVsOff proves the irGraph
// response graph is byte-identical whether the build-on-inspect dial is
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
// resolved build-on-inspect dial onto the pipeline's SkipRender option:
// dial off (the default) requests render-skip; dial on requests render. The
// pipeline-level proof (that SkipRender skips render and loads no rendering
// plugin) closes the chain: dial off => SkipRender true => no render/no
// plugin load; dial on => SkipRender false => render runs.
func TestServeIrGraphPipelineOptionsReflectsDial(t *testing.T) {
	t.Parallel()
	srv := &Server{}

	srv.buildOnInspect = false
	if opts := srv.irGraphPipelineOptions(); !opts.SkipRender {
		t.Error("dial off (default): SkipRender should be true so render is skipped")
	}

	srv.buildOnInspect = true
	if opts := srv.irGraphPipelineOptions(); opts.SkipRender {
		t.Error("dial on: SkipRender should be false so render runs on the irGraph path")
	}
}

// treeSnapshot maps every regular file under root to the SHA-256 of its bytes,
// keyed by root-relative path. Two snapshots comparing equal prove no file was
// added, removed, or rewritten between them.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(body))
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return out
}

// TestServeIrGraphWritesNothingToDisk pins the half of the dial's contract that
// its name invites a reader to doubt: turning it on runs the rendering stage,
// not the build's output. The dial's help text and its config-schema description
// both state that nothing is written to disk — no rendered artifacts, no output
// directory, no lockfile — so the claim is asserted here rather than left to the
// reader's trust. The Release declares a boundary directory (./dist/app), which
// is exactly where a render that wrote its artifacts would land them.
func TestServeIrGraphWritesNothingToDisk(t *testing.T) {
	t.Parallel()
	ws, rel := renderDialWorkspace(t)

	before := treeSnapshot(t, ws)
	// An empty snapshot would make every comparison below vacuous, so the
	// instrument is checked before its reading is trusted.
	if len(before) == 0 {
		t.Fatal("precondition: the workspace snapshot is empty, so a write could not be observed")
	}
	if graph := irGraphWithDial(t, ws, rel, true); graph == nil {
		t.Fatal("precondition: the dial-on inspection produced no graph, so it proves nothing about writes")
	}
	after := treeSnapshot(t, ws)

	for path, sum := range after {
		prev, ok := before[path]
		switch {
		case !ok:
			t.Errorf("inspection created %s; the dial runs the rendering stage, never its output", path)
		case prev != sum:
			t.Errorf("inspection rewrote %s (%s -> %s)", path, prev, sum)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("inspection removed %s", path)
		}
	}
}
