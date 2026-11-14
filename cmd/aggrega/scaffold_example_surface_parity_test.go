// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// Example parity across the three surfaces that carry the capability, each driven
// through the real command tree in this process — the command line, the editor
// daemon over its framed stream, and the assistant tool protocol over its own —
// against one in-process registry serving one signed documentation bundle. Each
// surface writes the same example into a workspace of its own, and the files they
// wrote, the commands they report next and the account a reader is shown are
// compared.
//
// It is in-process rather than spawned because the registry is: the bundle is
// reached through the target the launch context states, so no surface reaches
// beyond this test.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle/docsbundletest"
)

// exampleTreeOf reads every file under dir, keyed by its slash-separated path
// relative to dir.
func exampleTreeOf(t *testing.T, dir string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		tree[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	return tree
}

// exampleViaDaemon runs `aggrega serve` over ws with one request writing name,
// and returns the answer's result.
func exampleViaDaemon(t *testing.T, ctx context.Context, ws, name string) map[string]json.RawMessage {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "aggrega/scaffoldEntity",
		"params": map[string]any{"workspaceRoot": ws, "example": name},
	})
	if err != nil {
		t.Fatalf("marshal the request: %v", err)
	}
	var stdin, stdout, stderr bytes.Buffer
	fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(body))
	stdin.Write(body)
	cmd := newRootCmd()
	cmd.SetIn(&stdin)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"serve", "--workspace-root", ws})
	if code := executeRoot(ctx, cmd); code != 0 {
		t.Fatalf("aggrega serve exited %d\nstderr:\n%s", code, stderr.String())
	}
	frame := daemonFrameAnswering(t, stdout.Bytes(), 1)
	if raw, refused := frame["error"]; refused {
		t.Fatalf("aggrega/scaffoldEntity was refused: %s", raw)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(frame["result"], &result); err != nil {
		t.Fatalf("decode the daemon's answer: %v\n%s", err, frame["result"])
	}
	return result
}

// exampleViaToolProtocol runs `aggrega mcp` over ws with one call writing name,
// and returns the answer's text block and structured payload.
func exampleViaToolProtocol(t *testing.T, ctx context.Context, ws, name string) (string, map[string]json.RawMessage) {
	t.Helper()
	args, err := json.Marshal(map[string]any{"example": name})
	if err != nil {
		t.Fatalf("marshal the arguments: %v", err)
	}
	var stdin, stdout, stderr bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/call", fmt.Sprintf(`{"name":%q,"arguments":%s}`, scaffoldToolName(t), args)))
	cmd := newRootCmd()
	cmd.SetIn(&stdin)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"mcp", "--workspace-root", ws})
	if code := executeRoot(ctx, cmd); code != 0 {
		t.Fatalf("aggrega mcp exited %d\nstderr:\n%s", code, stderr.String())
	}
	answers := answersByID(t, stdout.String(), 1, 2)
	if answers[2].Error != nil {
		t.Fatalf("the scaffold tool was refused: %s", answers[2].Error.Message)
	}
	var call struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent map[string]json.RawMessage `json:"structuredContent"`
		IsError           bool                       `json:"isError"`
	}
	if err := json.Unmarshal(answers[2].Result, &call); err != nil || call.IsError || len(call.Content) == 0 {
		t.Fatalf("the scaffold tool did not write the example (isError=%v): %v\n%s", call.IsError, err, answers[2].Result)
	}
	return call.Content[0].Text, call.StructuredContent
}

// exampleStrings decodes a list of strings from an answer's field.
func exampleStrings(t *testing.T, field string, raw json.RawMessage) []string {
	t.Helper()
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v (%s)", field, err, raw)
	}
	return out
}

// TestScaffoldExampleSurfacesWriteTheSameFilesAndReportTheSameNextCommands is the
// proof that one implementation does the work: the command line, the daemon and
// the tool, asked for the same example, write the same files with the same
// content and report the same next commands — and the tool's account is the
// command line's, word for word, closed by the one line naming the folder it
// wrote in full.
func TestScaffoldExampleSurfacesWriteTheSameFilesAndReportTheSameNextCommands(t *testing.T) {
	isolateCLIEnvironment(t)
	signer := docsbundletest.NewSigner(t)
	f := newExampleFixture(t, exampleMembers(), signer, signer)
	ctx := f.online()

	fromDaemonWS, fromToolWS := t.TempDir(), t.TempDir()
	for _, ws := range []string{fromDaemonWS, fromToolWS} {
		docsbundletest.WriteTrustingWorkspace(t, ws, exampleRegistryHost, signer)
	}

	// The command line writes relative to where it runs, so it runs in its own
	// workspace and names the directory as the other two do: by the example's name.
	t.Chdir(f.ws)
	cliOut, notes, err := f.run(t, ctx, "scaffold", "--example", "demo")
	if err != nil {
		t.Fatalf("aggrega scaffold --example demo: %v\nnotes: %s", err, notes)
	}
	daemon := exampleViaDaemon(t, ctx, fromDaemonWS, "demo")
	toolText, tool := exampleViaToolProtocol(t, ctx, fromToolWS, "demo")

	fromCLI := exampleTreeOf(t, filepath.Join(f.ws, "demo"))
	if len(fromCLI) == 0 {
		t.Fatal("the command line wrote no file, so the comparison asserts nothing")
	}
	for surface, ws := range map[string]string{"the daemon": fromDaemonWS, "the tool": fromToolWS} {
		if got := exampleTreeOf(t, filepath.Join(ws, "demo")); !maps.Equal(got, fromCLI) {
			t.Errorf("%s wrote %v, the command line %v", surface, examplePaths(got), examplePaths(fromCLI))
		}
	}

	for surface, answer := range map[string]map[string]json.RawMessage{"the daemon": daemon, "the tool": tool} {
		files := exampleStrings(t, surface+" files", answer["files"])
		slices.Sort(files)
		if want := examplePaths(fromCLI); !slices.Equal(files, want) {
			t.Errorf("%s reports the files %v, and wrote %v", surface, files, want)
		}
		if next := exampleStrings(t, surface+" next", answer["next"]); !slices.Equal(next, docsbundletest.ExampleNext) {
			t.Errorf("%s reports the next commands %q, want %q", surface, next, docsbundletest.ExampleNext)
		}
		for _, line := range docsbundletest.ExampleNext {
			if !strings.Contains(cliOut, "  "+line+"\n") {
				t.Errorf("the command line does not print the next command %q:\n%s", line, cliOut)
			}
		}
		var dir string
		if err := json.Unmarshal(answer["dir"], &dir); err != nil || dir != "demo" {
			t.Errorf("%s reports the directory %s (%v), want demo", surface, answer["dir"], err)
		}
	}
	// The tool's account is the command line's, closed by one line naming the
	// folder in full: the tool's caller named it relative to a workspace whose place
	// on the machine it may not know, which a terminal's reader always does.
	var outputDir string
	if err := json.Unmarshal(tool["output_dir"], &outputDir); err != nil || !sameDirectory(t, outputDir, filepath.Join(fromToolWS, "demo")) {
		t.Errorf("the tool reports the folder %s (%v), want %s in full", tool["output_dir"], err, filepath.Join(fromToolWS, "demo"))
	}
	if want := cliOut + "\nThe folder written is " + outputDir + ".\n"; toolText != want {
		t.Errorf("the tool's account is not the command line's closed by the folder it wrote.\ntool:\n%s\nwant:\n%s", toolText, want)
	}
}

// examplePaths returns a file tree's paths in order.
func examplePaths(tree map[string]string) []string {
	out := slices.Collect(maps.Keys(tree))
	slices.Sort(out)
	return out
}
