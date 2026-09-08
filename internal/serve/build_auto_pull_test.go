// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// What this file reaches, and why it reaches further than its neighbours.
//
// The two files beside this one cannot drive a SUCCEEDING build: their fixture
// writes a minimal lockfile that does not match its own closure, and producing
// one that does means running the dependency resolution. That is exactly what
// the field under test here runs, so a build that fills first renders against a
// lockfile derived from its own closure and completes — which makes the
// succeeding write reachable from this package for the first time.
//
// So the assertions here are about what the field CHANGES, in the workspace, on
// both sides of it: with the field off a Release the resolution has not reached
// is refused and nothing is written, and with it on the lockfile and the
// artifacts both appear. A test that only checked the field was accepted would
// pass against a handler that decoded it and threw it away.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// autoPullWorkspace is the rendering fixture with its catalog directory declared
// in a workspace configuration file.
//
// The declaration is what the resolution needs and the render does not: the
// Server is handed its search stack directly by the test harness, while the
// resolution reads the workspace's own resolved configuration, so a fixture that
// only set the former would refuse to resolve anything and the refusal would
// look like a fault in the field under test.
func autoPullWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	ws, releaseRel = renderDialWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws, ".aggrega-config.yaml"),
		[]byte("workspace:\n  catalog_dirs: [catalog]\n"), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return ws, releaseRel
}

// newFillCapableServer is a server for ws that can run a dependency resolution:
// the in-memory harness builds a Server as a struct literal, which holds no
// resolved workspace, and the resolution takes one rather than resolving a
// directory nobody named.
func newFillCapableServer(t *testing.T, ws string) *Server {
	t.Helper()
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	rw, err := workspaceconfig.Resolve(ws, ws, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve the fixture workspace: %v", err)
	}
	srv.resolvedWorkspace = rw
	return srv
}

// driveBuildWithRawParams runs ONE aggrega/build request whose params document
// is the given JSON, so a test controls exactly which keys are PRESENT.
//
// Sending raw JSON rather than a struct is what lets an omitted key and a key
// present and false be told apart: marshalling the params struct would collapse
// the two, which is the distinction the parity assertion below rests on.
func driveBuildWithRawParams(t *testing.T, srv *Server, params string) (buildResponse, string) {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/build", json.RawMessage(params)))
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
			t.Fatalf("no build response was written; the wire carried:\n%s", pushed)
		}
		var probe struct {
			ID *int `json:"id"`
		}
		if json.Unmarshal(frame, &probe) != nil || probe.ID == nil {
			continue
		}
		var resp buildResponse
		if uerr := json.Unmarshal(frame, &resp); uerr != nil {
			t.Fatalf("decode response: %v (body %s)", uerr, frame)
		}
		return resp, pushed
	}
}

// TestBuildAutoPullFillsWhatTheRefusalNames is the falsifiable pair the field
// exists to produce: one workspace, three requests differing only in that field,
// and a different workspace on disk afterwards.
//
// The two off rows are the parity assertion stated behaviourally rather than as
// a default value — an omitted key and a key present and false reach the same
// refusal, so a client that never learned the field behaves exactly as one that
// sent it false. The on row then writes both the lockfile the refusal named and
// the artifacts the refusal withheld.
//
// The refusing rows are checked for having written NOTHING, which is what
// separates the field from one that fills opportunistically: a handler that ran
// the resolution and then refused would leave a lockfile behind on a request
// that asked for no fill.
func TestBuildAutoPullFillsWhatTheRefusalNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		// prepare leaves the workspace in the state the row is about and returns
		// the fault the refusing rows must name.
		prepare func(t *testing.T, lockfilePath string)
		refusal int
	}{
		{
			name: "a Release the resolution has never reached",
			prepare: func(t *testing.T, lockfilePath string) {
				if err := os.Remove(lockfilePath); err != nil {
					t.Fatalf("remove the lockfile: %v", err)
				}
			},
			refusal: errLockfileAbsent,
		},
		{
			name: "a Release whose recorded resolution has drifted",
			// The fixture's own lockfile pins one entity and its closure reaches
			// several, so it is left exactly as the fixture wrote it.
			prepare: func(t *testing.T, lockfilePath string) {
				if _, err := os.Stat(lockfilePath); err != nil {
					t.Fatalf("precondition: the fixture wrote no lockfile to drift: %v", err)
				}
			},
			// The code a halted render carries, which is the shared failure
			// envelope's rather than a condition chosen for drift. It is asserted
			// as measured: what this row is about is that the field changes the
			// outcome, and pinning the code keeps the row honest about which
			// refusal it compared against.
			refusal: jsonRPCInternalError,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ws, releaseRel := autoPullWorkspace(t)
			lockfilePath := lockfile.PathFor(filepath.Join(ws, releaseRel))
			tc.prepare(t, lockfilePath)

			before := treeSnapshot(t, ws)
			if len(before) == 0 {
				t.Fatal("precondition: the workspace snapshot is empty, so a write could not be observed")
			}

			// Both spellings of "do not fill", which must be one behaviour.
			for _, params := range []string{
				`{"release":"` + releaseRel + `"}`,
				`{"release":"` + releaseRel + `","autoPull":false}`,
			} {
				resp, pushed := driveBuildWithRawParams(t, newFillCapableServer(t, ws), params)
				if resp.Error == nil {
					t.Fatalf("params %s built a Release the resolution has not covered; the field is off "+
						"unless a request asks for it\nwire:\n%s", params, pushed)
				}
				if resp.Error.Code != tc.refusal {
					t.Errorf("params %s refuse with code %d, want %d: %s",
						params, resp.Error.Code, tc.refusal, resp.Error.Message)
				}
				if after := treeSnapshot(t, ws); len(after) != len(before) {
					t.Errorf("params %s changed the workspace while refusing: %d files before, %d after",
						params, len(before), len(after))
				}
			}

			resp, pushed := driveBuildWithRawParams(t, newFillCapableServer(t, ws),
				`{"release":"`+releaseRel+`","autoPull":true}`)
			if resp.Error != nil {
				t.Fatalf("the build refused with the fill asked for; the resolution it runs is the one "+
					"aggrega/resolveDependencies runs, so the same workspace resolves\n"+
					"code %d: %s\nwire:\n%s", resp.Error.Code, resp.Error.Message, pushed)
			}
			// The lockfile the refusal above named, now on disk.
			filled, _, err := lockfile.LoadLockfile(lockfilePath)
			if err != nil {
				t.Fatalf("the filling build wrote no readable lockfile: %v", err)
			}
			if filled == nil {
				t.Fatal("the filling build wrote no lockfile at all")
			}
			if len(filled.Entities) == 0 {
				t.Error("the filled lockfile pins no entity, so the resolution reached nothing")
			}
			// And the artifacts the refusal withheld.
			if len(resp.Result.Files) == 0 {
				t.Error("the filling build reported no artifacts")
			}
			for _, name := range resp.Result.Files {
				if _, err := os.Stat(filepath.Join(resp.Result.OutputDirectory, name)); err != nil {
					t.Errorf("the build reported %s but it is not on disk: %v", name, err)
				}
			}
			if _, err := os.Stat(filepath.Join(ws, artifactout.DefaultOutputDir)); err != nil {
				t.Errorf("the filling build created no output directory: %v", err)
			}
		})
	}
}

// TestBuildPreviewRefusesTheDependencyFill drives the incompatible pairing over
// the wire and asserts on what came back, in both directions.
//
// Both directions are the assertion. The refusal alone would pass against a
// handler that refused every request it was sent, so the second half runs the
// same request over the same workspace with the field off and establishes that
// this refusal is about the field rather than about the workspace.
func TestBuildPreviewRefusesTheDependencyFill(t *testing.T) {
	ws, releaseRel := renderDialWorkspace(t)

	refused, refusedWire := driveBuildPreviewWithParams(t, ws, releaseRel, true)
	if refused.Error == nil {
		t.Fatalf("the preview accepted the dependency fill; it writes nothing, so it cannot fill a "+
			"lockfile and remain what it says it is\nwire:\n%s", refusedWire)
	}
	if refused.Error.Code != jsonRPCInvalidParams {
		t.Errorf("the refusal carries code %d, not invalid-params %d: %s",
			refused.Error.Code, jsonRPCInvalidParams, refused.Error.Message)
	}
	// The message names the field it refused, the method it refused it on, and
	// what to send instead. Each is checked separately so a failure says which
	// half went missing.
	for _, want := range []string{
		"autoPull",
		"aggrega/buildPreview",
		"aggrega/resolveDependencies",
	} {
		if !strings.Contains(refused.Error.Message, want) {
			t.Errorf("the refusal does not name %q, so a caller cannot act on it: %s",
				want, refused.Error.Message)
		}
	}
	// It is worded for the reader of this surface: a client author never typed a
	// command-line flag, so naming one would point at a surface they are not on.
	for _, absent := range []string{"--auto-pull", "--dry-run", "aggrega pull"} {
		if strings.Contains(refused.Error.Message, absent) {
			t.Errorf("the refusal names %q, which belongs to the command line rather than to this "+
				"surface: %s", absent, refused.Error.Message)
		}
	}

	// The calibrating opposite: the same request, the same workspace, field off.
	off, offWire := driveBuildPreviewWithParams(t, ws, releaseRel, false)
	if off.Error != nil && off.Error.Code == jsonRPCInvalidParams &&
		strings.Contains(off.Error.Message, "autoPull") {
		t.Fatalf("the preview refused the field it was not sent, so the refusal above is not "+
			"evidence about the field\nwire:\n%s", offWire)
	}
}

// driveBuildPreviewWithParams runs ONE aggrega/buildPreview request carrying the
// dependency-fill field explicitly, so both of its positions are sent rather
// than one being inferred from an omission.
func driveBuildPreviewWithParams(t *testing.T, ws, releaseRel string, autoPull bool) (buildPreviewResponse, string) {
	t.Helper()
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	return driveBuildPreviewParamsOn(t, srv, buildPreviewParams{Release: releaseRel, AutoPull: autoPull})
}

// driveBuildPreviewParamsOn is driveBuildPreviewOn taking the whole params value
// rather than only the Release.
func driveBuildPreviewParamsOn(t *testing.T, srv *Server, params buildPreviewParams) (buildPreviewResponse, string) {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/buildPreview", params))
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
			continue
		}
		var resp buildPreviewResponse
		if uerr := json.Unmarshal(frame, &resp); uerr != nil {
			t.Fatalf("decode response: %v (body %s)", uerr, frame)
		}
		return resp, pushed
	}
}

// TestDependencyFillFieldDefaultsOff pins the default as a property of the
// decode rather than as a value someone wrote down: a params document that omits
// the field decodes to the same value as one sending it false, on both methods.
//
// It is asserted on the decode as well as behaviourally above because the two
// fail differently. A default changed to true would still refuse the same
// workspaces for a different reason, so the behavioural pair alone could go on
// passing while every request that omitted the field started filling a lockfile.
func TestDependencyFillFieldDefaultsOff(t *testing.T) {
	t.Parallel()

	t.Run("aggrega/build", func(t *testing.T) {
		t.Parallel()
		var omitted, explicit buildParams
		if err := json.Unmarshal([]byte(`{"release":"r.yaml"}`), &omitted); err != nil {
			t.Fatalf("decode the omitting document: %v", err)
		}
		if err := json.Unmarshal([]byte(`{"release":"r.yaml","autoPull":false}`), &explicit); err != nil {
			t.Fatalf("decode the explicit document: %v", err)
		}
		if omitted != explicit {
			t.Errorf("omitting the field decodes to %+v and sending it false to %+v", omitted, explicit)
		}
		if omitted.AutoPull {
			t.Error("a request that omits the field asks for a dependency fill; the command line " +
				"performs none unless asked, and this surface carries the same default")
		}
	})

	t.Run("aggrega/buildPreview", func(t *testing.T) {
		t.Parallel()
		var omitted, explicit buildPreviewParams
		if err := json.Unmarshal([]byte(`{"release":"r.yaml"}`), &omitted); err != nil {
			t.Fatalf("decode the omitting document: %v", err)
		}
		if err := json.Unmarshal([]byte(`{"release":"r.yaml","autoPull":false}`), &explicit); err != nil {
			t.Fatalf("decode the explicit document: %v", err)
		}
		if omitted != explicit {
			t.Errorf("omitting the field decodes to %+v and sending it false to %+v", omitted, explicit)
		}
		if omitted.AutoPull {
			t.Error("a request that omits the field would be refused for asking for a fill it never asked for")
		}
	})
}

// TestDependencyResolutionNoticesCarryTheirSurface pins that the fill's own
// lines reach a caller under the surface that raised them rather than under the
// empty class a notice gets when this daemon cannot name its surface.
//
// The empty-input row is what keeps the conversion from padding an answer: a
// resolution that said nothing adds no notices at all, so a client is not handed
// an entry to render for a step that reported nothing.
//
// The class is spelled out here rather than read from the constant, and that
// duplication is DELIBERATE: it is the wire token a client branches on, so the
// assertion has to fail if it moves. Reading the constant would compare the
// implementation against itself and could not witness a rename.
func TestDependencyResolutionNoticesCarryTheirSurface(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		lines []string
		want  []buildNotice
	}{
		{name: "a resolution that said nothing", lines: nil, want: nil},
		{name: "an empty slice", lines: []string{}, want: nil},
		{
			name:  "the lines the resolution raised",
			lines: []string{"reusing the recorded pin for demo/svc", "verified one signature"},
			want: []buildNotice{
				{Class: "dependency-resolution", Text: "reusing the recorded pin for demo/svc"},
				{Class: "dependency-resolution", Text: "verified one signature"},
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := dependencyResolutionNotices(tc.lines)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d notices, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("notice %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
