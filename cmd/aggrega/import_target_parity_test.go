// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// What an import given a target composes, compared across every surface that
// carries the capability, each driven as a spawned process of the built binary
// over its own wire: the command line's --target, the assistant's import tool and
// the import command's tool, each sent target, and the editor's session, sent
// target on aggrega/import.
//
// The workspace's catalog holds the draft-composition catalog — Components
// example/x, example/y and example/z and the Recipes implementing them — and the
// draft-set importer, which drafts exactly the files its source spells out, with
// its module beside its entity, so the importer is reached the way a workspace
// reaches one. The source drafts an Aggregate and a Release naming no Manifest.

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// targetSourceName is the name of the draft-set source in the fixture's root.
const targetSourceName = "drafts.txt"

// layTargetImport builds the fixture a row runs in: a workspace at parent/ws whose
// catalog holds the draft-composition catalog and the draft-set importer, with the
// draft-set source at its root. It returns parent and the workspace root.
func layTargetImport(t *testing.T) (parent, root string) {
	t.Helper()
	parent = t.TempDir()
	root = filepath.Join(parent, "ws")
	importopstest.WriteConfiguration(t, root)
	importopstest.LayDraftComposerCatalog(t, root, filepath.Join(root, workspaceconfig.ConventionalCatalogDir))
	writeFixture(t, filepath.Join(root, targetSourceName), importopstest.DraftComposerSource())
	return parent, root
}

// runImportCommand runs the built binary with args in dir and returns everything
// it wrote and whether it exited zero.
func runImportCommand(t *testing.T, bin, dir string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = importParityEnv(t, "")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if _, exited := err.(*exec.ExitError); !exited {
			t.Fatalf("run aggrega %s: %v", strings.Join(args, " "), err)
		}
		return string(out), false
	}
	return string(out), true
}

// callImportTool launches the built binary as `aggrega mcp` in dir, with the
// workspace root named by the launch, calls one tool, and returns the text the
// answer carries and whether the call succeeded. A protocol error fails the test.
func callImportTool(t *testing.T, bin, dir, launchRoot, tool string, args map[string]any) (string, bool) {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": tool, "arguments": args})
	if err != nil {
		t.Fatalf("encode the call: %v", err)
	}
	cmd := exec.Command(bin, "mcp")
	cmd.Dir = dir
	cmd.Env = importParityEnv(t, launchRoot)
	cmd.Stdin = strings.NewReader(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`) +
		rpcLine(2, "tools/call", string(params)))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("aggrega mcp: %v (stderr: %s)", err, stderr.String())
	}
	frame := answersByID(t, stdout.String(), 1, 2)[2]
	if frame.Error != nil {
		t.Fatalf("tools/call %s: error %d: %s", tool, frame.Error.Code, frame.Error.Message)
	}
	var call struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(frame.Result, &call); err != nil {
		t.Fatalf("decode the %s answer: %v (%s)", tool, err, frame.Result)
	}
	var texts []string
	for _, c := range call.Content {
		texts = append(texts, c.Text)
	}
	return strings.Join(texts, "\n"), !call.IsError
}

// sessionImport starts the built binary as `aggrega serve` at root, sends one
// aggrega/import carrying params, and returns the answer: its result, or the text
// of its error and false.
func sessionImport(t *testing.T, bin, root string, params map[string]any) (string, bool) {
	t.Helper()
	cmd := exec.Command(bin, "serve")
	cmd.Dir = root
	cmd.Env = importParityEnv(t, "")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("language server input: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("language server output: %v", err)
	}
	stdout := bufio.NewReader(stdoutPipe)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the language server: %v", err)
	}
	if _, err := stdin.Write(framedFor(t, cmd, 1, "aggrega/import", params)); err != nil {
		t.Fatalf("write the request: %v", err)
	}
	resp := readOneResponse(t, stdout, responseLivenessCeiling(t))
	_ = stdin.Close()
	waited := make(chan error, 1)
	go func() {
		_, _ = io.Copy(io.Discard, stdout)
		waited <- cmd.Wait()
	}()
	select {
	case <-waited:
	case <-time.After(responseLivenessCeiling(t)):
		_ = cmd.Process.Kill()
		t.Fatalf("the language server did not exit once its input closed (stderr: %s)", stderr.String())
	}
	if failure, refused := resp["error"]; refused {
		body, _ := json.Marshal(failure)
		return string(body), false
	}
	body, _ := json.Marshal(resp["result"])
	return string(body), true
}

// targetRow is one surface handed one target.
type targetRow struct {
	name string
	run  func(t *testing.T, bin, root, target string) (string, bool)
}

// targetRows are the surfaces that carry an import's target, each handed the
// target it is given exactly as that surface carries one, every other argument
// alike: the draft-set source at the workspace root, its format named, and no
// destination of the surface's own, so every one drafts beneath the root.
var targetRows = []targetRow{
	{
		name: "the command line's --target",
		run: func(t *testing.T, bin, root, target string) (string, bool) {
			return runImportCommand(t, bin, root, "import", targetSourceName, "--format",
				importopstest.DraftSetFormat, "--target", target)
		},
	},
	{
		name: "the import tool's target",
		run: func(t *testing.T, bin, root, target string) (string, bool) {
			return callImportTool(t, bin, root, root, "import", map[string]any{
				"source": targetSourceName, "format": importopstest.DraftSetFormat, "target": target})
		},
	},
	{
		name: "the import command's tool, its generated target argument",
		run: func(t *testing.T, bin, root, target string) (string, bool) {
			return callImportTool(t, bin, root, root, "cli-import", map[string]any{
				"args": []string{targetSourceName}, "format": importopstest.DraftSetFormat, "target": target})
		},
	},
	{
		name: "the session's target on aggrega/import",
		run: func(t *testing.T, bin, root, target string) (string, bool) {
			return sessionImport(t, bin, root, map[string]any{
				"source": targetSourceName, "outputDir": ".", "format": importopstest.DraftSetFormat, "target": target})
		},
	},
}

// TestEverySurfaceComposesOneDraftSetForATarget imports the draft-set source for
// the compose target through every surface that carries a target, each over a
// fresh copy of the fixture, and reads what landed off the content digests of the
// whole fixture: every surface drafts the same four files beneath the workspace
// root and changes nothing else, and the drafted Manifest and Release are, byte
// for byte, the ones the import operation composes — the Manifest mapping
// example/x alone, and the Release naming it.
//
// 🔴 PLANTED DEFECTS:
//   - have the session hand the import no target (internal/serve/import.go, the
//     importops.Options literal): the session row reddens, drafting no Manifest.
//   - compose on one surface: have the import tool rewrite the drafted Manifest
//     it is handed (internal/mcp/tool_import.go), or the command line hand
//     --target lowercased differently from the others — the row that differs
//     reddens against the shared bytes.
func TestEverySurfaceComposesOneDraftSetForATarget(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)
	wantChanges := []string{"ws/" + importops.RecordFileName + " was created", "ws/aggregate.yaml was created",
		"ws/manifest.yaml was created", "ws/release.yaml was created"}
	for _, row := range targetRows {
		t.Run(row.name, func(t *testing.T) {
			parent, root := layTargetImport(t)
			before := treeFingerprint(t, parent)
			if out, ok := row.run(t, bin, root, "compose"); !ok {
				t.Fatalf("the import for the compose target was refused: %s", out)
			}
			changes := fingerprintDifferences(before, treeFingerprint(t, parent))
			if !slices.Equal(changes, wantChanges) {
				t.Errorf("the import changed the fixture as %v; want exactly %v", changes, wantChanges)
			}
			for file, want := range map[string]string{
				"manifest.yaml": importopstest.DraftedManifestComposed,
				"release.yaml":  importopstest.DraftedReleaseComposed,
			} {
				body, err := os.ReadFile(filepath.Join(root, file))
				if err != nil {
					t.Fatalf("the drafted %s is not at the workspace root: %v", file, err)
				}
				if string(body) != want {
					t.Errorf("%s holds\n%s\nwant the bytes every surface drafts\n%s", file, body, want)
				}
			}
		})
	}
}

// TestEverySurfaceRefusesATargetNoRecipeBindsTo imports, for the target "helmm",
// through every surface that carries a target, and holds each to the same
// refusal: the registered condition, its short code, the target as given and the
// capabilities the catalog's Recipes bind to — and nothing anywhere in the
// fixture changed, so not even the importer's record was written.
func TestEverySurfaceRefusesATargetNoRecipeBindsTo(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)
	for _, row := range targetRows {
		t.Run(row.name, func(t *testing.T) {
			parent, root := layTargetImport(t)
			before := treeFingerprint(t, parent)
			out, ok := row.run(t, bin, root, "helmm")
			if ok {
				t.Fatalf("the import for the target \"helmm\" succeeded: %s", out)
			}
			// The session answers JSON, so its quotation marks arrive escaped.
			text := strings.ReplaceAll(out, `\"`, `"`)
			for _, want := range []string{"E0349", `target "helmm"`, `the catalog's Recipes bind to "compose", "helm"`} {
				if !strings.Contains(text, want) {
					t.Errorf("the refusal does not carry %s:\n%s", want, out)
				}
			}
			if changes := fingerprintDifferences(before, treeFingerprint(t, parent)); len(changes) != 0 {
				t.Errorf("the refused import changed the fixture: %v", changes)
			}
		})
	}
}
