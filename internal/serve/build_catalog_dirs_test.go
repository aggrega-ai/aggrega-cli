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
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The two build methods answer a catalog directory the workspace configuration
// lists for a Release as `aggrega build` answers it for the same Release. A
// listed directory where nothing exists is not in the stack the Release reads, so
// it is read as holding nothing and the build writes exactly what the fixture
// builds without it, and the answer names the directory once, in the line
// `aggrega build` prints for it. A listed directory that is there and cannot be walked is
// refused with the whole-catalog load's own refusal before anything is written —
// the lockfile a filling build writes included.
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

// listedDirectoryState is what a row leaves at the listed directory's path.
type listedDirectoryState int

const (
	// listedAbsent leaves nothing there at all.
	listedAbsent listedDirectoryState = iota
	// listedEmpty creates the directory, holding nothing.
	listedEmpty
	// listedUnreadable creates the directory and takes away every permission on
	// it, so it is there and no walk can list it. It sits outside the workspace,
	// where a configuration may list a directory, so nothing that walks the
	// workspace itself meets it and the row's one fault is the listing.
	listedUnreadable
)

// listedDirectory returns the directory a row lists for the workspace at ws and
// the configuration listing it after the fixture's own catalog directory.
func listedDirectory(t *testing.T, ws string, state listedDirectoryState) (listed, config string) {
	t.Helper()
	if state != listedUnreadable {
		return filepath.Join(ws, "no-such-catalog"), catalogDirsListing
	}
	listed = filepath.Join(t.TempDir(), "unreadable-catalog")
	return listed, "workspace:\n  catalog_dirs:\n    - ./catalog\n    - " + listed + "\n"
}

// makeListedDirectory lays down state at listed, skipping the row where the
// file system cannot hold a directory this user is unable to read.
func makeListedDirectory(t *testing.T, listed string, state listedDirectoryState) {
	t.Helper()
	if state == listedAbsent {
		return
	}
	if err := os.Mkdir(listed, 0o755); err != nil {
		t.Fatalf("create the listed directory: %v", err)
	}
	if state != listedUnreadable {
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not stop a directory being read on this platform")
	}
	if err := os.Chmod(listed, 0); err != nil {
		t.Fatalf("take the permissions off the listed directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(listed, 0o755) })
	if _, err := os.ReadDir(listed); err == nil {
		t.Skip("this user reads a directory carrying no permissions, so no directory here is unreadable")
	}
}

// TestBuildMethodsAnswerAListedCatalogDirectoryAsBuildDoes is the invocation
// proof for both methods, the build with and without its dependency fill: five
// rows build and three refuse, and each row's workspace differs from the state
// every row starts from in the listed directory and in nothing else.
//
// A building row is held to more than "it built": what the build reports and
// writes must be what it reported and wrote over the fixture's own list, byte for
// byte, and the preview must name the same files and write nothing, so a reading
// of the listing that changed what a build reads fails here even where it refuses
// nothing. Where nothing exists at the listed path, the row also holds the stack to
// leaving it out, and the answer to carrying the notice that names it exactly once,
// in the line the resolver composes for it — the line `aggrega pull` and `aggrega
// build` print: under the catalog surface and leading every other notice, on a
// build that does not fill and on the preview; and under the resolution's surface,
// as the fill's own line, on a build that fills. A row with the directory there
// carries no such notice, whichever method it sends.
//
// A refusing row is held to the whole of what `aggrega build` does with the same
// stack: the refusal names the directory and the load, carries the code a
// failure to resolve what the Release reaches carries and no document, reads
// exactly as the graph request's refusal of the same stack reads, and leaves
// every file in the workspace as it was — no lockfile from a fill, no output
// directory. The refusal names no registered condition — the load wraps the
// filesystem's own error under the catalog category — so it is identified through
// the accessors it does carry: the category, the directory the load reports, and
// the filesystem's permission condition beneath them.
func TestBuildMethodsAnswerAListedCatalogDirectoryAsBuildDoes(t *testing.T) {
	for _, tc := range []struct {
		name string
		// preview sends aggrega/buildPreview rather than aggrega/build.
		preview bool
		// autoPull asks the build to fill its lockfile before it renders.
		autoPull bool
		state    listedDirectoryState
	}{
		{name: "the build reads a listed directory where nothing exists as holding nothing and its answer says so once", state: listedAbsent},
		{name: "the build asked to fill reads it as holding nothing and its answer says so once", autoPull: true, state: listedAbsent},
		{name: "the preview reads it as holding nothing and its answer says so once", preview: true, state: listedAbsent},
		{name: "the build asked to fill writes what it writes without the listed directory, once it is there", autoPull: true, state: listedEmpty},
		{name: "the preview names what the build writes without the listed directory, once it is there", preview: true, state: listedEmpty},
		{name: "the build refuses a listed directory that cannot be read and writes nothing", state: listedUnreadable},
		{name: "the build asked to fill refuses it before the fill writes the lockfile", autoPull: true, state: listedUnreadable},
		{name: "the preview refuses it", preview: true, state: listedUnreadable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, release := renderDialWorkspace(t)
			listed, config := listedDirectory(t, ws, tc.state)
			dist := filepath.Join(ws, artifactout.DefaultOutputDir)
			wantFiles, wantWritten := builtOverTheFixturesOwnList(t, ws, release)

			makeListedDirectory(t, listed, tc.state)
			writeFixtureFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename), config)
			probe := catalogDirsServer(t, ws, &bytes.Buffer{})
			dirs := probe.catalogDirsFor(filepath.Join(ws, release))
			if reads := containsPath(dirs, listed); reads != (tc.state != listedAbsent) {
				t.Fatalf("precondition: the stack the Release reads is %v, and whether it lists %s is not what the row is about", dirs, listed)
			}
			// The line the resolver composes for this Release's stack, as every
			// surface that names the directory prints it, with the indentation a
			// terminal nests it under dropped.
			var composed bytes.Buffer
			probe.resolvedWorkspace.NoteAbsentCatalogDirs(&composed, filepath.Join(ws, release), nil)
			wantLine := strings.TrimSpace(composed.String())
			if (wantLine != "") != (tc.state == listedAbsent) || (wantLine != "" && !strings.Contains(wantLine, listed)) {
				t.Fatalf("precondition: the resolver composes %q for this stack, and whether it names %s is not what the row is about", wantLine, listed)
			}

			method := "aggrega/build"
			var params any = buildParams{Release: release, AutoPull: tc.autoPull}
			if tc.preview {
				method = "aggrega/buildPreview"
				params = buildPreviewParams{Release: release}
			}

			before := treeSnapshot(t, ws)
			resp := buildCatalogDirsAnswer(t, ws, method, params)

			if tc.state != listedUnreadable {
				var files []string
				var notices []buildNotice
				if tc.preview {
					var result buildPreviewResult
					decodeBuildAnswer(t, resp, &result)
					files, notices = result.Files, result.Notices
					if changes := snapshotChanges(before, treeSnapshot(t, ws)); len(changes) != 0 {
						t.Errorf("the preview wrote into the workspace: %v", changes)
					}
				} else {
					var result buildResult
					decodeBuildAnswer(t, resp, &result)
					files, notices = result.Files, result.Notices
					if changes := snapshotChanges(wantWritten, treeSnapshot(t, dist)); len(changes) != 0 {
						t.Errorf("the listed directory changed what the build wrote: %v", changes)
					}
				}
				if !slices.Equal(files, wantFiles) {
					t.Errorf("the listed directory changed the files %s reports:\n with it:    %v\n without it: %v", method, files, wantFiles)
				}
				var noted []buildNotice
				for _, n := range notices {
					if strings.Contains(n.Text, listed+" is listed under") {
						noted = append(noted, n)
					}
				}
				// Named once wherever nothing exists at the listed path: by the build
				// itself under the catalog surface where no fill runs, and by the fill,
				// under its own surface, where one does.
				wantNoted, wantClass := 0, ""
				if tc.state == listedAbsent {
					wantNoted, wantClass = 1, noticeClassCatalog
					if tc.autoPull {
						wantClass = noticeClassDependencyResolution
					}
				}
				if len(noted) != wantNoted {
					t.Errorf("the answer carries %d notice(s) naming %s; want %d: %+v", len(noted), listed, wantNoted, notices)
				}
				for _, n := range noted {
					if n.Class != wantClass {
						t.Errorf("the notice naming %s travels under class %q; want %q", listed, n.Class, wantClass)
					}
					if n.Text != wantLine {
						t.Errorf("the notice naming %s reads\n %q\nwant the resolver's own line\n %q", listed, n.Text, wantLine)
					}
				}
				if wantClass == noticeClassCatalog && len(noted) == 1 && notices[0] != noted[0] {
					t.Errorf("the notice naming %s does not lead the answer's notices: %+v", listed, notices)
				}
				return
			}

			if resp.Error == nil {
				t.Fatalf("%s answered over a stack listing %s, which cannot be read: %s", method, listed, resp.Result)
			}
			if resp.Error.Code != errEntityNotResolvable {
				t.Errorf("the refusal carries code %d; want %d, a failure to resolve what the Release reaches", resp.Error.Code, errEntityNotResolvable)
			}
			for _, want := range []string{listed, "catalog load"} {
				if !strings.Contains(resp.Error.Message, want) {
					t.Errorf("the refusal does not say %q: %s", want, resp.Error.Message)
				}
			}
			if d := resp.Error.Data; d != nil && (d.File != "" || d.Line != 0 || d.Lockfile != "") {
				t.Errorf("the refusal points at %+v; a directory is not a document a position is given in", *d)
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
			if !errors.Is(envelope.cause, fs.ErrPermission) {
				t.Errorf("the refusal does not carry the filesystem's permission condition: %v", envelope.cause)
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
