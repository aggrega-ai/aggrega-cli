// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// Package main's coverage for the one half of the import tool that lives here:
// the plugin resolution an importer's bytes are loaded through.
//
// 🔴 That half is covered nowhere else, and the reason is structural rather than
// an oversight. Every other proof of this tool stands the server up itself and
// sets that resolution by hand, so all of them pass over a command that supplies
// nothing at all — and a tool wired everywhere except at its entry point is
// declared and cannot run, which is the shape that passes quietly at every gate.
// So the case below runs the command.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// capturedImportSeam holds what the running command handed the shared import
// layer: the workspace it resolved and the plugin-bytes resolution it supplied.
//
// The resolution is recorded rather than consumed, because the seam that would
// consume it is the one being stood in for. Recording it leaves the case free to
// drive it directly, which is what turns "something non-nil arrived" into a
// statement about WHICH resolution arrived.
type capturedImportSeam struct {
	rw      *workspaceconfig.ResolvedWorkspace
	resolve importops.PluginBytesResolver
	reached bool
}

// captureImportSeamArguments records what a run hands the import seam, over
// whatever seam pointImportSeamAtDemoGuest installed.
//
// It decorates that helper rather than restating it: which importer runs and
// where its bytes come from are already settled there, and a second demo seam
// beside it would be a copy that agreed on the day it was written. The cleanup
// that helper registers restores the real seam past this decoration.
//
// 🔴 It swaps a package-level variable, so no case using it may be made
// parallel.
func captureImportSeamArguments(t *testing.T) *capturedImportSeam {
	t.Helper()
	pointImportSeamAtDemoGuest(t)
	demo := importops.BuildSeam
	captured := &capturedImportSeam{}
	importops.BuildSeam = func(rw *workspaceconfig.ResolvedWorkspace,
		resolve importops.PluginBytesResolver) (importops.Seam, error) {
		captured.rw, captured.resolve, captured.reached = rw, resolve, true
		return demo(rw, resolve)
	}
	return captured
}

// mcpImportWorkspace declares an isolated workspace with the demo source in it
// and returns the root, so a call naming that source resolves it inside the
// workspace the server serves.
func mcpImportWorkspace(t *testing.T, rel string) string {
	t.Helper()
	root := resolveTempWorkspace(t, tempWorkspaceOptions{}).Root()
	if err := os.WriteFile(filepath.Join(root, rel), []byte(demoSourceBody), 0o644); err != nil {
		t.Fatalf("seed the served workspace: %v", err)
	}
	return root
}

// importCallOverStdio drives one import call at the named server entry and
// returns the answer to it. The two callers differ in which server they stand
// up, which is the whole of what they are comparing, so the request they send
// and the way its answer is read have one home here.
func importCallOverStdio(t *testing.T, run func(stdin, stdout, stderr *bytes.Buffer)) (isError bool,
	text string, structured json.RawMessage) {
	t.Helper()

	var stdin, stdout, stderr bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/call",
		`{"name":"import","arguments":{"source":"`+demoImportSourceName+`","format":"demo"}}`))
	run(&stdin, &stdout, &stderr)

	answers := answersByID(t, stdout.String(), 1, 2)
	if answers[2].Error != nil {
		t.Fatalf("the import call was not dispatched at all: %s (stderr: %s)",
			answers[2].Error.Message, stderr.String())
	}
	var call struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(answers[2].Result, &call); err != nil {
		t.Fatalf("unmarshal the import answer: %v (body %s)", err, answers[2].Result)
	}
	if len(call.Content) > 0 {
		text = call.Content[0].Text
	}
	return call.IsError, text, call.StructuredContent
}

// demoImportSourceName is the source both cases name, inside the workspace the
// server serves.
const demoImportSourceName = "demo-source.txt"

// TestTheMCPEntryPointSuppliesThePluginResolutionAnImportRunsThrough drives
// `aggrega mcp` at its real entry point, over its real transport, and asserts
// that an import called there both completes and runs through the plugin
// cascade this package assembles.
//
// Three claims, and none substitutes for another. The drafted file read off the
// tree is the end-to-end one: the call was answered rather than refused for want
// of a resolution, and what it produced reached disk. The recorded resolution
// being non-nil is the field itself. Driving that recorded resolution against an
// embedded built-in, and comparing what it answers with what this package's own
// adapter answers, is what separates "the field was set to something" from "the
// field was set to the cascade the command line resolves by" — a bare non-nil
// function resolves nothing.
//
// 🔴 PLANTED DEFECT, DEMONSTRATED: remove ResolveImportPlugin from the
// mcp.Config literal the command builds and the call is refused ahead of all of
// this, saying the server was started without the plugin resolution an importer
// is loaded through; nothing is drafted and the seam is never reached.
func TestTheMCPEntryPointSuppliesThePluginResolutionAnImportRunsThrough(t *testing.T) {
	// Not parallel: points the shared import seam, a package-level variable.
	withCLIRegistrations(t, nil)
	seam := captureImportSeamArguments(t)
	root := mcpImportWorkspace(t, demoImportSourceName)

	isError, text, structured := importCallOverStdio(t, func(stdin, stdout, stderr *bytes.Buffer) {
		cmd := newRootCmd()
		cmd.SetIn(stdin)
		cmd.SetOut(stdout)
		cmd.SetErr(stderr)
		cmd.SetArgs([]string{"mcp", "--workspace-root", root})
		if code := executeRoot(context.Background(), cmd); code != 0 {
			t.Fatalf("aggrega mcp exited %d\nstderr:\n%s", code, stderr.String())
		}
	})
	if isError {
		t.Fatalf("the import was refused: %s", text)
	}

	var payload struct {
		WorkspaceRoot string   `json:"workspace_root"`
		Format        string   `json:"format"`
		Written       []string `json:"written"`
	}
	if err := json.Unmarshal(structured, &payload); err != nil {
		t.Fatalf("the answer carries no structured payload: %v (body %s)", err, structured)
	}
	if payload.Format != "demo" {
		t.Errorf("the source was read as the %q format, want demo", payload.Format)
	}
	if !slices.Equal(payload.Written, []string{demoDraftedFileName, importops.RecordFileName}) {
		t.Errorf("the answer names %v as what reached disk, want exactly [%s %s]",
			payload.Written, demoDraftedFileName, importops.RecordFileName)
	}
	if payload.WorkspaceRoot != root {
		t.Errorf("the answer names %q as the root the drafts are relative to, want the served workspace %q",
			payload.WorkspaceRoot, root)
	}

	// The tree rather than the answer: a call that drafted the right file, named
	// it, and put it somewhere else satisfies every assertion above.
	body, err := os.ReadFile(filepath.Join(root, demoDraftedFileName))
	if err != nil {
		t.Fatalf("the drafted file is not under the served workspace root: %v", err)
	}
	if string(body) != demoEchoBody {
		t.Errorf("the drafted file carries %q, want %q", string(body), demoEchoBody)
	}

	// The field itself, as the shared import layer received it.
	if !seam.reached {
		t.Fatal("the shared import layer was never reached, so nothing below says anything about what " +
			"the command supplied")
	}
	if seam.resolve == nil {
		t.Fatal("the command handed the shared import layer no plugin resolution, so no importer can be " +
			"reached however the workspace is configured")
	}

	// Which resolution it is. An embedded built-in is served by the cascade's
	// embed tier and by nothing a stand-in improvises, so resolving one to bytes
	// is what tells the command's own cascade from any non-nil function.
	builtins := embedded.URIs()
	if len(builtins) == 0 {
		t.Fatal("this binary embeds no built-in plugin, so the probe below cannot tell the command's " +
			"cascade from any non-nil function")
	}
	probe := builtins[0]
	resolved, err := seam.resolve(seam.rw, probe, nil)
	if err != nil {
		t.Fatalf("the resolution the command supplied cannot resolve the embedded built-in %s: %v", probe, err)
	}
	if len(resolved.Bytes) == 0 {
		t.Errorf("the resolution the command supplied answered for %s with no module bytes", probe)
	}

	// The same probe through this package's own adapter, so the two are compared
	// rather than each judged alone: what a served session resolves and what the
	// command line resolves are required to be the one cascade.
	direct, err := importPluginBytes(seam.rw, probe, nil)
	if err != nil {
		t.Fatalf("the command line's own resolution of %s failed, so the comparison below measures "+
			"nothing: %v", probe, err)
	}
	if !bytes.Equal(resolved.Bytes, direct.Bytes) || resolved.Tier != direct.Tier {
		t.Errorf("the served session resolves %s at tier %v to %d bytes; the command line resolves it at "+
			"tier %v to %d bytes", probe, resolved.Tier, len(resolved.Bytes), direct.Tier, len(direct.Bytes))
	}
}

// demoDraftedFileName is what the demo importer guest drafts for the demo
// format, and the file the case above reads back off the tree.
const demoDraftedFileName = "demo.yaml"

// TestTheImportToolRefusesWithoutThePluginResolution is the negative control for
// the case above. It stands the server up with no resolution — which is what
// every caller other than the command does — and pins the sentence such a
// session answers with, so a green above is known to be the absence of THIS
// refusal rather than the absence of any refusal at all.
func TestTheImportToolRefusesWithoutThePluginResolution(t *testing.T) {
	t.Parallel()
	root := mcpImportWorkspace(t, demoImportSourceName)

	isError, text, _ := importCallOverStdio(t, func(stdin, stdout, stderr *bytes.Buffer) {
		if err := mcp.Run(context.Background(), mcp.Config{
			Stdin:         stdin,
			Stdout:        stdout,
			Stderr:        stderr,
			ServerVersion: "test",
			WorkspaceRoot: root,
		}); err != nil {
			t.Fatalf("mcp.Run: %v (stderr: %s)", err, stderr.String())
		}
	})
	if !isError {
		t.Fatalf("a session carrying no plugin resolution answered the import rather than refusing it: %s", text)
	}
	if !strings.Contains(text, "without the plugin resolution an importer is loaded through") {
		t.Errorf("the refusal a resolution-less session answers with has moved; the case above is asserting "+
			"the absence of a sentence nothing produces: %q", text)
	}
	if _, err := os.Stat(filepath.Join(root, demoDraftedFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused import drafted into the served workspace anyway: %v", err)
	}
}
