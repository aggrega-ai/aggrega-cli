// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// attachVariantFeature is a Feature document applying to every node labelled
// tier=edge, which is what the graph node the proofs below address carries.
func attachVariantFeature(name string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Feature\nmetadata:\n  name: " + name +
		"\n  version: \"1.0.0\"\nspec:\n  appliesTo: tier=edge\n"
}

// attachVariantWorkspace is graphNodeWorkspace with a second Release beside the
// first and a workspace configuration giving each Release catalog directories of
// its own, opened the way the daemon opens any workspace a request names. It
// returns the server, the root and the two Releases' paths relative to it.
//
// Every Feature below applies to the graph node checkout (tier=edge), so which of
// them an answer holds is decided by whose terms it was given in and by nothing
// about the labels:
//
//   - features-a/ declares example/variant-tls and example/only-a; features-b/
//     declares its own example/variant-tls. Both sit under the root, so the walk of
//     the workspace reaches both whichever Release is named.
//   - beside-feature.yaml declares example/beside under no catalog directory, so it
//     belongs to every Release's answer.
//   - outside-b, outside the root, declares example/b-outside and is read by the
//     second Release alone, so it can reach an answer only through that Release's
//     own lookup.
//   - app-release.yaml reads catalog/, features-a/ and the fixture's Features
//     directory outside the root; app-release-b.yaml reads catalog/, features-b/
//     and outside-b. The workspace's own list is catalog/ and that Features
//     directory.
func attachVariantWorkspace(t *testing.T) (srv *Server, ws, releaseA, releaseB string) {
	t.Helper()
	ws, featuresDir, releaseA := graphNodeWorkspace(t)
	outsideB := t.TempDir()
	releaseB = "app-release-b.yaml"

	release, err := os.ReadFile(filepath.Join(ws, releaseA))
	if err != nil {
		t.Fatalf("read %s: %v", releaseA, err)
	}
	if n := strings.Count(string(release), "name: app-release\n"); n != 1 {
		t.Fatalf("%s: want exactly one metadata.name to rename, found %d", releaseA, n)
	}
	renamed := strings.Replace(string(release), "name: app-release\n", "name: app-release-b\n", 1)

	writeCursorFiles(t, map[string]string{
		filepath.Join(ws, "features-a", "tls.yaml"):    attachVariantFeature("example/variant-tls"),
		filepath.Join(ws, "features-a", "only-a.yaml"): attachVariantFeature("example/only-a"),
		filepath.Join(ws, "features-b", "tls.yaml"):    attachVariantFeature("example/variant-tls"),
		filepath.Join(ws, "beside-feature.yaml"):       attachVariantFeature("example/beside"),
		filepath.Join(outsideB, "b-outside.yaml"):      attachVariantFeature("example/b-outside"),
		filepath.Join(ws, releaseB):                    renamed,
	})

	// The second Release is pinned to the same entities as the first, so its graph
	// builds through the same lockfile gate.
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(filepath.Join(ws, releaseA)))
	if err != nil || lf == nil {
		t.Fatalf("load the fixture's lockfile: %v", err)
	}
	lf.Release = "./" + releaseB
	if err := lockfile.WriteLockfile(lockfile.PathFor(filepath.Join(ws, releaseB)), lf); err != nil {
		t.Fatalf("write the second Release's lockfile: %v", err)
	}

	config := fmt.Sprintf("workspace:\n  catalog_dirs:\n    - catalog\n    - %s\n  releases:\n"+
		"    %s:\n      catalog_dirs:\n        - catalog\n        - features-a\n        - %s\n"+
		"    %s:\n      catalog_dirs:\n        - catalog\n        - features-b\n        - %s\n",
		featuresDir, releaseA, featuresDir, releaseB, outsideB)
	if err := os.WriteFile(filepath.Join(ws, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	srv, err = newTestSession(t, ws, bytes.NewReader(nil), &bytes.Buffer{}).workspaceFor(ws)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", ws, err)
	}
	return srv, ws, releaseA, releaseB
}

// candidateLines renders a candidate list as "name [file] origin" lines, in the
// order the answer gave them, so an assertion reads which document each candidate
// came from and where it was read rather than only its name.
func candidateLines(cands []AttachCandidate) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.Name+" ["+c.File+"] "+c.Origin)
	}
	return out
}

// TestWhatMayAttachToAGraphNodeIsWhatItsReleaseReaches is the invocation proof for
// what may attach to a node on a compiled graph: one daemon, the same node on each
// Release's graph, and the same node's labels supplied with no Release.
//
// Each row names the whole candidate list, in the answer's order, so a candidate
// present or missing from either population fails it:
//
//   - each Release is offered its own example/variant-tls and never the other's,
//     and example/only-a only by the Release whose directory declares it — the
//     reach over the workspace documents;
//   - example/b-outside reaches the second Release's answer alone, and the
//     workspace list's Features reach the first's alone — the catalog-directory
//     Features are read through the lookup the graph was built from;
//   - example/beside, under no catalog directory, is offered on every row;
//   - a supplied label set names no Release, so it is answered over every
//     workspace document and the workspace's own directories, as it always was.
//
// Planted defects each catches: draw the workspace documents without the Release's
// reach and the second Release's row gains features-a/'s two Features; read the
// catalog-directory Features from the workspace's lookup rather than the Release's
// and the second Release's row loses example/b-outside and gains the workspace
// list's two; apply a Release's reach to a supplied label set and its row loses a
// variant.
func TestWhatMayAttachToAGraphNodeIsWhatItsReleaseReaches(t *testing.T) {
	t.Parallel()
	srv, _, releaseA, releaseB := attachVariantWorkspace(t)
	edge := map[string]string{"component.aggrega.ai/type": "server", "tier": "edge"}

	cases := []struct {
		name   string
		params attachCandidatesParams
		want   []string
	}{
		{
			name:   "the graph node on the first Release's graph",
			params: attachCandidatesParams{GraphNode: "checkout", Release: releaseA},
			want: []string{
				"example/beside [beside-feature.yaml] workspace",
				"example/edge-tls [] catalog",
				"example/observability [] catalog",
				"example/only-a [features-a/only-a.yaml] workspace",
				"example/variant-tls [features-a/tls.yaml] workspace",
			},
		},
		{
			name:   "the graph node on the second Release's graph",
			params: attachCandidatesParams{GraphNode: "checkout", Release: releaseB},
			want: []string{
				"example/b-outside [] catalog",
				"example/beside [beside-feature.yaml] workspace",
				"example/variant-tls [features-b/tls.yaml] workspace",
			},
		},
		{
			name:   "the same labels supplied, naming no Release",
			params: attachCandidatesParams{Labels: edge},
			want: []string{
				"example/beside [beside-feature.yaml] workspace",
				"example/edge-tls [] catalog",
				"example/observability [] catalog",
				"example/only-a [features-a/only-a.yaml] workspace",
				"example/variant-tls [features-a/tls.yaml] workspace",
				"example/variant-tls [features-b/tls.yaml] workspace",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := decodeAttachResult(t, callDaemonMethod(t, srv, "aggrega/attachCandidates", tc.params))
			if got := candidateLines(result.Candidates); !slices.Equal(got, tc.want) {
				t.Errorf("candidates:\n got  %q\n want %q", got, tc.want)
			}
			if tc.params.GraphNode != "" && result.NodeLabels["tier"] != "edge" {
				t.Errorf("the match ran over %v, not the graph node's own labels", result.NodeLabels)
			}
		})
	}
}

// TestAGraphNodeOnAReleaseNoLongerOnDiskIsRefusedAsBefore pins that answering in a
// Release's terms adds no refusal of its own: a Release the graph cannot be built
// for is refused by the graph's own gate, before its terms are asked for, with the
// condition and words it was refused with before.
func TestAGraphNodeOnAReleaseNoLongerOnDiskIsRefusedAsBefore(t *testing.T) {
	t.Parallel()
	srv, _, _, _ := attachVariantWorkspace(t)
	resp := attachRequestPastPushes(t, srv, attachCandidatesParams{GraphNode: "checkout", Release: "release-missing.yaml"})
	if resp.Error == nil {
		t.Fatalf("a Release with no file and no lockfile was answered: %s", resp.Result)
	}
	if resp.Error.Code != errLockfileAbsent {
		t.Errorf("code = %d, want the lockfile gate's %d: %s", resp.Error.Code, errLockfileAbsent, resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "aggrega pull --release release-missing.yaml") {
		t.Errorf("the refusal does not name what to run for the Release named: %q", resp.Error.Message)
	}
}
