// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The two build methods answer a catalog directory the workspace configuration
// lists for a Release as `aggrega build` answers it for the same Release: a
// directory that does not exist is refused with the whole-catalog load's own
// refusal before anything is written — the lockfile a filling build writes
// included — and the same list with the directory there builds exactly what the
// fixture builds without it.
//
// Every row starts from one state: the rendering fixture filled and built once
// over its own catalog directory alone, so its lockfile matches its closure and a
// build there succeeds. The listed directory is then the only thing a row
// changes, which keeps each refusing row to one fault — a build refused for
// anything else fails its row rather than passing it.

// buildCatalogDirsAnswer drives one request through the production dispatch, on
// a server built by the constructor every workspace request is answered from,
// and returns the answer read past whatever the request pushed.
func buildCatalogDirsAnswer(t *testing.T, ws, method string, params any) jsonRPCResponse {
	t.Helper()
	var in, out bytes.Buffer
	srv := catalogDirsServer(t, ws, &out)
	in.Write(framedRequest(1, method, params))
	resp, _ := runOneRequestPastPushes(t, srv, &in, &out)
	return resp
}

// decodeBuildAnswer decodes the result an answer carries into v, failing the
// test on a refusal.
func decodeBuildAnswer(t *testing.T, resp jsonRPCResponse, v any) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("the request refused: code %d: %s", resp.Error.Code, resp.Error.Message)
	}
	if err := json.Unmarshal(resp.Result, v); err != nil {
		t.Fatalf("decode the answer: %v (body: %s)", err, resp.Result)
	}
}

// builtOverTheFixturesOwnList fills and builds the fixture over its own catalog
// directory alone, returns the names the build reported and the bytes it wrote
// under the output directory, and then removes that directory, so whatever a
// later request leaves there is its own.
func builtOverTheFixturesOwnList(t *testing.T, ws, release string) ([]string, map[string]string) {
	t.Helper()
	writeFixtureFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename), "workspace:\n  catalog_dirs:\n    - ./catalog\n")
	var result buildResult
	decodeBuildAnswer(t, buildCatalogDirsAnswer(t, ws, "aggrega/build", buildParams{Release: release, AutoPull: true}), &result)
	dist := filepath.Join(ws, artifactout.DefaultOutputDir)
	written := treeSnapshot(t, dist)
	if len(result.Files) == 0 || len(written) == 0 {
		t.Fatalf("precondition: the fixture's own build reported %v and wrote %d file(s), so no row has a build to compare against",
			result.Files, len(written))
	}
	if err := os.RemoveAll(dist); err != nil {
		t.Fatalf("remove the first build's output: %v", err)
	}
	return result.Files, written
}

// snapshotChanges names every file created, rewritten or removed between two
// tree snapshots, in path order.
func snapshotChanges(before, after map[string]string) []string {
	var changes []string
	for _, path := range slices.Sorted(maps.Keys(after)) {
		prev, ok := before[path]
		switch {
		case !ok:
			changes = append(changes, "created "+path)
		case prev != after[path]:
			changes = append(changes, "rewrote "+path)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(before)) {
		if _, ok := after[path]; !ok {
			changes = append(changes, "removed "+path)
		}
	}
	return changes
}

// TestBuildMethodsAnswerAListedCatalogDirectoryAsBuildDoes is the invocation
// proof for both methods, the build with and without its dependency fill: three
// rows refuse and two build, and each row's workspace differs from the state
// every row starts from in the listed directory and in nothing else.
//
// A refusing row is held to the whole of what `aggrega build` does with the same
// stack: the refusal names the directory and the load, carries the code a
// failure to resolve what the Release reaches carries and no document, reads
// exactly as the graph request's refusal of the same stack reads, and leaves
// every file in the workspace as it was — no lockfile from a fill, no output
// directory. The refusal names no registered condition — the load wraps the
// filesystem's own error under the catalog category — so it is identified through
// the accessors it does carry: the category, the directory the load reports, and
// the filesystem's not-exist condition beneath them.
//
// A building row is held to more than "it built": what the build reports and
// writes with the listed directory present and empty must be what it reported and
// wrote over the fixture's own list, byte for byte, and the preview must name the
// same files and write nothing, so a check that changed what a build reads fails
// here even where it refuses nothing.
func TestBuildMethodsAnswerAListedCatalogDirectoryAsBuildDoes(t *testing.T) {
	for _, tc := range []struct {
		name string
		// preview sends aggrega/buildPreview rather than aggrega/build.
		preview bool
		// autoPull asks the build to fill its lockfile before it renders.
		autoPull bool
		// present creates the listed directory, empty, before the request.
		present bool
	}{
		{name: "the build refuses a listed directory that does not exist and writes nothing"},
		{name: "the build asked to fill refuses it before the fill writes the lockfile", autoPull: true},
		{name: "the preview refuses it", preview: true},
		{name: "the build asked to fill writes what it writes without the listed directory, once it is there", autoPull: true, present: true},
		{name: "the preview names what the build writes without the listed directory, once it is there", preview: true, present: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, release := renderDialWorkspace(t)
			listed := filepath.Join(ws, "no-such-catalog")
			dist := filepath.Join(ws, artifactout.DefaultOutputDir)
			wantFiles, wantWritten := builtOverTheFixturesOwnList(t, ws, release)

			if tc.present {
				if err := os.Mkdir(listed, 0o755); err != nil {
					t.Fatalf("create the listed directory: %v", err)
				}
			}
			writeFixtureFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename), catalogDirsListing)
			if dirs := catalogDirsServer(t, ws, &bytes.Buffer{}).catalogDirsFor(filepath.Join(ws, release)); !containsPath(dirs, listed) {
				t.Fatalf("precondition: the stack the Release reads is %v, which does not list %s, so no row below is about it", dirs, listed)
			}

			method := "aggrega/build"
			var params any = buildParams{Release: release, AutoPull: tc.autoPull}
			if tc.preview {
				method = "aggrega/buildPreview"
				params = buildPreviewParams{Release: release}
			}

			before := treeSnapshot(t, ws)
			resp := buildCatalogDirsAnswer(t, ws, method, params)

			if tc.present {
				var files []string
				if tc.preview {
					var result buildPreviewResult
					decodeBuildAnswer(t, resp, &result)
					files = result.Files
					if changes := snapshotChanges(before, treeSnapshot(t, ws)); len(changes) != 0 {
						t.Errorf("the preview wrote into the workspace: %v", changes)
					}
				} else {
					var result buildResult
					decodeBuildAnswer(t, resp, &result)
					files = result.Files
					if changes := snapshotChanges(wantWritten, treeSnapshot(t, dist)); len(changes) != 0 {
						t.Errorf("an empty listed directory changed what the build wrote: %v", changes)
					}
				}
				if !slices.Equal(files, wantFiles) {
					t.Errorf("an empty listed directory changed the files %s reports:\n with it:    %v\n without it: %v", method, files, wantFiles)
				}
				return
			}

			if resp.Error == nil {
				t.Fatalf("%s answered over a stack listing %s, which does not exist: %s", method, listed, resp.Result)
			}
			if resp.Error.Code != errEntityNotResolvable {
				t.Errorf("the refusal carries code %d; want %d, a failure to resolve what the Release reaches", resp.Error.Code, errEntityNotResolvable)
			}
			for _, want := range []string{listed, "catalog load", "no such file or directory"} {
				if !strings.Contains(resp.Error.Message, want) {
					t.Errorf("the refusal does not say %q: %s", want, resp.Error.Message)
				}
			}
			if resp.Error.Data != nil {
				t.Errorf("the refusal points at %+v; a directory is not a document a position is given in", resp.Error.Data)
			}
			var graphOut bytes.Buffer
			graph := irGraphAnswer(t, catalogDirsServer(t, ws, &graphOut), &graphOut, release)
			if graph.Error == nil || graph.Error.Message != resp.Error.Message {
				t.Errorf("the graph request answers the same stack otherwise:\n %s: %s\n aggrega/irGraph: %+v", method, resp.Error.Message, graph.Error)
			}

			srv := catalogDirsServer(t, ws, &bytes.Buffer{})
			ctx, flush := serializerContext(t, &bytes.Buffer{})
			var envelope *jsonRPCError
			if tc.preview {
				_, envelope = srv.handleBuildPreview(ctx, buildPreviewParams{Release: release})
			} else {
				_, envelope = srv.handleBuild(ctx, buildParams{Release: release, AutoPull: tc.autoPull})
			}
			flush()
			if envelope == nil {
				t.Fatal("the handler answered a request the wire refused")
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

			if changes := snapshotChanges(before, treeSnapshot(t, ws)); len(changes) != 0 {
				t.Errorf("the refused %s wrote into the workspace: %v", method, changes)
			}
			if _, err := os.Stat(dist); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the refused %s left an output directory; stat err = %v", method, err)
			}
		})
	}
}
