// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Where an import lands its drafts, compared across every surface that carries
// the capability, each driven as a spawned process of the built binary over its
// own wire: the command line, the assistant's in-process import tool and its
// command tool over the tool protocol, and the editor's language server.
//
// Nothing here points a test seam. A spawned binary cannot take one, and the
// command tool re-runs the binary in a process of its own, so the importer is
// reached the way a workspace reaches one: the catalog advertises it, and its
// module is read from the workspace's content cache.

import (
	"bufio"
	"encoding/json"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cachedImportLayout is one row's fixture: a workspace whose catalog advertises
// the demo importer and whose content cache holds its module, a sub-folder of
// it, and another folder beside it. Every path is absolute and sits beneath
// parent, so one fingerprint of parent sees everything a run could touch.
type cachedImportLayout struct {
	parent, root, sub, other string
}

// demoImporterEntity is the Plugin entity the fixture's catalog advertises the
// demo importer through. Its coordinate is what the module is cached under.
const demoImporterEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: demo-importer
  version: 1.0.0
  pluginType: importer
spec:
  abiVersion: v1alpha1
  importer:
    capabilities:
      - import:demo
`

// layCachedImport builds the fixture a row runs in. The module is written
// through the content cache's own writer, so where it lands is the cache's one
// spelling of the slot rather than a second one written here.
func layCachedImport(t *testing.T) cachedImportLayout {
	t.Helper()
	parent := t.TempDir()
	l := cachedImportLayout{parent: parent, root: filepath.Join(parent, "ws"), other: filepath.Join(parent, "other")}
	l.sub = filepath.Join(l.root, "sub")
	for _, dir := range []string{l.sub, l.other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	writeFixture(t, filepath.Join(l.root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	writeFixture(t, filepath.Join(l.root, workspaceconfig.ConventionalCatalogDir, "demo-importer.yaml"), demoImporterEntity)
	writeFixture(t, filepath.Join(l.root, demoImportSourceName), demoSourceBody)
	module := wasmtest.EnsureFixtureBytes(t, "import-go.wasm")
	cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
	if err := oci.WriteCached(l.root, cacheDir, oci.PluginArtifactKind(), types.PluginCoordinate("importer", "demo-importer"), "", module); err != nil {
		t.Fatalf("cache the demo importer's module: %v", err)
	}
	return l
}

// importParityEnv is the environment a spawned binary runs under here: offline,
// since nothing concerns a registry, and with the workspace root named by the
// variable only where launchRoot is non-empty. An inherited value is dropped
// first, so a row naming no root runs as a launch naming none.
func importParityEnv(t *testing.T, launchRoot string) []string {
	t.Helper()
	var env []string
	for _, entry := range builtBinaryEnv(t, "AGGREGA_OFFLINE=1") {
		if name, _, _ := strings.Cut(entry, "="); name == workspaceconfig.EnvWorkspaceRoot {
			continue
		}
		env = append(env, entry)
	}
	if launchRoot != "" {
		env = append(env, workspaceconfig.EnvWorkspaceRoot+"="+launchRoot)
	}
	return env
}

// importViaCommandLine runs the built binary with args in dir.
func importViaCommandLine(t *testing.T, bin, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = importParityEnv(t, "")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("aggrega %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

// importViaToolProtocol launches the built binary as `aggrega mcp` in dir, with
// the workspace root named by the launch where launchRoot is non-empty, calls one
// tool and fails the test unless the call succeeded.
func importViaToolProtocol(t *testing.T, bin, dir, launchRoot, tool string, args map[string]any) {
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
	if call.IsError {
		t.Fatalf("%s was refused: %+v", tool, call.Content)
	}
}

// importViaLanguageServer starts the built binary as `aggrega serve` at the
// workspace root and sends one aggrega/import naming the output directory ".",
// and holds the answer's decisions to an empty list.
func importViaLanguageServer(t *testing.T, bin, root string) {
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
	if _, err := stdin.Write(framedFor(t, cmd, 1, "aggrega/import", map[string]any{
		"source":    demoImportSourceName,
		"outputDir": ".",
		"format":    "demo",
	})); err != nil {
		t.Fatalf("write the request: %v", err)
	}
	resp := readOneResponse(t, stdout, responseLivenessCeiling(t))
	_ = stdin.Close()
	// The rest of the output is drained before waiting, because Wait closes the
	// pipe it is read from; both run off this goroutine so the ceiling below
	// bounds a server that never exits.
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
		t.Fatalf("aggrega/import was refused: %+v (stderr: %s)", failure, stderr.String())
	}
	// The demo importer reports no mapping decisions, so the answer is the case of
	// an importer with none: decisions is still there, as an empty list rather than
	// an absent key or a null.
	result, _ := resp["result"].(map[string]any)
	decisions, present := result["decisions"]
	if list, isList := decisions.([]any); !present || !isList || len(list) != 0 {
		t.Errorf("an importer reporting no decisions answered decisions as %#v (present=%v); want an empty list",
			decisions, present)
	}
}

// TestEverySurfaceLandsAnImportAtTheWorkspaceRoot drives one import over one
// source through every surface that carries it, each over a fresh copy of the
// fixture, and reads where the drafts landed off the content digests of the
// whole fixture: the command line from a sub-folder and from another folder
// naming the workspace with --workspace-root; the in-process import tool with
// the workspace named by the launch and by the call; the import command's tool,
// whose command starts in the root the launch names, or, with none named, in the
// folder the server was started in inside the workspace, where its own
// resolution finds the root; and the language server, sent the output directory
// ".". Each names no destination of its own except the language server, whose
// "." is the workspace root.
//
// Every one of them lands the same draft, byte for byte, at the workspace root,
// with the record of what it wrote beside it, and changes nothing else. One more row passes the command tool its generated
// output_dir argument, which reaches the command's --output-dir flag and lands
// the draft beneath that directory, resolved in the folder the command runs in —
// here the root the launch names.
//
// 🔴 PLANTED DEFECTS:
//   - have outputDirFor (internal/importops/import.go) return "." for an empty
//     name: the command-line rows land in ws/sub and other, the import-tool rows
//     in other, and the command tool's no-root row in ws/sub, so each reddens.
//   - have the command hand OutputDir "." again (cmd/aggrega/import.go): the
//     command-line rows and the command tool's no-root row redden.
//   - have the import tool hand the layer
//     OutputDir: tw.root + string(os.PathSeparator) + "drafts"
//     (internal/mcp/tool_import.go, the importops.Options literal): both
//     import-tool rows redden, landing in ws/drafts.
func TestEverySurfaceLandsAnImportAtTheWorkspaceRoot(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)

	// Every surface writes the record of what the import wrote beside the draft.
	atTheRoot := []string{"ws/" + importops.RecordFileName + " was created", "ws/demo.yaml was created"}
	for _, row := range []struct {
		name        string
		run         func(t *testing.T, l cachedImportLayout)
		wantChanges []string
		wantDraft   string
	}{
		{
			name: "the command line from a sub-folder",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaCommandLine(t, bin, l.sub, "import", filepath.Join(l.root, demoImportSourceName), "--format", "demo")
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the command line from another folder, the workspace named with --workspace-root",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaCommandLine(t, bin, l.other, "--workspace-root", l.root,
					"import", filepath.Join(l.root, demoImportSourceName), "--format", "demo")
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the import tool, the workspace named by the launch",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaToolProtocol(t, bin, l.other, l.root, "import",
					map[string]any{"source": demoImportSourceName, "format": "demo"})
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the import tool, the workspace named by the call",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaToolProtocol(t, bin, l.other, "", "import", map[string]any{
					"source": filepath.Join(l.root, demoImportSourceName), "format": "demo", "workspace_root": l.root,
				})
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the import command's tool, its command started in the root the launch names",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaToolProtocol(t, bin, l.other, l.root, "cli-import",
					map[string]any{"args": []string{demoImportSourceName}, "format": "demo"})
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the import command's tool, no root named, the server started in a sub-folder of the workspace",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaToolProtocol(t, bin, l.sub, "", "cli-import",
					map[string]any{"args": []string{"../" + demoImportSourceName}, "format": "demo"})
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the language server, sent the output directory \".\"",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaLanguageServer(t, bin, l.root)
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the import command's tool, its generated output_dir argument reaching --output-dir",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaToolProtocol(t, bin, l.other, l.root, "cli-import", map[string]any{
					"args": []string{demoImportSourceName}, "format": "demo", "output_dir": "drafts",
				})
			},
			wantChanges: []string{"ws/drafts was created", "ws/drafts/" + importops.RecordFileName + " was created",
				"ws/drafts/demo.yaml was created"},
			wantDraft: "ws/drafts/demo.yaml",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			l := layCachedImport(t)
			before := treeFingerprint(t, l.parent)
			row.run(t, l)
			changes := fingerprintDifferences(before, treeFingerprint(t, l.parent))
			if !slices.Equal(changes, row.wantChanges) {
				t.Errorf("the import changed the fixture as %v; want exactly %v", changes, row.wantChanges)
			}
			body, err := os.ReadFile(filepath.Join(l.parent, filepath.FromSlash(row.wantDraft)))
			if err != nil {
				t.Fatalf("the draft is not at %s: %v", row.wantDraft, err)
			}
			if string(body) != demoEchoBody {
				t.Errorf("%s holds %q; want the bytes every surface drafts, %q", row.wantDraft, body, demoEchoBody)
			}
		})
	}
}

// importOptionAnswer is what one surface answered for one import, brought to one
// shape so three surfaces' answers can be compared field by field: the format the
// source was read as, the directory the drafted paths are relative to, the files
// that reached disk, and — under a dry run — each drafted file handed back, by its
// path. ok is false for a refused import, and text then carries what it said.
type importOptionAnswer struct {
	ok        bool
	text      string
	format    string
	outputDir string
	written   []string
	reported  map[string]string
}

// importOption is one option the command, the editor's session and the curated
// assistant tool all take, in each surface's own spelling, beside the source and
// the demo format every row names. It is the one option a row sends, so a row
// differs from the plain import in that option and nothing else.
type importOption struct {
	command []string
	session map[string]any
	tool    map[string]any
}

// importOptionSurface is one surface, run over a layout with one option.
type importOptionSurface struct {
	name string
	run  func(t *testing.T, bin string, l cachedImportLayout, option importOption) importOptionAnswer
}

// importOptionSurfaces are the three surfaces carrying every import option: the
// command line run at the workspace root, the editor's session sent exactly the
// fields the editor's import control sends — the source and only the options a
// person chose, with no destination unless one was chosen — and the assistant's
// curated import tool over the process's own streams, the workspace named by the
// launch.
var importOptionSurfaces = []importOptionSurface{
	{name: "the command line", run: importOptionViaCommand},
	{name: "the editor's session", run: importOptionViaSession},
	{name: "the assistant's import tool", run: importOptionViaTool},
}

// importOptionViaCommand runs `aggrega import` at the workspace root and reads its
// answer off its two streams: the first line of its commentary names the format
// and the directory the drafted paths are relative to, its standard output names
// each file written, and under --dry-run it carries the drafts instead.
func importOptionViaCommand(t *testing.T, bin string, l cachedImportLayout, option importOption) importOptionAnswer {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"import", demoImportSourceName, "--format", "demo"}, option.command...)...)
	cmd.Dir = l.root
	cmd.Env = importParityEnv(t, "")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, exited := err.(*exec.ExitError); !exited {
			t.Fatalf("run aggrega import: %v", err)
		}
		return importOptionAnswer{text: stdout.String() + stderr.String()}
	}
	answer := importOptionAnswer{ok: true, written: []string{}}
	for _, line := range strings.Split(stderr.String(), "\n") {
		if rest, found := strings.CutPrefix(line, "read as the "); found {
			format, dir, _ := strings.Cut(rest, " format, with drafted paths relative to ")
			answer.format, answer.outputDir = format, dir
			break
		}
	}
	if slices.Contains(option.command, "--dry-run") {
		docs, err := authoredcontent.Decode([]byte(stdout.String()))
		if err != nil {
			t.Fatalf("decode the reported drafts: %v\n%s", err, stdout.String())
		}
		answer.reported = map[string]string{}
		for _, doc := range docs {
			answer.reported[doc.Path] = doc.Content
		}
		return answer
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		if file, found := strings.CutPrefix(line, "wrote "); found {
			answer.written = append(answer.written, file)
		}
	}
	return answer
}

// importOptionViaSession starts `aggrega serve` at the workspace root and sends one
// aggrega/import carrying the source, the format and the option.
func importOptionViaSession(t *testing.T, bin string, l cachedImportLayout, option importOption) importOptionAnswer {
	t.Helper()
	params := map[string]any{"source": demoImportSourceName, "format": "demo"}
	maps.Copy(params, option.session)
	body, ok := sessionImport(t, bin, l.root, params)
	if !ok {
		return importOptionAnswer{text: body}
	}
	var result struct {
		Format    string   `json:"format"`
		OutputDir string   `json:"outputDir"`
		Written   []string `json:"written"`
		Files     []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("decode the session's answer: %v\n%s", err, body)
	}
	answer := importOptionAnswer{ok: true, format: result.Format, outputDir: result.OutputDir, written: result.Written}
	if dryRun, _ := option.session["dryRun"].(bool); dryRun {
		answer.reported = map[string]string{}
		for _, f := range result.Files {
			answer.reported[f.Path] = f.Content
		}
	}
	return answer
}

// importOptionViaTool launches `aggrega mcp` with the workspace root named by the
// launch and calls the import tool with the source, the format and the option,
// reading its structured answer.
func importOptionViaTool(t *testing.T, bin string, l cachedImportLayout, option importOption) importOptionAnswer {
	t.Helper()
	args := map[string]any{"source": demoImportSourceName, "format": "demo"}
	maps.Copy(args, option.tool)
	call := callImportToolStructured(t, bin, l.other, l.root, args)
	if call.IsError {
		var texts []string
		for _, c := range call.Content {
			texts = append(texts, c.Text)
		}
		return importOptionAnswer{text: strings.Join(texts, "\n")}
	}
	var payload struct {
		Format    string   `json:"format"`
		OutputDir *string  `json:"output_dir"`
		Written   []string `json:"written"`
		Drafts    []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"drafts"`
	}
	if err := json.Unmarshal(call.StructuredContent, &payload); err != nil {
		t.Fatalf("decode the tool's structured answer: %v\n%s", err, call.StructuredContent)
	}
	answer := importOptionAnswer{ok: true, format: payload.Format, written: payload.Written}
	if payload.OutputDir != nil {
		answer.outputDir = *payload.OutputDir
	}
	if dryRun, _ := option.tool["dry_run"].(bool); dryRun {
		answer.reported = map[string]string{}
		for _, d := range payload.Drafts {
			answer.reported[d.Path] = d.Content
		}
	}
	return answer
}

// importToolCall is one tools/call answer as the protocol carries it.
type importToolCall struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

// callImportToolStructured launches the built binary as `aggrega mcp` in dir,
// with the workspace root named by the launch, calls the import tool with args
// and returns its answer, failing the test on a protocol error.
func callImportToolStructured(t *testing.T, bin, dir, launchRoot string, args map[string]any) importToolCall {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": "import", "arguments": args})
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
		t.Fatalf("tools/call import: error %d: %s", frame.Error.Code, frame.Error.Message)
	}
	var call importToolCall
	if err := json.Unmarshal(frame.Result, &call); err != nil {
		t.Fatalf("decode the import answer: %v (%s)", err, frame.Result)
	}
	return call
}

// TestEverySurfaceCarriesEachImportOptionOnTheSameTerms imports one source through
// the command line, the editor's session and the assistant's import tool with
// each option the three carry in turn — the format, a preview, a destination and
// a sync — each surface over a fresh copy of the fixture, and holds the three to
// one another and to the tree: the same changes beneath the fixture, the same
// bytes in every drafted file and in the record beside them, and the same answer
// — the format, the directory the drafted paths are relative to, relative to the
// fixture, the files written, and under a preview the drafts handed back.
//
// The format, preview and sync rows name no destination on any surface, so they
// are also the rule's absent row: every surface drafts beneath the workspace root
// and names it, the session included, sent no outputDir.
//
// 🔴 PLANTED DEFECTS — each one surface dropping one option turns that row red:
//   - format: drop Format from the importops.Options literal in dispatchImport
//     (internal/serve/import.go): the session's rows are refused, the demo source
//     matching no importer's signature.
//   - preview: have importInvoke (internal/mcp/tool_import.go) select the writing
//     delivery whatever dry_run says: the tool's preview row writes the drafts.
//   - destination: drop OutputDir from the importops.Options literal in
//     importInvoke: the tool's destination row drafts at the workspace root.
//   - sync: drop Sync from the importops.Options literal in dispatchImport: the
//     session's sync row writes the draft again rather than answering in step.
//   - the destination's absence: restore the session's refusal of an empty
//     outputDir: the session's format, preview and sync rows are refused.
func TestEverySurfaceCarriesEachImportOptionOnTheSameTerms(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)

	atTheRoot := []string{"ws/" + importops.RecordFileName + " was created", "ws/demo.yaml was created"}
	written := []string{"demo.yaml", importops.RecordFileName}
	for _, row := range []struct {
		name   string
		option importOption
		// first is an import every surface runs before the row's own, in its own
		// spelling, and whose changes the row's are read after.
		first *importOption
		// wantChanges is every change the row's import makes beneath the fixture.
		wantChanges []string
		// wantDir is the directory the drafted paths are relative to, relative to
		// the fixture.
		wantDir string
		// wantWritten is the files the answer names as written.
		wantWritten []string
		// draftedAt is where the drafts are read from, relative to the fixture:
		// the tree for a write, and for a preview the place they would land.
		draftedAt string
		preview   bool
	}{
		{
			name:        "the format, named",
			wantChanges: atTheRoot, wantDir: "ws", wantWritten: written, draftedAt: "ws",
		},
		{
			name: "a preview",
			option: importOption{command: []string{"--dry-run"}, session: map[string]any{"dryRun": true},
				tool: map[string]any{"dry_run": true}},
			wantDir: "ws", wantWritten: []string{}, preview: true,
		},
		{
			name: "a destination inside the workspace",
			option: importOption{command: []string{"--output-dir", "drafts"}, session: map[string]any{"outputDir": "drafts"},
				tool: map[string]any{"output_dir": "drafts"}},
			wantChanges: []string{"ws/drafts was created", "ws/drafts/" + importops.RecordFileName + " was created",
				"ws/drafts/demo.yaml was created"},
			wantDir: "ws/drafts", wantWritten: written, draftedAt: "ws/drafts",
		},
		{
			name: "a sync of drafts in step with their source",
			option: importOption{command: []string{"--sync"}, session: map[string]any{"sync": true},
				tool: map[string]any{"sync": true}},
			first:   &importOption{},
			wantDir: "ws", wantWritten: []string{}, draftedAt: "ws",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			answers := make([]importOptionAnswer, len(importOptionSurfaces))
			drafted := make([]map[string]string, len(importOptionSurfaces))
			for i, surface := range importOptionSurfaces {
				l := layCachedImport(t)
				if row.first != nil {
					if first := surface.run(t, bin, l, *row.first); !first.ok {
						t.Fatalf("%s: the first import was refused: %s", surface.name, first.text)
					}
				}
				before := treeFingerprint(t, l.parent)
				answer := surface.run(t, bin, l, row.option)
				if !answer.ok {
					t.Fatalf("%s refused the import: %s", surface.name, answer.text)
				}
				changes := fingerprintDifferences(before, treeFingerprint(t, l.parent))
				if !slices.Equal(changes, row.wantChanges) {
					t.Errorf("%s changed the fixture as %v; want exactly %v", surface.name, changes, row.wantChanges)
				}
				if answer.format != "demo" {
					t.Errorf("%s read the source as the %q format; want demo", surface.name, answer.format)
				}
				if !filepath.IsAbs(answer.outputDir) ||
					!sameDirectory(t, answer.outputDir, filepath.Join(l.parent, filepath.FromSlash(row.wantDir))) {
					t.Errorf("%s names %q as the directory the drafts are relative to; want the fixture's %s",
						surface.name, answer.outputDir, row.wantDir)
				}
				if !slices.Equal(answer.written, row.wantWritten) {
					t.Errorf("%s names %v as written; want %v", surface.name, answer.written, row.wantWritten)
				}
				answers[i] = answer
				if row.preview {
					drafted[i] = answer.reported
					continue
				}
				drafted[i] = map[string]string{}
				for _, file := range written {
					body, err := os.ReadFile(filepath.Join(l.parent, filepath.FromSlash(row.draftedAt), file))
					if err != nil {
						t.Fatalf("%s: the drafted %s is not beneath %s: %v", surface.name, file, row.draftedAt, err)
					}
					drafted[i][file] = string(body)
				}
			}
			if drafted[0]["demo.yaml"] != demoEchoBody {
				t.Errorf("%s drafted demo.yaml as %q; want %q", importOptionSurfaces[0].name, drafted[0]["demo.yaml"],
					demoEchoBody)
			}
			for i := 1; i < len(importOptionSurfaces); i++ {
				if !maps.Equal(drafted[i], drafted[0]) {
					t.Errorf("%s drafted %v; %s drafted %v — one import drafts one set of bytes on every surface",
						importOptionSurfaces[i].name, drafted[i], importOptionSurfaces[0].name, drafted[0])
				}
			}
		})
	}
}

// TestTheImportDestinationRuleHoldsOnEverySurface holds the rest of the one
// destination rule the command line, the editor's session and the assistant's
// import tool share, past the absent and inside rows the option test above
// covers. The session holds a named destination inside the workspace it opened:
// a value climbing out of it, an absolute directory outside it and a directory
// leading out of it through a link are each refused naming outputDir, with
// nothing written anywhere in the fixture. The tool over the process's own
// streams takes a destination as named, as the command's --output-dir does: a
// directory outside the workspace is drafted into, the same bytes the command
// drafts there, and a link standing in it that leads out of it is refused on both
// with nothing written.
//
// 🔴 PLANTED DEFECTS:
//   - resolve the session's outputDir with selectedPath rather than workspacePath
//     (internal/serve/import.go): the three session rows are admitted and redden.
//     The link row reddens on the fingerprint, which records ws/out-link as the
//     link it is and other/, the directory it leads to, file by file: the drafts
//     written through the link appear as other/demo.yaml and the record created.
//   - have importInvoke (internal/mcp/tool_import.go) hand the layer no
//     destination: the tool's outside row drafts at the workspace root and
//     reddens.
func TestTheImportDestinationRuleHoldsOnEverySurface(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)

	for _, row := range []struct {
		name      string
		outputDir func(l cachedImportLayout) string
		arrange   func(t *testing.T, l cachedImportLayout)
	}{
		{name: "a value climbing out of the workspace", outputDir: func(cachedImportLayout) string { return "../other" }},
		{name: "an absolute directory outside the workspace", outputDir: func(l cachedImportLayout) string { return l.other }},
		{
			name:      "a directory leading out of the workspace through a link",
			outputDir: func(cachedImportLayout) string { return "out-link" },
			arrange: func(t *testing.T, l cachedImportLayout) {
				if err := os.Symlink(l.other, filepath.Join(l.root, "out-link")); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			},
		},
	} {
		t.Run("the session refuses "+row.name, func(t *testing.T) {
			l := layCachedImport(t)
			if row.arrange != nil {
				row.arrange(t, l)
			}
			before := treeFingerprint(t, l.parent)
			answer := importOptionViaSession(t, bin, l, importOption{session: map[string]any{"outputDir": row.outputDir(l)}})
			if answer.ok {
				t.Fatalf("the session drafted into %q rather than refusing it", row.outputDir(l))
			}
			if !strings.Contains(answer.text, "outputDir must name a path inside the workspace this session opened") {
				t.Errorf("the refusal does not name outputDir and the bound: %s", answer.text)
			}
			if changes := fingerprintDifferences(before, treeFingerprint(t, l.parent)); len(changes) != 0 {
				t.Errorf("a refused import changed the fixture: %v", changes)
			}
		})
	}

	// outsideRows are the command's --output-dir and the tool's output_dir over the
	// process's own streams, each naming the directory beside the workspace.
	outsideRows := []struct {
		name string
		run  func(t *testing.T, l cachedImportLayout) importOptionAnswer
	}{
		{"the command line's --output-dir", func(t *testing.T, l cachedImportLayout) importOptionAnswer {
			return importOptionViaCommand(t, bin, l, importOption{command: []string{"--output-dir", l.other}})
		}},
		{"the import tool's output_dir over standard input and output", func(t *testing.T, l cachedImportLayout) importOptionAnswer {
			return importOptionViaTool(t, bin, l, importOption{tool: map[string]any{"output_dir": l.other}})
		}},
	}
	for _, row := range outsideRows {
		t.Run(row.name+" drafts into a directory outside the workspace", func(t *testing.T) {
			l := layCachedImport(t)
			before := treeFingerprint(t, l.parent)
			answer := row.run(t, l)
			if !answer.ok {
				t.Fatalf("the import into %s was refused: %s", l.other, answer.text)
			}
			want := []string{"other/" + importops.RecordFileName + " was created", "other/demo.yaml was created"}
			if changes := fingerprintDifferences(before, treeFingerprint(t, l.parent)); !slices.Equal(changes, want) {
				t.Errorf("the import changed the fixture as %v; want exactly %v", changes, want)
			}
			if !sameDirectory(t, answer.outputDir, l.other) {
				t.Errorf("the answer names %q as the directory the drafts are relative to; want %s", answer.outputDir,
					l.other)
			}
			if body, err := os.ReadFile(filepath.Join(l.other, "demo.yaml")); err != nil || string(body) != demoEchoBody {
				t.Errorf("other/demo.yaml holds %q (%v); want %q", body, err, demoEchoBody)
			}
		})
		t.Run(row.name+" refuses a link leading out of that directory", func(t *testing.T) {
			l := layCachedImport(t)
			target := filepath.Join(l.parent, "elsewhere", "target.yaml")
			writeFixture(t, target, "left as it was\n")
			if err := os.Symlink(target, filepath.Join(l.other, "demo.yaml")); err != nil {
				t.Skipf("symbolic links are not available here: %v", err)
			}
			before := treeFingerprint(t, l.parent)
			answer := row.run(t, l)
			if answer.ok {
				t.Fatalf("the import wrote through a link leading out of %s rather than refusing it", l.other)
			}
			if !strings.Contains(answer.text, "E0320") {
				t.Errorf("the refusal does not carry the write-leads-out condition's code E0320: %s", answer.text)
			}
			if changes := fingerprintDifferences(before, treeFingerprint(t, l.parent)); len(changes) != 0 {
				t.Errorf("a refused import changed the fixture: %v", changes)
			}
		})
	}
}
