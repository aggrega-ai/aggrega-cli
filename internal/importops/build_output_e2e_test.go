// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs, through the real Import, that a directory source is read
// without the workspace's build output folder wherever that folder lies beneath
// it: what a build of the workspace wrote there is neither recognised as a project
// of its own nor handed to the importer, and a directory that has been built is
// re-imported, with a sync, as the project it holds.
//
// The documentation examples' walkthroughs import the workspace root they run in,
// `aggrega import .`, and build there, so the build output folder lands inside the
// source. The cases here do the same: the project's files are laid at the
// workspace root, imported, built, the build's rendered files written where a
// build of that workspace writes them, and the root imported again.
//
// These cases read the import examples of the aggrega-docs repository and the
// catalog of the aggrega-hub repository, each checked out beside this one, as the
// as-drafted build proofs do. Without those checkouts they fail rather than skip.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// layExampleFiles copies the files named, by their paths inside the source/
// directory of the documentation example import-<format>, to the same paths
// beneath dir. Only the files named are copied, so the example's own workspace
// configuration never replaces the one the case wrote.
func layExampleFiles(t *testing.T, format, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(importExamplesDir, "import-"+format, "source", filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read the import-%s example's %s: %v", format, name, err)
		}
		importopstest.WriteSource(t, filepath.Join(dir, filepath.FromSlash(name)), string(body))
	}
}

// importWorkspaceRoot imports the workspace root of rw as `aggrega import .` run
// there does: the root is the source, named ".", the drafts are written beside
// the project, and no format is named.
func importWorkspaceRoot(rw *workspaceconfig.ResolvedWorkspace, target string, sync bool,
	delivery authoredcontent.Delivery) (Result, error) {
	return Import(context.Background(), rw, Options{
		SourcePath:    rw.Root(),
		SourceName:    ".",
		Target:        target,
		Sync:          sync,
		Delivery:      delivery,
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
}

// buildIntoTheWorkspace builds the Release the import drafted at the workspace
// root of rw, through the build-faithful harness over the hub's catalog
// ([buildAsDrafted]), and writes what it rendered where a build of that
// workspace writes it — the build output folder composed beneath the root, by the
// build's own writer. It returns the build output folder, having required the
// one Docker Compose file the build rendered to sit inside it.
func buildIntoTheWorkspace(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) string {
	t.Helper()
	artifacts := buildAsDrafted(t, rw.Root())
	output := artifactout.DefaultOutputDirIn(rw.Root())
	if _, err := artifactout.WriteBuildOutput(rw.Root(), output, artifacts); err != nil {
		t.Fatalf("write the build's rendered files: %v", err)
	}
	var compose []string
	for name := range artifacts {
		if name == "compose/docker-compose.yaml" || strings.HasSuffix(name, "/compose/docker-compose.yaml") {
			compose = append(compose, name)
		}
	}
	if len(compose) != 1 {
		t.Fatalf("the build rendered the Docker Compose files %q; want one, the file a re-import must not read", compose)
	}
	written := filepath.Join(output, filepath.FromSlash(compose[0]))
	if _, err := os.Stat(written); err != nil {
		t.Fatalf("the build's Docker Compose file is not inside the source at %s: %v", written, err)
	}
	return output
}

// requireTheBuildOutputIsRecognisedAsCompose imports the build output folder
// itself, reporting into a scratch directory, and requires it recognised as the
// compose format: the folder is the source named, so it is read, and what the
// build wrote there is a Docker Compose project — which is what a re-import of the
// root would otherwise meet beside the project.
func requireTheBuildOutputIsRecognisedAsCompose(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, output string) {
	t.Helper()
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    output,
		SourceName:    filepath.Base(output),
		OutputDir:     filepath.Join(t.TempDir(), "drafts"),
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if err != nil {
		t.Fatalf("the build output folder, imported as the source, was refused: %v", err)
	}
	if res.Format != "compose" {
		t.Fatalf("the build output folder, imported as the source, was read as %q; want compose, or the case "+
			"would measure nothing", res.Format)
	}
}

// TestABuiltLangGraphFolderResyncsAsTheProjectItHolds is the LangGraph
// walkthrough as a reader runs it at the workspace root: `aggrega import .
// --target compose`, a build, and `aggrega import . --sync` naming no format.
//
// The build writes a Docker Compose file inside the source, which a Compose
// importer recognises — the build output folder imported on its own is read as
// compose — and the sync still reads the root as the langgraph format alone and
// finds the drafts in step with the project.
//
// 🔴 PLANTED DEFECT: in Import (internal/importops/import.go), hand readSource ""
// in place of the build output folder: the sync is refused with E0339, the root
// recognised as both compose and langgraph.
func TestABuiltLangGraphFolderResyncsAsTheProjectItHolds(t *testing.T) {
	t.Parallel()
	rw := wholeHubWorkspace(t)
	layExampleFiles(t, "langgraph", rw.Root(), "langgraph.json", AnswersFileName)

	res, err := importWorkspaceRoot(rw, "compose", false, authoredcontent.DeliveryFor(false))
	if err != nil {
		t.Fatalf("the first import of the root was refused: %v", err)
	}
	if res.Format != "langgraph" || res.Halted() || len(res.Owed) != 0 {
		t.Fatalf("the first import read the root as %q, halted %v, owing %v; want langgraph, complete: %+v",
			res.Format, res.Halted(), owedPlaces(res), res.Diagnostics)
	}

	output := buildIntoTheWorkspace(t, rw)
	requireTheBuildOutputIsRecognisedAsCompose(t, rw, output)

	synced, err := importWorkspaceRoot(rw, "", true, authoredcontent.DeliveryFor(false))
	if err != nil {
		t.Fatalf("the sync of the built root, naming no format, was refused: %v", err)
	}
	if synced.Format != "langgraph" {
		t.Errorf("the sync read the built root as %q; want langgraph", synced.Format)
	}
	if len(synced.Drift) != 0 || len(synced.Files) != 0 || len(synced.Removed) != 0 {
		t.Errorf("the sync of the built root drifts %+v, writes %v and removes %v; want it in step",
			synced.Drift, draftPaths(synced), synced.Removed)
	}
}

// TestABuiltComposeFolderResyncsWithoutReadingTheBuildsComposeFile is the same
// walkthrough over the Compose example, whose project and build output are one
// format: recognition alone is never ambiguous there, so what the case holds is
// what the importer is handed. The sync reports the drafts in step, and nothing
// it reports names the build output folder — the Compose importer was never
// handed the build's own Docker Compose file, which would otherwise be drafted as
// a second copy of every service.
//
// 🔴 PLANTED DEFECT: as for the LangGraph case: the sync reports the services of
// the build's Docker Compose file added beside the project's, and fails.
func TestABuiltComposeFolderResyncsWithoutReadingTheBuildsComposeFile(t *testing.T) {
	t.Parallel()
	rw := wholeHubWorkspace(t)
	layExampleFiles(t, "compose", rw.Root(), "compose.yaml")

	res, err := importWorkspaceRoot(rw, "compose", false, authoredcontent.DeliveryFor(false))
	if err != nil {
		t.Fatalf("the first import of the root was refused: %v", err)
	}
	if res.Format != "compose" || res.Halted() || len(res.Owed) != 0 {
		t.Fatalf("the first import read the root as %q, halted %v, owing %v; want compose, complete: %+v",
			res.Format, res.Halted(), owedPlaces(res), res.Diagnostics)
	}

	output := buildIntoTheWorkspace(t, rw)
	requireTheBuildOutputIsRecognisedAsCompose(t, rw, output)

	synced, err := importWorkspaceRoot(rw, "", true, authoredcontent.DeliveryFor(true))
	if err != nil {
		t.Fatalf("the sync of the built root was refused: %v", err)
	}
	if synced.Format != "compose" {
		t.Errorf("the sync read the built root as %q; want compose", synced.Format)
	}
	if len(synced.Drift) != 0 {
		lines := make([]string, 0, len(synced.Drift))
		for _, d := range synced.Drift {
			lines = append(lines, DriftLine(d))
		}
		t.Errorf("the sync of the built root drifts: %q; want it in step", lines)
	}
	outputName := filepath.Base(output) + "/"
	for _, d := range synced.Diagnostics {
		if strings.Contains(d.Message, outputName) {
			t.Errorf("the sync reports %q, naming the build output folder, whose files the importer must not be "+
				"handed", d.Message)
		}
	}
}

// TestTwoProjectsBesideTheBuildOutputFolderAreStillAmbiguous holds the exclusion
// to the build output folder alone: a workspace root holding a LangGraph
// configuration and a Docker Compose file of its own — two projects, outside the
// build output folder — is refused as ambiguous between the two, whatever the
// build wrote, exactly as an unbuilt one is.
func TestTwoProjectsBesideTheBuildOutputFolderAreStillAmbiguous(t *testing.T) {
	t.Parallel()
	rw, _ := catalogFolderImportWorkspace(t)
	layExampleFiles(t, "langgraph", rw.Root(), "langgraph.json")
	layExampleFiles(t, "compose", rw.Root(), "compose.yaml")
	layExampleFiles(t, "compose", artifactout.DefaultOutputDirIn(rw.Root()), "compose.yaml")

	_, err := importWorkspaceRoot(rw, "", false, authoredcontent.DeliveryFor(true))
	var ambiguous *AmbiguousFormatError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("a root holding two projects was not refused as ambiguous: %T: %v", err, err)
	}
	if !slices.Equal(ambiguous.Formats(), []string{"compose", "langgraph"}) {
		t.Errorf("the root was recognised as %q; want compose and langgraph", ambiguous.Formats())
	}
	if !errors.Is(err, ErrAmbiguousImportFormat()) {
		t.Errorf("the refusal is not the ambiguous-format condition: %v", err)
	}
	if short, _ := cerrors.ShortCodeOf(err); short != "E0339" {
		t.Errorf("the refusal carries short code %q, want E0339", short)
	}
}

// TestADirectoryImportIsHandedNothingFromTheBuildOutputFolderBeneathIt drives the
// files-probe importer, which reports exactly what it was handed, over a
// workspace whose build output folder holds a file its patterns select, and holds
// what the importer is handed to the bounds of the exclusion, recognition naming
// no format:
//
//   - the workspace root, and the root spelled through a link to it: the folder's
//     file is never opened — it is unreadable, so opening it would refuse the
//     import — and the importer is handed the project's file alone;
//   - the build output folder itself, and a directory inside it: read as named;
//   - a directory outside the workspace holding a folder of the build output
//     folder's name: handed that folder's file too.
func TestADirectoryImportIsHandedNothingFromTheBuildOutputFolderBeneathIt(t *testing.T) {
	t.Parallel()
	skipWhereNoFileIsUnreadable(t)
	project := importopstest.FilesProbeBody("project")
	built := importopstest.FilesProbeBody("built")

	for _, row := range []struct {
		name string
		// lay puts the row's files down beneath the workspace root and returns the
		// source to import and the sources the importer is to be handed.
		lay func(t *testing.T, root string) (string, []abi.ImportSource)
	}{
		{"the workspace root", func(t *testing.T, root string) (string, []abi.ImportSource) {
			importopstest.WriteSource(t, filepath.Join(root, "a.probe.json"), project)
			makeUnreadable(t, importopstest.WriteSource(t,
				filepath.Join(artifactout.DefaultOutputDirIn(root), "b.probe.json"), built))
			return root, []abi.ImportSource{{Name: "a.probe.json", Bytes: []byte(project)}}
		}},
		{"the workspace root through a link to it", func(t *testing.T, root string) (string, []abi.ImportSource) {
			importopstest.WriteSource(t, filepath.Join(root, "a.probe.json"), project)
			makeUnreadable(t, importopstest.WriteSource(t,
				filepath.Join(artifactout.DefaultOutputDirIn(root), "b.probe.json"), built))
			link := filepath.Join(t.TempDir(), "linked")
			mustSymlink(t, root, link)
			return link, []abi.ImportSource{{Name: "a.probe.json", Bytes: []byte(project)}}
		}},
		{"the build output folder itself", func(t *testing.T, root string) (string, []abi.ImportSource) {
			importopstest.WriteSource(t, filepath.Join(root, "a.probe.json"), project)
			output := artifactout.DefaultOutputDirIn(root)
			importopstest.WriteSource(t, filepath.Join(output, "b.probe.json"), built)
			return output, []abi.ImportSource{{Name: "b.probe.json", Bytes: []byte(built)}}
		}},
		{"a directory inside the build output folder", func(t *testing.T, root string) (string, []abi.ImportSource) {
			inside := filepath.Join(artifactout.DefaultOutputDirIn(root), "compose")
			importopstest.WriteSource(t, filepath.Join(inside, "b.probe.json"), built)
			return inside, []abi.ImportSource{{Name: "b.probe.json", Bytes: []byte(built)}}
		}},
		{"a directory outside the workspace holding a folder of the same name",
			func(t *testing.T, root string) (string, []abi.ImportSource) {
				elsewhere := filepath.Join(t.TempDir(), "elsewhere")
				own := filepath.Join(elsewhere, filepath.Base(artifactout.DefaultOutputDirIn(root)))
				importopstest.WriteSource(t, filepath.Join(elsewhere, "a.probe.json"), project)
				importopstest.WriteSource(t, filepath.Join(own, "b.probe.json"), built)
				return elsewhere, []abi.ImportSource{
					{Name: "a.probe.json", Bytes: []byte(project)},
					{Name: filepath.Base(own) + "/b.probe.json", Bytes: []byte(built)},
				}
			}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			rw, _ := directoryImportWorkspace(t, nil)
			source, want := row.lay(t, rw.Root())

			res, err := importDirectory(t, rw, source, filepath.Base(source), "")
			if err != nil {
				t.Fatalf("the directory import was refused: %v", err)
			}
			if res.Format != importopstest.FilesProbeFormat {
				t.Errorf("the directory was recognised as %q, want %q", res.Format, importopstest.FilesProbeFormat)
			}
			report := probeReportOf(t, res)
			if !slices.EqualFunc(report.Sources, want, func(a, b abi.ImportSource) bool {
				return a.Name == b.Name && string(a.Bytes) == string(b.Bytes)
			}) {
				t.Errorf("the importer was handed %s; want exactly %s", sourceNames(report.Sources), sourceNames(want))
			}
		})
	}
}
