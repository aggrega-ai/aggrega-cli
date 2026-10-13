// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage, at the command line, the assistant's import tool through
// `aggrega mcp` and the session's aggrega/import method through `aggrega serve`,
// for a source that is a directory. The workspace's catalog folder holds the
// files-probe importer — a guest that declares two file patterns and reports back
// exactly what it was handed — the demo importer, which declares none, and the
// JSON parser, each a Plugin entity generated from its built module with the
// module beside it. Each surface is driven at the real command tree, with the real
// import seam and the plugin resolution this package supplies.
//
// What the importer was handed is read off its own report on each surface — the
// command's error stream, the tool's diagnostics, the session's diagnostics — so a
// surface cannot pass on what it meant to send.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// directoryImportRoot lays the files-probe importer, the demo importer and the
// JSON parser into the conventional catalog folder of a fresh workspace, writes its
// configuration carrying the attachments attach returns for the probe as laid
// (attach may be nil), and returns the workspace root. A case calls
// isolateDirectoryImports first.
func directoryImportRoot(t *testing.T,
	attach func(t *testing.T, probe importopstest.LaidPlugin) []importopstest.Attachment) string {
	t.Helper()
	root := t.TempDir()
	catalogDir := filepath.Join(root, workspaceconfig.ConventionalCatalogDir)
	probe := importopstest.LayFilesProbeImporter(t, root, catalogDir)
	importopstest.LayDemoImporter(t, root, catalogDir)
	importopstest.LayJSONParser(t, root, catalogDir)
	var attachments []importopstest.Attachment
	if attach != nil {
		attachments = attach(t, probe)
	}
	importopstest.WriteConfiguration(t, root, attachments...)
	return root
}

// isolateDirectoryImports builds the guests the directory cases lay down and then
// isolates the per-user configuration and cache locations, as
// isolateMachineConfiguration does for the reference importers: the toolchain
// building a guest reads those locations, so every guest is built before they move.
func isolateDirectoryImports(t *testing.T) {
	t.Helper()
	importopstest.FilesProbeModule(t)
	wasmtest.EnsureFixtureBuilt(t, "import-go.wasm")
	isolateMachineConfiguration(t)
}

// skipWithoutUnreadableFiles skips a case needing a file whose opening fails, on
// the one platform where taking a file's permission bits away leaves it readable.
func skipWithoutUnreadableFiles(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows reads a file whatever its permission bits say, so no file here can be made unreadable")
	}
}

// unreadable takes every permission off the file at path for the rest of the test,
// failing the case where it can still be read.
func unreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("take the permissions off %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if _, err := os.ReadFile(path); err == nil {
		t.Fatalf("%s is still readable with no permissions, so nothing here can show it was never opened", path)
	}
}

// probeFlows lays the directory clause 3's first case names at dir: two files the
// probe's patterns select, of one base name in two subdirectories, and a third no
// pattern selects, made unreadable. It returns the names the importer is to be
// handed, in order, and the bytes each carries.
func probeFlows(t *testing.T, dir string) ([]string, map[string]string) {
	t.Helper()
	bodies := map[string]string{
		"a/x.probe.json": importopstest.FilesProbeBody("a"),
		"b/x.probe.json": importopstest.FilesProbeBody("b"),
	}
	for name, body := range bodies {
		importopstest.WriteSource(t, filepath.Join(dir, filepath.FromSlash(name)), body)
	}
	unreadable(t, importopstest.WriteSource(t, filepath.Join(dir, "notes.txt"), "notes"))
	return []string{"a/x.probe.json", "b/x.probe.json"}, bodies
}

// assertProbeHanded holds a report the probe made, read off a surface's answer, to
// the named sources it is to be handed, in order, with empty primary bytes.
func assertProbeHanded(t *testing.T, messages []string, names []string, bodies map[string]string) {
	t.Helper()
	report := importopstest.ReadFilesProbeReport(t, messages)
	if len(report.Primary) != 0 {
		t.Errorf("a directory import handed the importer primary bytes %q; want none", report.Primary)
	}
	got := make([]string, 0, len(report.Sources))
	for _, s := range report.Sources {
		got = append(got, s.Name)
		if string(s.Bytes) != bodies[s.Name] {
			t.Errorf("%s reached the importer carrying %q; want %q", s.Name, s.Bytes, bodies[s.Name])
		}
	}
	if !slices.Equal(got, names) {
		t.Errorf("the importer was handed %v; want exactly %v", got, names)
	}
}

// commentaryMessages returns the importer's diagnostic messages off the command's
// error stream, where each is written as "<severity>: <message>" after the
// preamble.
func commentaryMessages(stderr string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(stderr, "\n"), "\n") {
		if msg, ok := strings.CutPrefix(line, "info: "); ok {
			out = append(out, msg)
		}
	}
	return out
}

// TestImportCommandReadsADirectoryIntoNamedSources is clause 3's first and second
// cases at the command line: `aggrega import <directory>`, with the format named
// and with it recognised, hands the importer the two files its patterns select,
// named by their paths inside the directory, in that order — and never opens the
// third, which the case made unreadable.
func TestImportCommandReadsADirectoryIntoNamedSources(t *testing.T) {
	// Not parallel, rows included: captureStderr swaps process-global os.Stderr,
	// and the environment is set for the whole test.
	skipWithoutUnreadableFiles(t)
	withCLIRegistrations(t, nil)
	isolateDirectoryImports(t)

	for _, row := range []struct {
		name string
		args []string
	}{
		{"the format named", []string{"--format", importopstest.FilesProbeFormat}},
		{"the format recognised", nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			root := directoryImportRoot(t, nil)
			dir := filepath.Join(t.TempDir(), "flows")
			names, bodies := probeFlows(t, dir)

			code, stdout, stderr, process := runImportAtTheRoot(t, root, append([]string{dir, "--dry-run"}, row.args...)...)
			if code != 0 {
				t.Fatalf("aggrega import exited %d\nstdout:\n%s\ncommand error writer:\n%s\nprocess error stream:\n%s",
					code, stdout, stderr, process)
			}
			if want := "read as the " + importopstest.FilesProbeFormat + " format, with drafted paths relative to " +
				root + "\n"; !strings.HasPrefix(stderr, want) {
				t.Errorf("the commentary stream opens %q; want %q", stderr, want)
			}
			assertProbeHanded(t, commentaryMessages(stderr), names, bodies)
		})
	}
}

// directoryRefusalRows are the directory imports every surface refuses, each
// laying its own directory and naming the condition, by its accessor, the refusal
// carries.
var directoryRefusalRows = []struct {
	name string
	// attach is the workspace's attachments for the probe as laid, or nil.
	attach func(t *testing.T, probe importopstest.LaidPlugin) []importopstest.Attachment
	// lay puts the directory down at dir and returns the format the import names,
	// empty to have it recognised.
	lay       func(t *testing.T, dir string) string
	condition func() error
}{
	{
		name: "a link leading out of the directory",
		lay: func(t *testing.T, dir string) string {
			unreadable(t, importopstest.WriteSource(t, filepath.Join(dir, "a", "x.probe.json"),
				importopstest.FilesProbeBody("a")))
			target := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "x.probe.json"),
				importopstest.FilesProbeBody("outside"))
			if err := os.MkdirAll(filepath.Join(dir, "b"), 0o755); err != nil {
				t.Fatalf("create the link's directory: %v", err)
			}
			if err := os.Symlink(target, filepath.Join(dir, "b", "x.probe.json")); err != nil {
				t.Fatalf("make the link: %v", err)
			}
			return importopstest.FilesProbeFormat
		},
		condition: readscope.ErrReadLeadsOutOfWorkspace,
	},
	{
		name: "an importer declaring no file patterns",
		lay: func(t *testing.T, dir string) string {
			unreadable(t, importopstest.WriteSource(t, filepath.Join(dir, "x.probe.json"), importopstest.FilesProbeBody("x")))
			return "demo"
		},
		condition: importops.ErrImporterDeclaresNoFilePatterns,
	},
	{
		name: "a directory holding no file the patterns select",
		lay: func(t *testing.T, dir string) string {
			importopstest.WriteSource(t, filepath.Join(dir, "notes.txt"), "notes")
			return importopstest.FilesProbeFormat
		},
		condition: importops.ErrImportDirectoryMatchedNothing,
	},
	{
		name: "a name a directory file and a with.sources file share",
		attach: func(t *testing.T, probe importopstest.LaidPlugin) []importopstest.Attachment {
			attached := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "top.probe.json"),
				importopstest.FilesProbeBody("attached"))
			return []importopstest.Attachment{{URI: probe.Identity, Sources: []string{attached}}}
		},
		lay: func(t *testing.T, dir string) string {
			importopstest.WriteSource(t, filepath.Join(dir, "top.probe.json"), importopstest.FilesProbeBody("dir"))
			return importopstest.FilesProbeFormat
		},
		condition: importops.ErrImportSourceNameCollision,
	},
}

// conditionCodes returns the short code and the registered sentence of the
// condition condition names, failing the test where it carries either not.
func conditionCodes(t *testing.T, condition error) (string, string) {
	t.Helper()
	short, ok := cerrors.ShortCodeOf(condition)
	if !ok {
		t.Fatalf("the condition %v carries no short code", condition)
	}
	sentence, ok := cerrors.MessageOf(condition)
	if !ok {
		t.Fatalf("the condition %v carries no registered sentence", condition)
	}
	return short, sentence
}

// TestImportCommandRefusesADirectoryUnderEachCondition is clause 3's third, fourth
// and fifth cases at the command line, with the refusal beside the fourth: each is
// refused opening with its condition's short code and sentence, nothing reaches
// standard output, and nothing is read that the case made unreadable.
func TestImportCommandRefusesADirectoryUnderEachCondition(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	skipWithoutUnreadableFiles(t)
	withCLIRegistrations(t, nil)
	isolateDirectoryImports(t)

	for _, row := range directoryRefusalRows {
		t.Run(row.name, func(t *testing.T) {
			root := directoryImportRoot(t, row.attach)
			dir := filepath.Join(t.TempDir(), "flows")
			format := row.lay(t, dir)

			code, stdout, _, process := runImportAtTheRoot(t, root, dir, "--dry-run", "--format", format)
			if code == 0 {
				t.Fatalf("aggrega import accepted a directory it must refuse:\n%s", stdout)
			}
			short, sentence := conditionCodes(t, row.condition())
			if !strings.Contains(process, "["+short+"]") {
				t.Errorf("the refusal does not open with %s:\n%s", short, process)
			}
			if !strings.Contains(process, sentence) {
				t.Errorf("the refusal does not carry the condition's sentence %q:\n%s", sentence, process)
			}
			if strings.Contains(process, "permission denied") {
				t.Errorf("the refusal reports a file it should never have opened:\n%s", process)
			}
			if stdout != "" {
				t.Errorf("a refused import reported drafts on standard output:\n%s", stdout)
			}
		})
	}
}

// directoryToolAnswer is the import tool's answer, as the assistant's client reads
// it.
type directoryToolAnswer struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent struct {
		Format      string `json:"format"`
		Diagnostics []struct {
			Message string `json:"message"`
		} `json:"diagnostics"`
	} `json:"structuredContent"`
	IsError bool `json:"isError"`
}

// callImportToolOverItsStreams sends one import call to `aggrega mcp` over its own
// streams, served at root, and returns the answer.
func callImportToolOverItsStreams(t *testing.T, root string, arguments map[string]any) directoryToolAnswer {
	t.Helper()
	args, err := json.Marshal(map[string]any{"name": "import", "arguments": arguments})
	if err != nil {
		t.Fatalf("marshal the call: %v", err)
	}
	var stdin, stdout, stderr bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/call", string(args)))
	cmd := newRootCmd()
	cmd.SetIn(&stdin)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"mcp", "--workspace-root", root})
	if code := executeRoot(context.Background(), cmd); code != 0 {
		t.Fatalf("aggrega mcp exited %d\nstderr:\n%s", code, stderr.String())
	}
	answers := answersByID(t, stdout.String(), 1, 2)
	if answers[2].Error != nil {
		t.Fatalf("the import call was not dispatched: %s", answers[2].Error.Message)
	}
	var answer directoryToolAnswer
	if err := json.Unmarshal(answers[2].Result, &answer); err != nil {
		t.Fatalf("decode the import answer: %v (body %s)", err, answers[2].Result)
	}
	return answer
}

// TestTheAssistantImportToolReadsADirectory is clause 3's cases at the assistant's
// import tool over the process's own streams: the directory, named relative to the
// served workspace, hands the importer exactly the two files with the format named
// and recognised, and each refusal opens with its condition's short code.
func TestTheAssistantImportToolReadsADirectory(t *testing.T) {
	// Not parallel: the environment is set for the whole test.
	skipWithoutUnreadableFiles(t)
	withCLIRegistrations(t, nil)
	isolateDirectoryImports(t)

	for _, row := range []struct {
		name   string
		format string
	}{
		{"the format named", importopstest.FilesProbeFormat},
		{"the format recognised", ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			root := directoryImportRoot(t, nil)
			names, bodies := probeFlows(t, filepath.Join(root, "flows"))
			arguments := map[string]any{"source": "flows", "dry_run": true}
			if row.format != "" {
				arguments["format"] = row.format
			}
			answer := callImportToolOverItsStreams(t, root, arguments)
			if answer.IsError {
				t.Fatalf("the import was refused: %+v", answer.Content)
			}
			messages := make([]string, 0, len(answer.StructuredContent.Diagnostics))
			for _, d := range answer.StructuredContent.Diagnostics {
				messages = append(messages, d.Message)
			}
			assertProbeHanded(t, messages, names, bodies)
		})
	}

	for _, row := range directoryRefusalRows {
		t.Run(row.name, func(t *testing.T) {
			root := directoryImportRoot(t, row.attach)
			format := row.lay(t, filepath.Join(root, "flows"))
			answer := callImportToolOverItsStreams(t, root,
				map[string]any{"source": "flows", "dry_run": true, "format": format})
			if !answer.IsError || len(answer.Content) == 0 {
				t.Fatalf("the tool accepted a directory it must refuse: %+v", answer)
			}
			short, sentence := conditionCodes(t, row.condition())
			text := answer.Content[0].Text
			if !strings.Contains(text, short+": ") || !strings.Contains(text, sentence) {
				t.Errorf("the refusal does not name %s and its sentence %q: %s", short, sentence, text)
			}
		})
	}
}

// callImportSession sends one aggrega/import request to `aggrega serve` over its
// own framing, served at root, and returns the answering frame.
func callImportSession(t *testing.T, root string, params map[string]any) map[string]json.RawMessage {
	t.Helper()
	params["workspaceRoot"] = root
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "aggrega/import", "params": params})
	if err != nil {
		t.Fatalf("marshal the request: %v", err)
	}
	var stdin, stdout, stderr bytes.Buffer
	fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(request))
	stdin.Write(request)
	cmd := newRootCmd()
	cmd.SetIn(&stdin)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"serve", "--workspace-root", root})
	if code := executeRoot(context.Background(), cmd); code != 0 {
		t.Fatalf("aggrega serve exited %d\nstderr:\n%s", code, stderr.String())
	}
	return daemonFrameAnswering(t, stdout.Bytes(), 1)
}

// TestTheSessionImportMethodReadsADirectory is clause 3's cases at the session's
// aggrega/import method: the directory, selected by an absolute path outside the
// workspace as a person picking it would, hands the importer exactly the two files
// with the format named and recognised, and each refusal carries its condition's
// short code.
func TestTheSessionImportMethodReadsADirectory(t *testing.T) {
	// Not parallel: the environment is set for the whole test.
	skipWithoutUnreadableFiles(t)
	withCLIRegistrations(t, nil)
	isolateDirectoryImports(t)

	for _, row := range []struct {
		name   string
		format string
	}{
		{"the format named", importopstest.FilesProbeFormat},
		{"the format recognised", ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			root := directoryImportRoot(t, nil)
			dir := filepath.Join(t.TempDir(), "flows")
			names, bodies := probeFlows(t, dir)
			params := map[string]any{"source": dir, "outputDir": "drafts", "dryRun": true}
			if row.format != "" {
				params["format"] = row.format
			}
			frame := callImportSession(t, root, params)
			if raw, refused := frame["error"]; refused {
				t.Fatalf("aggrega/import was refused: %s", raw)
			}
			var result struct {
				Format      string `json:"format"`
				Diagnostics []struct {
					Message string `json:"message"`
				} `json:"diagnostics"`
			}
			if err := json.Unmarshal(frame["result"], &result); err != nil {
				t.Fatalf("decode the import answer: %v\n%s", err, frame["result"])
			}
			if result.Format != importopstest.FilesProbeFormat {
				t.Errorf("the directory was read as the %q format, want %q", result.Format, importopstest.FilesProbeFormat)
			}
			messages := make([]string, 0, len(result.Diagnostics))
			for _, d := range result.Diagnostics {
				messages = append(messages, d.Message)
			}
			assertProbeHanded(t, messages, names, bodies)
		})
	}

	for _, row := range directoryRefusalRows {
		t.Run(row.name, func(t *testing.T) {
			root := directoryImportRoot(t, row.attach)
			dir := filepath.Join(t.TempDir(), "flows")
			format := row.lay(t, dir)
			frame := callImportSession(t, root,
				map[string]any{"source": dir, "outputDir": "drafts", "dryRun": true, "format": format})
			raw, refused := frame["error"]
			if !refused {
				t.Fatalf("aggrega/import accepted a directory it must refuse: %s", frame["result"])
			}
			short, sentence := conditionCodes(t, row.condition())
			if !strings.Contains(string(raw), short) || !strings.Contains(string(raw), jsonText(t, sentence)) {
				t.Errorf("the refusal does not carry %s and its sentence %q: %s", short, sentence, raw)
			}
		})
	}
}

// jsonText is s as it reads inside a JSON string, without the quotes, so a
// sentence carrying a character JSON escapes is looked for as the frame spells it.
func jsonText(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("encode %q: %v", s, err)
	}
	return string(b[1 : len(b)-1])
}
