// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// One build, one render — asserted on this surface, through its own dispatch.
//
// The claim is that a build asked to resolve its dependencies first renders the
// Release ONCE rather than twice. Byte identity cannot witness that: the file set
// a call returns is the same under one render and under two, so comparing it
// would be green either way. Something has to COUNT renders.
//
// What counts them is a line the render step writes once per render — a warning
// naming a field the target cannot express — shown by the pipeline, from one call
// site, on the display the surface hands it, which on this surface is the
// server's log. The fixture
// authors such a field deliberately, so each render of it produces exactly one
// line and the number of lines IS the number of renders.
//
// The calibration leg is not decoration. A call that does not fill renders once
// by construction, so it fixes what ONE render's worth of output looks like on
// this fixture. Without it, a count of one on the filling leg could as easily
// mean the fixture stopped warning at all.
//
// 🔴 The calls here are not sent write, so this surface writes no artifact and
// leaves no output tree to check the render against. What stands in for that is
// the returned file set, which is populated only from a finished render.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// captureServerLog points s's log stream at a buffer for the duration of fn and
// returns what was written to it.
//
// It is the server's log rather than any answer, and that is the subject rather
// than a shortcut: every render this server runs — the build's own and the one a
// fill makes — shows its per-render warnings there, through the one display the
// render writes to on every surface, so a tool-protocol render and a terminal
// render are counted by the same instrument.
func captureServerLog(t *testing.T, s *server, fn func()) string {
	t.Helper()
	var buf lockedBuffer
	old := s.logw
	s.logw = &buf
	defer func() { s.logw = old }()
	fn()
	return buf.String()
}

// unsupportedFieldMarker is the user-facing text of one diagnostic of the
// unsupported-field class, taken from the class's own constructor rather than
// written out here, and it fails the test when that constructor does not carry
// the code the display filter matches on.
//
// The warning a reader sees carries only the rendered text, so a count driven
// through a call has no way to match the code directly. Deriving the expected
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

// writeRenderFixture writes one fixture file, creating the directories above it.
func writeRenderFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// unsupportedFieldWorkspace lays down a workspace whose single node renders
// through the compose target and whose Workload declares a replica count — a
// field compose cannot express — so a real render of it emits exactly one line
// of the unsupported-field class.
//
// Offline is what makes it hermetic rather than merely local: the egress gate
// refuses before any dial, so the resolution answers from this catalog alone and
// no leg here measures a registry's availability. It returns the server, the
// root, the catalog stack and the Release path.
//
// No lockfile is written. A leg that needs one resolves first, which is the same
// operation the filling build runs, so neither leg is held to a pin set the other
// did not see.
func unsupportedFieldWorkspace(t *testing.T) (s *server, root string, dirs []string, releasePath string) {
	t.Helper()
	root = t.TempDir()

	catalogDir := filepath.Join(root, "catalog")
	writeRenderFixture(t, filepath.Join(catalogDir, "components", "svc.yaml"), `apiVersion: aggrega.ai/v1beta1
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
	writeRenderFixture(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: demo-server-compose
  version: "1.0.0"
spec:
  capability: compose
  implements: demo/svc
  workload: |
    apiVersion: aggrega.ai/v1beta1
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
	writeRenderFixture(t, filepath.Join(catalogDir, "plugins", "compose-renderer.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: compose
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1beta1
  provider:
    capabilities:
      - compose
  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)

	writeRenderFixture(t, filepath.Join(root, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
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
	writeRenderFixture(t, filepath.Join(root, "app-agg.yaml"), `apiVersion: aggrega.ai/v1beta1
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
	releasePath = filepath.Join(root, "app-release.yaml")
	// The replica count lives on the Release's per-component infra, which is what
	// makes the workload template resolve to a value compose cannot express. With
	// the default the template falls back to, the render warns about nothing and
	// this file would count zeroes.
	writeRenderFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
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
	// The configuration anchors the root and closes the network; it declares no
	// catalog directory, because every call here passes the stack explicitly and
	// the resolution prepends what it is handed to the workspace's own. Declaring
	// it in both places searches one directory twice and buries the line this file
	// counts under shadowing notices for entities shadowing themselves.
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\noffline: true\n"), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}

	s = newTestServer(t)
	return s, root, []string{catalogDir}, releasePath
}

// TestBuildAutoPullRendersTheReleaseOnce is the assertion byte identity cannot
// make, driven through this surface's own tools/call dispatch.
//
// The two legs run against separate workspaces, so neither can read state the
// other left, and each drives one build call through the dispatch rather than
// calling the invoke — the count is about what a caller gets.
func TestBuildAutoPullRendersTheReleaseOnce(t *testing.T) {
	// The surface under assertion is each server's own log, which this test points
	// at a buffer for the duration of one call.
	marker := unsupportedFieldMarker(t, "compose", "replicas")

	// Leg 1 — calibration. A call that does not fill renders once by
	// construction, so this fixes what one render looks like. The priming
	// resolution renders too, and its warning is deliberately OUTSIDE the
	// capture: what is being calibrated is one build's worth of output.
	plainServer, plainRoot, plainDirs, plainRelease := unsupportedFieldWorkspace(t)
	if prime := callPull(t, plainServer, pullArgs{
		Release: plainRelease, WorkspaceRoot: plainRoot, CatalogDirs: plainDirs,
	}); prime.IsError {
		t.Fatalf("prime the lockfile: %s", prime.Content[0].Text)
	}

	var plainRes *callToolResult
	plainStderr := captureServerLog(t, plainServer, func() {
		plainRes = callBuildWith(t, plainServer, plainRoot, plainDirs, plainRelease, false)
	})
	if plainRes.IsError {
		t.Fatalf("the calibrating build refused: %s", plainRes.Content[0].Text)
	}
	if got := countOccurrences(plainStderr, marker); got != 1 {
		t.Fatalf("a build that does not fill renders once, so its warning must appear once; got %d\n"+
			"marker: %s\nstderr:\n%s", got, marker, plainStderr)
	}

	// Leg 2 — the assertion. A filling build resolves and renders in one call;
	// the warning appearing twice means the Release was rendered twice.
	fillServer, fillRoot, fillDirs, fillRelease := unsupportedFieldWorkspace(t)
	var fillRes *callToolResult
	fillStderr := captureServerLog(t, fillServer, func() {
		fillRes = callBuildWith(t, fillServer, fillRoot, fillDirs, fillRelease, true)
	})
	if fillRes.IsError {
		t.Fatalf("the filling build refused: %s", fillRes.Content[0].Text)
	}
	if got := countOccurrences(fillStderr, marker); got != 1 {
		t.Errorf("a filling build call rendered the Release %d time(s); it must render it exactly once.\n"+
			"The warning below is written once per render, so a count above one is a second render.\n"+
			"marker: %s\nstderr:\n%s", got, marker, fillStderr)
	}
	// The count says nothing unless the call actually rendered: one that halted
	// before the render would also report one warning, or none. A call not sent
	// write writes nothing, so the returned set is what stands in for an output
	// tree — and it is populated only from a finished render.
	payload := unmarshalBuildPayload(t, fillRes)
	if len(payload.Files) == 0 {
		t.Fatal("the filling build returned no rendered files, so its warning count says nothing about rendering")
	}
	// And the fill really did fill: a call that resolved nothing would have
	// rendered once for the ordinary reason rather than for the one under test.
	if payload.DependencyResolution == nil {
		t.Error("the filling build reported no dependency resolution, so the leg above did not exercise " +
			"the filling path at all")
	}
}
