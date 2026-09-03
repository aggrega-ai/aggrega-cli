// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The inspection renders through a frozen external-input seam, and the two
// halves of that are proved separately here because they fail independently.
//
// The first half is the seam: a pinned reference is SERVED from the
// content-addressed store, which is what makes the rendered bytes the bytes a
// build produces — without a seam attached the guest receives a not-available
// envelope and renders something else. The second half is the honesty: an
// unpinned reference is declined, and the decline is REPORTED, because a render
// that quietly stopped short of the bytes a build would emit is
// indistinguishable to the reader from an accurate one.
//
// One fault per fixture, asserted against the condition that fault raises. Each
// absence assertion carries a needle, because a zero is a claim about the
// instrument until something shows the instrument fires.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// pinnedInputWorkspace seeds a workspace artifact cache with one external input
// pinned into the content-addressed store, and returns the cache directory, the
// lockfile pinning it, and the exact bytes the store holds.
//
// The blob is written under the digest the service will recompute, so a read
// that verifies against the record succeeds. Size is recorded too: the service
// checks it, so a record carrying the wrong one fails for the wrong reason.
func pinnedInputWorkspace(t *testing.T, url string, body []byte) (cacheDir string, lf *lockfile.Lockfile) {
	t.Helper()
	cacheDir = filepath.Join(t.TempDir(), ".aggrega", "cache")
	sum := sha256.Sum256(body)
	hexDigest := hex.EncodeToString(sum[:])

	blobDir := filepath.Join(externalinputs.StoreRoot(cacheDir), "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatalf("mkdir store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, hexDigest), body, 0o644); err != nil {
		t.Fatalf("seed store blob: %v", err)
	}
	return cacheDir, &lockfile.Lockfile{
		Version: 1,
		ExternalInputs: []lockfile.ExternalInput{{
			URL:        url,
			Sha256:     hexDigest,
			Size:       int64(len(body)),
			RequiredBy: []string{"test"},
		}},
	}
}

// storeEntries lists the store's blob names, so an assertion about what a Fetch
// wrote names what appeared rather than reporting a count.
func storeEntries(t *testing.T, cacheDir string) []string {
	t.Helper()
	dir := filepath.Join(externalinputs.StoreRoot(cacheDir), "sha256")
	items, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read store: %v", err)
	}
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Name())
	}
	return names
}

// TestFrozenSeamServesAPinnedReference is the byte half. A pinned reference must
// come back as the store's exact bytes, because that is the whole mechanism by
// which the inspection's render produces what a build produces: the alternative
// is no seam, and no seam means a not-available envelope in place of the
// content.
//
// The assertion is on the bytes rather than on the absence of an error. A seam
// that returned no error and empty content would satisfy the weaker check while
// rendering something a build never would.
func TestFrozenSeamServesAPinnedReference(t *testing.T) {
	t.Parallel()
	const url = "https://example.com/chart-values.yaml"
	body := []byte("replicaCount: 3\nimage: demo:1.0.0\n")
	cacheDir, lf := pinnedInputWorkspace(t, url, body)

	seam := frozenExternalInputs(cacheDir, lf)
	got, err := seam.Fetch(context.Background(), url, "test-node")
	if err != nil {
		t.Fatalf("a pinned reference must be served from the store: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("served bytes differ from the pinned blob:\n got %q\nwant %q", got, body)
	}
	// Nothing was declined, so the inspection owes no divergence note. This is the
	// negative control for the reporting half below: without it, a note published
	// unconditionally would still pass every assertion there.
	if declined := seam.Declined(); len(declined) != 0 {
		t.Errorf("a served reference must not be recorded as declined; got %v", declined)
	}
	if diags := externalInputDivergenceDiagnostics(seam.Declined(), "release.yaml"); len(diags) != 0 {
		t.Errorf("a fully-pinned render must publish no divergence advisory; got %d", len(diags))
	}
}

// TestFrozenSeamDeclinesAnUnpinnedReference is the fault the reporting half
// exists for, pinned to the condition that raises it rather than to any message
// text: the text is presentation and free to change, while the condition is what
// the refusal IS.
//
// The fixture pins one reference and requests a DIFFERENT one, so the single
// fault is "this reference is not pinned" and nothing else — a store with no
// pins at all would also fail a reader's expectation that the store loads.
func TestFrozenSeamDeclinesAnUnpinnedReference(t *testing.T) {
	t.Parallel()
	const pinned = "https://example.com/pinned.yaml"
	const unpinned = "https://example.com/not-pinned.yaml"
	cacheDir, lf := pinnedInputWorkspace(t, pinned, []byte("pinned: true\n"))

	seam := frozenExternalInputs(cacheDir, lf)
	_, err := seam.Fetch(context.Background(), unpinned, "test-node")
	if err == nil {
		t.Fatal("an unpinned reference must refuse loudly under a frozen closure")
	}
	if !errors.Is(err, externalinputs.ErrAcquisitionDisabled()) {
		t.Fatalf("the refusal must be the frozen-closure condition, not some other failure: %v", err)
	}
	declined := seam.Declined()
	if len(declined) != 1 || declined[0] != unpinned {
		t.Fatalf("Declined() = %v; want exactly the unpinned reference", declined)
	}
	// The needle for the assertion above: the pinned reference on the SAME seam
	// still serves, so "declined" is a fact about that reference rather than about
	// a seam that refuses everything.
	if _, err := seam.Fetch(context.Background(), pinned, "test-node"); err != nil {
		t.Fatalf("the pinned reference on the same seam must still serve: %v", err)
	}
}

// TestFrozenSeamWritesNothingAndCannotReachTheNetwork holds the two standing
// promises of the inspection's seam, and keeps them at the scope they are made
// at rather than a broader one.
//
// The claim is about the external-input store: neither serving a pinned
// reference nor declining an unpinned one adds a blob to it. It deliberately
// does not assert that the render as a whole writes nothing anywhere — the
// render populates a compiled-guest cache, so a broadened claim would be false
// the moment a reader checked that directory.
//
// The network half is structural rather than observed: the service is
// constructed with acquisition off AND a nil transport, which are independent
// switches, so a later edit turning acquisition back on would still not reach
// the network. Asserting the declined outcome is what shows the closed path.
func TestFrozenSeamWritesNothingAndCannotReachTheNetwork(t *testing.T) {
	t.Parallel()
	const pinned = "https://example.com/pinned.yaml"
	const unpinned = "https://example.com/not-pinned.yaml"
	body := []byte("pinned: true\n")
	cacheDir, lf := pinnedInputWorkspace(t, pinned, body)

	before := storeEntries(t, cacheDir)
	if len(before) != 1 {
		t.Fatalf("precondition: the store must hold exactly the seeded blob; got %v", before)
	}

	seam := frozenExternalInputs(cacheDir, lf)
	if _, err := seam.Fetch(context.Background(), pinned, "n"); err != nil {
		t.Fatalf("serve a pinned reference: %v", err)
	}
	if _, err := seam.Fetch(context.Background(), unpinned, "n"); err == nil {
		t.Fatal("the unpinned reference must refuse, or the write assertion below covers nothing")
	}

	after := storeEntries(t, cacheDir)
	if strings.Join(after, ",") != strings.Join(before, ",") {
		t.Errorf("the frozen seam changed the store:\nbefore %v\nafter  %v", before, after)
	}
}

// TestDivergenceAdvisoryNamesTheReferenceAndTheRemedy pins what the reader
// actually receives. A note saying only that something diverged leaves them
// unable to act; the reference and the remedy are what make it a repair
// instruction rather than a warning.
//
// The code is asserted to be the registered one read off the condition, not a
// literal, so the editor's advisory and a terminal's refusal name one identity.
func TestDivergenceAdvisoryNamesTheReferenceAndTheRemedy(t *testing.T) {
	t.Parallel()
	const url = "https://example.com/not-pinned.yaml"
	diags := externalInputDivergenceDiagnostics([]string{url}, "app-release.yaml")
	if len(diags) != 1 {
		t.Fatalf("one declined reference must produce one advisory; got %d", len(diags))
	}
	d := diags[0]
	for _, want := range []string{url, "cannot show what a build would emit", "aggrega acquire", "app-release.yaml"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("the advisory does not carry %q:\n%s", want, d.Message)
		}
	}
	wantCode, ok := cerrors.CodeOf(externalinputs.ErrAcquisitionDisabled())
	if !ok {
		t.Fatal("the frozen-closure condition carries no registered code; the advisory has nothing to name")
	}
	if d.Code != wantCode {
		t.Errorf("Code = %q; want the condition's registered code %q", d.Code, wantCode)
	}
	if strings.Contains(d.Message, wantCode) {
		t.Errorf("the code is embedded in the message; it travels as its own key and nowhere else:\n%s", d.Message)
	}
	if d.Severity != editorSeverityWarning {
		t.Errorf("Severity = %q; want warning — the graph and the partial render are still worth reading",
			d.Severity)
	}
	// One advisory per reference, so the reader learns WHICH references are
	// missing rather than only that some are.
	many := externalInputDivergenceDiagnostics([]string{"https://a.example/x", "https://b.example/y"}, "r.yaml")
	if len(many) != 2 {
		t.Errorf("two declined references must produce two advisories; got %d", len(many))
	}
}

// TestDivergenceAdvisoryReachesTheEditor drives the real publication path and
// reads the pushed notification off the wire, so the claim is that the advisory
// reaches a client rather than that a constructor built one.
//
// It publishes a render advisory ALONGSIDE the divergence note in one call,
// which is the trap this wiring has to survive: the publication replaces the
// whole render layer, so a second call for the second class would erase the
// first. Both classes are asserted present in the composed answer.
func TestDivergenceAdvisoryReachesTheEditor(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	var pushed bytes.Buffer
	ctx, flush := serializerContext(t, &pushed)
	const file = "/w/app-agg.yaml"
	const url = "https://example.com/not-pinned.yaml"

	s.publishRenderDiagnostics(ctx, file,
		[]renderer.Diagnostic{{Severity: abi.SeverityWarn, Message: "a render advisory"}},
		externalInputDivergenceDiagnostics([]string{url}, "app-release.yaml"))
	flush()

	got := s.snapshotDiagnostics(file)
	if len(got) != 2 {
		t.Fatalf("the layer must carry both advisory classes; got %d: %v", len(got), messages(got))
	}
	var sawRender, sawDivergence bool
	for _, d := range got {
		if strings.Contains(d.Message, "a render advisory") {
			sawRender = true
		}
		if strings.Contains(d.Message, url) {
			sawDivergence = true
		}
	}
	if !sawRender {
		t.Error("the render advisory was erased; both classes must survive one publication")
	}
	if !sawDivergence {
		t.Error("the divergence note was erased; both classes must survive one publication")
	}
	// The notification actually went out, so the assertions above are about what a
	// client receives and not only about server-side state.
	body := pushed.String()
	if !strings.Contains(body, url) {
		t.Errorf("the pushed notification does not name the declined reference\npushed: %s", body)
	}
}

// TestIrGraphPreviewInputsCarryTheSeam is the invocation guard, and it is here
// because every other assertion in this file would survive the one edit that
// undoes the whole change: removing the seam from what the inspection hands the
// shared computation.
//
// The seam's effect on rendered bytes is observable only through a provider
// guest that requests an external input, and the fixture corpus has none — so
// there is no behavioural assertion available for the attachment itself, and a
// gate would report nothing if the field went missing. This asserts the
// production construction site carries it, which is the strongest guard the
// absent fixture leaves available. It is deliberately structural, and it says so
// rather than reading as a behavioural proof.
//
// The lockfile is nil on purpose: the daemon serves Releases with no lockfile,
// and the seam is owed on that path too — a construction that attached one only
// when a lockfile happened to be present would leave the commoner case
// producing divergent bytes.
func TestIrGraphPreviewInputsCarryTheSeam(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)

	for _, tc := range []struct {
		name string
		lf   *lockfile.Lockfile
	}{
		{name: "no lockfile", lf: nil},
		{name: "a lockfile pinning nothing", lf: &lockfile.Lockfile{Version: 1}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			seam := srv.irGraphExternalInputs(tc.lf)
			if seam == nil {
				t.Fatal("the inspection built no external-input seam")
			}
			in := srv.irGraphPreviewInputs(srv.lazyCatalog.ForRequest(nil), "release.yaml", tc.lf, seam)
			if in.ExternalInputs == nil {
				t.Fatal("the inspection hands the shared computation no external-input seam; " +
					"a guest asking for an external input would receive a not-available envelope " +
					"and render different bytes than a build, with nothing said about it")
			}
			if in.ExternalInputs != seam {
				t.Error("the attached seam is not the one the inspection built, so what was proved " +
					"about the frozen seam does not describe what the render receives")
			}
		})
	}
}

// TestIrGraphAttachesTheFrozenSeam is the end-to-end half: it drives the real
// handleIrGraph with the render dial ON over a workspace whose render executes a
// guest to completion, and asserts the run still produces a graph with the seam
// attached.
//
// What it establishes and what it does not, stated because the distinction
// matters: it shows the attachment is on the production path and perturbs
// nothing for a Release requesting no external input. It does NOT show a fetched
// input's bytes reaching an artifact, because no provider guest in the fixture
// corpus requests an external input during a render — that comparison needs a
// fetching provider fixture this package does not have.
func TestIrGraphAttachesTheFrozenSeam(t *testing.T) {
	ws, releaseRel := renderDialWorkspace(t)
	graph := irGraphWithDial(t, ws, releaseRel, true)
	if len(graph) == 0 {
		t.Fatal("the dial-on inspection produced no graph, so the seam attachment proved nothing")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(graph, &probe); err != nil {
		t.Fatalf("the graph is not a JSON object: %v", err)
	}
	if len(probe) == 0 {
		t.Fatal("the graph is empty; the render did not run to completion")
	}
}
