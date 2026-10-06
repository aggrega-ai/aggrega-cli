// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The references a Release and a Manifest make to other files are resolved by the
// shared closure resolution every build, pull, preview, graph, lint, validation and
// describe runs, and the parameters a Release imports by the build that follows it.
// These tests drive both over a workspace laid out in a temporary directory beside a
// sibling directory outside it, from the self-contained fullstack-app fixture: its
// Release, Aggregate and Manifest are copied into the workspace, and its catalog is
// named as a catalog directory where it lies, outside the workspace, as a
// configured catalog directory may.

// referenceFixture is the fixture the rows rearrange: its three documents, read
// once, and its catalog directory.
type referenceFixture struct {
	release, aggregate, manifest string
	catalogDir                   string
}

// fixtureReleaseHead is the part of the fixture Release a row rewrites: the two
// references it makes, beside it.
const fixtureReleaseHead = "  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n"

func loadReferenceFixture(t *testing.T) referenceFixture {
	t.Helper()
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	dir := filepath.Join(filepath.Dir(filepath.Dir(cwd)), "internal", "compiler", "testdata", "fullstack-app")
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read the fixture's %s: %v", name, err)
		}
		return string(b)
	}
	fx := referenceFixture{
		release:    read("release.yaml"),
		aggregate:  read("aggregate.yaml"),
		manifest:   read("manifest.yaml"),
		catalogDir: filepath.Join(dir, "catalog"),
	}
	if !strings.Contains(fx.release, fixtureReleaseHead) {
		t.Fatalf("the fixture Release no longer names its documents as %q, so no row below rewrites it", fixtureReleaseHead)
	}
	return fx
}

// releaseWith is the fixture Release with its two references replaced by head.
func (fx referenceFixture) releaseWith(head string) string {
	return strings.Replace(fx.release, fixtureReleaseHead, head, 1)
}

// childRelease is a Release that names a parent and nothing else, so every other
// field is the parent's.
func childRelease(extends string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: child\n  environment: production\n" +
		"  version: \"1.0.0\"\nspec:\n  extends: " + extends + "\n"
}

// writeReferenceFile writes body at path, making its directory.
func writeReferenceFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// referenceView is the request-scoped lookup over the fixture's catalog for the
// workspace at ws.
func referenceView(ws, catalogDir string, wsCfg workspaceconfig.Config) *catalog.LazyLookup {
	return catalog.NewLazyLookup(ws, wsCfg.ResolvedArtifactCacheDir(), []string{catalogDir}, nil).ForRequest(nil)
}

// namedRelease is a Release called name that extends extends and declares
// nothing else, for the middle link of a chain.
func namedRelease(name, extends string) string {
	return strings.Replace(childRelease(extends), "  name: child\n", "  name: "+name+"\n", 1)
}

// realPath is p with its links resolved, the spelling a refusal names the place a
// link leads to in.
func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("resolve %s: %v", p, err)
	}
	return r
}

// outsideTheWorkspace and outsideTheNamedDirectory are what a refusal says about
// the directory a reference must stay inside: the workspace, or the directory of a
// Release named outside it.
func outsideTheWorkspace(ws string) string { return "outside the workspace " + ws }

func outsideTheNamedDirectory(dir, ws string) string {
	return "outside " + dir + ", the directory of a document named outside the workspace " + ws
}

// TestEveryReferenceAWorkspaceDocumentMakesIsHeldToTheWorkspace drives one row per
// family of reference, each with one fault, through the real entries, and reads
// back where the resolution stopped and on which condition.
//
// A Release in the workspace has its references held to the workspace; a Release
// the owner typed from outside the workspace has them held to its own directory,
// and so has every Release its extends chain leads to. Each link of a chain names
// its parent beside itself.
//
// The controls are rows of the same table: the fixture as it is, a parameters
// import beside the Release and beside a link of a chain, a chain whose second link
// sits in another directory and names its parent beside itself, a Release typed
// from outside the workspace whose references stay in its own directory, and such a
// Release's chain whose link in a subdirectory names a parent elsewhere in that
// directory. Without them, a build that refused every reference would pass every
// refusal row. What a refusal must name is composed from the row's own directories,
// so every expected path is one the test made.
func TestEveryReferenceAWorkspaceDocumentMakesIsHeldToTheWorkspace(t *testing.T) {
	fx := loadReferenceFixture(t)

	for _, row := range []struct {
		name string
		// lay writes the row's documents into ws and outside and returns the Release
		// the row resolves.
		lay func(t *testing.T, ws, outside string) string
		// refusedAt is the closure stage the row halts at; zero with refusedInBuild
		// false is a row that resolves and builds.
		refusedAt ClosureStage
		// refusedInBuild is a row whose closure resolves and whose build halts: the
		// parameters a Release heading no chain imports are read by the build.
		refusedInBuild bool
		// names are what the refusal must say, beyond its condition, given the row's
		// workspace and the directory outside it.
		names func(t *testing.T, ws, outside string) []string
	}{
		{
			name: "control: every reference beside the Release",
			lay: func(t *testing.T, ws, _ string) string {
				writeReferenceFile(t, filepath.Join(ws, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(ws, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(ws, "release.yaml"), fx.release)
				return filepath.Join(ws, "release.yaml")
			},
		},
		{
			name: "a Release's aggregate climbing out of the workspace",
			lay: func(t *testing.T, ws, outside string) string {
				writeReferenceFile(t, filepath.Join(outside, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(ws, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(ws, "release.yaml"),
					fx.releaseWith("  aggregate: ../outside/aggregate.yaml\n  manifest: ./manifest.yaml\n"))
				return filepath.Join(ws, "release.yaml")
			},
			refusedAt: ClosureStageLoadAggregate,
			names: func(_ *testing.T, ws, outside string) []string {
				return []string{".spec.aggregate: ../outside/aggregate.yaml", "resolved from " + ws,
					"which leads to " + filepath.Join(outside, "aggregate.yaml"), outsideTheWorkspace(ws)}
			},
		},
		{
			name: "a Release's aggregate carried out of the workspace by a link",
			lay: func(t *testing.T, ws, outside string) string {
				writeReferenceFile(t, filepath.Join(outside, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(ws, "manifest.yaml"), fx.manifest)
				if err := os.Symlink(outside, filepath.Join(ws, "shared")); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
				writeReferenceFile(t, filepath.Join(ws, "release.yaml"),
					fx.releaseWith("  aggregate: ./shared/aggregate.yaml\n  manifest: ./manifest.yaml\n"))
				return filepath.Join(ws, "release.yaml")
			},
			refusedAt: ClosureStageLoadAggregate,
			names: func(t *testing.T, ws, outside string) []string {
				return []string{".spec.aggregate: ./shared/aggregate.yaml", outsideTheWorkspace(ws),
					"the link " + filepath.Join(ws, "shared") + " leads to " + realPath(t, outside)}
			},
		},
		{
			name: "a Release's manifest climbing out of the workspace",
			lay: func(t *testing.T, ws, outside string) string {
				writeReferenceFile(t, filepath.Join(ws, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(outside, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(ws, "release.yaml"),
					fx.releaseWith("  aggregate: ./aggregate.yaml\n  manifest: ../outside/manifest.yaml\n"))
				return filepath.Join(ws, "release.yaml")
			},
			refusedAt: ClosureStageResolveManifest,
			names: func(_ *testing.T, ws, outside string) []string {
				return []string{".spec.manifest: ../outside/manifest.yaml",
					"which leads to " + filepath.Join(outside, "manifest.yaml"), outsideTheWorkspace(ws)}
			},
		},
		{
			name: "a Release's extends climbing out of the workspace",
			lay: func(t *testing.T, ws, outside string) string {
				writeReferenceFile(t, filepath.Join(ws, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(ws, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(outside, "base.yaml"), fx.release)
				writeReferenceFile(t, filepath.Join(ws, "release.yaml"), childRelease("../outside/base.yaml"))
				return filepath.Join(ws, "release.yaml")
			},
			refusedAt: ClosureStageResolveReleaseExtends,
			names: func(_ *testing.T, ws, outside string) []string {
				return []string{".spec.extends: ../outside/base.yaml", "written in " + filepath.Join(ws, "release.yaml"),
					"which leads to " + filepath.Join(outside, "base.yaml"), outsideTheWorkspace(ws)}
			},
		},
		{
			name: "a Manifest's extends climbing out of the workspace",
			lay: func(t *testing.T, ws, outside string) string {
				writeReferenceFile(t, filepath.Join(ws, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(outside, "base-manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(ws, "manifest.yaml"),
					strings.Replace(fx.manifest, "\nspec:\n", "\nspec:\n  extends: ../outside/base-manifest.yaml\n", 1))
				writeReferenceFile(t, filepath.Join(ws, "release.yaml"), fx.release)
				return filepath.Join(ws, "release.yaml")
			},
			refusedAt: ClosureStageComposeManifest,
			names: func(_ *testing.T, ws, outside string) []string {
				return []string{".spec.extends: ../outside/base-manifest.yaml", "written in " + filepath.Join(ws, "manifest.yaml"),
					"which leads to " + filepath.Join(outside, "base-manifest.yaml"), outsideTheWorkspace(ws)}
			},
		},
		{
			name: "a parameters import of a chain link climbing out of the workspace",
			lay: func(t *testing.T, ws, outside string) string {
				writeReferenceFile(t, filepath.Join(ws, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(ws, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(outside, "params.yaml"), "{}\n")
				writeReferenceFile(t, filepath.Join(ws, "base.yaml"),
					fx.releaseWith(fixtureReleaseHead+"  parameters: {$import: ../outside/params.yaml}\n"))
				writeReferenceFile(t, filepath.Join(ws, "release.yaml"), childRelease("./base.yaml"))
				return filepath.Join(ws, "release.yaml")
			},
			refusedAt: ClosureStageResolveReleaseExtends,
			names: func(_ *testing.T, ws, outside string) []string {
				return []string{".spec.parameters.$import: ../outside/params.yaml", "written in " + filepath.Join(ws, "base.yaml"),
					"which leads to " + filepath.Join(outside, "params.yaml"), outsideTheWorkspace(ws)}
			},
		},
		{
			name: "a parameters import of a Release heading no chain climbing out of the workspace",
			lay: func(t *testing.T, ws, outside string) string {
				writeReferenceFile(t, filepath.Join(ws, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(ws, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(outside, "params.yaml"), "{}\n")
				writeReferenceFile(t, filepath.Join(ws, "release.yaml"),
					fx.releaseWith(fixtureReleaseHead+"  parameters: {$import: ../outside/params.yaml}\n"))
				return filepath.Join(ws, "release.yaml")
			},
			refusedInBuild: true,
			names: func(_ *testing.T, ws, outside string) []string {
				return []string{".spec.parameters.$import: ../outside/params.yaml", "written in " + filepath.Join(ws, "release.yaml"),
					"which leads to " + filepath.Join(outside, "params.yaml"), outsideTheWorkspace(ws)}
			},
		},
		{
			name: "control: a parameters import beside a Release heading no chain",
			lay: func(t *testing.T, ws, _ string) string {
				writeReferenceFile(t, filepath.Join(ws, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(ws, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(ws, "params.yaml"), "{}\n")
				writeReferenceFile(t, filepath.Join(ws, "release.yaml"),
					fx.releaseWith(fixtureReleaseHead+"  parameters: {$import: ./params.yaml}\n"))
				return filepath.Join(ws, "release.yaml")
			},
		},
		{
			name: "control: a parameters import beside a link of a chain",
			lay: func(t *testing.T, ws, _ string) string {
				writeReferenceFile(t, filepath.Join(ws, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(ws, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(ws, "params.yaml"), "{}\n")
				writeReferenceFile(t, filepath.Join(ws, "base.yaml"),
					fx.releaseWith(fixtureReleaseHead+"  parameters: {$import: ./params.yaml}\n"))
				writeReferenceFile(t, filepath.Join(ws, "release.yaml"), childRelease("./base.yaml"))
				return filepath.Join(ws, "release.yaml")
			},
		},
		{
			// The second link sits in envs/ and names its parent ./base.yaml, which is
			// envs/base.yaml beside it. A base.yaml that is no Release at all sits beside
			// the Release the command names, so a chain resolving every link from there
			// would fail on it, and only one resolving each link beside its writer builds.
			name: "control: each link of a chain across directories names its parent beside itself",
			lay: func(t *testing.T, ws, _ string) string {
				writeReferenceFile(t, filepath.Join(ws, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(ws, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(ws, "base.yaml"), "::: the file a start-directory reading would open :::\n")
				writeReferenceFile(t, filepath.Join(ws, "envs", "base.yaml"), fx.release)
				writeReferenceFile(t, filepath.Join(ws, "envs", "mid.yaml"), namedRelease("mid", "./base.yaml"))
				writeReferenceFile(t, filepath.Join(ws, "release.yaml"), childRelease("./envs/mid.yaml"))
				return filepath.Join(ws, "release.yaml")
			},
		},
		{
			name: "control: a Release typed from outside the workspace, its references in its own directory",
			lay: func(t *testing.T, _, outside string) string {
				named := filepath.Join(outside, "releases")
				writeReferenceFile(t, filepath.Join(named, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(named, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(named, "release.yaml"), fx.release)
				return filepath.Join(named, "release.yaml")
			},
		},
		{
			name: "a Release typed from outside the workspace, its aggregate climbing out of its own directory",
			lay: func(t *testing.T, _, outside string) string {
				named := filepath.Join(outside, "releases")
				writeReferenceFile(t, filepath.Join(outside, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(named, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(named, "release.yaml"),
					fx.releaseWith("  aggregate: ../aggregate.yaml\n  manifest: ./manifest.yaml\n"))
				return filepath.Join(named, "release.yaml")
			},
			refusedAt: ClosureStageLoadAggregate,
			names: func(_ *testing.T, ws, outside string) []string {
				named := filepath.Join(outside, "releases")
				return []string{".spec.aggregate: ../aggregate.yaml", "resolved from " + named,
					"which leads to " + filepath.Join(outside, "aggregate.yaml"), outsideTheNamedDirectory(named, ws),
					"reaches only files inside its own directory " + named}
			},
		},
		{
			// The chain's second link sits in envs/ and names ../base.yaml: outside its own
			// directory, inside the directory of the Release the owner typed — which is the
			// one every reference along the chain is held to.
			name: "control: a chain typed from outside the workspace, a link naming a parent elsewhere in the typed Release's directory",
			lay: func(t *testing.T, _, outside string) string {
				named := filepath.Join(outside, "releases")
				writeReferenceFile(t, filepath.Join(named, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(named, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(named, "base.yaml"), fx.release)
				writeReferenceFile(t, filepath.Join(named, "envs", "mid.yaml"), namedRelease("mid", "../base.yaml"))
				writeReferenceFile(t, filepath.Join(named, "release.yaml"), childRelease("./envs/mid.yaml"))
				return filepath.Join(named, "release.yaml")
			},
		},
		{
			name: "a chain typed from outside the workspace, a link climbing out of the typed Release's directory",
			lay: func(t *testing.T, _, outside string) string {
				named := filepath.Join(outside, "releases")
				writeReferenceFile(t, filepath.Join(named, "aggregate.yaml"), fx.aggregate)
				writeReferenceFile(t, filepath.Join(named, "manifest.yaml"), fx.manifest)
				writeReferenceFile(t, filepath.Join(outside, "base.yaml"), fx.release)
				writeReferenceFile(t, filepath.Join(named, "envs", "mid.yaml"), namedRelease("mid", "../../base.yaml"))
				writeReferenceFile(t, filepath.Join(named, "release.yaml"), childRelease("./envs/mid.yaml"))
				return filepath.Join(named, "release.yaml")
			},
			refusedAt: ClosureStageResolveReleaseExtends,
			names: func(_ *testing.T, ws, outside string) []string {
				named := filepath.Join(outside, "releases")
				return []string{".spec.extends: ../../base.yaml", "written in " + filepath.Join(named, "envs", "mid.yaml"),
					"resolved from " + filepath.Join(named, "envs"), "which leads to " + filepath.Join(outside, "base.yaml"),
					outsideTheNamedDirectory(named, ws)}
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			base := t.TempDir()
			ws, outside := filepath.Join(base, "ws"), filepath.Join(base, "outside")
			for _, dir := range []string{ws, outside} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			releasePath := row.lay(t, ws, outside)
			wsCfg := workspaceconfig.Default()
			view := referenceView(ws, fx.catalogDir, wsCfg)

			resolved, cerr := ResolveClosure(context.Background(), ClosureInputs{
				View:            view,
				ReleasePath:     releasePath,
				WorkspaceRoot:   ws,
				WorkspaceConfig: wsCfg,
			})
			if row.refusedAt != 0 {
				if cerr == nil {
					t.Fatalf("the closure resolved past a reference leading out of its root")
				}
				if cerr.Stage != row.refusedAt {
					t.Errorf("stage = %d, want %d", cerr.Stage, row.refusedAt)
				}
				assertReferenceRefusal(t, cerr, row.names(t, ws, outside))
				return
			}
			if cerr != nil {
				t.Fatalf("the closure did not resolve: stage=%d %v", cerr.Stage, cerr)
			}

			_, err := BuildToBuffer(context.Background(), BuildInputs{
				View:                             view,
				Resolved:                         resolved,
				ReleasePath:                      releasePath,
				WorkspaceConfig:                  wsCfg,
				WorkspaceRoot:                    ws,
				CompilerVersion:                  "test",
				TolerateCatalogViewInconsistency: true,
			}, pipeline.Options{SkipRender: true})
			if row.refusedInBuild {
				if err == nil {
					t.Fatalf("the build read a parameters import leading out of the workspace")
				}
				assertReferenceRefusal(t, err, row.names(t, ws, outside))
				return
			}
			if err != nil {
				t.Fatalf("the build did not complete: %v", err)
			}
		})
	}
}

// assertReferenceRefusal reads the halt through its accessor, never its string
// code, and then what it names.
func assertReferenceRefusal(t *testing.T, err error, names []string) {
	t.Helper()
	if !errors.Is(err, readscope.ErrReferenceLeadsOutOfWorkspace()) {
		t.Fatalf("want the reference-leads-out-of-workspace halt, got %v", err)
	}
	for _, want := range names {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not name %q: %v", want, err)
		}
	}
}

// TestAParametersImportIsReadBesideTheReleaseFromAnyDirectory builds one Release,
// kept in a directory below the workspace root, from the workspace root and from
// the Release's own directory, naming the Release as a command does — relative to
// the directory it is run in.
//
// The import is written ./params.yaml, and two files of that name exist: the one
// beside the Release, which parses, and one at the workspace root, which does not.
// The working directory of the first build is the workspace root, so a build that
// resolved the import there would read the second and halt, and only a build that
// resolves it beside the Release completes from both. Completing from both is the
// whole assertion: the same file was read, wherever the build was run from.
func TestAParametersImportIsReadBesideTheReleaseFromAnyDirectory(t *testing.T) {
	fx := loadReferenceFixture(t)
	ws := t.TempDir()
	rel := filepath.Join(ws, "rel")
	writeReferenceFile(t, filepath.Join(rel, "aggregate.yaml"), fx.aggregate)
	writeReferenceFile(t, filepath.Join(rel, "manifest.yaml"), fx.manifest)
	writeReferenceFile(t, filepath.Join(rel, "params.yaml"), "{}\n")
	writeReferenceFile(t, filepath.Join(ws, "params.yaml"), "::: the file a working-directory reading would open :::\n")
	writeReferenceFile(t, filepath.Join(rel, "release.yaml"),
		fx.releaseWith(fixtureReleaseHead+"  parameters: {$import: ./params.yaml}\n"))

	for _, from := range []struct {
		name, dir, release string
	}{
		{name: "built from the workspace root", dir: ws, release: filepath.Join("rel", "release.yaml")},
		{name: "built from the Release's own directory", dir: rel, release: "release.yaml"},
	} {
		t.Run(from.name, func(t *testing.T) {
			t.Chdir(from.dir)
			wsCfg := workspaceconfig.Default()
			view := referenceView(ws, fx.catalogDir, wsCfg)
			resolved, cerr := ResolveClosure(context.Background(), ClosureInputs{
				View:            view,
				ReleasePath:     from.release,
				WorkspaceRoot:   ws,
				WorkspaceConfig: wsCfg,
			})
			if cerr != nil {
				t.Fatalf("the closure did not resolve: stage=%d %v", cerr.Stage, cerr)
			}
			if _, err := BuildToBuffer(context.Background(), BuildInputs{
				View:                             view,
				Resolved:                         resolved,
				ReleasePath:                      from.release,
				WorkspaceConfig:                  wsCfg,
				WorkspaceRoot:                    ws,
				CompilerVersion:                  "test",
				TolerateCatalogViewInconsistency: true,
			}, pipeline.Options{SkipRender: true}); err != nil {
				t.Fatalf("the build did not read the import beside the Release: %v", err)
			}
		})
	}
}
