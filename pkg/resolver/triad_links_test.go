// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// triadLinksAggregate is the Aggregate a row lays, carrying the name the Release
// refers to it by.
const triadLinksAggregate = "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  version: \"1.0.0\"\n" +
	"  name: probe-app\nspec:\n  components: {}\n"

// triadLinksManifest is the Manifest a row lays.
const triadLinksManifest = "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  version: \"1.0.0\"\n" +
	"  name: probe-manifest\nspec:\n  mappings:\n    components:\n      type: {}\n"

// triadLinksRelease is the Release a row names, referring to its Aggregate and its
// Manifest by name, so resolving either scans the directory it is named in.
const triadLinksRelease = "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: probe-release\n" +
	"  version: \"1.0.0\"\n  environment: production\nspec:\n  aggregate: probe-app\n  manifest: probe-manifest\n"

// triadLinksChildManifest is a Manifest named on its own that extends the one a
// row lays by name.
const triadLinksChildManifest = "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  version: \"1.0.0\"\n" +
	"  name: probe-child\nspec:\n  extends: probe-manifest\n  mappings:\n    components:\n      type: {}\n"

// TestTheReleaseDirectoryScanDoesNotReadALinkLeadingOut drives the scan that
// resolves the names a Release gives its Aggregate and its Manifest, over one
// layout per row, and reads back what each resolution found or halted on.
//
// Every row offers a catalog tier holding a decoy Aggregate of the same name. A row
// whose document beside the Release is a link leading out of the root the scan is
// held to must halt naming that link rather than resolve the decoy — passing over
// the link silently is exactly what would let the decoy resolve in its place — and
// a row whose link leads inside, or whose Release directory lies outside the
// workspace with nothing leading out of it, must resolve the document beside it.
//
// The rows naming an origin hand the scan the file the resolution started at — a
// Release, or a Manifest named on its own — which was admitted where it was named.
// That one entry is not judged again, so a Release named through a link leading
// out resolves the names beside the place it was named; every other entry is held
// as before, so a neighbouring Release that is a link leading out still halts.
func TestTheReleaseDirectoryScanDoesNotReadALinkLeadingOut(t *testing.T) {
	decoy := func(name string) (ResolvedAggregate, bool) {
		return ResolvedAggregate{Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: name}},
			Path: "decoy.yaml"}, true
	}
	rows := []struct {
		name string
		// lay places the documents and returns the Release's directory, given the
		// workspace and a directory outside it.
		lay func(t *testing.T, ws, outside string) string
		// manifest resolves the Manifest rather than the Aggregate.
		manifest bool
		// origin is the file the resolution started at, appended to the Release's
		// directory as written (so a row can spell it another way); empty for none.
		origin string
		// haltAt is the link a halt must name, relative to ws ("outside/…" relative
		// to the directory outside); empty where the document resolves.
		haltAt string
	}{
		{
			name: "control: the Aggregate beside the Release",
			lay: func(t *testing.T, ws, outside string) string {
				writeTriadFile(t, ws, "aggregate.yaml", triadLinksAggregate)
				return ws
			},
		},
		{
			name: "the Aggregate beside the Release a link leading out of the workspace",
			lay: func(t *testing.T, ws, outside string) string {
				writeTriadFile(t, outside, "aggregate.yaml", triadLinksAggregate)
				triadLink(t, filepath.Join(outside, "aggregate.yaml"), filepath.Join(ws, "aggregate.yaml"))
				return ws
			},
			haltAt: "aggregate.yaml",
		},
		{
			name: "the Manifest beside the Release a link leading out of the workspace",
			lay: func(t *testing.T, ws, outside string) string {
				writeTriadFile(t, outside, "manifest.yaml", triadLinksManifest)
				triadLink(t, filepath.Join(outside, "manifest.yaml"), filepath.Join(ws, "manifest.yaml"))
				return ws
			},
			manifest: true,
			haltAt:   "manifest.yaml",
		},
		{
			name: "control: the Manifest beside the Release",
			lay: func(t *testing.T, ws, outside string) string {
				writeTriadFile(t, ws, "manifest.yaml", triadLinksManifest)
				return ws
			},
			manifest: true,
		},
		{
			name: "an unrelated document beside the Release a link leading out halts the scan as well",
			lay: func(t *testing.T, ws, outside string) string {
				writeTriadFile(t, ws, "aggregate.yaml", triadLinksAggregate)
				writeTriadFile(t, outside, "other.yaml", "kind: ConfigMap\n")
				triadLink(t, filepath.Join(outside, "other.yaml"), filepath.Join(ws, "other.yaml"))
				return ws
			},
			haltAt: "other.yaml",
		},
		{
			name: "the Aggregate a link leading to a file elsewhere inside the workspace is read",
			lay: func(t *testing.T, ws, outside string) string {
				if err := os.MkdirAll(filepath.Join(ws, "shared"), 0o755); err != nil {
					t.Fatal(err)
				}
				writeTriadFile(t, filepath.Join(ws, "shared"), "aggregate.yaml", triadLinksAggregate)
				if err := os.MkdirAll(filepath.Join(ws, "releases"), 0o755); err != nil {
					t.Fatal(err)
				}
				triadLink(t, filepath.Join(ws, "shared", "aggregate.yaml"), filepath.Join(ws, "releases", "aggregate.yaml"))
				return filepath.Join(ws, "releases")
			},
		},
		{
			name: "control: a Release directory outside the workspace is a root of its own",
			lay: func(t *testing.T, ws, outside string) string {
				dir := filepath.Join(outside, "rel")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				writeTriadFile(t, dir, "aggregate.yaml", triadLinksAggregate)
				return dir
			},
		},
		{
			name: "beneath a Release directory outside the workspace, a link leading out of it halts the scan",
			lay: func(t *testing.T, ws, outside string) string {
				dir := filepath.Join(outside, "rel")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				writeTriadFile(t, outside, "aggregate.yaml", triadLinksAggregate)
				triadLink(t, filepath.Join(outside, "aggregate.yaml"), filepath.Join(dir, "aggregate.yaml"))
				return dir
			},
			haltAt: "outside/rel/aggregate.yaml",
		},
		{
			name: "the Release being resolved a link leading out of the workspace resolves its Aggregate beside it",
			lay: func(t *testing.T, ws, outside string) string {
				writeTriadFile(t, ws, "aggregate.yaml", triadLinksAggregate)
				writeTriadFile(t, outside, "release-copy.yaml", triadLinksRelease)
				triadLink(t, filepath.Join(outside, "release-copy.yaml"), filepath.Join(ws, "release-linked.yaml"))
				return ws
			},
			origin: "release-linked.yaml",
		},
		{
			name: "the Release being resolved a link leading out of the workspace resolves its Manifest beside it",
			lay: func(t *testing.T, ws, outside string) string {
				writeTriadFile(t, ws, "manifest.yaml", triadLinksManifest)
				writeTriadFile(t, outside, "release-copy.yaml", triadLinksRelease)
				triadLink(t, filepath.Join(outside, "release-copy.yaml"), filepath.Join(ws, "release-linked.yaml"))
				return ws
			},
			manifest: true,
			origin:   "release-linked.yaml",
		},
		{
			name: "the Release being resolved is the same entry however its path is spelled",
			lay: func(t *testing.T, ws, outside string) string {
				writeTriadFile(t, ws, "aggregate.yaml", triadLinksAggregate)
				writeTriadFile(t, outside, "release-copy.yaml", triadLinksRelease)
				triadLink(t, filepath.Join(outside, "release-copy.yaml"), filepath.Join(ws, "release-linked.yaml"))
				return ws
			},
			origin: "./release-linked.yaml",
		},
		{
			name: "a sibling Release a link leading out still halts the scan beside the Release being resolved",
			lay: func(t *testing.T, ws, outside string) string {
				writeTriadFile(t, ws, "aggregate.yaml", triadLinksAggregate)
				writeTriadFile(t, ws, "release.yaml", triadLinksRelease)
				writeTriadFile(t, outside, "sibling-copy.yaml", triadLinksRelease)
				triadLink(t, filepath.Join(outside, "sibling-copy.yaml"), filepath.Join(ws, "sibling.yaml"))
				return ws
			},
			origin: "release.yaml",
			haltAt: "sibling.yaml",
		},
		{
			name: "a sibling Release a link leading out halts though the Release being resolved is one as well",
			lay: func(t *testing.T, ws, outside string) string {
				writeTriadFile(t, ws, "aggregate.yaml", triadLinksAggregate)
				writeTriadFile(t, outside, "release-copy.yaml", triadLinksRelease)
				triadLink(t, filepath.Join(outside, "release-copy.yaml"), filepath.Join(ws, "release-linked.yaml"))
				writeTriadFile(t, outside, "sibling-copy.yaml", triadLinksRelease)
				triadLink(t, filepath.Join(outside, "sibling-copy.yaml"), filepath.Join(ws, "sibling.yaml"))
				return ws
			},
			origin: "release-linked.yaml",
			haltAt: "sibling.yaml",
		},
		{
			name: "a Release outside the workspace named through a link leading out of its own directory resolves beside it",
			lay: func(t *testing.T, ws, outside string) string {
				dir := filepath.Join(outside, "rel")
				elsewhere := filepath.Join(outside, "elsewhere")
				for _, d := range []string{dir, elsewhere} {
					if err := os.MkdirAll(d, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				writeTriadFile(t, dir, "aggregate.yaml", triadLinksAggregate)
				writeTriadFile(t, elsewhere, "release-copy.yaml", triadLinksRelease)
				triadLink(t, filepath.Join(elsewhere, "release-copy.yaml"), filepath.Join(dir, "release-linked.yaml"))
				return dir
			},
			origin: "release-linked.yaml",
		},
		{
			name: "a Manifest named on its own a link leading out resolves the Manifest it extends beside it",
			lay: func(t *testing.T, ws, outside string) string {
				writeTriadFile(t, ws, "manifest.yaml", triadLinksManifest)
				writeTriadFile(t, outside, "child-copy.yaml", triadLinksChildManifest)
				triadLink(t, filepath.Join(outside, "child-copy.yaml"), filepath.Join(ws, "child-linked.yaml"))
				return ws
			},
			manifest: true,
			origin:   "child-linked.yaml",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := t.TempDir()
			outside := t.TempDir()
			dir := row.lay(t, ws, outside)
			origin := ""
			if row.origin != "" {
				origin = dir + string(filepath.Separator) + filepath.FromSlash(row.origin)
			}
			var err error
			var name string
			if row.manifest {
				var res ResolvedManifest
				res, err = ResolveManifest(ws, dir, origin, "probe-manifest")
				if err == nil {
					name = res.Manifest.Metadata.Name + " from " + filepath.Base(res.Path)
				}
			} else {
				var res ResolvedAggregate
				res, err = ResolveAggregate(ws, dir, origin, "probe-app", decoy, nil)
				if err == nil {
					name = res.Aggregate.Metadata.Name + " from " + filepath.Base(res.Path)
				}
			}
			if row.haltAt == "" {
				if err != nil {
					t.Fatalf("refused a document the root holds: %v", err)
				}
				if strings.HasSuffix(name, "decoy.yaml") {
					t.Fatalf("resolved %s, the catalog's decoy, rather than the document beside the Release", name)
				}
				return
			}
			want := filepath.Join(ws, filepath.FromSlash(row.haltAt))
			if strings.HasPrefix(row.haltAt, "outside/") {
				want = filepath.Join(outside, filepath.FromSlash(strings.TrimPrefix(row.haltAt, "outside/")))
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read-leads-out-of-workspace halt naming %s, got %v (resolved %q)", want, err, name)
			}
			if code, _ := cerrors.ShortCodeOf(err); code != "E0321" {
				t.Errorf("short code = %q, want E0321", code)
			}
			if !strings.Contains(err.Error(), "the link "+want+" leads to ") {
				t.Errorf("the halt must name the link %s:\n%v", want, err)
			}
		})
	}
}

// TestAManifestChainHaltsOnALinkBesideItAsTheScanDoes composes a Manifest whose
// extends names its parent by name, so the walk scans the directory for it, and
// lays a neighbouring document a link leading out of the workspace. The chain halts
// with the read-leads-out-of-workspace condition naming that link, as the scan does
// for a Release's own references, rather than as a parent the walk could not find:
// the author's fix is the link, not the name. The control composes with the link
// absent.
func TestAManifestChainHaltsOnALinkBesideItAsTheScanDoes(t *testing.T) {
	for _, row := range []struct {
		name string
		link bool
	}{
		{name: "control: nothing beside the Manifest leads out"},
		{name: "a neighbour a link leading out halts the chain with its own condition", link: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			writeTriadFile(t, ws, "manifest.yaml", triadLinksManifest)
			writeTriadFile(t, ws, "child.yaml", triadLinksChildManifest)
			if row.link {
				writeTriadFile(t, outside, "other.yaml", "kind: ConfigMap\n")
				triadLink(t, filepath.Join(outside, "other.yaml"), filepath.Join(ws, "other.yaml"))
			}
			childPath := filepath.Join(ws, "child.yaml")
			child, err := ResolveManifest(ws, ws, childPath, "./child.yaml")
			if err != nil {
				t.Fatalf("resolve the child Manifest by its path: %v", err)
			}
			composed, err := ResolveManifestExtends(ws, ws, childPath, child)
			if !row.link {
				if err != nil {
					t.Fatalf("the chain refused a directory with nothing leading out: %v", err)
				}
				if composed == nil || composed.Metadata.Name != "probe-child" {
					t.Fatalf("composed %+v, want the child probe-child", composed)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read-leads-out-of-workspace halt, got %v", err)
			}
			if code, _ := cerrors.ShortCodeOf(err); code != "E0321" {
				t.Errorf("short code = %q, want E0321", code)
			}
			if want := "the link " + filepath.Join(ws, "other.yaml") + " leads to "; !strings.Contains(err.Error(), want) {
				t.Errorf("the halt must name the link:\n%v", err)
			}
		})
	}
}

func triadLink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
