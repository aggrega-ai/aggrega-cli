// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// What this file proves, and why the expectations are not taken from the answer
// under test.
//
// A build asked to fill its lockfile runs a dependency resolution as its own
// step, and that resolution computes facts: the file it wrote, what it pinned,
// how many external inputs the Release consumes, and whether it could finish
// discovering them. The question here is whether the build's answer carries those
// facts or only the prose the resolution wrote while working.
//
// EVERY EXPECTATION BELOW COMES FROM A SEPARATE CALL TO THE RESOLUTION ITSELF,
// never from the response being checked and never through the projection the
// response is built by. A test that compared the answer against the same
// projection that produced it would agree with any projection at all, including
// one that read the wrong field or returned a constant — which is precisely the
// class of divergence this file exists to catch.
//
// The rows differ in the VALUES they expect, not only in their names. A single
// row would pass against a report that printed constants, so the two rows are
// chosen to disagree: one Release whose render consumes no external input and one
// whose render consumes exactly one. Each row asserts its own population first,
// so a fixture that stopped exercising its case fails as a precondition rather
// than passing vacuously.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
)

// resolutionFacts is what the shared resolution reported, reduced to the scalars
// this file compares. It is built from a pullops.Result directly rather than
// through the wire projection, so the comparison has an independent left side.
type resolutionFacts struct {
	lockfilePath    string
	verified        bool
	entities        int
	sourceArtifacts int
	trustedPlugins  int
	externalInputs  int
	incomplete      string
}

// independentResolutionFacts runs the resolution the daemon runs and reads its
// result directly.
//
// It deliberately calls the shared operation rather than any daemon method: the
// facts are read off pullops.Result field by field here, so a projection that
// dropped or mixed up a field cannot make this side of the comparison agree with
// it. The diagnostic stream is discarded because the prose is not what this file
// is about.
func independentResolutionFacts(t *testing.T, srv *Server, ws, releaseRel string) resolutionFacts {
	t.Helper()
	res, err := pullops.Run(t.Context(), pullops.Config{
		Workspace:       srv.resolvedWorkspace,
		ReleasePath:     filepath.Join(ws, releaseRel),
		CompilerVersion: srv.compilerVersion,
		Toolchain:       srv.toolchain,
		DiagOut:         io.Discard,
	})
	if err != nil {
		t.Fatalf("the independent resolution failed, so this test has no expectation to compare "+
			"against: %v", err)
	}
	facts := resolutionFacts{
		lockfilePath:    res.LockfilePath,
		verified:        res.Verified,
		entities:        len(res.Closure.Entities),
		sourceArtifacts: len(res.Closure.SourceArtifacts),
		trustedPlugins:  res.TrustedPlugins,
		externalInputs:  res.ExternalInputs,
	}
	if res.ExternalInputDiscovery != nil {
		facts.incomplete = res.ExternalInputDiscovery.Error()
	}
	return facts
}

// externalInputFillWorkspace is the external-input fixture with its own endpoint,
// in the shape the table below takes.
//
// The endpoint is started here rather than by the caller so the row is one entry
// rather than a special case: the URL is an implementation detail of the fixture,
// and what the row is about is that the render consumes an external input at all.
func externalInputFillWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	schemaURL, _ := externalInputSchemaServer(t)
	return externalInputFetchWorkspace(t, schemaURL)
}

// refusingExternalInputWorkspace is the same fixture pointed at an endpoint that
// answers every request with a failure, so the discovery render cannot finish.
//
// It exists to reach the one state the succeeding fixtures cannot: a resolution
// that pinned its dependencies and still has something to say about what it could
// not reach. Without it the incomplete-discovery field would only ever be asserted
// empty, and a projection that never filled it would pass every row.
func refusingExternalInputWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return externalInputFetchWorkspace(t, srv.URL+"/values-schema.json")
}

// TestResolutionReportsAnUnfinishedExternalInputDiscovery is the non-empty half of
// the incomplete-discovery field.
//
// The resolution SUCCEEDS here, which is the shape of the outcome rather than a
// softening of it: the dependency pins are complete and on disk, and what the run
// could not finish is the discovery of which external inputs the render consumes.
// So the answer must carry that fact — a caller told only "resolved" would read a
// partial pin set as a complete one.
//
// It is asserted on the daemon's answer against the resolution's own result, and
// the precondition below is what keeps it from passing on a fixture whose endpoint
// quietly started working.
func TestResolutionReportsAnUnfinishedExternalInputDiscovery(t *testing.T) {
	ws, releaseRel := refusingExternalInputWorkspace(t)
	t.Chdir(ws)
	srv := newFillCapableServer(t, ws)

	got, errEnvelope := srv.handleResolveDependencies(
		context.Background(),
		resolveDependenciesParams{Release: releaseRel},
		srv.progressFor(context.Background()))
	if errEnvelope != nil {
		t.Fatalf("the resolution failed outright; the incomplete-discovery fact rides on a SUCCESSFUL "+
			"answer, so this fixture no longer reaches the state under test: %s", errEnvelope.Message)
	}

	want := independentResolutionFacts(t, newFillCapableServer(t, ws), ws, releaseRel)
	if want.incomplete == "" {
		t.Fatal("precondition: the resolution finished its discovery against a refusing endpoint, so " +
			"the field under test would compare empty against empty")
	}
	if got.IncompleteExternalInputs != want.incomplete {
		t.Errorf("the answer says incompleteExternalInputs=%q; the resolution reported %q, so a caller "+
			"is told the pin set is complete when the run could not establish that",
			got.IncompleteExternalInputs, want.incomplete)
	}
}

// TestBuildCarriesTheFactsItsOwnFillComputed is the assertion that a build which
// filled its lockfile answers with what that fill resolved.
//
// The report is checked against the resolution's own result, fact by fact. The
// lockfile is checked by joining the reported workspace-relative path back onto
// the root and comparing absolute paths, which is an independent derivation
// rather than a second call to the helper the response used.
func TestBuildCarriesTheFactsItsOwnFillComputed(t *testing.T) {
	for _, tc := range []struct {
		name string
		// workspace builds the fixture and returns it with the Release inside it.
		workspace func(t *testing.T) (ws, releaseRel string)
		// wantExternalInputs is the population this row exists to exercise. It is
		// asserted against the independent resolution before anything is compared,
		// so a fixture that stopped consuming an external input fails here rather
		// than reducing this row to a duplicate of the other one.
		wantExternalInputs int
	}{
		{
			name:               "a Release whose render consumes no external input",
			workspace:          autoPullWorkspace,
			wantExternalInputs: 0,
		},
		{
			name:               "a Release whose render consumes one external input",
			workspace:          externalInputFillWorkspace,
			wantExternalInputs: 1,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ws, releaseRel := tc.workspace(t)
			// The harness declares the artifact cache as a RELATIVE path, so the
			// store this run writes lands beside the working directory rather than
			// inside the fixture.
			t.Chdir(ws)

			resp, pushed := driveBuildWithRawParams(t, newFillCapableServer(t, ws),
				`{"release":"`+releaseRel+`","autoPull":true}`)
			if resp.Error != nil {
				t.Fatalf("the filling build refused: code %d: %s\nwire:\n%s",
					resp.Error.Code, resp.Error.Message, pushed)
			}
			if resp.Result == nil {
				t.Fatalf("the filling build succeeded and carried no result\nwire:\n%s", pushed)
			}
			report := resp.Result.DependencyResolution
			if report == nil {
				t.Fatalf("the build filled its lockfile and reported nothing about the resolution that "+
					"filled it, so a caller that asked for the fill has to send a second request to find "+
					"out what its own build just pinned\nwire:\n%s", pushed)
			}

			want := independentResolutionFacts(t, newFillCapableServer(t, ws), ws, releaseRel)

			// The population preconditions, before any comparison.
			if want.externalInputs != tc.wantExternalInputs {
				t.Fatalf("precondition: the resolution reports %d external input(s) and this row exists "+
					"to exercise %d, so the row is no longer about what it says it is",
					want.externalInputs, tc.wantExternalInputs)
			}
			if want.entities == 0 {
				t.Fatal("precondition: the resolution pinned no entity, so every count below would " +
					"compare zero against zero")
			}

			if got := filepath.Join(ws, report.Lockfile); got != want.lockfilePath {
				t.Errorf("the report names lockfile %q, which resolves to %q; the resolution wrote %q",
					report.Lockfile, got, want.lockfilePath)
			}
			if report.Verified != want.verified {
				t.Errorf("the report says verified=%v; the resolution reported %v",
					report.Verified, want.verified)
			}
			if len(report.Entities) != want.entities {
				t.Errorf("the report carries %d entity/entities; the resolution pinned %d",
					len(report.Entities), want.entities)
			}
			if report.SourceArtifacts != want.sourceArtifacts {
				t.Errorf("the report says sourceArtifacts=%d; the resolution reported %d",
					report.SourceArtifacts, want.sourceArtifacts)
			}
			if report.TrustedPlugins != want.trustedPlugins {
				t.Errorf("the report says trustedPlugins=%d; the resolution reported %d",
					report.TrustedPlugins, want.trustedPlugins)
			}
			if report.ExternalInputs != want.externalInputs {
				t.Errorf("the report says externalInputs=%d; the resolution reported %d, so a caller "+
					"cannot tell how much of its render was pinned",
					report.ExternalInputs, want.externalInputs)
			}
			if report.IncompleteExternalInputs != want.incomplete {
				t.Errorf("the report says incompleteExternalInputs=%q; the resolution reported %q",
					report.IncompleteExternalInputs, want.incomplete)
			}
		})
	}
}

// TestBuildWithoutAFillReportsNoResolution is the withheld half, and it is what
// keeps the field from becoming an always-present shape a client learns to ignore.
//
// A build that did not fill resolved nothing, so there is no resolution to report
// and the key is absent from the wire entirely rather than present and empty. It
// is asserted on the raw wire as well as on the decoded value, because a decoded
// nil cannot on its own witness that the key never reached the client — and an
// absent key is the claim.
//
// 🔴 The build under test SUCCEEDS, and that is what makes the assertion mean
// anything. A refused build carries no result at all, so the field would be
// absent from a refusal however the success path behaved — the case is reached by
// filling the lockfile with one request and then building against it with the fill
// off, which is a build that had everything it needed and still resolved nothing.
func TestBuildWithoutAFillReportsNoResolution(t *testing.T) {
	ws, releaseRel := autoPullWorkspace(t)
	t.Chdir(ws)

	// The fixture's own lockfile does not match its closure, so the first request
	// fills one that does. Its report is not what this test is about; what it
	// leaves behind is.
	filled, filledWire := driveBuildWithRawParams(t, newFillCapableServer(t, ws),
		`{"release":"`+releaseRel+`","autoPull":true}`)
	if filled.Error != nil {
		t.Fatalf("the filling build refused, so the no-fill build below has no lockfile to succeed "+
			"against: code %d: %s\nwire:\n%s", filled.Error.Code, filled.Error.Message, filledWire)
	}
	if filled.Result == nil || filled.Result.DependencyResolution == nil {
		t.Fatal("precondition: the filling build reported no resolution, so this test cannot " +
			"distinguish a withheld report from one that is never produced at all")
	}

	// The case under test: everything present, nothing asked to be resolved.
	resp, pushed := driveBuildWithRawParams(t, newFillCapableServer(t, ws),
		`{"release":"`+releaseRel+`"}`)
	if resp.Error != nil {
		t.Fatalf("the build refused against the lockfile the previous request wrote: code %d: %s\n"+
			"wire:\n%s", resp.Error.Code, resp.Error.Message, pushed)
	}
	if resp.Result == nil {
		t.Fatal("the build succeeded and carried no result")
	}
	if resp.Result.DependencyResolution != nil {
		t.Errorf("a build that was not asked to fill reported a resolution: %+v",
			resp.Result.DependencyResolution)
	}
	if strings.Contains(pushed, "dependencyResolution") {
		t.Errorf("the wire carries a dependencyResolution key for a build that ran no resolution, so a "+
			"client cannot tell a fill that resolved nothing from a build that never filled\nwire:\n%s",
			pushed)
	}
}

// TestResolveDependenciesCarriesTheExternalInputFacts pins the two facts the
// dependency-resolution answer did not carry, on the request whose whole subject
// is the resolution.
//
// They are asserted here as well as on the build because the two answers are
// projected by one function: a regression that dropped a field would show on both,
// and a reader of either surface is owed the same facts. The expectation is again
// the resolution's own result rather than the response.
func TestResolveDependenciesCarriesTheExternalInputFacts(t *testing.T) {
	ws, releaseRel := externalInputFillWorkspace(t)
	t.Chdir(ws)
	srv := newFillCapableServer(t, ws)

	got, errEnvelope := srv.handleResolveDependencies(
		context.Background(),
		resolveDependenciesParams{Release: releaseRel},
		srv.progressFor(context.Background()))
	if errEnvelope != nil {
		t.Fatalf("the resolution failed: %s", errEnvelope.Message)
	}

	want := independentResolutionFacts(t, newFillCapableServer(t, ws), ws, releaseRel)
	if want.externalInputs == 0 {
		t.Fatal("precondition: the fixture's render consumed no external input, so the field under " +
			"test would compare zero against zero")
	}
	if got.ExternalInputs != want.externalInputs {
		t.Errorf("the answer says externalInputs=%d; the resolution reported %d",
			got.ExternalInputs, want.externalInputs)
	}
	if got.IncompleteExternalInputs != want.incomplete {
		t.Errorf("the answer says incompleteExternalInputs=%q; the resolution reported %q",
			got.IncompleteExternalInputs, want.incomplete)
	}
	// The pre-existing facts are asserted alongside, so a change that added the two
	// new fields by rebuilding the answer from scratch cannot quietly drop one of
	// the fields the answer already carried.
	if got.Lockfile == "" {
		t.Error("the answer names no lockfile")
	}
	if len(got.Entities) != want.entities {
		t.Errorf("the answer carries %d entity/entities; the resolution pinned %d",
			len(got.Entities), want.entities)
	}
}
