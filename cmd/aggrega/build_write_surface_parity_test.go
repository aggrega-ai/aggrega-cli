// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// Where a build's artifacts land, compared across the three surfaces that write
// them: the command line's real command tree, the language server's own framing,
// and the assistant's in-process build tool sent write, each driven through its
// own entry point over one workspace, from a folder that is not its root.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
)

// buildToolAnswer is one answer of the assistant's in-process build tool.
type buildToolAnswer struct {
	text    string
	isError bool
	payload struct {
		OutputDir         *string `json:"output_dir"`
		OutputDirWithheld bool    `json:"output_dir_withheld"`
		Files             []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
	}
}

// callBuildTool sends one build call through the tool protocol's own message
// loop, launched with the workspace root named, and returns its answer.
//
// The session is given no command surface: a tool derived from the command tree
// re-invokes the running binary, which under test is this test binary. It is
// stamped with the compiler version and toolchain the command line stamps, so the
// bytes it renders are the bytes the command line renders.
func callBuildTool(t *testing.T, launchRoot string, args map[string]any) buildToolAnswer {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": "build", "arguments": args})
	if err != nil {
		t.Fatalf("encode the build call: %v", err)
	}
	var stdin bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/call", string(params)))

	var stdout, stderr bytes.Buffer
	if rerr := mcp.Run(context.Background(), mcp.Config{
		Stdin:         &stdin,
		Stdout:        &stdout,
		Stderr:        &stderr,
		ServerVersion: provenanceCompilerVersion(),
		Toolchain:     toolchainStamp(),
		WorkspaceRoot: launchRoot,
	}); rerr != nil {
		t.Fatalf("the tool session exited with an error: %v\nstderr: %s", rerr, stderr.String())
	}
	frame := answersByID(t, stdout.String(), 1, 2)[2]
	if frame.Error != nil {
		t.Fatalf("the build call was answered with an error envelope: %d %s", frame.Error.Code, frame.Error.Message)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(frame.Result, &result); err != nil {
		t.Fatalf("decode the build answer: %v\n%s", err, frame.Result)
	}
	var out buildToolAnswer
	out.isError = result.IsError
	if len(result.Content) > 0 {
		out.text = result.Content[0].Text
	}
	if err := json.Unmarshal(result.StructuredContent, &out.payload); err != nil {
		t.Fatalf("decode the build payload: %v\n%s", err, result.StructuredContent)
	}
	return out
}

// renderedInMemory is the fixture's expected tree: the files one render of the
// Release produces and their bytes, keyed by the path each occupies beneath an
// output directory. It is read off the build tool's in-memory answer — a render
// no write path, no destination and no surface's output directory takes part in —
// so every surface's written tree is held to the render itself rather than only to
// the trees the other surfaces wrote.
func renderedInMemory(t *testing.T, root, release string) map[string]string {
	t.Helper()
	got := callBuildTool(t, root, map[string]any{"release": release, "response_format": "detailed"})
	if got.isError {
		t.Fatalf("the in-memory render of the fixture was refused: %s", got.text)
	}
	if got.payload.OutputDir != nil || got.payload.OutputDirWithheld {
		t.Fatalf("a build not sent write named an output directory: %q", got.text)
	}
	tree := map[string]string{}
	for _, f := range got.payload.Files {
		tree[filepath.FromSlash(f.Path)] = f.Content
	}
	if len(tree) == 0 {
		t.Fatal("the in-memory render produced no file, so every tree below would be held to nothing")
	}
	return tree
}

// staleBoundaryFile is a file a previous build left beneath the boundaries subtree
// of the output directory, which this render does not produce. A build clears
// that subtree before it writes, so the file is gone after every surface's write.
var staleBoundaryFile = filepath.Join("boundaries", "previous-variant-q4z8", "stale.yaml")

// TestTheCommandLineTheLanguageServerAndTheBuildToolWriteOneTree drives one
// Release in one workspace through the command line (the real command tree,
// --workspace-root naming the workspace), the language server (aggrega/build, no
// outputParent) and the assistant's in-process build tool sent write (the
// workspace named by its launch, the Release named absolutely), each run from a
// folder that is not the workspace root.
//
// Each row starts with <root>/dist holding a file a previous build left under its
// boundaries subtree, and each must end with <root>/dist holding exactly the
// expected tree — the stale file cleared — name that directory in full, and write
// nothing in the folder it ran from. The expected tree and the directory are
// stated once for the fixture, so the three rows are held to them rather than to
// each other alone: three surfaces agreeing on a wrong directory, or on a tree the
// render did not produce, still fail.
//
// 🔴 PLANTED DEFECTS:
//   - have artifactout.DefaultOutputDirIn (internal/artifactout/artifactout.go)
//     join DefaultOutputDir+"-elsewhere" instead of DefaultOutputDir: all three
//     rows redden together, each finding no tree at <root>/dist, which is what
//     shows the tool reads that function rather than a join of its own.
//   - in toolWorkspace.writeRenderedArtifacts (internal/mcp/tool_build.go), call
//     artifactout.WriteAll instead of artifactout.WriteBuildOutput: the build-tool
//     row reddens on the stale file left beneath boundaries, and the other two stay
//     green.
//   - make buildArgs.writesArtifacts (internal/mcp/tool_build.go) return false:
//     the build-tool row reddens, finding no tree, while the other two stay green.
//
// Not parallel: the rows move the process working directory and read the process
// standard output.
func TestTheCommandLineTheLanguageServerAndTheBuildToolWriteOneTree(t *testing.T) {
	root, release := anchorFixture(t)
	dist := filepath.Join(root, "dist")
	expected := renderedInMemory(t, root, release)
	if _, err := os.Stat(dist); err == nil {
		t.Fatalf("precondition: the in-memory render left %s on disk", dist)
	}

	for _, row := range []struct {
		name string
		// run builds the fixture from runIn and returns the directory the surface
		// names as the one it wrote.
		run func(t *testing.T, runIn string) string
	}{
		{
			name: "the command line, --workspace-root naming the workspace",
			run: func(t *testing.T, runIn string) string {
				args := []string{"--workspace-root", root, "build", "--release", release}
				var err error
				var cobraOut string
				stdout := captureBuildStdout(t, func() { cobraOut, err = runAggrega(t, args...) })
				if err != nil {
					t.Fatalf("aggrega %s failed: %v\n%s", strings.Join(args, " "), err, cobraOut)
				}
				line := buildClosingLine(stdout)
				written := countOfNoun(len(expected), "file", "files")
				named, found := strings.CutPrefix(line, fmt.Sprintf("Build completed: %s written to ", written))
				if !found {
					t.Fatalf("the closing line reads %q; want it to name %s written to a directory", line, written)
				}
				return named
			},
		},
		{
			name: "the language server, aggrega/build naming no outputParent",
			run: func(t *testing.T, runIn string) string {
				raw := daemonAnswer(t, root, "aggrega/build", map[string]any{"workspaceRoot": root, "release": filepath.Base(release)})
				var answer struct {
					OutputDirectory string `json:"outputDirectory"`
				}
				if err := json.Unmarshal(raw, &answer); err != nil {
					t.Fatalf("decode the build's answer: %v\n%s", err, raw)
				}
				return answer.OutputDirectory
			},
		},
		{
			name: "the assistant's build tool sent write, the workspace named by its launch",
			run: func(t *testing.T, runIn string) string {
				got := callBuildTool(t, root, map[string]any{"release": release, "write": true, "response_format": "concise"})
				if got.isError {
					t.Fatalf("the build tool refused the write: %s", got.text)
				}
				if got.payload.OutputDir == nil {
					t.Fatalf("the build tool wrote and named no output_dir (withheld=%v): %q",
						got.payload.OutputDirWithheld, got.text)
				}
				var listed []string
				for _, f := range got.payload.Files {
					listed = append(listed, filepath.FromSlash(f.Path))
				}
				sort.Strings(listed)
				var want []string
				for p := range expected {
					want = append(want, p)
				}
				sort.Strings(want)
				if !reflect.DeepEqual(listed, want) {
					t.Errorf("the build tool lists %v as written, want the render's files %v", listed, want)
				}
				if line := fmt.Sprintf("wrote %d file(s) to %s:", len(expected), *got.payload.OutputDir); !strings.Contains(got.text, line) {
					t.Errorf("the build tool's text does not say %q:\n%s", line, got.text)
				}
				return *got.payload.OutputDir
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			if err := os.RemoveAll(dist); err != nil {
				t.Fatalf("clear %s: %v", dist, err)
			}
			writeFixture(t, filepath.Join(dist, staleBoundaryFile), "left by a previous build\n")
			runIn := t.TempDir()
			t.Chdir(runIn)

			named := row.run(t, runIn)

			if named != dist {
				t.Errorf("the surface names %q as the directory it wrote, want %q", named, dist)
			}
			if _, err := os.Stat(filepath.Join(dist, staleBoundaryFile)); err == nil {
				t.Errorf("%s, left beneath boundaries by a previous build, survived the write", staleBoundaryFile)
			}
			if tree := renderedTree(t, dist); !reflect.DeepEqual(tree, expected) {
				t.Errorf("the surface wrote a tree beneath %s other than the render's:\n got: %v\nwant: %v",
					dist, keysOfTree(tree), keysOfTree(expected))
			}
			if stray := strayBuildOutput(runIn); len(stray) > 0 {
				t.Errorf("the build wrote %v in %s, the folder it ran from", stray, runIn)
			}
		})
	}
}

// keysOfTree lists a tree's paths in order, for a failure message.
func keysOfTree(tree map[string]string) []string {
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
