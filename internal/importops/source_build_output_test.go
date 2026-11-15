// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// The listing of a directory source and the workspace's build output folder: what
// a build of the workspace wrote is left out of a directory holding that folder,
// however either is spelled, and only there.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
)

// layBuiltProject lays a project at <scratch>/ws, the root of a workspace whose
// build has run: two files of the project's own, and the build output folder the
// build of that workspace writes, holding a rendered Docker Compose file and a
// file beside it. It returns the root and the build output folder.
func layBuiltProject(t *testing.T) (root, buildOutput string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "ws")
	buildOutput = artifactout.DefaultOutputDirIn(root)
	importopstest.WriteSource(t, filepath.Join(root, "langgraph.json"), "{}")
	importopstest.WriteSource(t, filepath.Join(root, "src", "agent.py"), "graph = None\n")
	importopstest.WriteSource(t, filepath.Join(buildOutput, "compose", "docker-compose.yaml"), "services: {}\n")
	importopstest.WriteSource(t, filepath.Join(buildOutput, "notes.txt"), "notes\n")
	return root, buildOutput
}

// projectFiles are the names a listing of layBuiltProject's root holds once the
// build output folder is left out.
var projectFiles = []string{"langgraph.json", "src/agent.py"}

// builtComposeFile is the name the rendered Docker Compose file layBuiltProject
// lays is listed under where the build output folder is not left out: beneath the
// folder's own name, which the build's composition supplies.
var builtComposeFile = artifactout.DefaultOutputDir + "/compose/docker-compose.yaml"

// TestADirectoryHoldingTheBuildOutputFolderIsListedWithoutIt holds a directory's
// listing to leaving the workspace's build output folder out, however the
// directory and the folder are spelled — the folder is compared as a place on
// disk — and holds the fixture to measuring something: read with no build output
// folder to leave out, the same directory lists the build's files.
//
// Not parallel: the rows naming the directory relative to the working directory
// move the process into another folder.
//
// 🔴 PLANTED DEFECT: in listDirectory (internal/importops/source.go), make
// isBuildOutput return false: every row lists dist/compose/docker-compose.yaml and
// dist/notes.txt.
func TestADirectoryHoldingTheBuildOutputFolderIsListedWithoutIt(t *testing.T) {
	root, buildOutput := layBuiltProject(t)
	if got := entryNames(readDirectorySource(t, root, "")); !slices.Contains(got, builtComposeFile) {
		t.Fatalf("the directory read with nothing to leave out lists %v, without the build's own file, so the "+
			"rows below would measure nothing", got)
	}
	linkedRoot := filepath.Join(t.TempDir(), "linked-ws")
	mustSymlink(t, root, linkedRoot)

	for _, row := range []struct {
		name string
		// arrange returns the source path as the row spells it, and the build output
		// folder as the row spells it, moving the process where the row says.
		arrange func(t *testing.T) (source, output string)
	}{
		{"the directory's absolute path", func(*testing.T) (string, string) { return root, buildOutput }},
		{"the path ending in a separator", func(*testing.T) (string, string) {
			return root + string(filepath.Separator), buildOutput
		}},
		{"the current directory, spelled .", func(t *testing.T) (string, string) {
			t.Chdir(root)
			return ".", buildOutput
		}},
		{"a path relative to the working directory", func(t *testing.T) (string, string) {
			t.Chdir(filepath.Dir(root))
			return "ws", buildOutput
		}},
		{"a path through a link to the directory", func(*testing.T) (string, string) {
			return linkedRoot, buildOutput
		}},
		{"the build output folder spelled through a link to the workspace", func(*testing.T) (string, string) {
			return root, artifactout.DefaultOutputDirIn(linkedRoot)
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			source, output := row.arrange(t)
			if got := entryNames(readDirectorySource(t, source, output)); !slices.Equal(got, projectFiles) {
				t.Errorf("the directory is listed as %v; want the project's files alone, %v", got, projectFiles)
			}
		})
	}
}

// TestALinkToTheBuildOutputFolderIsLeftOutOfTheListing holds a link inside the
// directory that leads to the build output folder to the folder's own fate: it is
// not listed, so it is neither judged nor read through. A link to another
// directory of the project is still listed.
func TestALinkToTheBuildOutputFolderIsLeftOutOfTheListing(t *testing.T) {
	t.Parallel()
	root, buildOutput := layBuiltProject(t)
	mustSymlink(t, buildOutput, filepath.Join(root, "latest"))
	mustSymlink(t, filepath.Join(root, "src"), filepath.Join(root, "code"))

	want := []string{"code", "langgraph.json", "src/agent.py"}
	if got := entryNames(readDirectorySource(t, root, buildOutput)); !slices.Equal(got, want) {
		t.Errorf("the directory is listed as %v; want %v, the link to the build output folder left out", got, want)
	}
}

// TestADirectoryTheBuildOutputFolderIsNotBeneathIsListedWhole holds the bounds of
// the exclusion. Only a build output folder beneath the directory is left out, so:
//
//   - the build output folder itself, imported, is read as named;
//   - a directory inside it is read as named;
//   - a directory outside the workspace, holding a folder of the build output
//     folder's name, is listed with that folder's files;
//   - a workspace whose build has written no folder leaves nothing out, and a file
//     standing where the folder would be is listed as a file;
//   - a file inside the build output folder, imported, is read whole.
func TestADirectoryTheBuildOutputFolderIsNotBeneathIsListedWhole(t *testing.T) {
	t.Parallel()
	_, buildOutput := layBuiltProject(t)

	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	importopstest.WriteSource(t, filepath.Join(elsewhere, "langgraph.json"), "{}")
	importopstest.WriteSource(t, filepath.Join(elsewhere, filepath.Base(buildOutput), "compose", "docker-compose.yaml"),
		"services: {}\n")

	unbuilt := filepath.Join(t.TempDir(), "unbuilt")
	importopstest.WriteSource(t, filepath.Join(unbuilt, "langgraph.json"), "{}")
	fileStanding := filepath.Join(t.TempDir(), "file-standing")
	importopstest.WriteSource(t, filepath.Join(fileStanding, "langgraph.json"), "{}")
	importopstest.WriteSource(t, artifactout.DefaultOutputDirIn(fileStanding), "not a folder\n")

	for _, row := range []struct {
		name, source, output string
		want                 []string
	}{
		{"the build output folder itself", buildOutput, buildOutput,
			[]string{"compose/docker-compose.yaml", "notes.txt"}},
		{"a directory inside the build output folder", filepath.Join(buildOutput, "compose"), buildOutput,
			[]string{"docker-compose.yaml"}},
		{"a directory outside the workspace holding a folder of the same name", elsewhere, buildOutput,
			[]string{builtComposeFile, "langgraph.json"}},
		{"a workspace whose build has written no folder", unbuilt, artifactout.DefaultOutputDirIn(unbuilt),
			[]string{"langgraph.json"}},
		{"a file standing where the folder would be", fileStanding, artifactout.DefaultOutputDirIn(fileStanding),
			[]string{artifactout.DefaultOutputDir, "langgraph.json"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			if got := entryNames(readDirectorySource(t, row.source, row.output)); !slices.Equal(got, row.want) {
				t.Errorf("the directory is listed as %v; want %v", got, row.want)
			}
		})
	}

	inside := filepath.Join(buildOutput, "compose", "docker-compose.yaml")
	src, err := readSource(inside, "docker-compose.yaml", buildOutput)
	if err != nil {
		t.Fatalf("read the file inside the build output folder: %v", err)
	}
	body, err := os.ReadFile(inside)
	if err != nil {
		t.Fatal(err)
	}
	if src.directory || len(src.entries) != 1 || string(src.entries[0].content) != string(body) {
		t.Errorf("a file inside the build output folder was not read whole as a file source: %+v", src.entries)
	}
}
