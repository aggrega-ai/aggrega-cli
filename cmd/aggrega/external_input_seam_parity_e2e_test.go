// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Three surfaces render a Release: the command line, the editor daemon, and the
// tool protocol. All three run one preview computation, and that computation
// installs a render-time external-input seam. A surface that hands it no seam
// renders a not-available envelope where the content should be — so the SAME
// Release emits different bytes depending on which surface asked, and every
// completeness check still passes, because each check asserts the path exists
// rather than what it produces.
//
// This file is the comparison that can see the difference. It needs a provider
// guest whose render requests an external input, because that is the only shape
// under which a seam changes an emitted byte; the fixture corpus had none, so
// internal/wasm/testdata/fetching-provider is built here for it.
//
// The negative control is the load-bearing half. Three surfaces agreeing proves
// nothing on its own: three surfaces each rendering the same not-available
// envelope agree perfectly. So the same workspace is also rendered with the seam
// explicitly absent, and that render must produce DIFFERENT bytes — which is what
// establishes the fixture can tell the two apart at all.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	// seamFixtureURL is the external reference the fetching-provider guest asks
	// for on every render.
	//
	// 🔴 DELIBERATE DUPLICATE, recorded so it is not "unified" away: the guest
	// declares the same URL in its own source. It has to — the guest is built with
	// GOWORK=off against its own module, so it shares no Go constant with this
	// test. The duplication is a language-boundary one. A mismatch is not silent:
	// the reference stops being pinned and the render reports it declined.
	seamFixtureURL = "https://fixtures.invalid/external-input.txt"

	// seamFixtureBody is the pinned content the store serves for that reference.
	seamFixtureBody = "pinned-external-input-bytes\n"

	// seamFixtureFile is the output-relative name the guest emits, and
	// seamFixturePrefix the prefix it puts before the fetch outcome. Both mirror
	// the guest's own constants, on the same terms as the URL above.
	seamFixtureFile   = "external-input.txt"
	seamFixturePrefix = "external-input:"
)

// seamFixtureServedContent is what the emitted file must contain when a seam is
// attached and the reference is pinned: the prefix followed by the store's bytes.
// It is the single expected value every surface's render is compared against, so
// "the three agree" and "the three are right" are one assertion rather than two.
func seamFixtureServedContent() string { return seamFixturePrefix + seamFixtureBody }

// writeFetchingProviderWorkspace lays down a workspace whose single node renders
// through a provider guest that requests an external input, with that input
// pinned into the workspace's content-addressed store.
//
// It returns the release path. The guest is routed to by capability: the Recipe
// names the capability, the Plugin entity declares it served, and the guest's
// .wasm is staged beside that entity so the co-located tier resolves it. Nothing
// is bound in workspace configuration and nothing is fetched, so the fixture
// exercises the same resolution an offline catalog does.
func writeFetchingProviderWorkspace(t *testing.T, root string) string {
	t.Helper()

	src, err := filepath.Abs(filepath.Join("..", "..", "internal", "wasm", "testdata", "fetching-provider"))
	if err != nil {
		t.Fatalf("resolve guest source: %v", err)
	}
	// No skip path, deliberately: the guest is plain Go targeting wasip1, which
	// ships with every Go distribution, so a skip here could only hide a proof
	// that did not run.
	guestPath := wasmtest.EnsureLocalGuestBuilt(t, src, "fetching-provider.wasm")

	catalogDir := filepath.Join(root, "catalog")

	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), `workspace:
  catalog_dirs:
    - ./catalog
`)

	writeFixture(t, filepath.Join(catalogDir, "components", "fetcher.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/fetcher
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`)

	writeFixture(t, filepath.Join(catalogDir, "recipes", "fetcher.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-fetcher-synthesized
  version: "1.0.0"
spec:
  capability: fetching
  implements: demo/fetcher
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-fetcher:latest
`)

	writeFixture(t, filepath.Join(catalogDir, "plugins", "fetching-provider.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: fetching
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  provider:
    capabilities:
      - fetching
    contract:
      inputKinds:
        synthesized: true
      boundaryAssembly: none
      reusableInstance: true
  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)

	// The guest is staged BESIDE its Plugin entity, which is the co-located tier of
	// the plugin-resolution cascade — the offline path for a catalog whose plugins
	// are not compiled into the binary. The filename is derived through the same
	// mapping the resolver reads it back by, rather than spelled here, so a change
	// to that mapping moves both sides at once.
	//
	// Both the pinned and the bare reference are staged: which one the render
	// resolves by depends on whether the closure carried a pin, and staging one
	// leaf would make this fixture depend on that detail.
	guestBytes, err := os.ReadFile(guestPath)
	if err != nil {
		t.Fatalf("read the built guest: %v", err)
	}
	pluginDir := filepath.Join(catalogDir, "plugins")
	for _, ref := range []string{"provider/fetching", "provider/fetching:1.0.0"} {
		writeFixtureBytes(t, filepath.Join(pluginDir, pluginpath.PluginLeaf(ref)), guestBytes)
	}

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: seam-app
  version: "1.0.0"
spec:
  components:
    app:
      type: "demo/fetcher:1.0.0"
`)

	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: seam-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/fetcher: demo-fetcher-synthesized
`)

	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: seam-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    app:
      directory: ./app
      components:
        app: {}
`)

	return releasePath
}

// seedPinnedExternalInput primes the Release's lockfile — the pull is the only
// writer of closure pins — and then records the fixture's external input in it,
// seeding the store with the exact bytes the pin names.
//
// The two halves are separate because the pull resolves the closure and the
// acquire records external inputs, and this fixture needs both without reaching
// the network for either.
func seedPinnedExternalInput(t *testing.T, root, releasePath string, cacheDir string) {
	t.Helper()

	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
	}); err != nil {
		t.Fatalf("prime the lockfile via pull: %v", err)
	}

	lockPath := lockfile.PathFor(releasePath)
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the primed lockfile: %v", err)
	}
	if lf == nil {
		t.Fatal("the pull wrote no lockfile, so nothing can pin the external input")
	}

	body := []byte(seamFixtureBody)
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	blobDir := filepath.Join(externalinputs.StoreRoot(cacheDir), "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatalf("mkdir store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, digest), body, 0o644); err != nil {
		t.Fatalf("seed store blob: %v", err)
	}

	lf.ExternalInputs = []lockfile.ExternalInput{{
		URL:        seamFixtureURL,
		Sha256:     digest,
		Size:       int64(len(body)),
		RequiredBy: []string{"fetching"},
	}}
	if err := lockfile.WriteLockfile(lockPath, lf); err != nil {
		t.Fatalf("write the lockfile carrying the external-input pin: %v", err)
	}
}

// readRenderedTree reads every file under dir into an output-relative
// path→content map, so three surfaces writing to three roots compare on the
// names the render produced rather than on where each surface put them.
func readRenderedTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("read rendered tree %s: %v", dir, err)
	}
	return out
}

// renderedFileNamed returns the content of the one emitted file whose name ends
// in the fixture's output name, and fails when the render produced no such file.
// Matching on the suffix rather than the whole path keeps the assertion about the
// bytes rather than about a boundary's directory layout.
func renderedFileNamed(t *testing.T, tree map[string]string, surface string) string {
	t.Helper()
	var got []string
	for name, content := range tree {
		got = append(got, name)
		if strings.HasSuffix(name, seamFixtureFile) {
			return content
		}
	}
	sort.Strings(got)
	t.Fatalf("%s emitted no %s; it emitted %v", surface, seamFixtureFile, got)
	return ""
}

// TestExternalInputSeamRendersIdenticalBytesOnEverySurface is the byte proof. It
// renders one Release through all three surfaces' real entry points and compares
// the emitted bytes, over a provider guest whose render requests an external
// input — the one shape under which the seam is observable at all.
func TestExternalInputSeamRendersIdenticalBytesOnEverySurface(t *testing.T) {
	// Not parallel: spawns a daemon subprocess, and chdirs for the command line.
	root := t.TempDir()

	// The workspace is written BEFORE the chdir, because building the guest walks
	// up from the working directory to find the umbrella checkout and a temporary
	// directory has no checkout above it.
	releasePath := writeFetchingProviderWorkspace(t, root)

	// And the chdir comes before ANY cache-dir resolution. The resolved artifact
	// cache directory is workspace-relative, so resolving it from the test's own
	// working directory would seed the pinned blob beside this source file instead
	// of inside the fixture — and every surface would then miss the pin and reach
	// for the network. The daemon subprocess is anchored the same way, by its
	// working directory.
	t.Chdir(root)

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	seedPinnedExternalInput(t, root, releasePath, ws.Config().ResolvedArtifactCacheDir())

	want := seamFixtureServedContent()

	// ---- The negative control, FIRST, because everything below is worthless
	// without it. The same workspace rendered with the seam explicitly absent
	// must produce DIFFERENT bytes; if it does not, the fixture cannot tell a
	// seam from no seam and the three-way agreement below means nothing.
	absent := renderWithoutSeam(t, root, releasePath, ws)
	if absent == want {
		t.Fatalf("a render with NO external-input seam produced the same bytes as one with a seam;\n"+
			"the fixture cannot see the difference, so the parity assertions below prove nothing.\ngot %q", absent)
	}
	t.Logf("negative control: with no seam the render emits %q", absent)

	// ---- The tool protocol, first among the surfaces because it writes nothing.
	toolContent := renderThroughToolProtocol(t, root, releasePath)

	// ---- The command line.
	if err := dispatchBuild(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, nil, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("the command line's build failed: %v", err)
	}
	cliTree := readRenderedTree(t, filepath.Join(root, "dist"))
	cliContent := renderedFileNamed(t, cliTree, "the command line")
	if err := os.RemoveAll(filepath.Join(root, "dist")); err != nil {
		t.Fatalf("clear the command line's output before the daemon writes its own: %v", err)
	}

	// ---- The editor daemon, over its real wire protocol in a spawned process.
	serveTree := renderThroughServeDaemon(t, root)
	serveContent := renderedFileNamed(t, serveTree, "the editor daemon")

	// ---- The three-way comparison.
	for _, c := range []struct {
		surface string
		got     string
	}{
		{"the command line", cliContent},
		{"the editor daemon", serveContent},
		{"the tool protocol", toolContent},
	} {
		if c.got != want {
			t.Errorf("%s rendered different bytes than the seam should produce:\n got %q\nwant %q",
				c.surface, c.got, want)
		}
	}
	if cliContent != toolContent {
		t.Errorf("the command line and the tool protocol disagree — the defect this test exists for:\n cli %q\ntool %q",
			cliContent, toolContent)
	}
	if cliContent != serveContent {
		t.Errorf("the command line and the editor daemon disagree:\n   cli %q\nserve %q", cliContent, serveContent)
	}
}

// renderWithoutSeam renders the same workspace through the shared computation
// with the no-seam sentinel named, and returns the emitted file's content.
//
// It is the discriminator, and it reproduces exactly the condition the tool
// protocol was in before the seam was made a required input: no service
// attached, so the guest's fetch comes back as a not-available envelope and the
// render carries on with different bytes. Without this, three surfaces all
// rendering that same envelope would satisfy every parity assertion.
func renderWithoutSeam(t *testing.T, root, releasePath string, ws *workspaceconfig.ResolvedWorkspace) string {
	t.Helper()

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load the lockfile for the no-seam render: %v", err)
	}
	cacheDir := ws.Config().ResolvedArtifactCacheDir()
	view := catalog.NewLazyLookup(root, cacheDir, []string{filepath.Join(root, "catalog")}, nil)
	view.SetPins(catalog.PinsFromLockfile(lf))

	preview, err := compilerops.Preview(
		context.Background(),
		compilerops.NewPreviewInputs(
			compilerops.NoExternalInputs(),
			view,
			releasePath,
			root,
			ws.Config(),
			"test",
			compilerops.WithLockfile(lf, true),
		),
		compilerops.ArtifactPolicy(),
		pipeline.Options{},
	)
	if err != nil {
		t.Fatalf("the no-seam render failed; it is supposed to SUCCEED with different bytes, "+
			"which is what makes it a discriminator rather than an error: %v", err)
	}
	for name, content := range preview.Pipeline.Artifacts {
		if strings.HasSuffix(name, seamFixtureFile) {
			return string(content)
		}
	}
	t.Fatalf("the no-seam render emitted no %s", seamFixtureFile)
	return ""
}

// renderThroughToolProtocol drives the tool protocol's real message loop and
// returns the content of the file its build tool reported.
func renderThroughToolProtocol(t *testing.T, root, releasePath string) string {
	t.Helper()

	var stdin, stdout, stderr bytes.Buffer
	for _, msg := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "seam-parity", "version": "1"},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name": "build",
			"arguments": map[string]any{
				"release":         releasePath,
				"workspace_root":  root,
				"response_format": "detailed",
			},
		}},
	} {
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal tool request: %v", err)
		}
		stdin.Write(raw)
		stdin.WriteByte('\n')
	}

	if err := mcp.Run(mcp.Config{Stdin: &stdin, Stdout: &stdout, Stderr: &stderr, ServerVersion: "test"}); err != nil {
		t.Fatalf("the tool protocol's message loop failed: %v\nstderr:\n%s", err, stderr.String())
	}

	type toolFile struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	type toolPayload struct {
		Files                  []toolFile `json:"files"`
		DeclinedExternalInputs []string   `json:"declined_external_inputs"`
	}
	type toolResult struct {
		ID     int `json:"id"`
		Result *struct {
			IsError           bool         `json:"isError"`
			StructuredContent *toolPayload `json:"structuredContent"`
			Content           []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}

	var payload *toolPayload
	var errText string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var r toolResult
		if json.Unmarshal([]byte(line), &r) != nil || r.ID != 2 || r.Result == nil {
			continue
		}
		if r.Result.IsError {
			if len(r.Result.Content) > 0 {
				errText = r.Result.Content[0].Text
			}
			break
		}
		payload = r.Result.StructuredContent
	}
	if errText != "" {
		t.Fatalf("the tool protocol's build refused: %s", errText)
	}
	if payload == nil {
		t.Fatalf("the tool protocol returned no build payload; the wire carried:\n%s", stdout.String())
	}
	if len(payload.DeclinedExternalInputs) > 0 {
		t.Fatalf("the tool protocol declined %v; the fixture's reference is supposed to be pinned, "+
			"so a decline means the pin and the guest name different references",
			payload.DeclinedExternalInputs)
	}
	for _, f := range payload.Files {
		if strings.HasSuffix(f.Path, seamFixtureFile) {
			return f.Content
		}
	}
	t.Fatalf("the tool protocol emitted no %s; it emitted %+v", seamFixtureFile, payload.Files)
	return ""
}

// renderThroughServeDaemon spawns the real daemon, sends one aggrega/build, and
// returns the tree it wrote. The subprocess is the point: the daemon's own
// startup wiring — workspace discovery, config merge, catalog construction — is
// what a comparison of surfaces has to include, and constructing a Server
// directly would skip exactly that.
func renderThroughServeDaemon(t *testing.T, root string) map[string]string {
	t.Helper()

	bin := buildServeBinary(t)
	cmd := exec.Command(bin, "serve")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	if _, err := stdin.Write(framed(1, "aggrega/initialize", map[string]any{})); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if resp := readOneResponse(t, stdout, 30*time.Second); resp["error"] != nil {
		t.Fatalf("the daemon's initialize failed: %+v", resp)
	}

	if _, err := stdin.Write(framed(2, "aggrega/build", map[string]any{"release": "release.yaml"})); err != nil {
		t.Fatalf("write build: %v", err)
	}
	// The daemon pushes diagnostic notifications alongside its answer; read until
	// the framed message carrying this request's id arrives.
	var result map[string]any
	for i := 0; i < 20; i++ {
		resp := readOneResponse(t, stdout, 120*time.Second)
		if resp["id"] == nil {
			continue
		}
		if e := resp["error"]; e != nil {
			t.Fatalf("the daemon's build refused: %+v", e)
		}
		r, _ := resp["result"].(map[string]any)
		if r != nil {
			result = r
			break
		}
	}
	if result == nil {
		t.Fatal("the daemon wrote no build result")
	}
	outDir, _ := result["outputDirectory"].(string)
	if outDir == "" {
		t.Fatalf("the daemon's build result names no output directory: %+v", result)
	}
	return readRenderedTree(t, outDir)
}

// writeFixtureBytes writes binary content to path, creating parent directories as
// needed. It is the byte-oriented companion to writeFixture, for a staged .wasm
// guest whose bytes are not text.
func writeFixtureBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
