// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Three surfaces render a Release: the command line, the editor daemon, and the
// tool protocol. All three run one preview computation. The guarantee they owe a
// caller is that ONE Release built through any of them emits the SAME artifact
// set, byte for byte — and every completeness check the product has asserts
// something adjacent to that: that each path exists, that each runs the same
// gates, that each name lines up. None of them compares the bytes.
//
// This file is that comparison. It compares the emitted artifact set as a SET
// first and as content second, because a walk over only the files both sides
// produced passes when one side emits FEWER — which is precisely the live shape
// of a surface that renders around missing content instead of failing.
//
// # Why this fixture, and not a simpler one
//
// The computation installs a render-time external-input seam, and a surface that
// hands it no seam renders a not-available envelope where the content should be.
// That divergence is observable only through a provider guest whose render
// REQUESTS an external input; the fixture corpus had none, so
// internal/wasm/testdata/fetching-provider is built here for it. Under any other
// provider fixture, a surface with a seam and a surface without one emit
// identical bytes, and the comparison below would be green and worthless.
//
// The negative control is the load-bearing half. Three surfaces agreeing proves
// nothing on its own: three surfaces each rendering the same not-available
// envelope agree perfectly. So the same workspace is also rendered with the seam
// explicitly absent, and that render must produce DIFFERENT bytes — which is what
// establishes the fixture can tell the two apart at all.
//
// # The differences between the surfaces that legitimately survive, each named
//
// The three do not do identical work around the render, and a difference this
// comparison silently normalised away would be a divergence it was built to hide.
// So every one of them is named here with its reason, and the comparison is over
// the rendered artifact CONTENT alone:
//
//   - WHERE THE OUTPUT GOES. The command line writes the default output directory
//     under its working directory; the daemon writes the same directory name
//     resolved against the workspace it opened at startup; the tool protocol
//     writes nothing and returns buffers. Compared on OUTPUT-RELATIVE names,
//     because the destination is each surface's own choice about where to put a
//     render and is not an input to the render.
//
//   - THE LOCKFILE DRIFT VERIFY. The command line and the daemon run it; the tool
//     protocol holds it off — the one artifact-posture gate that differs. Its
//     reason is that surface's lockfile posture rather than a view about drift:
//     it renders a Release that may have no lockfile at all, and refusing a
//     partial lockfile while rendering an absent one would make less pinning
//     information the more permissive input. The gate halts or does not; a run
//     that does not halt renders identically, so it reaches no emitted byte. This
//     fixture pins a complete lockfile, so no surface's verify has anything to
//     refuse and all three reach the render.
//
//   - THE SEAM'S ACQUISITION POSTURE. The command line's default seam ACQUIRES an
//     unpinned reference; the daemon's and the tool protocol's are FROZEN and
//     decline one. For a fully-pinned Release all three serve the reference from
//     the content-addressed store and the bytes are identical — which is why the
//     fixture pins its reference before any surface runs, and why a decline on the
//     tool protocol is treated as a fixture fault below rather than tolerated.
//
//   - THE CATALOG ADVISORIES. Only the command line eagerly loads a whole catalog,
//     so only it hands the computation that advisory set. Advisories are reported,
//     never rendered; they reach no emitted byte.
//
//   - THE PROGRESS AND COMPILE CALLBACKS. Only the command line sets them. Each is
//     documented as writing to a diagnostic sink and carrying no authority over
//     the render.
//
// # The compiler version, which would otherwise flake this
//
// The rendered output can carry a compiler-version value, so two renders from
// differently-stamped binaries are not comparable. In production all three
// surfaces read it from one derivation, so the question does not arise. Under
// test it very nearly does — the command line's dispatch and the daemon
// subprocess both reach that derivation, while the tool protocol takes its value
// from the caller — so the value is passed explicitly from the same derivation
// and asserted, rather than left to be discovered as an intermittent failure.
//
// 🔴 That single-sourcing is LOAD-BEARING, not belt-and-braces, and the reason is
// measured rather than assumed: handing this surface a deliberately different
// version leaves the comparison below GREEN. The guest here emits its own bytes
// and never reads the value, and no artifact this fixture produces carries the
// provenance labels that would. So the divergence is invisible today and would
// begin flaking the day a fixture emitting those labels joins the comparison.
// Passing one derivation to all three is what closes it in both eras.
//
// # What this comparison structurally CANNOT see
//
// It binds the BYTES. It does not bind the gate posture, and the difference was
// measured rather than reasoned: switching the tool protocol from the
// artifact-producing posture to the inspecting one — every refusing gate off —
// leaves this green, because a gate that refuses nothing reaches no emitted byte
// on a fixture where no gate had anything to refuse. That is correct behaviour
// for a byte comparison and not a hole to plug here: the per-surface gate-parity
// tests bind which gates each surface runs, and the two proofs are complementary.
// Reading a green from this file as "the three surfaces are identical" overstates
// it; what it says is that they emit the same artifacts.

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

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmfixture"
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

	// seamFixtureRevisionFile and seamFixtureRevision are the HOST side of the
	// fixture-revision cross-check: the second file the guest emits, and the stamp
	// its bytes must carry.
	//
	// 🔴 DELIBERATE DUPLICATE, and here the duplication IS the mechanism rather
	// than a language-boundary concession — recorded so it is not "unified" away.
	// A parity test over a shared fixture proves the two sides AGREE, never that
	// both RAN: a guest that compiles and is never invoked reports nothing, and
	// the green then certifies a comparison that never happened. The guest declares
	// its own copy of this stamp and emits it, this side declares another and
	// asserts them equal — so the assertion can only pass if the guest EXECUTED, at
	// the revision this file was written against. One shared constant could not
	// disagree with itself, and disagreement is the entire signal.
	//
	// 🔴 TWO BOUNDS SHIP WITH THIS CHECK AND NEITHER IS A DEFECT TO BE FIXED.
	// First, it cannot catch a runner-level filter: no test observes its own
	// non-execution, so a run that never selected this function reports exactly
	// what a clean tree reports. Second, the two sides' constants and their
	// comparison are deliberate near-duplicates for the reason above — sharing
	// either would blind the cross-check to what it exists to see.
	seamFixtureRevisionFile = "fixture-revision.txt"
	seamFixtureRevision     = "fetching-provider/3"
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
	guestPath := wasmfixture.EnsureLocalGuestBuilt(t, src, "fetching-provider.wasm")

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
func seedPinnedExternalInput(t *testing.T, root, releasePath string, workspaceRoot, cacheDir string) {
	t.Helper()

	if err := pullRun(context.Background(), pullConfig{
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
	blobDir := filepath.Join(externalinputs.StoreRoot(workspaceRoot, cacheDir), "sha256")
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

// surfaceRender is one surface's emitted artifact set, keyed by output-relative
// name, together with the name to call that surface in a failure message.
type surfaceRender struct {
	surface string
	tree    map[string]string
}

// sortedNames returns the artifact names a render emitted, sorted, so a failure
// message reports the same sequence on every run.
func (r surfaceRender) sortedNames() []string {
	names := make([]string, 0, len(r.tree))
	for name := range r.tree {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// compareArtifactSets reports every way two surfaces' emitted artifact sets
// differ: first the KEY SET, then the content of the names both produced.
//
// 🔴 The key set is compared FIRST, and separately, because it is the half a
// naive comparison omits. A walk over only the files both sides produced passes
// when one side emits FEWER — which is the exact live failure shape here, since a
// guest handed no external-input seam renders around the missing content rather
// than failing. So the names missing from each side are named, on the side they
// are missing from, before any byte is looked at.
//
// It reports rather than halting, and reports every difference rather than the
// first: a reader diagnosing a divergence needs the whole shape of it, and a
// comparison that stopped at the first differing file would make a set-level
// divergence look like a content-level one.
func compareArtifactSets(t *testing.T, a, b surfaceRender) {
	t.Helper()

	missing := func(from, against surfaceRender) []string {
		var out []string
		for name := range against.tree {
			if _, ok := from.tree[name]; !ok {
				out = append(out, name)
			}
		}
		sort.Strings(out)
		return out
	}

	if only := missing(a, b); len(only) > 0 {
		t.Errorf("%s emitted %d artifact(s) that %s did not: %v\n  %s emitted %v\n  %s emitted %v",
			b.surface, len(only), a.surface, only,
			a.surface, a.sortedNames(), b.surface, b.sortedNames())
	}
	if only := missing(b, a); len(only) > 0 {
		t.Errorf("%s emitted %d artifact(s) that %s did not: %v\n  %s emitted %v\n  %s emitted %v",
			a.surface, len(only), b.surface, only,
			a.surface, a.sortedNames(), b.surface, b.sortedNames())
	}

	for _, name := range a.sortedNames() {
		want, ok := b.tree[name]
		if !ok {
			continue
		}
		if got := a.tree[name]; got != want {
			t.Errorf("%s and %s rendered different bytes for %s:\n  %s %q\n  %s %q",
				a.surface, b.surface, name, a.surface, got, b.surface, want)
		}
	}
}

// assertFixtureRevision checks the guest's revision stamp reached this surface's
// emitted set and names the revision this file was written against.
//
// It is the half of the cross-check that runs on the host side, and it is
// deliberately NOT shared with the guest's declaration of the same string — the
// constants' comment says why. Passing establishes that the guest EXECUTED on
// this surface at the revision the assertions above were written for; a shared
// fixture on its own establishes only that the surfaces agree.
func assertFixtureRevision(t *testing.T, r surfaceRender) {
	t.Helper()
	for name, content := range r.tree {
		if !strings.HasSuffix(name, seamFixtureRevisionFile) {
			continue
		}
		if content != seamFixtureRevision {
			t.Errorf("%s emitted fixture revision %q, but this test was written against %q; "+
				"the guest and the test that drives it have moved apart, so the comparison above "+
				"is against a contract that no longer holds",
				r.surface, content, seamFixtureRevision)
		}
		return
	}
	t.Errorf("%s emitted no %s, so nothing establishes that the guest ran at the revision this test "+
		"was written against; it emitted %v", r.surface, seamFixtureRevisionFile, r.sortedNames())
}

// TestThreeSurfacesEmitByteIdenticalArtifactSets is the byte proof. It builds one
// Release through all three surfaces' real entry points — the command dispatch, a
// request over the daemon's own wire in a spawned process, and a call through the
// tool dispatch — and asserts the emitted artifact SETS are identical: same names,
// same count, same content.
//
// Driven through the real entry points rather than by calling the shared
// computation three times. A test that calls one function three times proves the
// function is deterministic, which nobody doubts; the property under test is that
// three transports hand it the same inputs.
//
// The file comment above carries what a reader needs before the assertions make
// sense: why this fixture is the only one that can see the seam divergence, every
// difference between the surfaces that legitimately survives, and how the
// compiler-version value is kept from flaking the comparison.
func TestThreeSurfacesEmitByteIdenticalArtifactSets(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Building the guest walks up from the working directory to find this
	// module's root, which the package directory the test runs in sits beneath.
	releasePath := writeFetchingProviderWorkspace(t, root)

	// The command line's render is placed under the fixture root, and the daemon
	// subprocess is anchored there by its own working directory, so both outputs
	// sit where they can be compared below.
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	seedPinnedExternalInput(t, root, releasePath, ws.Root(), ws.Config().ResolvedArtifactCacheDir())

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

	// The compiler-version value every surface renders under. The command line's
	// dispatch and the daemon subprocess each reach this derivation on their own —
	// the dispatch through its own build path, the subprocess because it IS the
	// binary — so naming it here and handing it to the tool protocol is what makes
	// all three stamped identically rather than two of them by luck.
	compilerVersion := provenanceCompilerVersion()
	if compilerVersion == "" {
		t.Fatal("this binary reports no compiler version, so the three surfaces cannot be " +
			"confirmed to render under the same one and any agreement below would be untrustworthy")
	}
	t.Logf("all three surfaces render under compiler version %q", compilerVersion)

	// ---- The tool protocol, first among the surfaces because it writes nothing.
	tool := surfaceRender{"the tool protocol", renderThroughToolProtocol(t, root, releasePath, compilerVersion)}

	// ---- The command line.
	if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, nil, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("the command line's build failed: %v", err)
	}
	cli := surfaceRender{"the command line", readRenderedTree(t, filepath.Join(root, "dist"))}
	if err := os.RemoveAll(filepath.Join(root, "dist")); err != nil {
		t.Fatalf("clear the command line's output before the daemon writes its own: %v", err)
	}

	// ---- The editor daemon, over its real wire protocol in a spawned process.
	serve := surfaceRender{"the editor daemon", renderThroughServeDaemon(t, root)}

	// ---- The artifact SETS, compared pairwise. Names and count first, content
	// second; compareArtifactSets says why that order is the whole point.
	compareArtifactSets(t, cli, tool)
	compareArtifactSets(t, cli, serve)

	// ---- Each surface against the value the seam is supposed to produce, so
	// "the three agree" and "the three are right" are separate findings. Three
	// surfaces agreeing on the WRONG bytes is a state this fixture can reach —
	// the negative control above is exactly that state — and only this catches it.
	for _, r := range []surfaceRender{cli, serve, tool} {
		if got := renderedFileNamed(t, r.tree, r.surface); got != want {
			t.Errorf("%s rendered different bytes than the seam should produce:\n got %q\nwant %q",
				r.surface, got, want)
		}
		assertFixtureRevision(t, r)
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

	preview, err := compilerops.PreviewForArtifact(
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
		pipeline.Options{},
	)
	if err != nil {
		t.Fatalf("the no-seam render failed; it is supposed to SUCCEED with different bytes, "+
			"which is what makes it a discriminator rather than an error: %v", err)
	}
	for name, content := range preview.PipelineOutcome().Completed().Artifacts {
		if strings.HasSuffix(name, seamFixtureFile) {
			return string(content)
		}
	}
	t.Fatalf("the no-seam render emitted no %s", seamFixtureFile)
	return ""
}

// renderThroughToolProtocol drives the tool protocol's real message loop and
// returns the artifact set its build tool reported, keyed by output-relative
// name — the same keying the other two surfaces' on-disk trees produce, so the
// three compare on the names the render produced.
//
// compilerVersion is passed in rather than defaulted because this is the one
// surface whose value comes from its caller: the command line's dispatch and the
// daemon subprocess both derive it themselves, and a value invented here would
// stamp this surface differently from the other two for no reason a reader could
// see.
func renderThroughToolProtocol(t *testing.T, root, releasePath, compilerVersion string) map[string]string {
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

	if err := mcp.Run(context.Background(), mcp.Config{
		Stdin: &stdin, Stdout: &stdout, Stderr: &stderr, ServerVersion: compilerVersion,
	}); err != nil {
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
	// The whole reported set, not the one file the seam moves. A surface that
	// emitted FEWER artifacts is the failure shape the comparison exists to catch,
	// and picking one file out here would discard exactly that evidence before any
	// comparison could see it.
	tree := make(map[string]string, len(payload.Files))
	for _, f := range payload.Files {
		tree[f.Path] = f.Content
	}
	return tree
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
	cmd.Env = builtBinaryEnv(t)
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

	// One ceiling for the handshake read and for the build read below, even
	// though the two answers cost wildly different work: the bound is not a
	// budget for either of them, so sizing it per request would be pricing
	// work the reads do not measure. The build leg is the demanding one — a
	// render that fetches an external input — and the derivation's cap sits
	// well above what that costs, while its floor applies only when the
	// runner's own deadline is closer than the floor itself, at which point
	// the runner ends the package before any longer bound here could fire.
	ceiling := responseLivenessCeiling(t)

	if _, err := stdin.Write(framed(1, "aggrega/initialize", map[string]any{})); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if resp := readOneResponse(t, stdout, ceiling); resp["error"] != nil {
		t.Fatalf("the daemon's initialize failed: %+v", resp)
	}

	if _, err := stdin.Write(framedFor(t, cmd, 2, "aggrega/build", map[string]any{"release": "release.yaml"})); err != nil {
		t.Fatalf("write build: %v", err)
	}
	// The daemon pushes diagnostic notifications alongside its answer; read until
	// the framed message carrying this request's id arrives.
	var result map[string]any
	for i := 0; i < 20; i++ {
		resp := readOneResponse(t, stdout, ceiling)
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
