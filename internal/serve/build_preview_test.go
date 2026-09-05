// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// What this file can and cannot reach, said once so no assertion below has to be
// read as stronger than it is.
//
// The build preview renders under the posture of a surface that emits an
// artifact, so every gate is on and a drifted lockfile halts it. No fixture in
// this package carries a lockfile that matches its own closure — each writes a
// minimal one and relies on the graph inspection tolerating the divergence — and
// producing one means running the dependency-resolution pass, which lives above
// this package. A lockfile derived here from the closure the preview just
// computed would agree with the implementation by construction and could no
// longer witness anything.
//
// So the assertions here are about the REFUSING path and the pure conversions,
// and the discriminating one is that this method refuses precisely where the
// graph inspection draws. The rendering half is covered where it belongs: the
// shared computation's own tests run it in this exact posture over a fixture
// workspace, produce artifacts and assert that not one byte of the workspace
// moved.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// buildPreviewResponse is the decoded shape a buildPreview request answers with:
// the result on success, the envelope on a refusal. Both are read from one decode
// so a test can assert on whichever arrived without guessing first.
type buildPreviewResponse struct {
	Result *buildPreviewResult `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// driveBuildPreview runs ONE aggrega/buildPreview request to completion at
// pool=1 over the workspace at ws against the supplied server, and returns the
// decoded response together with everything the handler put on the wire — the
// diagnostic notifications included, which is where its positioned findings
// travel.
func driveBuildPreviewOn(t *testing.T, srv *Server, releaseRel string) (buildPreviewResponse, string) {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/buildPreview", buildPreviewParams{Release: releaseRel}))
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

	pushed := out.String()
	r := bufio.NewReader(strings.NewReader(pushed))
	for {
		frame, rerr := readMessage(r)
		if rerr != nil {
			t.Fatalf("no buildPreview response was written; the wire carried:\n%s", pushed)
		}
		var probe struct {
			ID *int `json:"id"`
		}
		if json.Unmarshal(frame, &probe) != nil || probe.ID == nil {
			// A server-push notification, not the answer to this request.
			continue
		}
		var resp buildPreviewResponse
		if uerr := json.Unmarshal(frame, &resp); uerr != nil {
			t.Fatalf("decode response: %v (body %s)", uerr, frame)
		}
		return resp, pushed
	}
}

// driveBuildPreview is driveBuildPreviewOn over a server built for ws.
func driveBuildPreview(t *testing.T, ws, releaseRel string) (buildPreviewResponse, string) {
	t.Helper()
	var in, out bytes.Buffer
	return driveBuildPreviewOn(t, newPinIsolationServer(t, ws, &in, &out, 1), releaseRel)
}

// TestBuildPreviewRefusesWhereTheGraphInspectionDraws is the discriminating
// assertion for this method's whole reason to exist, and it needs no clock and no
// fixture the package does not already have: one workspace, two requests, two
// different answers.
//
// The graph inspection draws over a workspace whose lockfile no longer matches
// its closure, because a reader left with no graph is worse off than one left
// with a graph and a diagnostic. A build over that same workspace halts, and the
// preview exists to report what a build would do — so it halts too, naming the
// command that refreshes the lockfile. A handler that had reached for the
// inspection posture would have answered with a file list here, which is what
// makes the pair evidence rather than a restatement of the code.
func TestBuildPreviewRefusesWhereTheGraphInspectionDraws(t *testing.T) {
	ws, releaseRel := renderDialWorkspace(t)

	if graph := irGraphWithDial(t, ws, releaseRel, true); len(graph) == 0 {
		t.Fatal("precondition: the graph inspection produced nothing over this workspace, " +
			"so the two answers cannot be compared")
	}

	resp, pushed := driveBuildPreview(t, ws, releaseRel)
	if resp.Error == nil {
		t.Fatalf("the preview answered a workspace a build would refuse; it renders under the "+
			"posture of a surface that emits an artifact, so a divergence halts\nwire:\n%s", pushed)
	}
	if !strings.Contains(resp.Error.Message, "lockfile") {
		t.Errorf("the refusal does not name what diverged: %s", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "aggrega pull") {
		t.Errorf("the refusal does not name the command that resolves it: %s", resp.Error.Message)
	}
}

// TestBuildPreviewReplacesTheBuildDiagnosticLayer proves this handler reaches the
// publication rather than only being able to.
//
// A stale entry is seeded on the build layer first; the run then replaces that
// layer with what it raised, so the stale entry disappears and the client is told
// about the file it disappeared from. Without the publication on the handler's
// path the entry would survive, and every other assertion in this file would pass
// unchanged — which is why the check is on the clearing rather than on an
// advisory the fixture cannot raise.
func TestBuildPreviewReplacesTheBuildDiagnosticLayer(t *testing.T) {
	ws, releaseRel := renderDialWorkspace(t)
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)

	stale := filepath.Join(ws, "app-agg.yaml")
	srv.setRenderDiagnostics(map[string][]Diagnostic{
		stale: {{Line: 1, Column: 1, Severity: editorSeverityWarning, Message: "an advisory from an earlier render"}},
	})
	if got := srv.snapshotDiagnostics(stale); len(got) != 1 {
		t.Fatalf("precondition: the seeded advisory is not on the layer; got %d", len(got))
	}

	_, pushed := driveBuildPreviewOn(t, srv, releaseRel)

	if got := srv.snapshotDiagnostics(stale); len(got) != 0 {
		t.Errorf("the earlier render's advisory survived this run; the layer is replaced whole, "+
			"so a finding that stopped firing must disappear. Still present: %v", got)
	}
	if !strings.Contains(pushed, "aggrega/publishDiagnostics") {
		t.Errorf("the run pushed no diagnostic notification, so a client is never told the "+
			"advisory was cleared\nwire:\n%s", pushed)
	}
}

// TestBuildPreviewWritesNothingOnTheRefusingPath brackets a real run with a tree
// comparison. It covers the path this package can drive; the rendering path's
// write-freedom is a property of the shared computation and is asserted there,
// over a fixture whose render produces artifacts.
func TestBuildPreviewWritesNothingOnTheRefusingPath(t *testing.T) {
	ws, releaseRel := renderDialWorkspace(t)

	before := treeSnapshot(t, ws)
	// An empty snapshot would make every comparison below vacuous, so the
	// instrument is checked before its reading is trusted.
	if len(before) == 0 {
		t.Fatal("precondition: the workspace snapshot is empty, so a write could not be observed")
	}
	if resp, _ := driveBuildPreview(t, ws, releaseRel); resp.Error == nil && resp.Result == nil {
		t.Fatal("precondition: the preview answered with neither a result nor an envelope, " +
			"so it proves nothing about writes")
	}
	after := treeSnapshot(t, ws)

	for path, sum := range after {
		prev, ok := before[path]
		switch {
		case !ok:
			t.Errorf("the preview created %s; it renders a build's artifacts and persists none of them", path)
		case prev != sum:
			t.Errorf("the preview rewrote %s (%s -> %s)", path, prev, sum)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("the preview removed %s", path)
		}
	}
}

// TestBuildPreviewRefusesAWorkspaceWithNoLockfile asserts the gate fires on this
// method's own path rather than only on the graph inspection's. A build renders
// against pinned dependencies and this daemon fills no lockfile, so the absence
// is named together with the command that generates one.
func TestBuildPreviewRefusesAWorkspaceWithNoLockfile(t *testing.T) {
	ws, releaseRel := renderDialWorkspace(t)
	if err := os.Remove(lockfile.PathFor(filepath.Join(ws, releaseRel))); err != nil {
		t.Fatalf("remove lockfile: %v", err)
	}

	resp, pushed := driveBuildPreview(t, ws, releaseRel)
	if resp.Error == nil {
		t.Fatalf("the preview answered a workspace with no lockfile instead of refusing\nwire:\n%s", pushed)
	}
	if resp.Error.Code != errLockfileAbsent {
		t.Errorf("the refusal carries code %d, not the lockfile-absent code %d: %s",
			resp.Error.Code, errLockfileAbsent, resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "aggrega pull") {
		t.Errorf("the refusal does not name the command that generates a lockfile: %s", resp.Error.Message)
	}
}

// TestBuildPreviewRefusesAnUnreadableLockfileWhereTheInspectionCarriesOn is the
// second place the two postures part, and it was measured rather than reasoned
// about: over a workspace whose lockfile is present but unreadable, the command
// line's build stops at the load failure while the graph inspection draws its
// graph with no pins at all.
//
// A preview that took the inspection's route there would render a Release the
// build never reaches and report a list of artifacts nobody can obtain — a
// wrong answer rather than a degraded one, and the worse kind because it reads
// exactly like a right one. So the preview stops where the build stops, and it
// uses the loader's own words for the fault.
func TestBuildPreviewRefusesAnUnreadableLockfileWhereTheInspectionCarriesOn(t *testing.T) {
	ws, releaseRel := renderDialWorkspace(t)
	// An empty document is a lockfile the loader refuses: the file exists, so the
	// absent-lockfile gate above passes and the load is what fails.
	if err := os.WriteFile(lockfile.PathFor(filepath.Join(ws, releaseRel)), nil, 0o644); err != nil {
		t.Fatalf("truncate lockfile: %v", err)
	}

	if graph := irGraphWithDial(t, ws, releaseRel, true); len(graph) == 0 {
		t.Fatal("precondition: the graph inspection refused this workspace too, so the " +
			"two surfaces cannot be told apart here")
	}

	resp, pushed := driveBuildPreview(t, ws, releaseRel)
	if resp.Error == nil {
		t.Fatalf("the preview answered over a lockfile it could not read; a build stops at the "+
			"load failure, so the answer would name artifacts no build produces\nwire:\n%s", pushed)
	}
	if resp.Error.Code != errEntityNotResolvable {
		t.Errorf("the refusal carries code %d, not the entity-not-resolvable code %d: %s",
			resp.Error.Code, errEntityNotResolvable, resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "load lockfile") {
		t.Errorf("the refusal does not word the fault the way the command line words it: %s",
			resp.Error.Message)
	}
}

// TestBuildPreviewRefusesAReleaseOutsideTheWorkspace asserts the Release
// parameter's contract is enforced on this method's path. The check has one home;
// what a per-method test establishes is that this method reaches it, which
// nothing about the helper's own correctness shows.
func TestBuildPreviewRefusesAReleaseOutsideTheWorkspace(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		release string
		fault   string
	}{
		{"absolute", "/etc/release.yaml", "absolute path"},
		{"escaping", "../../elsewhere/release.yaml", "outside the workspace root"},
		{"empty", "", "named no Release"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp, pushed := driveBuildPreview(t, t.TempDir(), tc.release)
			if resp.Error == nil {
				t.Fatalf("the preview accepted %q instead of refusing it\nwire:\n%s", tc.release, pushed)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("the refusal carries code %d, not invalid-params %d: %s",
					resp.Error.Code, jsonRPCInvalidParams, resp.Error.Message)
			}
			if !strings.Contains(resp.Error.Message, tc.fault) {
				t.Errorf("the refusal does not name the fault %q: %s", tc.fault, resp.Error.Message)
			}
		})
	}
}

// TestNoticeClassNamesEveryAdvisorySurface walks every class the shared
// computation raises and pins the wire name each one carries, plus the answer for
// a class this daemon does not name.
//
// The unknown row is the one worth having: a nearest match there would tell a
// client the advisory came from a surface it did not come from, which is worse
// than telling it nothing.
func TestNoticeClassNamesEveryAdvisorySurface(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		class compilerops.WarningClass
		want  string
	}{
		{"deprecation", compilerops.WarningDeprecation, noticeClassDeprecation},
		{"catalog", compilerops.WarningCatalog, noticeClassCatalog},
		{"catalog lint", compilerops.WarningCatalogLint, noticeClassCatalogLint},
		{"extends", compilerops.WarningExtends, noticeClassExtends},
		{"a class this daemon does not name", compilerops.WarningClass(-1), ""},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := noticeClass(tc.class); got != tc.want {
				t.Errorf("noticeClass(%v) = %q, want %q", tc.class, got, tc.want)
			}
		})
	}
}

// TestPreviewNoticesCarryEveryWarningInSequence asserts the conversion keeps
// every advisory and keeps them in the order the computation raised them. That
// order is what the preview's own gate sequence defines, so a reader meets a
// finding where the run found it rather than in an order this layer chose.
func TestPreviewNoticesCarryEveryWarningInSequence(t *testing.T) {
	t.Parallel()
	preview := &compilerops.PreviewResult{Warnings: []compilerops.Warning{
		{Class: compilerops.WarningExtends, Text: "a broken parent"},
		{Class: compilerops.WarningDeprecation, Text: "a withdrawn pin"},
		{Class: compilerops.WarningCatalogLint, Text: "an overlapping pillar"},
	}}
	got := previewNotices(preview)
	want := []buildPreviewNotice{
		{Class: noticeClassExtends, Text: "a broken parent"},
		{Class: noticeClassDeprecation, Text: "a withdrawn pin"},
		{Class: noticeClassCatalogLint, Text: "an overlapping pillar"},
	}
	if len(got) != len(want) {
		t.Fatalf("the conversion carried %d of %d advisories: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("advisory %d is %+v, want %+v", i, got[i], want[i])
		}
	}
	if previewNotices(nil) != nil {
		t.Error("a preview that raised nothing must carry no advisories")
	}
	if previewNotices(&compilerops.PreviewResult{}) != nil {
		t.Error("a preview with an empty advisory set must carry no advisories")
	}
}

// TestRenderedArtifactNamesAreSorted pins the two answers the artifact-name walk
// owes: one sorted sequence for a result that rendered, and nothing for a run
// that halted before it could.
//
// The input is deliberately not in sorted order. Go randomises map iteration, so
// a walk that reported range order would name the same build differently on every
// call and a reader could not diff two previews of one workspace.
func TestRenderedArtifactNamesAreSorted(t *testing.T) {
	t.Parallel()
	if got := renderedArtifactNames(nil); got != nil {
		t.Errorf("a halted run has no artifacts to name; got %v", got)
	}
	result := &pipeline.Result{Artifacts: map[string][]byte{
		"boundaries/web/service.yaml": []byte("b"),
		"boundaries/api/deploy.yaml":  []byte("a"),
		"boundaries/web/deploy.yaml":  []byte("c"),
	}}
	want := []string{
		"boundaries/api/deploy.yaml",
		"boundaries/web/deploy.yaml",
		"boundaries/web/service.yaml",
	}
	got := renderedArtifactNames(result)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the artifact names are not sorted:\n got %v\nwant %v", got, want)
	}
}
