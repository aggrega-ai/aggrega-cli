// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// One build, one render — asserted on this surface, over the wire.
//
// The claim is that a build asked to resolve its dependencies first renders the
// Release ONCE rather than twice. Byte identity cannot witness that: an artifact
// set is the same under one render and under two, so comparing output would be
// green either way. Something has to COUNT renders.
//
// What counts them is a line the render step writes once per render — a warning
// naming a field the target cannot express — shown by the pipeline, from one call
// site, on the display the surface hands it, which on this surface is the stream
// the process was launched with. The fixture
// authors such a field deliberately, so each render of it produces exactly one
// line and the number of lines IS the number of renders.
//
// The calibration leg is not decoration. A build that does not fill renders once
// by construction, so it fixes what ONE render's worth of output looks like on
// this fixture. Without it, a count of one on the filling leg could as easily
// mean the fixture stopped warning at all.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// lockedBuffer keeps what is written to it, safe for the concurrent writes a
// request's work may make.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureServerStderr points srv's error stream at a buffer for the duration of
// fn and returns what was written to it.
//
// It is the stream the process was launched with rather than any answer, and
// that is the subject rather than a shortcut: every render this process runs —
// the build's own and the one a fill makes — shows its per-render warnings on
// that stream, through the one display the render writes to on every surface, so
// a daemon render and a terminal render are counted by the same instrument.
func captureServerStderr(t *testing.T, srv *Server, fn func()) string {
	t.Helper()
	var buf lockedBuffer
	old := srv.session.stderr
	srv.session.stderr = &buf
	defer func() { srv.session.stderr = old }()
	fn()
	return buf.String()
}

// unsupportedFieldMarker is the user-facing text of one diagnostic of the
// unsupported-field class, taken from the class's own constructor rather than
// written out here, and it fails the test when that constructor does not carry
// the code the display filter matches on.
//
// The warning a reader sees carries only the rendered text, so a count driven
// through a request has no way to match the code directly. Deriving the expected
// text from the constructor buys the property that matters: rewording the
// diagnostic cannot fail this, while repointing the constructor at a different
// class fails it loudly.
func unsupportedFieldMarker(t *testing.T, target, field string) string {
	t.Helper()
	d := rendercore.UnsupportedFieldDiagnostic(target, field)
	if d.Code != renderer.UnsupportedFieldCode {
		t.Fatalf("the constructor carries code %q, want the class code %q the display filter matches on",
			d.Code, renderer.UnsupportedFieldCode)
	}
	if d.Message == "" {
		t.Fatal("the constructor produced an empty message, so there is nothing to count")
	}
	return d.Message
}

// countOccurrences reports how many times needle appears in s, non-overlapping.
func countOccurrences(s, needle string) int {
	if needle == "" {
		return 0
	}
	return strings.Count(s, needle)
}

// unsupportedFieldWorkspace lays down a workspace whose single node renders
// through the compose target and whose Workload declares a replica count — a
// field compose cannot express — so a real render of it emits exactly one line
// of the unsupported-field class.
//
// The catalog directory is declared in the workspace configuration file as well
// as handed to the Server, and both are needed for different halves: the render
// reads the harness's search stack, while the dependency resolution reads the
// workspace's own resolved configuration. A fixture that set only one would
// refuse to resolve, and the refusal would look like a fault in what is under
// test.
//
// No lockfile is written. A leg that needs one resolves first, which is the same
// operation the filling build runs, so neither leg is held to a pin set the other
// did not see.
func unsupportedFieldWorkspace(t *testing.T) (ws, releaseRel string) {
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
    values:
      port: { type: integer, default: 8080 }
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-server-compose
  version: "1.0.0"
spec:
  capability: compose
  implements: demo/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      main:
        image: demo-server:latest
        ports:
          web: {{ .values.port }}
`)
	// The compose provider Plugin binds the compose capability to the embedded
	// compose rendering guest; its digest-free spec.plugin identity resolves
	// through the plugin-resolution cascade's embed tier with no cache and no
	// registry.
	writeFixtureFile(t, filepath.Join(catalogDir, "plugins", "compose-renderer.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: compose
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  provider:
    capabilities:
      - compose
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
        demo/svc: demo-server-compose
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
      values:
        port: 8080
`)
	relPath := filepath.Join(ws, "app-release.yaml")
	// The replica count lives on the Release's per-component infra, which is what
	// makes the workload template resolve to a value compose cannot express. With
	// the default the template falls back to, the render warns about nothing and
	// this file would count zeroes.
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
        app:
          infra:
            replicas: 2
`)
	if err := os.WriteFile(filepath.Join(ws, ".aggrega-config.yaml"),
		[]byte("workspace:\n  catalog_dirs: [catalog]\n"+testOwnedRegistryConfig(t)), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return ws, filepath.Base(relPath)
}

// TestBuildAutoPullRendersTheReleaseOnce is the assertion byte identity cannot
// make, driven over this surface's own wire.
//
// The two legs run against separate workspaces, so neither can read state the
// other left, and each drives one aggrega/build request through the dispatch
// rather than calling the handler — the count is about what a client gets.
func TestBuildAutoPullRendersTheReleaseOnce(t *testing.T) {
	// The surface under assertion is each server's own error stream, which this
	// test points at a buffer for the duration of one request.
	marker := unsupportedFieldMarker(t, "compose", "replicas")

	// Leg 1 — calibration. A build over a lockfile a separate resolution wrote
	// renders once by construction, so this fixes what one render looks like.
	//
	// The priming resolution renders too, and its warning is deliberately OUTSIDE
	// the capture: what is being calibrated is one build's worth of output.
	wsPlain, releasePlain := unsupportedFieldWorkspace(t)
	primeSrv := newFillCapableServer(t, wsPlain)
	if _, errEnvelope := primeSrv.handleResolveDependencies(
		context.Background(),
		resolveDependenciesParams{Release: releasePlain},
		primeSrv.progressFor(context.Background())); errEnvelope != nil {
		t.Fatalf("prime the lockfile: %s", errEnvelope.Message)
	}

	var plainResp buildResponse
	var plainWire string
	plainSrv := newFillCapableServer(t, wsPlain)
	plainStderr := captureServerStderr(t, plainSrv, func() {
		plainResp, plainWire = driveBuildWithRawParams(t, plainSrv, `{"release":"`+releasePlain+`"}`)
	})
	if plainResp.Error != nil {
		t.Fatalf("the calibrating build refused: code %d: %s\nwire:\n%s",
			plainResp.Error.Code, plainResp.Error.Message, plainWire)
	}
	if got := countOccurrences(plainStderr, marker); got != 1 {
		t.Fatalf("a build that does not fill renders once, so its warning must appear once; got %d\n"+
			"marker: %s\nstderr:\n%s", got, marker, plainStderr)
	}

	// Leg 2 — the assertion. A filling build resolves and renders in one request;
	// the warning appearing twice means the Release was rendered twice.
	wsFill, releaseFill := unsupportedFieldWorkspace(t)
	var fillResp buildResponse
	var fillWire string
	fillSrv := newFillCapableServer(t, wsFill)
	fillStderr := captureServerStderr(t, fillSrv, func() {
		fillResp, fillWire = driveBuildWithRawParams(t, fillSrv, `{"release":"`+releaseFill+`","autoPull":true}`)
	})
	if fillResp.Error != nil {
		t.Fatalf("the filling build refused: code %d: %s\nwire:\n%s",
			fillResp.Error.Code, fillResp.Error.Message, fillWire)
	}
	if got := countOccurrences(fillStderr, marker); got != 1 {
		t.Errorf("a filling aggrega/build rendered the Release %d time(s); it must render it exactly once.\n"+
			"The warning below is written once per render, so a count above one is a second render.\n"+
			"marker: %s\nstderr:\n%s", got, marker, fillStderr)
	}
	// The count says nothing unless the request actually rendered: a build that
	// halted before the render would also report one warning, or none.
	if len(fillResp.Result.Files) == 0 {
		t.Fatalf("the filling build reported no artifacts, so its warning count says nothing about "+
			"rendering\nwire:\n%s", fillWire)
	}
	for _, name := range fillResp.Result.Files {
		if _, err := os.Stat(filepath.Join(fillResp.Result.OutputDirectory, name)); err != nil {
			t.Errorf("the build reported %s but it is not on disk: %v", name, err)
		}
	}
	// And the fill really did fill: a request that resolved nothing would have
	// rendered once for the ordinary reason rather than for the one under test.
	if fillResp.Result.DependencyResolution == nil {
		t.Error("the filling build reported no dependency resolution, so the leg above did not exercise " +
			"the filling path at all")
	}
}
