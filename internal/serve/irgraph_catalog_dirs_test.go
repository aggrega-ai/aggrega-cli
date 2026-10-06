// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The graph request answers a catalog directory the workspace configuration
// lists for a Release as `aggrega inspect` answers it for the same Release: a
// directory that does not exist is refused with the whole-catalog load's own
// refusal, and the same list with the directory there draws the graph.
//
// The server here is built by the constructor every request naming a workspace
// is answered from, over a resolution of the fixture's own configuration file, so
// the stack the request reads is the one the resolver composes from the list as a
// reader wrote it rather than one a harness assigned.

// catalogDirsListing is the configuration line that lists the directory under
// test after the fixture's own catalog directory.
const catalogDirsListing = "workspace:\n  catalog_dirs:\n    - ./catalog\n    - ./no-such-catalog\n"

// catalogDirsServer resolves the workspace at ws from its own configuration, with
// no global layer and a frozen environment, and returns the entry a request naming
// ws is answered from.
func catalogDirsServer(t *testing.T, ws string, out *bytes.Buffer) *Server {
	t.Helper()
	rw, err := workspaceconfig.ResolveWithoutGlobal(ws, ws, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve the fixture workspace: %v", err)
	}
	sess := newTestSession(t, ws, nil, out)
	srv := sess.newWorkspace(rw)
	sess.workspaces.mu.Lock()
	sess.workspaces.byRoot[filepath.Clean(ws)] = srv
	sess.workspaces.mu.Unlock()
	return srv
}

// irGraphAnswer drives one aggrega/irGraph request through the production
// dispatch and returns the answer, read past whatever the request pushed.
func irGraphAnswer(t *testing.T, srv *Server, out *bytes.Buffer, release string) jsonRPCResponse {
	t.Helper()
	var in bytes.Buffer
	in.Write(framedRequest(1, "aggrega/irGraph", irGraphParams{Release: release}))
	resp, _ := runOneRequestPastPushes(t, srv, &in, out)
	return resp
}

// drawnGraph returns the graph an answer carries, failing the test on a refusal.
func drawnGraph(t *testing.T, resp jsonRPCResponse) json.RawMessage {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("the graph request refused: code %d: %s", resp.Error.Code, resp.Error.Message)
	}
	var result irGraphResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode the answer: %v (body: %s)", err, resp.Result)
	}
	if len(result.Graph) == 0 || string(result.Graph) == "null" {
		t.Fatalf("the answer carries no graph: %s", resp.Result)
	}
	return result.Graph
}

// TestIrGraphAnswersAListedCatalogDirectoryAsInspectDoes is the invocation proof:
// one row refuses and one draws, and each row's fixture differs from the other in
// whether the listed directory is there and in nothing else.
//
// The refusal names no registered condition — the load wraps the filesystem's own
// error under the catalog category — so it is identified through the accessors it
// does carry: the category and the directory the load reports, and the
// filesystem's not-exist condition beneath them. Its message is asserted to name
// the directory and the load, which is what a reader is shown.
//
// The drawing row is held to more than "a graph came back": the graph drawn with
// the listed directory present and empty must be the graph drawn over the
// fixture's own list, byte for byte, so a check that changed what a graph reads
// fails here even where it refuses nothing.
func TestIrGraphAnswersAListedCatalogDirectoryAsInspectDoes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// present creates the listed directory, empty, before the request.
		present bool
	}{
		{name: "a listed directory that does not exist is refused, naming it", present: false},
		{name: "the same list with the directory there draws the graph the fixture draws without it", present: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws, release := renderDialWorkspace(t)
			listed := filepath.Join(ws, "no-such-catalog")

			// The baseline, over the fixture's own configuration, read before the
			// listing is written so both answers come from one workspace path.
			var baseline json.RawMessage
			if tc.present {
				writeFixtureFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename), "workspace:\n  catalog_dirs:\n    - ./catalog\n")
				var out bytes.Buffer
				baseline = drawnGraph(t, irGraphAnswer(t, catalogDirsServer(t, ws, &out), &out, release))
				if err := os.Mkdir(listed, 0o755); err != nil {
					t.Fatalf("create the listed directory: %v", err)
				}
			}
			writeFixtureFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename), catalogDirsListing)

			var out bytes.Buffer
			srv := catalogDirsServer(t, ws, &out)
			if dirs := srv.catalogDirsFor(filepath.Join(ws, release)); !containsPath(dirs, listed) {
				t.Fatalf("precondition: the stack the Release reads is %v, which does not list %s, so no row below is about it", dirs, listed)
			}
			resp := irGraphAnswer(t, srv, &out, release)

			if tc.present {
				if got := drawnGraph(t, resp); !bytes.Equal(got, baseline) {
					t.Errorf("an empty listed directory changed the graph:\n with it:    %s\n without it: %s", got, baseline)
				}
				return
			}

			if resp.Error == nil {
				t.Fatalf("a graph was drawn over a stack listing %s, which does not exist: %s", listed, resp.Result)
			}
			if resp.Error.Code != errEntityNotResolvable {
				t.Errorf("the refusal carries code %d; want %d, a failure to resolve what the Release reaches", resp.Error.Code, errEntityNotResolvable)
			}
			for _, want := range []string{listed, "catalog load", "no such file or directory"} {
				if !strings.Contains(resp.Error.Message, want) {
					t.Errorf("the refusal does not say %q: %s", want, resp.Error.Message)
				}
			}
			if d := resp.Error.Data; d != nil && (d.File != "" || d.Line != 0 || d.Lockfile != "") {
				t.Errorf("the refusal points at %+v; a directory is not a document a position is given in", *d)
			}

			ctx, flush := serializerContext(t, &bytes.Buffer{})
			_, envelope := srv.handleIrGraph(ctx, irGraphParams{Release: release})
			flush()
			if envelope == nil {
				t.Fatal("the handler drew a graph the wire refused")
			}
			var load *cerrors.CompilerError
			if !errors.As(envelope.cause, &load) {
				t.Fatalf("the refusal carries no compiler error beneath it: %v", envelope.cause)
			}
			if load.Category() != cerrors.CatalogError {
				t.Errorf("the refusal is filed under %q; want %q", load.Category(), cerrors.CatalogError)
			}
			if load.Source().File != listed {
				t.Errorf("the refusal names %q; want the listed directory %q", load.Source().File, listed)
			}
			if !errors.Is(envelope.cause, fs.ErrNotExist) {
				t.Errorf("the refusal does not carry the filesystem's not-exist condition: %v", envelope.cause)
			}
		})
	}
}

// containsPath reports whether dirs names dir, compared cleaned.
func containsPath(dirs []string, dir string) bool {
	for _, d := range dirs {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}
