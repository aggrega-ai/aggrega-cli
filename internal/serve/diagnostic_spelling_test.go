// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// spellingFixture stands a real Tier-4 workspace up in a tempdir and returns the
// server plus the two spellings of its Aggregate. The two spellings name one
// document, which is the whole premise the tests below measure against.
//
// The parameter-type-mismatch fixture is used because it produces a genuine
// semantic finding through the real pipeline: asserting on an injected
// diagnostic would prove the store round-trips a value and nothing about which
// key the validation writes.
func spellingFixture(t *testing.T) (srv *Server, ws, relSpelling, absSpelling string) {
	t.Helper()
	ws = t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "tier4", "parameter-type-mismatch")
	for _, name := range []string{"release.yaml", "manifest.yaml", "aggregate.yaml"} {
		data, rerr := os.ReadFile(filepath.Join(fixtureDir, name))
		if rerr != nil {
			t.Fatalf("read fixture %s: %v", name, rerr)
		}
		if werr := os.WriteFile(filepath.Join(ws, name), data, 0o644); werr != nil {
			t.Fatalf("write fixture %s: %v", name, werr)
		}
	}

	srv = newTestServer(t, ws, nil, nil)
	return srv, ws, "aggregate.yaml", filepath.Join(ws, "aggregate.yaml")
}

// validateOverTheWire drives one aggrega/validate request naming file and
// returns its diagnostics, failing on an error envelope.
func validateOverTheWire(t *testing.T, srv *Server, ws, file string) []Diagnostic {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(ws, "aggregate.yaml"))
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    file,
		Content: string(content),
		Release: "release.yaml",
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("validate %q: unexpected envelope %+v", file, resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal validate result: %v", err)
	}
	return result.Diagnostics
}

// diagnosticsOverTheWire drives one aggrega/diagnostics request naming file and
// returns the decoded response, leaving the caller to judge result or envelope.
func diagnosticsOverTheWire(t *testing.T, srv *Server, file string) jsonRPCResponse {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/diagnostics", diagnosticsParams{File: file}))
	return runOneRequest(t, srv, &in, &out)
}

func diagnosticsResultOf(t *testing.T, resp jsonRPCResponse, file string) []Diagnostic {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("diagnostics %q: unexpected envelope %+v", file, resp.Error)
	}
	var result diagnosticsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal diagnostics result: %v", err)
	}
	return result.Diagnostics
}

// TestDiagnosticsAnswerTheFileHoweverTheRequestSpelledIt is the load-bearing
// guard on the whole per-file diagnostic surface: what a request is answered
// with depends on the document it named, not on how it spelled the name.
//
// It covers BOTH layers in one session and the coverage is the point. The two
// layers are written by different requests and each names the file in its own
// terms — the semantic layer by a validation carrying whatever the sender sent,
// the render layer by an inspection attributing advisories to the resolved
// Aggregate's absolute path — so a fix that unified one and not the other would
// leave this exact defect standing on the layer nobody probed, and a
// single-layer assertion would pass over it.
//
// The two writing spellings are crossed BOTH ways rather than in the one
// arrangement production happens to produce today. A check gated on two
// independent axes has a grid of behaviours and not a list: asserting only the
// arrangement that was observed — a validation naming the file relatively while
// the inspection names it absolutely — specifies one cell and leaves the mirror
// unstated, and a write-side guard on either layer is then satisfied by an
// input that never exercises it. The mirror row is what makes each layer's own
// key conversion load-bearing instead of decorative; it was added after the
// single-arrangement version of this test proved unable to fail when the render
// layer's conversion was removed.
//
// The failure mode being guarded is silence rather than an error — a request on
// the spelling that missed was answered with an empty result, which a client
// cannot distinguish from "this file has no problems" — so the assertion is on
// the findings being present and equal, never on an error not being raised.
func TestDiagnosticsAnswerTheFileHoweverTheRequestSpelledIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// semanticSpelling / renderSpelling select which spelling each layer's
		// writer names the file by. relative is reported as true.
		semanticRelative bool
		renderRelative   bool
	}{
		{"the validation names it relatively and the inspection absolutely", true, false},
		{"the validation names it absolutely and the inspection relatively", false, true},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, ws, relSpelling, absSpelling := spellingFixture(t)
			pick := func(relative bool) string {
				if relative {
					return relSpelling
				}
				return absSpelling
			}

			// The semantic layer, written by a real validation through the wire.
			semantic := validateOverTheWire(t, srv, ws, pick(tc.semanticRelative))
			if len(semantic) == 0 {
				t.Fatal("precondition: the validation produced no semantic " +
					"finding, so the composed answer below would prove " +
					"nothing about that layer")
			}

			// The render layer, written the way an inspection writes it: as a
			// whole-layer replacement attributed to the Aggregate it rendered.
			var pushed bytes.Buffer
			ctx, flush := serializerContext(t, &pushed)
			advisory := rendercore.UnhonorableAnnotationDiagnostic(
				"helm", "aggrega.ai/namspace", "synthesized")
			advisory.Location = &renderer.DiagnosticLocation{URI: "app", Kind: "helm"}
			srv.publishRenderDiagnostics(ctx, pick(tc.renderRelative),
				[]renderer.Diagnostic{advisory}, nil, nil)
			flush()

			byRelative := diagnosticsResultOf(t,
				diagnosticsOverTheWire(t, srv, relSpelling), relSpelling)
			byAbsolute := diagnosticsResultOf(t,
				diagnosticsOverTheWire(t, srv, absSpelling), absSpelling)

			// Each answer carries both layers. Asserted per layer rather than by
			// count, so a failure names which layer went missing instead of
			// reporting a number.
			for _, probe := range []struct {
				spelling string
				got      []Diagnostic
			}{
				{relSpelling, byRelative},
				{absSpelling, byAbsolute},
			} {
				var sawSemantic, sawRender bool
				for _, d := range probe.got {
					switch d.Source {
					case DiagnosticSourceSemantic:
						sawSemantic = true
					case DiagnosticSourceBuild:
						sawRender = true
					}
				}
				if !sawSemantic {
					t.Errorf("the answer for %q carries no semantic finding; got %q",
						probe.spelling, diagMessages(probe.got))
				}
				if !sawRender {
					t.Errorf("the answer for %q carries no render advisory; got %q",
						probe.spelling, diagMessages(probe.got))
				}
			}

			if !reflect.DeepEqual(byRelative, byAbsolute) {
				t.Errorf("the two spellings of one document were answered differently\n"+
					" relative %q -> %q\n absolute %q -> %q",
					relSpelling, diagMessages(byRelative),
					absSpelling, diagMessages(byAbsolute))
			}
		})
	}
}

// TestRenderPushCarriesTheAbsolutePathTheClientResolves pins the published side
// of the same identity. The notification's file field is not a label — a client
// matches it against its own open documents by resolving it as a filesystem
// path, so a workspace-relative identifier there resolves against the client's
// own notion of the current directory and matches no open document, and the
// advisories then reach nothing while the daemon reports having published them.
//
// It is asserted on the push a RELATIVE attribution produces, because that is
// the case that would regress: an absolute attribution pushes an absolute
// identifier whether or not anything converts it.
func TestRenderPushCarriesTheAbsolutePathTheClientResolves(t *testing.T) {
	t.Parallel()
	srv, _, relSpelling, absSpelling := spellingFixture(t)

	var pushed bytes.Buffer
	ctx, flush := serializerContext(t, &pushed)
	advisory := rendercore.UnhonorableAnnotationDiagnostic(
		"helm", "aggrega.ai/namspace", "synthesized")
	advisory.Location = &renderer.DiagnosticLocation{URI: "app", Kind: "helm"}
	srv.publishRenderDiagnostics(ctx, relSpelling, []renderer.Diagnostic{advisory}, nil, nil)
	flush()

	body := pushed.String()
	if !containsSubstring(body, absSpelling) {
		t.Errorf("the notification does not carry the absolute path %q, which is "+
			"the identifier a client resolves\npushed: %s", absSpelling, body)
	}
}

// TestDiagnosticsRefuseAPathThatNamesNothing is the negative control that makes
// the test above evidence. Resolving a request's spelling must not turn a path
// naming nothing into a match for some other document, so each row asks about a
// path no validation ever named while the fixture's real Aggregate sits
// validated in the same session — a resolution that collapsed onto it would
// answer these with that file's findings.
//
// The refusal also names the path the request asked about rather than the
// resolved spelling, because a sender told about some other spelling of its own
// value has been handed a puzzle in place of an answer.
func TestDiagnosticsRefuseAPathThatNamesNothing(t *testing.T) {
	t.Parallel()
	srv, ws, relSpelling, _ := spellingFixture(t)
	if len(validateOverTheWire(t, srv, ws, relSpelling)) == 0 {
		t.Fatal("precondition: nothing was validated, so a miss below would be " +
			"vacuous rather than a refusal to collapse onto a real document")
	}

	for _, tc := range []struct {
		name string
		file string
	}{
		{"a file that does not exist, named relative to the workspace", "no-such-file.yaml"},
		{"a file that does not exist, named absolutely", filepath.Join(ws, "no-such-file.yaml")},
		// A sibling of the workspace: absolute, and outside the tree without
		// naming any OS-standard directory to get there.
		{"an absolute path outside the workspace tree", filepath.Join(filepath.Dir(ws), "outside-the-tree.yaml")},
		{"a relative path that climbs out of the workspace", "../outside-the-tree.yaml"},
		{"the empty string, which names no document at all", ""},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := diagnosticsOverTheWire(t, srv, tc.file)
			if resp.Error == nil {
				t.Fatalf("%q was answered with a result; want the "+
					"file-not-validated refusal", tc.file)
			}
			if resp.Error.Code != errFileNotValidated {
				t.Errorf("%q drew code %d; want %d", tc.file,
					resp.Error.Code, errFileNotValidated)
			}
			if !containsSubstring(resp.Error.Message, tc.file) {
				t.Errorf("the refusal for %q reads %q; want it to name the "+
					"path the request asked about", tc.file, resp.Error.Message)
			}
		})
	}
}

// TestCanonicalFileLeavesUnresolvableValuesAsTheyStand pins the conversion's own
// edges, which the request-level tests reach only indirectly. Each row is a
// value the daemon must key by exactly as it arrived, because there is nothing
// to resolve it against or nothing it could name.
//
// The absolute rows are the ones that matter for the wire: the notification's
// file identifier is this key, and a client resolves it as a filesystem path,
// so an absolute value has to survive unchanged.
func TestCanonicalFileLeavesUnresolvableValuesAsTheyStand(t *testing.T) {
	t.Parallel()
	const root = "/ws/proj"
	for _, tc := range []struct {
		name string
		root string
		file string
		want string
	}{
		{"an absolute path inside the workspace is already canonical",
			root, "/ws/proj/aggregate.yaml", "/ws/proj/aggregate.yaml"},
		{"an absolute path outside the workspace stays absolute",
			root, "/etc/hostname", "/etc/hostname"},
		{"a workspace-relative path resolves onto the root",
			root, "aggregate.yaml", "/ws/proj/aggregate.yaml"},
		{"an interior traversal resolves rather than being refused",
			root, "sub/../aggregate.yaml", "/ws/proj/aggregate.yaml"},
		{"a path climbing out of the workspace is left as it stands",
			root, "../outside.yaml", "../outside.yaml"},
		{"a relative path with no workspace root to resolve against is untouched",
			"", "aggregate.yaml", "aggregate.yaml"},
		{"the empty string names no document",
			root, "", ""},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{workspaceRoot: tc.root}
			if got := s.canonicalFile(tc.file); got != tc.want {
				t.Errorf("canonicalFile(%q) with root %q = %q; want %q",
					tc.file, tc.root, got, tc.want)
			}
		})
	}
}
