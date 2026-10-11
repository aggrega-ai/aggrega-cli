// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The build tool's write argument over the process's own streams: where a call
// sent write puts the rendered artifacts, what its answer says about them, and
// every way a call writes nothing.
//
// Every assertion that something was written reads the workspace on disk rather
// than the answer alone, because a tool that decoded the argument and threw it
// away answers a call exactly like one that honoured it everywhere but on disk.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// writeFixture is a workspace a build renders: the single-node compose workspace,
// its lockfile filled by the pull tool so a build reads it as it stands, and the
// files and bytes one render of its Release produces, read off a call not sent
// write — a render no write path and no output directory takes part in.
type writeFixture struct {
	root, release string
	expected      map[string]string
}

// newWriteFixture lays one down and reads the render it is held to.
//
// The call reading the expected tree names the workspace root, so its in-memory
// render is the render a write of the same Release in the same workspace performs;
// that it wrote nothing is asserted rather than assumed, because a fixture whose
// reference left a dist behind would let every write row pass on a stale tree.
func newWriteFixture(t *testing.T) writeFixture {
	t.Helper()
	s, root, _, release := unsupportedFieldWorkspace(t)
	if prime := callPull(t, s, pullArgs{Release: release, WorkspaceRoot: root}); prime.IsError {
		t.Fatalf("fill the fixture's lockfile: %s", prime.Content[0].Text)
	}
	format := responseFormatDetailed
	res := callTool(t, s, "build", buildArgs{Release: release, WorkspaceRoot: root, ResponseFormat: &format})
	if res.IsError {
		t.Fatalf("the fixture's in-memory render was refused: %s", res.Content[0].Text)
	}
	expected := map[string]string{}
	for _, f := range unmarshalBuildPayload(t, res).Files {
		expected[filepath.FromSlash(f.Path)] = f.Content
	}
	if len(expected) == 0 {
		t.Fatal("the fixture's render produced no file, so every row below would be held to nothing")
	}
	if _, err := os.Lstat(filepath.Join(root, "dist")); err == nil {
		t.Fatal("precondition: a build not sent write left a dist directory in the workspace")
	}
	return writeFixture{root: root, release: release, expected: expected}
}

// writtenTree reads every regular file under dir into a map keyed by its path
// relative to dir. A dir that does not exist reads as no tree at all.
func writtenTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(b)
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// treePaths lists a tree's paths in order, for a failure message.
func treePaths(tree map[string]string) []string {
	paths := make([]string, 0, len(tree))
	for p := range tree {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// treeDifference names how got departs from want, for a failure message: each
// file want holds and got lacks, each file got holds and want lacks, and each file
// both hold with different bytes, quoted on both sides. Two lists of paths read
// the same when only the bytes differ, so a message carrying the paths alone
// cannot say what a byte-for-byte comparison found.
func treeDifference(got, want map[string]string) string {
	var b strings.Builder
	for _, p := range treePaths(want) {
		g, ok := got[p]
		switch {
		case !ok:
			fmt.Fprintf(&b, "\n  missing %s", p)
		case g != want[p]:
			fmt.Fprintf(&b, "\n  %s differs:\n    got:  %q\n    want: %q", p, g, want[p])
		}
	}
	for _, p := range treePaths(got) {
		if _, ok := want[p]; !ok {
			fmt.Fprintf(&b, "\n  unexpected %s", p)
		}
	}
	return b.String()
}

// structuredKeys is the set of keys a result's structured payload carries.
func structuredKeys(t *testing.T, res *callToolResult) map[string]bool {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(res.StructuredContent, &fields); err != nil {
		t.Fatalf("the structured payload does not decode as an object: %v (%s)", err, res.StructuredContent)
	}
	keys := map[string]bool{}
	for k := range fields {
		keys[k] = true
	}
	return keys
}

// TestABuildSentWriteWritesTheRenderBeneathTheWorkspaceRoot drives the build tool
// sent write over each way a call over the process's own streams settles its
// workspace: the call naming the root, the launch naming it, and neither — the
// server started in a folder that anchors no workspace and the Release named
// absolutely, so the workspace is found from the Release's own folder.
//
// Every row writes <root>/dist holding exactly the render's files and bytes,
// names that directory in full as output_dir, lists the files written relative to
// it, says so in its text, and writes nothing in the folder the server was
// started in. <root>/dist is spelled here rather than read from the package, so
// a destination composed anywhere else fails every row.
//
// 🔴 PLANTED DEFECT: in buildInvoke (internal/mcp/tool_build.go), delete the call
// to writeRenderedArtifacts. Every row reddens, finding no tree at <root>/dist and
// no output_dir, while the answer still lists the rendered files.
//
// Not parallel: the third row moves the process working directory, which is the
// condition under test.
func TestABuildSentWriteWritesTheRenderBeneathTheWorkspaceRoot(t *testing.T) {
	for _, row := range []struct {
		name string
		// call sends the build for fx from a server this row composes, after
		// moving into startedIn where the row needs to.
		call func(t *testing.T, fx writeFixture, startedIn string) *callToolResult
	}{
		{
			name: "the call names the workspace root",
			call: func(t *testing.T, fx writeFixture, startedIn string) *callToolResult {
				return callTool(t, unlaunchedServer(startedIn), "build",
					map[string]any{"release": fx.release, "workspace_root": fx.root, "write": true})
			},
		},
		{
			name: "the launch names the workspace root and the Release is named relative to it",
			call: func(t *testing.T, fx writeFixture, startedIn string) *callToolResult {
				t.Chdir(startedIn)
				return callTool(t, unlaunchedServer(fx.root), "build",
					map[string]any{"release": filepath.Base(fx.release), "write": true})
			},
		},
		{
			name: "nothing names a root and the server sits in a folder anchoring no workspace",
			call: func(t *testing.T, fx writeFixture, startedIn string) *callToolResult {
				t.Chdir(startedIn)
				return callTool(t, unlaunchedServer(""), "build", map[string]any{"release": fx.release, "write": true})
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			fx := newWriteFixture(t)
			dist := filepath.Join(fx.root, "dist")
			startedIn := aDirectoryNoWorkspaceResolvesFrom(t)

			res := row.call(t, fx, startedIn)
			if res.IsError {
				t.Fatalf("the build sent write was refused: %s", res.Content[0].Text)
			}
			if tree := writtenTree(t, dist); !reflect.DeepEqual(tree, fx.expected) {
				t.Errorf("%s does not hold exactly the render's files, byte for byte:%s",
					dist, treeDifference(tree, fx.expected))
			}
			payload := unmarshalBuildPayload(t, res)
			if payload.OutputDir == nil || *payload.OutputDir != dist {
				t.Errorf("output_dir = %v (withheld %v), want %q", payload.OutputDir, payload.OutputDirWithheld, dist)
			}
			var listed []string
			for _, f := range payload.Files {
				listed = append(listed, filepath.FromSlash(f.Path))
			}
			sort.Strings(listed)
			if !reflect.DeepEqual(listed, treePaths(fx.expected)) {
				t.Errorf("files lists %v as written, want %v", listed, treePaths(fx.expected))
			}
			if want := "wrote " + strconv.Itoa(len(fx.expected)) + " file(s) to " + dist + ":"; !strings.Contains(res.Content[0].Text, want) {
				t.Errorf("the text does not say %q:\n%s", want, res.Content[0].Text)
			}
			assertNothingCreatedIn(t, startedIn)
		})
	}
}

// TestABuildSentWriteOverAListenerWritesOnlyUnderTheWorkspaceWriteScope drives the
// write over a real listener serving a workspace that builds, under the grants
// that separate it: the read scope alone, and the workspace-write scope.
//
// The tool is listed to both, because a listing reads the class the operation
// declares. Under the read scope alone a call sent write is refused 403 with a
// challenge naming aggrega:workspace-write and the served root gains no dist,
// while the same call not sent write is admitted and writes nothing. Under the
// workspace-write scope a call sent write writes <served root>/dist holding
// exactly the files and bytes this listener renders for the Release, and is told
// output_dir as "dist" — relative to the root, which is the only form a caller
// reaching a listener is told a place in.
//
// The tree that write is held to is read off this same listener, from a call not
// sent write, rather than taken from the fixture. A render stamps the compiler
// version of the server performing it, where that server carries one, into the
// artifacts it emits, and the listener's server is composed apart from the one
// the fixture's tree was read from, so holding one to the other would compare two
// servers' settings rather than what one server renders with what it writes. The
// file set, which no server setting moves, is still held to the fixture's, and the
// bytes to a render over the process's own streams carrying the listener's
// compiler version.
//
// The grid drives the same column without a Release the workspace holds; this
// is the half that shows an admitted write lands where it should.
//
// 🔴 PLANTED DEFECT: in writeRenderedArtifacts (internal/mcp/tool_build.go), set
// payload.OutputDir to &outDir instead of through reportedLocationField. The
// workspace-write row reddens on output_dir naming the host path.
func TestABuildSentWriteOverAListenerWritesOnlyUnderTheWorkspaceWriteScope(t *testing.T) {
	fx := newWriteFixture(t)
	f := listenerOverWorkspace(t, fx.root)
	dist := filepath.Join(fx.root, "dist")
	release := filepath.Base(fx.release)
	readOnly := f.grantOf(t, scopeRead)
	writing := f.grantOf(t, scopeWorkspaceWrite)
	expected := renderedOverTheListener(t, f, readOnly, release, fx)

	for name, credential := range map[string]string{"the read scope alone": readOnly, "the workspace-write scope": writing} {
		if listed := f.listed(t, credential); !slices.Contains(listed, "build") {
			t.Errorf("tools/list did not name build to a credential carrying %s; it named %v", name, sortedNames(listed))
		}
	}

	t.Run("the read scope alone is refused the write", func(t *testing.T) {
		got := f.callTool(t, readOnly, "build", map[string]any{"release": release, "write": true})
		wantRefusedForWantOfScope(t, "build", got, scopeAdmittingInThisTest(opsurface.ReachWorkspace))
		if _, err := os.Lstat(dist); err == nil {
			t.Error("a refused write left a dist directory in the served workspace")
		}
	})

	t.Run("the read scope alone builds in memory and writes nothing", func(t *testing.T) {
		got := f.callTool(t, readOnly, "build", map[string]any{"release": release})
		res := toolResultOf(t, "build", got)
		if res.IsError {
			t.Fatalf("a build not sent write was refused: %s", res.Content[0].Text)
		}
		if _, err := os.Lstat(dist); err == nil {
			t.Error("a build not sent write left a dist directory in the served workspace")
		}
	})

	t.Run("the workspace-write scope writes beneath the served root", func(t *testing.T) {
		got := f.callTool(t, writing, "build",
			map[string]any{"release": release, "write": true, "response_format": "concise"})
		wantTheCallRan(t, "build", got)
		res := toolResultOf(t, "build", got)
		if res.IsError {
			t.Fatalf("a write the credential admits was refused: %s", res.Content[0].Text)
		}
		if tree := writtenTree(t, dist); !reflect.DeepEqual(tree, expected) {
			t.Errorf("%s does not hold exactly the files this listener renders, byte for byte:%s",
				dist, treeDifference(tree, expected))
		}
		payload := unmarshalBuildPayload(t, &res)
		if payload.OutputDir == nil || *payload.OutputDir != "dist" || payload.OutputDirWithheld {
			t.Errorf("output_dir = %v (withheld %v), want %q, named relative to the served root",
				payload.OutputDir, payload.OutputDirWithheld, "dist")
		}
		if !strings.Contains(res.Content[0].Text, "file(s) to dist:") {
			t.Errorf("the text does not name dist as where the files were written:\n%s", res.Content[0].Text)
		}
	})
}

// renderedOverTheListener is the tree the listener answers a detailed build of
// release with under credential, read off a call not sent write: the files and
// bytes a write over that same listener renders.
//
// Its file set is held to the fixture's, and its bytes to the render a server over
// the process's own streams answers for the same Release in the same workspace
// when it carries the listener's compiler version — so the listener is shown to
// render what the other transport renders under the same setting, and only the
// setting that moves the bytes is aligned. The call is held to having written
// nothing, because a reference that left a dist behind would let the write row
// pass on a stale tree.
func renderedOverTheListener(t *testing.T, f *scopeGridFixture, credential, release string, fx writeFixture) map[string]string {
	t.Helper()
	res := toolResultOf(t, "build", f.callTool(t, credential, "build",
		map[string]any{"release": release, "response_format": responseFormatDetailed}))
	if res.IsError {
		t.Fatalf("the listener's in-memory render was refused: %s", res.Content[0].Text)
	}
	tree := map[string]string{}
	for _, file := range unmarshalBuildPayload(t, &res).Files {
		tree[filepath.FromSlash(file.Path)] = file.Content
	}
	if got, want := treePaths(tree), treePaths(fx.expected); !reflect.DeepEqual(got, want) {
		t.Fatalf("the listener rendered %v; want the fixture's files %v", got, want)
	}
	if _, err := os.Lstat(filepath.Join(fx.root, "dist")); err == nil {
		t.Fatal("precondition: a build not sent write over the listener left a dist directory in the served workspace")
	}

	sameVersion := unlaunchedServer(fx.root)
	sameVersion.serverVersion = f.srv.serverVersion
	streams := callTool(t, sameVersion, "build",
		map[string]any{"release": fx.release, "workspace_root": fx.root, "response_format": responseFormatDetailed})
	if streams.IsError {
		t.Fatalf("the in-memory render over the process's own streams was refused: %s", streams.Content[0].Text)
	}
	overStreams := map[string]string{}
	for _, file := range unmarshalBuildPayload(t, streams).Files {
		overStreams[filepath.FromSlash(file.Path)] = file.Content
	}
	if !reflect.DeepEqual(tree, overStreams) {
		t.Fatalf("the listener renders other bytes than a server over the process's own streams carrying the "+
			"same compiler version:%s", treeDifference(tree, overStreams))
	}
	return tree
}

// TestAServerWithdrawingWorkspaceReachRefusesABuildSentWrite is the bound above
// the credential: where mcp.deny_reach names workspace, a call sent write is
// refused naming that setting, with no challenge — no scope lifts it — and nothing
// written, under a credential carrying every scope this server publishes. The same
// call not sent write is answered, which is what shows the refusal is the write's
// rather than an outage.
func TestAServerWithdrawingWorkspaceReachRefusesABuildSentWrite(t *testing.T) {
	fx := newWriteFixture(t)
	f := scopeGridListenerOver(t, fx.root, []string{string(opsurface.ReachWorkspace)}, nil)
	credential := f.grantOf(t, everyPublishedScope...)
	release := filepath.Base(fx.release)
	dist := filepath.Join(fx.root, "dist")

	got := f.callTool(t, credential, "build", map[string]any{"release": release, "write": true})
	if got.frame.Error == nil {
		t.Fatalf("a call sent write was answered with a result where the server withdraws workspace reach: %q", got.raw)
	}
	if challenge := got.header.Get(headerAuthenticate); challenge != "" {
		t.Errorf("the refusal carries the challenge %q, inviting a caller to obtain a credential that cannot lift "+
			"a bound the server's own configuration set", challenge)
	}
	if !strings.Contains(got.frame.Error.Message, "mcp.deny_reach") {
		t.Errorf("the refusal does not name mcp.deny_reach: %q", got.frame.Error.Message)
	}
	if _, err := os.Lstat(dist); err == nil {
		t.Error("a refused write left a dist directory in the served workspace")
	}

	if res := toolResultOf(t, "build", f.callTool(t, credential, "build", map[string]any{"release": release})); res.IsError {
		t.Errorf("a build not sent write was refused where the server withdraws workspace reach: %s",
			res.Content[0].Text)
	}
}

// TestABuildNotSentWriteWritesNothingAndSaysSo is the negative half over the
// process's own streams: the same call with write left out, and with write sent
// false, writes no dist, carries no output_dir key at all and opens its text with
// the in-memory line, exactly as a build answered before the argument existed.
//
// 🔴 PLANTED DEFECT: make buildArgs.writesArtifacts return true. Both rows redden:
// each finds a dist and an output_dir.
func TestABuildNotSentWriteWritesNothingAndSaysSo(t *testing.T) {
	for _, row := range []struct {
		name string
		args map[string]any
	}{
		{"write left out", map[string]any{}},
		{"write sent false", map[string]any{"write": false}},
	} {
		t.Run(row.name, func(t *testing.T) {
			fx := newWriteFixture(t)
			args := map[string]any{"release": fx.release, "workspace_root": fx.root}
			for k, v := range row.args {
				args[k] = v
			}
			res := callTool(t, newTestServer(t), "build", args)
			if res.IsError {
				t.Fatalf("the build was refused: %s", res.Content[0].Text)
			}
			if _, err := os.Lstat(filepath.Join(fx.root, "dist")); err == nil {
				t.Error("a build not sent write left a dist directory in the workspace")
			}
			keys := structuredKeys(t, res)
			for _, key := range []string{"output_dir", "output_dir_withheld"} {
				if keys[key] {
					t.Errorf("a build not sent write carries %q, whose absence is what says nothing was written", key)
				}
			}
			if want := "rendered " + strconv.Itoa(len(fx.expected)) + " file(s) in memory (no artifact written to disk):"; !strings.Contains(res.Content[0].Text, want) {
				t.Errorf("the text does not open with the in-memory line %q:\n%s", want, res.Content[0].Text)
			}
		})
	}
}

// TestABuildSentWriteIsHeldWhereTheCommandLineIsHeld drives the write's refusals
// and its one failure: an output directory that is a link leading out of the
// workspace is refused with nothing removed where it leads; a render that refuses
// writes nothing and names no output directory; and a write into a root nobody
// may write in fails naming the directory and its cause, and lists no file as
// written.
//
// 🔴 PLANTED DEFECT: in writeRenderedArtifacts (internal/mcp/tool_build.go), hand
// artifactout.WriteBuildOutput outDir itself as the root in place of
// writescope.RootFor(tw.root, filepath.Dir(outDir)). The link row reddens: a root
// that is a link is followed where it was named, so the write is held to the place
// the link leads to rather than to the workspace, clears the sentinel there and
// lands there.
func TestABuildSentWriteIsHeldWhereTheCommandLineIsHeld(t *testing.T) {
	t.Run("an output directory leading out of the workspace is refused with nothing removed", func(t *testing.T) {
		fx := newWriteFixture(t)
		elsewhere := t.TempDir()
		sentinel := filepath.Join(elsewhere, "boundaries", "old", "sentinel.txt")
		if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sentinel, []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(fx.root, "dist")); err != nil {
			t.Skipf("this platform cannot make the link the row needs: %v", err)
		}
		before := writtenTree(t, elsewhere)

		res := callTool(t, newTestServer(t), "build",
			map[string]any{"release": fx.release, "workspace_root": fx.root, "write": true})
		if !res.IsError {
			t.Fatalf("a write through a dist leading out of the workspace was answered as a success: %s",
				res.Content[0].Text)
		}
		if after := writtenTree(t, elsewhere); !reflect.DeepEqual(after, before) {
			t.Errorf("the place the link leads to changed: before %v, after %v", treePaths(before), treePaths(after))
		}
		if files := unmarshalBuildPayload(t, res).Files; len(files) != 0 {
			t.Errorf("a refused write lists %d file(s) as written", len(files))
		}
	})

	t.Run("a render that refuses writes nothing", func(t *testing.T) {
		fx := newWriteFixture(t)
		// The Manifest maps the Component onto a Recipe nobody declares, so the
		// build refuses before anything renders.
		writeRenderFixture(t, filepath.Join(fx.root, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/svc: no-such-recipe-q4z8
`)
		res := callTool(t, newTestServer(t), "build",
			map[string]any{"release": fx.release, "workspace_root": fx.root, "write": true})
		if !res.IsError {
			t.Fatalf("a Release whose mapping names no Recipe built: %s", res.Content[0].Text)
		}
		if _, err := os.Lstat(filepath.Join(fx.root, "dist")); err == nil {
			t.Error("a build refused before its render left a dist directory in the workspace")
		}
		if keys := structuredKeys(t, res); keys["output_dir"] || keys["output_dir_withheld"] {
			t.Error("a build refused before its render names an output directory, which says it reached the write")
		}
	})

	t.Run("a write into a root nobody may write in fails naming the directory", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("a folder's write permission does not bind this process here")
		}
		fx := newWriteFixture(t)
		if err := os.Chmod(fx.root, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(fx.root, 0o755) })
		dist := filepath.Join(fx.root, "dist")

		res := callTool(t, newTestServer(t), "build",
			map[string]any{"release": fx.release, "workspace_root": fx.root, "write": true})
		if !res.IsError {
			t.Fatalf("a write into a read-only root was answered as a success: %s", res.Content[0].Text)
		}
		if text := res.Content[0].Text; !strings.Contains(text, dist) || !strings.Contains(text, "permission denied") {
			t.Errorf("the failure does not name %s and its cause:\n%s", dist, text)
		}
		payload := unmarshalBuildPayload(t, res)
		if len(payload.Files) != 0 {
			t.Errorf("a failed write lists %d file(s) as written", len(payload.Files))
		}
		if payload.OutputDir == nil || *payload.OutputDir != dist {
			t.Errorf("a failed write names output_dir %v, want %q, the directory the failure concerns",
				payload.OutputDir, dist)
		}
	})
}
