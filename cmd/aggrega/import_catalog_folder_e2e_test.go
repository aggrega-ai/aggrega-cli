// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation,
// for an importer authored in a catalog folder: the reference importer of each
// agent-flow format as a real Plugin entity with its built module beside it, and
// the JSON parser they call back to laid down the same way. `aggrega import`, the assistant's import
// tool through `aggrega mcp`, and the session's aggrega/import method through
// `aggrega serve` are each driven at the real command tree, with the real import
// seam and the plugin resolution this package supplies — nothing here points the
// seam or stands in for the resolution.
//
// Every expectation is the shared operation's own result over an identically laid
// workspace, run with this package's own resolution and handed no destination,
// so each surface is held to the one operation rather than to a rendering of it.
// The operation's own coverage of the same folder, which names the drafted files
// and the tier each module was read at, lives beside the operation.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// catalogFolderImportSources are the committed agent-flow fixtures each surface is
// driven with, keyed by the format each is recognised as.
var catalogFolderImportSources = []struct {
	format string
	path   string
}{
	{"langflow", "../../internal/compiler/importersocket/testdata/langflow-flow.json"},
	{"flowise", "../../internal/compiler/importersocket/testdata/flowise-flow.json"},
}

// catalogFolderWorkspace declares an isolated workspace whose conventional
// catalog folder holds the importers and the parser — with their modules beside
// them, or without — and whose configuration attaches each of the folder's
// importers with no settings, and returns its root and what the folder holds. A
// case calls isolateMachineConfiguration first.
//
// The attachments are what make an import of either format run the folder's
// importer: the binary's own importers advertise the same two formats, so with
// none the import would halt as ambiguous between the two. An attachment carrying
// no settings hands its importer nothing, so the importer runs exactly as one no
// attachment names would.
func catalogFolderWorkspace(t *testing.T, withModules bool) (string, importopstest.CatalogFolder) {
	t.Helper()
	root := t.TempDir()
	folder := importopstest.LayAttachedCatalogFolder(t, root,
		filepath.Join(root, workspaceconfig.ConventionalCatalogDir), withModules, nil)
	return resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}).Root(), folder
}

// readCatalogFolderSource reads one committed source fixture.
func readCatalogFolderSource(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the committed source fixture %s: %v", path, err)
	}
	return b
}

// sharedCatalogFolderImport is the expectation every surface is held to: the
// shared operation over a workspace laid out like the one under test, through the
// real seam and this package's own plugin resolution, handed no destination and
// asked to report rather than write. It refuses an expectation that would measure
// nothing — a halted import, or one that drafted nothing.
func sharedCatalogFolderImport(t *testing.T, source []byte, name string) importops.Result {
	t.Helper()
	root, _ := catalogFolderWorkspace(t, true)
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	res, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    importopstest.WriteSource(t, filepath.Join(t.TempDir(), name), string(source)),
		SourceName:    name,
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if err != nil {
		t.Fatalf("the shared import over the catalog folder was refused, so there is no expectation: %v", err)
	}
	if res.Halted() || len(res.Files) == 0 {
		t.Fatalf("the shared import over the catalog folder halted or drafted nothing, so the comparison "+
			"would measure nothing: %+v", res.Diagnostics)
	}
	return res
}

// isolateMachineConfiguration points the per-user configuration and cache
// locations at scratch directories for the rest of the test, so neither the
// command nor the shared operation it is held to reads configuration this machine
// happens to carry.
//
// The importer fixtures are built first, because the toolchain building them
// reads those same locations — its module and build caches live there — and a
// build pointed at empty ones would have to fetch what it already has. A fixture
// is built at most once per test process, so every later request for one, inside
// the isolated environment, is answered without building.
func isolateMachineConfiguration(t *testing.T) {
	t.Helper()
	importopstest.BuildImporterFixtures(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

// runImportAtTheRoot runs `aggrega --workspace-root root import <args>` through
// the real command tree and returns the exit status, the command's two writers,
// and the process error stream the root reports a failure on.
func runImportAtTheRoot(t *testing.T, root string, args ...string) (code int, stdout, stderr, process string) {
	t.Helper()
	cmd := newRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(append([]string{"--workspace-root", root, "import"}, args...))
	process = captureStderr(t, func() {
		code = executeRoot(context.Background(), cmd)
	})
	return code, out.String(), errOut.String(), process
}

// TestImportCommandRunsAnImporterFromTheCatalogFolderItsEntitySitsIn drives
// `aggrega import` over a workspace whose catalog folder holds the importers and
// their parser, under both deliveries, and reads what reached each stream and the
// disk.
//
// Under --dry-run the standard output stream is pinned whole against the shared
// result put through the reported form's own writer, and the workspace must be
// left exactly as it was. Under a write each drafted file is read back off the
// disk against the shared result's content, and what the write created is
// exactly the drafted files and the directories on the way to them.
//
// 🔴 PLANTED DEFECT: stop importPluginBytes handing the cascade the directories it
// is given (cmd/aggrega/import.go, drop the in.colocatedDirs assignment) and every
// row fails at the expectation already — the shared operation over the folder is
// refused naming a plugin no source holds — and the command is refused the same
// way.
func TestImportCommandRunsAnImporterFromTheCatalogFolderItsEntitySitsIn(t *testing.T) {
	// Not parallel, rows included: captureStderr swaps process-global os.Stderr,
	// and the environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)

	for _, src := range catalogFolderImportSources {
		for _, dryRun := range []bool{true, false} {
			delivery := "writing"
			if dryRun {
				delivery = "reporting"
			}
			t.Run(src.format+", "+delivery, func(t *testing.T) {
				body := readCatalogFolderSource(t, src.path)
				shared := sharedCatalogFolderImport(t, body, filepath.Base(src.path))
				root, _ := catalogFolderWorkspace(t, true)
				// The source sits outside the workspace, as an exported file usually
				// does, so the workspace's tree before and after says only what the
				// import did to it.
				external := filepath.Join(t.TempDir(), filepath.Base(src.path))
				if err := os.WriteFile(external, body, 0o644); err != nil {
					t.Fatalf("write the source: %v", err)
				}
				assertImportCommandDraftsTheSharedResult(t, root, external, src.format, dryRun, shared)
			})
		}
	}
}

// assertImportCommandDraftsTheSharedResult runs `aggrega import` over root for the
// source at external and holds both streams, and the workspace's tree, to the
// shared result.
func assertImportCommandDraftsTheSharedResult(t *testing.T, root, external, format string, dryRun bool,
	shared importops.Result) {
	t.Helper()
	before := treeFingerprint(t, root)
	args := []string{external}
	if dryRun {
		args = append(args, "--dry-run")
	}
	code, stdout, stderr, process := runImportAtTheRoot(t, root, args...)
	if code != 0 {
		t.Fatalf("aggrega import exited %d\nstdout:\n%s\ncommand error writer:\n%s\nprocess error stream:\n%s",
			code, stdout, stderr, process)
	}

	// The preamble names this run's own root; what follows it is whatever the
	// importer reported, which is the shared result's diagnostics, and then one line
	// per mapping decision the shared result carries. The lines themselves are held
	// to written literals beside their formatter; what this holds is that the
	// command puts every one of them on this stream, after the diagnostics.
	var diagnostics bytes.Buffer
	reportImportDiagnostics(&diagnostics, shared.Diagnostics)
	reportImportDecisions(&diagnostics, shared.Decisions)
	wantCommentary := "read as the " + format + " format, with drafted paths relative to " + root + "\n" +
		diagnostics.String()
	if stderr != wantCommentary {
		t.Errorf("the commentary stream carried %q; want %q", stderr, wantCommentary)
	}

	diffs := fingerprintDifferences(before, treeFingerprint(t, root))
	if dryRun {
		docs := make([]authoredcontent.Document, 0, len(shared.Files))
		for _, f := range shared.Files {
			docs = append(docs, authoredcontent.Document{Path: f.Path, Content: f.Content})
		}
		var want bytes.Buffer
		if err := authoredcontent.Report(&want, docs); err != nil {
			t.Fatalf("compose the reported expectation: %v", err)
		}
		if stdout != want.String() {
			t.Errorf("the standard output stream carried\n%s\nwant the shared result's drafts\n%s", stdout, want.String())
		}
		if len(diffs) != 0 {
			t.Errorf("a dry run changed the workspace:\n  %s", strings.Join(diffs, "\n  "))
		}
		return
	}

	// The expected change is put through the fingerprint's own comparison, so it
	// is named exactly as the change on disk is. The write creates each drafted
	// file and every directory on the way to one that the workspace did not hold,
	// because the fingerprint records directories as well as files. Every expected
	// entry is new, and the comparison reads only whether a new entry is present,
	// so the value one carries is a label and is never compared.
	expected := maps.Clone(before)
	var wantOut strings.Builder
	for _, f := range shared.Files {
		wantOut.WriteString("wrote " + f.Path + "\n")
		rel := filepath.FromSlash(f.Path)
		if _, held := before[rel]; held {
			t.Fatalf("the workspace held %s before the import, so the write creating it cannot be measured", rel)
		}
		expected[rel] = "drafted"
		for dir := filepath.Dir(rel); dir != "."; dir = filepath.Dir(dir) {
			if _, held := before[dir]; !held {
				expected[dir] = "on the way to a draft"
			}
		}
		got, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s is not under the workspace root: %v", f.Path, err)
		}
		if string(got) != f.Content {
			t.Errorf("%s on disk differs from the shared operation's draft:\n%s\nwant\n%s", f.Path, got, f.Content)
		}
	}
	if stdout != wantOut.String() {
		t.Errorf("the standard output stream carried %q; want %q", stdout, wantOut.String())
	}
	if wantDiffs := fingerprintDifferences(before, expected); !slices.Equal(diffs, wantDiffs) {
		t.Errorf("the write changed the workspace as %v; want exactly %v", diffs, wantDiffs)
	}
}

// TestImportCommandHaltsWhenTheImportersModuleIsNotBesideItsEntity is the case of
// an importer entity authored in a catalog folder with no module beside it.
//
// The halt is the one the reference documents for a plugin no source holds: its
// short code, and the sentence naming the reference and the sources consulted,
// read off the process error stream the root reports a failure on. Nothing is
// drafted and the workspace is left exactly as it was.
//
// The positive control is the case above, over the same folder with its modules:
// the only difference between the two is the module beside the entity.
//
// The format is named. Naming none would have the import recognise it first, by
// parsing the source through the folder's parser — whose module is not beside
// its entity either — so the halt would name the parser rather than the importer
// this case is about.
func TestImportCommandHaltsWhenTheImportersModuleIsNotBesideItsEntity(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	root, folder := catalogFolderWorkspace(t, false)
	importer := folder.ImporterFor(t, catalogFolderImportSources[0].format)
	if _, err := os.Stat(importer.Module); !os.IsNotExist(err) {
		t.Fatalf("the importer's module is beside its entity (stat: %v), so this case measures nothing", err)
	}
	source := filepath.Join(t.TempDir(), "flow.json")
	if err := os.WriteFile(source, readCatalogFolderSource(t, catalogFolderImportSources[0].path), 0o644); err != nil {
		t.Fatalf("write the source: %v", err)
	}

	before := treeFingerprint(t, root)
	code, stdout, _, process := runImportAtTheRoot(t, root, source, "--dry-run",
		"--format", catalogFolderImportSources[0].format)
	if code == 0 {
		t.Fatalf("an import whose importer module is nowhere on this machine succeeded:\n%s", stdout)
	}
	short, ok := cerrors.ShortCodeOf(pluginpath.ErrPluginArtifactNotFound())
	if !ok {
		t.Fatal("the condition for a plugin no source holds carries no short code, so the halt cannot be named")
	}
	message, ok := cerrors.MessageOf(pluginpath.ErrPluginArtifactNotFound())
	if !ok {
		t.Fatal("the condition for a plugin no source holds carries no registered sentence")
	}
	documented := fmt.Sprintf("plugin %q not resolvable through any tier (embed, colocated, local, cache): %s",
		importer.Reference, message)
	if !strings.Contains(process, "["+short+"]") {
		t.Errorf("the halt does not open with %s:\n%s", short, process)
	}
	if !strings.Contains(process, documented) {
		t.Errorf("the halt does not carry the documented sentence %q:\n%s", documented, process)
	}
	if stdout != "" {
		t.Errorf("a refused import reported drafts on standard output:\n%s", stdout)
	}
	if diffs := fingerprintDifferences(before, treeFingerprint(t, root)); len(diffs) != 0 {
		t.Errorf("a refused import changed the workspace:\n  %s", strings.Join(diffs, "\n  "))
	}
}

// TestTheAssistantImportToolRunsAnImporterFromTheCatalogFolder drives the
// assistant's import tool at `aggrega mcp`, the real entry point, over its own
// transport, against a workspace whose catalog folder holds the importers and
// their parser. The drafts are read back off the tree, beneath the served workspace's
// root where the tool writes them, against the shared operation's result.
//
// 🔴 PLANTED DEFECT: remove ResolveImportPlugin from the mcp.Config literal the
// command builds (cmd/aggrega/mcp.go) and the call is refused for want of the
// plugin resolution an importer is loaded through, and nothing is drafted.
func TestTheAssistantImportToolRunsAnImporterFromTheCatalogFolder(t *testing.T) {
	// Not parallel: the environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	body := readCatalogFolderSource(t, catalogFolderImportSources[0].path)
	shared := sharedCatalogFolderImport(t, body, "flow.json")
	root, _ := catalogFolderWorkspace(t, true)
	if err := os.WriteFile(filepath.Join(root, "flow.json"), body, 0o644); err != nil {
		t.Fatalf("seed the served workspace: %v", err)
	}

	var stdin, stdout, stderr bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/call", `{"name":"import","arguments":{"source":"flow.json"}}`))
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
	var call struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent struct {
			Format  string   `json:"format"`
			Written []string `json:"written"`
			Halted  bool     `json:"halted"`
		} `json:"structuredContent"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(answers[2].Result, &call); err != nil {
		t.Fatalf("decode the import answer: %v (body %s)", err, answers[2].Result)
	}
	if call.IsError {
		t.Fatalf("the import was refused: %+v", call.Content)
	}
	assertDraftsMatchTheSharedImport(t, root, call.StructuredContent.Written, shared)
}

// TestTheSessionImportMethodRunsAnImporterFromTheCatalogFolder drives the
// session's aggrega/import method at `aggrega serve`, the real entry point, over
// its own framing, against a workspace whose catalog folder holds the importers
// and their parser. The drafts are read back off the tree, beneath the output
// directory the request named, against the shared operation's result.
//
// 🔴 PLANTED DEFECT: remove ResolveImportPlugin from the serve.RunConfig literal
// runServe builds (cmd/aggrega/serve.go) and the request is refused because the
// daemon was started without a plugin resolver, and nothing is drafted.
func TestTheSessionImportMethodRunsAnImporterFromTheCatalogFolder(t *testing.T) {
	// Not parallel: the environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	body := readCatalogFolderSource(t, catalogFolderImportSources[0].path)
	shared := sharedCatalogFolderImport(t, body, "flow.json")
	root, _ := catalogFolderWorkspace(t, true)
	if err := os.WriteFile(filepath.Join(root, "flow.json"), body, 0o644); err != nil {
		t.Fatalf("seed the served workspace: %v", err)
	}

	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "aggrega/import",
		"params": map[string]any{"workspaceRoot": root, "source": "flow.json", "outputDir": "drafts"},
	})
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

	frame := daemonFrameAnswering(t, stdout.Bytes(), 1)
	if raw, refused := frame["error"]; refused {
		t.Fatalf("aggrega/import was refused: %s\nstderr:\n%s", raw, stderr.String())
	}
	var result struct {
		Format    string   `json:"format"`
		OutputDir string   `json:"outputDir"`
		Written   []string `json:"written"`
		Halted    bool     `json:"halted"`
	}
	if err := json.Unmarshal(frame["result"], &result); err != nil {
		t.Fatalf("decode the import answer: %v\n%s", err, frame["result"])
	}
	if result.Halted {
		t.Fatalf("the importer halted: %s", frame["result"])
	}
	if want := filepath.Join(root, "drafts"); result.OutputDir != want {
		t.Errorf("the answer names %q as the output directory, want %q", result.OutputDir, want)
	}
	assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
}

// assertDraftsMatchTheSharedImport holds what a surface wrote to the shared
// operation's result: the files it names as written are the shared drafts, in the
// importer's order, and each is on disk beneath dir carrying the shared content.
func assertDraftsMatchTheSharedImport(t *testing.T, dir string, written []string, shared importops.Result) {
	t.Helper()
	want := make([]string, 0, len(shared.Files))
	for _, f := range shared.Files {
		want = append(want, f.Path)
	}
	if !slices.Equal(written, want) {
		t.Fatalf("the surface names %v as written; the shared operation drafted %v", written, want)
	}
	for _, f := range shared.Files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatalf("%s is not beneath %s: %v", f.Path, dir, err)
		}
		if string(got) != f.Content {
			t.Errorf("%s on disk differs from the shared operation's draft:\n%s\nwant\n%s", f.Path, got, f.Content)
		}
	}
}
