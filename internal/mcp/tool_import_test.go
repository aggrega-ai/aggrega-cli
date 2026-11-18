// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// What stays on this surface when the import itself is the shared layer's: the
// argument decoding, the resolution of the source through this surface's one
// path helper, the destination it chooses, the delivery it selects, and the
// shape of the answer. Format detection, the socket, the guest and the
// containment of what the guest returns are covered where they live, and this
// surface's agreement with them is covered by the parity assertion beside this
// file.
//
// 🔴 That split is deliberate. A subsystem's tests follow the subsystem, which
// leaves the CALL SITE uncovered while every moved test passes.

// importDemoEchoBody is what the demo importer guest drafts: one Component whose
// name is the format it was asked for. It is read from the guest's behaviour
// rather than declared by it, which is why the format each case drives with is
// what names the file the case then looks for.
func importDemoEchoBody(format string) string {
	return "kind: Component\nmetadata:\n  name: " + format + "\n"
}

// A Langflow export, a Flowise export, an n8n workflow, a Claude Desktop MCP
// configuration, an MCP registry server description, a Compose application, a
// Dify application, a CrewAI crew written as JSON with comments, a LangGraph
// application's configuration, a Google ADK application's root agent
// configuration and an Agent Framework declarative agent, as the committed source fixtures carry them. They are read from the compiler's
// own fixtures rather than written out here: what these cases drive is
// recognition of an exported shape, and a shape invented in this file would be
// recognised by construction.
const (
	langflowFixture       = "../compiler/importersocket/testdata/langflow-flow.json"
	flowiseFixture        = "../compiler/importersocket/testdata/flowise-flow.json"
	n8nFixture            = "../importops/testdata/n8n/agent-workflow.json"
	mcpDesktopFixture     = "../importops/testdata/mcp/claude_desktop_config.json"
	mcpServerFixture      = "../importops/testdata/mcp/registry-npm.server.json"
	composeFixture        = "../importops/testdata/compose/compose-for-agents/langgraph.compose.yaml"
	difyFixture           = "../importops/testdata/dify/authored/workflow.yml"
	crewaiCrewFixture     = "../importops/testdata/crewai/jsonc/crew.jsonc"
	langgraphFixture      = "../importops/testdata/langgraph/python/langgraph.json"
	googleADKFixture      = "../importops/testdata/google-adk/tutor/root_agent.yaml"
	agentFrameworkFixture = "../importops/testdata/agent-framework/learn-agent.yaml"
)

// importWorkspaceConfig is the configuration every fixture workspace here
// carries.
//
// The cache directory is pinned inside the workspace deliberately. Without it a
// run would read whatever artifact cache this machine happens to hold, so a case
// asserting that NO importer advertises a format would be measuring the machine
// it ran on rather than the workspace under test.
const importWorkspaceConfig = "workspace: {}\noffline: true\nartifact_cache_dir: .aggrega/cache\n"

// importSourceWorkspace lays a source file down in a workspace of its own and
// returns the root and the source's workspace-relative name.
//
// The workspace carries its own configuration file so that resolution settles
// here. Without one it would walk upward until it found a configured workspace,
// reaching the checkout the suite happens to run inside — and the case would
// then draft entities into a tree nobody wrote it for.
func importSourceWorkspace(t *testing.T, rel string, content []byte) (root, relPath string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte(importWorkspaceConfig), 0o644); err != nil {
		t.Fatalf("write the workspace config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, rel), content, 0o644); err != nil {
		t.Fatalf("write the source fixture: %v", err)
	}
	return root, rel
}

// layEmptyCatalogFolder creates the workspace's conventional catalog folder at
// root, holding nothing. A workspace naming no catalog folder reads the catalog
// its default registry publishes instead, and these workspaces are offline and
// hold none, so a case over a catalog holding nothing names an empty folder.
func layEmptyCatalogFolder(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, workspaceconfig.ConventionalCatalogDir), 0o755); err != nil {
		t.Fatalf("create the empty catalog folder: %v", err)
	}
}

// readImportFixture reads one of the committed source fixtures.
func readImportFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the committed source fixture %s: %v", path, err)
	}
	return b
}

// importDemoResolver stands in for the catalog's resolution, so a run drives the
// real import socket and the real guest without a catalog on disk.
//
// It answers for ANY format, which is what lets one guest serve the rows that
// drive shape detection: the format the detection settled on is the format the
// guest is asked for, so the file it drafts is named after it and a case can
// read the detection's answer off the tree.
//
// attachedBy is the position of the plugins.importer entry it answers as
// attaching the demo importer, or -1 for none, and a position the workspace's
// list does not reach is answered as none. The import hands the importer the
// with block of that entry and of no other, so a case whose importer must read
// its entry's settings gives the position here. It is an answer the stand-in is
// given rather than one it derives: which entry attaches an importer is the
// catalog's rule, proven against a real catalog where that rule lives, and a case
// giving a position writes the entry that rule reads as attaching this importer —
// one naming it by its identity, importer/demo-importer, with a version.
type importDemoResolver struct {
	attachedBy int
}

func (r importDemoResolver) ImporterPluginForFormat(format string, attached []string) (types.Plugin, int, error) {
	at := -1
	if r.attachedBy >= 0 && r.attachedBy < len(attached) {
		at = r.attachedBy
	}
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: "demo-importer", Version: "1.0.0", PluginType: "importer"},
		Spec:     types.PluginSpec{Importer: &types.PluginImporter{Capabilities: []string{"import:" + format}}},
	}, at, nil
}

// pointImportSeamAtTheDemoGuest points the shared layer's resolver-and-loader
// seam at the built demo importer fixture for the duration of one case, with no
// plugins.importer entry attaching it.
//
// 🔴 It swaps a package-level variable, so no case using it may be made
// parallel. The swap is safe as written because the runner resumes a parallel
// test only once every sequential one has finished, and the cleanup restores it.
func pointImportSeamAtTheDemoGuest(t *testing.T) {
	t.Helper()
	pointImportSeamAtTheDemoGuestAttachedBy(t, -1)
}

// pointImportSeamAtTheDemoGuestAttachedBy is pointImportSeamAtTheDemoGuest with
// the demo importer attached by the workspace's plugins.importer entry at
// position at.
//
// The importers whose recognition signatures settle an unnamed format are the
// reference importers' entities, generated from their built modules, and the
// source is parsed through the binary's own parsers — so a case naming no
// format is recognised by exactly the declarations a catalog holding those
// importers carries, and then handed to the demo guest under that format.
func pointImportSeamAtTheDemoGuestAttachedBy(t *testing.T, at int) {
	t.Helper()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-go.wasm")
	importers := importopstest.ReferenceImporterEntities(t)
	parserRes, parserLoad := importopstest.EmbeddedParsers(t)
	prev := importops.BuildSeam
	t.Cleanup(func() { importops.BuildSeam = prev })
	importops.BuildSeam = func(*workspaceconfig.ResolvedWorkspace, importops.PluginBytesResolver) (importops.Seam, error) {
		return importops.Seam{
			ImporterRes: importDemoResolver{attachedBy: at},
			ImporterLoad: func(p types.Plugin) (wasm.PluginSpec, error) {
				return wasm.PluginSpec{Name: p.Coordinate(), Path: fixture}, nil
			},
			ParserRes:  parserRes,
			ParserLoad: parserLoad,
			Importers:  importers,
		}, nil
	}
}

// importToolResolver is the plugin-bytes seam a session under test carries.
//
// It resolves nothing, and nothing here asks it to: which importer runs is the
// catalog's decision and its bytes come from the loader above, so this value's
// whole job is to be non-nil — which is exactly what a session wired by the
// command layer has and a session that was not does not.
func importToolResolver(*workspaceconfig.ResolvedWorkspace, string, map[string]pluginpath.ColocatedDir) (pluginpath.ResolvedPlugin, error) {
	return pluginpath.ResolvedPlugin{}, errors.New("this fixture resolves no importer module of its own")
}

// importToolServer builds a session over the given workspace root, reached over
// the process's own streams, carrying the plugin-bytes seam the command layer
// supplies in production.
func importToolServer(t *testing.T, root string) *server {
	t.Helper()
	return &server{
		out: io.Discard, logw: io.Discard, serverVersion: "test",
		launchRoot:          root,
		resolveImportPlugin: importToolResolver,
	}
}

// importToolPayload decodes the structured answer, failing where the tool
// reported a failure instead.
func importToolPayload(t *testing.T, res *callToolResult) importPayload {
	t.Helper()
	if res.IsError {
		t.Fatalf("the import was refused: %s", res.Content[0].Text)
	}
	var p importPayload
	if err := json.Unmarshal(res.StructuredContent, &p); err != nil {
		t.Fatalf("decode the structured answer: %v (body %s)", err, res.StructuredContent)
	}
	return p
}

// treeUnder lists every file under root by its root-relative path, sorted, so a
// case asserts what a caller would find rather than what an answer claimed.
func treeUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// TestTheImportToolDraftsIntoTheWorkspaceTheCallResolved is the destination
// proof at this surface, and it is asserted against the tree rather than against
// the answer.
//
// A reported path and a written path are different facts: a surface that named
// the right paths and wrote them somewhere else would satisfy an assertion on
// the answer alone, and the file a caller then goes looking for would not be
// there. So the file is read back off disk, under the root the call resolved,
// and the answer is checked for agreeing with it rather than the other way
// round.
//
// It also drives shape detection, because the format is not named: the guest
// drafts a file named after the format it was asked for, so the file on disk is
// the detection's own answer.
func TestTheImportToolDraftsIntoTheWorkspaceTheCallResolved(t *testing.T) {
	pointImportSeamAtTheDemoGuest(t)
	root, rel := importSourceWorkspace(t, "flow.json", readImportFixture(t, langflowFixture))
	before := treeUnder(t, root)
	s := importToolServer(t, root)

	p := importToolPayload(t, callTool(t, s, importToolName, map[string]any{"source": rel}))

	if p.Format != "langflow" {
		t.Errorf("the source's shape was read as the %q format, want langflow — the detection did not reach "+
			"this tool", p.Format)
	}
	if !slices.Equal(p.Written, []string{"langflow.yaml", importops.RecordFileName}) {
		t.Errorf("the answer names %v as what reached disk, want exactly [langflow.yaml %s], the draft and its "+
			"record", p.Written, importops.RecordFileName)
	}
	if told(p.WorkspaceRoot) != root {
		t.Errorf("the answer names %q as the root the drafts are relative to, want the resolved workspace %q",
			told(p.WorkspaceRoot), root)
	}
	if p.Halted {
		t.Errorf("the import reported itself halted: %+v", p.Diagnostics)
	}
	if p.Drafts != nil {
		t.Errorf("a write handed the drafted content back as well, which spends a caller's budget on bytes it "+
			"asked to have written: %+v", p.Drafts)
	}

	// The tree, which is the claim. The drafted file is under the resolved root,
	// carrying the bytes the importer produced, and it is the ONLY thing the
	// import put there.
	got, err := os.ReadFile(filepath.Join(root, "langflow.yaml"))
	if err != nil {
		t.Fatalf("the drafted file is not under the workspace the call resolved: %v", err)
	}
	if string(got) != importDemoEchoBody("langflow") {
		t.Errorf("the drafted file carries %q, want %q", string(got), importDemoEchoBody("langflow"))
	}
	want := []string{importops.RecordFileName, "langflow.yaml"}
	if added := treeAdditions(before, treeUnder(t, root)); !slices.Equal(added, want) {
		t.Errorf("the import put %v into the workspace, want exactly %v", added, want)
	}
}

// TestTheImportToolsDryRunWritesNothingAndHandsBackTheDrafts pins the reporting
// delivery: every drafted file's path and content in the answer, and a tree left
// exactly as it was.
//
// Both halves are asserted. A delivery that wrote nothing and also reported
// nothing would satisfy a check on the tree alone, and one that reported the
// drafts and wrote them too would satisfy a check on the answer alone.
//
// The demo importer reports no mapping decisions, which makes this the case of an
// importer with none to report: the answer still carries decisions, as an empty
// list rather than an absent key, and the text answer carries no decision line.
func TestTheImportToolsDryRunWritesNothingAndHandsBackTheDrafts(t *testing.T) {
	pointImportSeamAtTheDemoGuest(t)
	root, rel := importSourceWorkspace(t, "flow.json", readImportFixture(t, langflowFixture))
	before := treeUnder(t, root)
	s := importToolServer(t, root)

	res := callTool(t, s, importToolName, map[string]any{"source": rel, "dry_run": true})
	p := importToolPayload(t, res)

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(res.StructuredContent, &keys); err != nil {
		t.Fatalf("decode the structured answer's keys: %v (body %s)", err, res.StructuredContent)
	}
	if got := string(keys["decisions"]); got != "[]" {
		t.Errorf("an importer reporting no decisions answered decisions as %q; want an empty list, present", got)
	}
	if text := res.Content[0].Text; strings.Contains(text, "mapped ") || strings.Contains(text, "made no choice") {
		t.Errorf("an importer reporting no decisions drew a decision line in the text answer:\n%s", text)
	}

	if len(p.Written) != 0 {
		t.Errorf("a reporting delivery named %v as written; nothing reaches disk under it", p.Written)
	}
	if len(p.Drafts) != 2 || p.Drafts[0].Path != "langflow.yaml" || p.Drafts[1].Path != importops.RecordFileName {
		t.Fatalf("the answer carries %+v, want the draft for langflow.yaml, then the record", p.Drafts)
	}
	if p.Drafts[0].Content != importDemoEchoBody("langflow") {
		t.Errorf("the reported draft carries %q, want the content a write would have put on disk, %q",
			p.Drafts[0].Content, importDemoEchoBody("langflow"))
	}
	if after := treeUnder(t, root); !slices.Equal(after, before) {
		t.Errorf("a reporting delivery changed the tree\n--- before ---\n%v\n--- after ---\n%v", before, after)
	}
}

// TestTheImportToolRefusesADraftClimbingOutOfTheWorkspaceUnderEitherDelivery
// holds a dry run to the refusal a write gives for a drafted path that climbs
// out of the directory the drafts are relative to — on this tool, the workspace
// the call resolved.
//
// The demo importer names its drafted file after the format it is asked for, so
// a format naming a climb makes the PLUGIN return the climbing path, through a
// slash or through a backslash. A dry run answering with that draft would tell
// the caller the import is fine while the write refuses it, which is what this
// tool's own dry_run description says does not happen. Each row asserts the
// refusal names the path the importer returned, and that nothing reached disk —
// in the workspace or in the directory holding it.
//
// 🔴 PLANTED DEFECT: have checkFiles (internal/importops/import.go) return nil at
// its head, leaving the judgement to the draftPlace call writeFiles makes for each
// file it writes, and both dry-run rows redden, answered with the draft instead of
// refused, while both write rows stay green.
func TestTheImportToolRefusesADraftClimbingOutOfTheWorkspaceUnderEitherDelivery(t *testing.T) {
	// Not parallel: points the shared import seam.
	pointImportSeamAtTheDemoGuest(t)

	for _, climb := range []struct {
		name    string
		format  string
		drafted string
	}{
		{"climbing through a slash", "../escaped", "../escaped.yaml"},
		{"climbing through a backslash", `..\escaped`, `..\escaped.yaml`},
	} {
		for _, delivery := range []struct {
			name   string
			dryRun bool
		}{
			{"a write", false},
			{"a dry run", true},
		} {
			t.Run(climb.name+", "+delivery.name, func(t *testing.T) {
				root, rel := importSourceWorkspace(t, "flow.json", readImportFixture(t, langflowFixture))
				// The directory holding the workspace is this case's own, so the tree
				// beneath it covers both the workspace and where a climb out of it
				// lands.
				above := filepath.Dir(root)
				before := treeUnder(t, above)

				res := callTool(t, importToolServer(t, root), importToolName, map[string]any{
					"source": rel, "format": climb.format, "dry_run": delivery.dryRun,
				})
				if !res.IsError {
					t.Fatalf("a drafted path climbing out of the workspace was answered rather than refused: %s",
						res.Content[0].Text)
				}
				text := res.Content[0].Text
				if !strings.Contains(text, strconv.Quote(climb.drafted)) {
					t.Errorf("the refusal does not name the path the importer returned, %s: %q",
						strconv.Quote(climb.drafted), text)
				}
				if after := treeUnder(t, above); !slices.Equal(after, before) {
					t.Errorf("a refused import changed the tree\n--- before ---\n%v\n--- after ---\n%v", before, after)
				}
			})
		}
	}
}

// TestTheImportToolRefusesASourceWhoseShapeItDoesNotRecognise is the other half
// of detection: a source that matches no recognised shape is refused asking for
// the format rather than handed to a guessed importer.
//
// The refusal is required to name the argument a caller here can actually send.
// The shipped sentence names the flag a reader at a terminal passes, which is a
// remedy no caller of this surface has, so a refusal repeating it would send the
// caller after something that does not exist.
func TestTheImportToolRefusesASourceWhoseShapeItDoesNotRecognise(t *testing.T) {
	pointImportSeamAtTheDemoGuest(t)
	root, rel := importSourceWorkspace(t, "mystery.json", []byte(`{"foo":"bar"}`))
	before := treeUnder(t, root)
	s := importToolServer(t, root)

	res := callTool(t, s, importToolName, map[string]any{"source": rel})
	if !res.IsError {
		t.Fatalf("a source matching no recognised shape was imported rather than refused: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "'format'") {
		t.Errorf("the refusal does not name the argument that would settle it: %q", text)
	}
	if !strings.Contains(text, rel) {
		t.Errorf("the refusal does not name the source it could not classify: %q", text)
	}
	if strings.Contains(text, "--format") {
		t.Errorf("the refusal names a command-line flag, which is a remedy this caller does not have: %q", text)
	}
	if after := treeUnder(t, root); !slices.Equal(after, before) {
		t.Errorf("a refused import wrote to the workspace\n--- before ---\n%v\n--- after ---\n%v", before, after)
	}

	// The control: the same tool over a source whose shape IS recognised is
	// answered, so the refusal above is attributable to the shape rather than to
	// anything else about the call.
	okRoot, okRel := importSourceWorkspace(t, "flow.json", readImportFixture(t, flowiseFixture))
	p := importToolPayload(t, callTool(t, importToolServer(t, okRoot), importToolName,
		map[string]any{"source": okRel}))
	if p.Format != "flowise" {
		t.Errorf("the control source was read as the %q format, want flowise", p.Format)
	}
}

// TestTheImportToolOffersADirectoryNoImporterRecognisesWhatCanHelpIt drives the
// tool, through the shared layer's own resolution over the compiler's core
// importers, at a directory holding only a Langflow export. The Langflow
// importer's signature names no file, so it applies to none of a directory's
// files, and naming its format would refuse the directory again: the refusal
// offers sending one of the directory's files on its own, or a format whose
// importer reads a directory, and never names langflow. Which formats those are is
// held to the importers' declarations where the refusal is built; here the case
// holds that this surface reaches the directory's wording at all. The workspace's
// catalog folder holds nothing, so the importers are the core importers alone.
func TestTheImportToolOffersADirectoryNoImporterRecognisesWhatCanHelpIt(t *testing.T) {
	embeddedentities.Require(t)
	root, _ := importSourceWorkspace(t, "notes.txt", []byte("notes\n"))
	layEmptyCatalogFolder(t, root)
	dir := filepath.Join(root, "flows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create the directory source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.json"), readImportFixture(t, langflowFixture), 0o644); err != nil {
		t.Fatalf("seed the directory source: %v", err)
	}
	before := treeUnder(t, root)

	res := callTool(t, importToolServer(t, root), importToolName, map[string]any{"source": "flows"})
	if !res.IsError {
		t.Fatalf("a directory no importer recognises was imported rather than refused: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	for _, want := range []string{
		"E0350: " + importToolName + " did not recognise the source format of the directory flows",
		"send one of its files on its own as 'source', or send 'format' naming a format whose importer reads a " +
			"directory (one of ",
		`"langgraph"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal %q does not carry %q", text, want)
		}
	}
	if strings.Contains(text, `"langflow"`) || strings.Contains(text, "--format") {
		t.Errorf("the refusal offers a format that cannot read a directory, or the terminal's flag: %q", text)
	}
	if after := treeUnder(t, root); !slices.Equal(after, before) {
		t.Errorf("a refused import wrote to the workspace\n--- before ---\n%v\n--- after ---\n%v", before, after)
	}
}

// TestTheImportToolRewordsAnUnsettledFormatByItsType pins this surface's two
// re-wordings of a format the shared layer could not settle. Each names the
// argument a caller here can send and never the terminal's flag, names the
// source, and names the formats the refusal itself carries — the ones the
// catalog's importers advertise, or the ones the source was recognised as — and
// each opens with its condition's short code. Any other error is left to the
// caller.
//
// A directory no importer recognises is offered one of its files on its own, or
// only the formats whose importers read a directory — never a format that would
// refuse the same directory again — while a file keeps naming every format.
//
// 🔴 PLANTED DEFECT: in importFormatRefusal (tool_import.go), write
// `if true || !undetected.IsDirectory() {`, so a directory is answered in the
// file's words: both directory rows redden.
func TestTheImportToolRewordsAnUnsettledFormatByItsType(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		err    error
		want   []string
		absent []string
	}{
		{"no importer recognises the source",
			&importops.UndetectedFormatError{Source: "mystery.json", Advertised: []string{"langflow", "flowise"},
				DirectoryAdvertised: []string{"langgraph"}},
			[]string{"E0350: ", "mystery.json", "'format'", `one of "langflow", "flowise"`},
			[]string{"one of its files", "directory"}},
		{"no importer advertises a format",
			&importops.UndetectedFormatError{Source: "mystery.json"},
			[]string{"E0350: ", "mystery.json", "'format'", "no importer in this workspace's catalog advertises a format to name"},
			nil},
		{"no importer recognises a directory",
			&importops.UndetectedFormatError{Source: "flows", Directory: true,
				Advertised: []string{"langflow", "langgraph", "mcp"}, DirectoryAdvertised: []string{"langgraph", "mcp"}},
			[]string{"E0350: " + importToolName + " did not recognise the source format of the directory flows from the " +
				"signatures the importers declare, and a directory is read only through the file patterns its " +
				"importer declares: send one of its files on its own as 'source', or send 'format' naming a format " +
				`whose importer reads a directory (one of "langgraph", "mcp"), and call again. Nothing was written.`},
			[]string{`"langflow"`}},
		{"no importer that reads a directory",
			&importops.UndetectedFormatError{Source: "flows", Directory: true, Advertised: []string{"langflow"}},
			[]string{"the directory flows", "send one of its files on its own as 'source' and call again, since no " +
				"importer in this workspace's catalog declares file patterns. Nothing was written."},
			[]string{`"langflow"`, "send 'format'"}},
		{"importers of two formats recognise the source",
			&importops.AmbiguousFormatError{Source: "flow.json", Matched: []string{"langflow", "nested-flow"}},
			[]string{"E0339: ", "flow.json", "'format'", `"langflow", "nested-flow"`},
			nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, unsettled := importFormatRefusal(tc.err)
			if !unsettled {
				t.Fatalf("the refusal was not recognised by its type: %v", tc.err)
			}
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("the refusal %q does not carry %q", text, want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(text, absent) {
					t.Errorf("the refusal %q carries %q", text, absent)
				}
			}
			if strings.Contains(text, "--format") {
				t.Errorf("the refusal names a command-line flag, which is a remedy this caller does not have: %q", text)
			}
		})
	}
	if _, unsettled := importFormatRefusal(errors.New("something else")); unsettled {
		t.Error("an error that is not an unsettled format was taken for one")
	}
}

// unadvertisedFormat is a format no importer advertises: not one the workspace's
// catalog holds an importer for, and not one the binary's own importers serve.
const unadvertisedFormat = "unadvertised"

// TestTheImportToolRefusesWhenNoImporterAdvertisesTheFormat is the
// nothing-attached case, and it is driven with the shared layer's own resolution
// rather than with the seam the cases above point.
//
// The format is named, and it is one no importer advertises. The binary's own
// importers serve the Langflow, Flowise and n8n formats whatever the workspace's
// catalog holds, so naming any of them would resolve one of them rather than reach
// this refusal; and naming a format is what takes the case past recognition, to
// where the format is settled and no importer serves it.
//
// The refusal is the shipped one: the condition the catalog registers for a
// format no plugin advertises, carrying the capability an importer would have to
// advertise — which is what a reader has to attach. Composing a sentence here
// instead would be this surface inventing an account of a resolution it does not
// perform. The workspace's catalog folder holds nothing, so no importer of its
// catalog advertises the format either.
func TestTheImportToolRefusesWhenNoImporterAdvertisesTheFormat(t *testing.T) {
	root, rel := importSourceWorkspace(t, "flow.json", readImportFixture(t, langflowFixture))
	layEmptyCatalogFolder(t, root)
	before := treeUnder(t, root)
	s := importToolServer(t, root)

	res := callTool(t, s, importToolName, map[string]any{"source": rel, "format": unadvertisedFormat})
	if !res.IsError {
		t.Fatalf("a workspace attaching no importer imported anyway: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "import:"+unadvertisedFormat) {
		t.Errorf("the refusal does not name the capability an importer would have to advertise, which is what "+
			"the reader has to attach: %q", text)
	}
	if !strings.Contains(text, "E0097") {
		t.Errorf("the refusal carries no short code, so a caller cannot tell an unattached importer from any "+
			"other failure to import: %q", text)
	}
	if after := treeUnder(t, root); !slices.Equal(after, before) {
		t.Errorf("a refused import wrote to the workspace\n--- before ---\n%v\n--- after ---\n%v", before, after)
	}
}

// TestTheImportToolRefusesWhenTheSessionCarriesNoPluginResolution pins the
// wiring refusal.
//
// A session built without the seam the command layer supplies can reach no
// importer at all, whatever the workspace attaches, and it says so rather than
// reporting a conversion that did not happen — which is the silent shape a nil
// seam would otherwise produce.
func TestTheImportToolRefusesWhenTheSessionCarriesNoPluginResolution(t *testing.T) {
	pointImportSeamAtTheDemoGuest(t)
	root, rel := importSourceWorkspace(t, "flow.json", readImportFixture(t, langflowFixture))
	before := treeUnder(t, root)

	s := importToolServer(t, root)
	s.resolveImportPlugin = nil

	res := callTool(t, s, importToolName, map[string]any{"source": rel})
	if !res.IsError {
		t.Fatalf("a session carrying no plugin resolution imported anyway: %s", res.Content[0].Text)
	}
	if after := treeUnder(t, root); !slices.Equal(after, before) {
		t.Errorf("a refused import wrote to the workspace\n--- before ---\n%v\n--- after ---\n%v", before, after)
	}

	// The control: the same call on a session that carries it is answered, so
	// the refusal above measures the seam rather than the fixture.
	okRoot, okRel := importSourceWorkspace(t, "flow.json", readImportFixture(t, langflowFixture))
	importToolPayload(t, callTool(t, importToolServer(t, okRoot), importToolName,
		map[string]any{"source": okRel}))
}

// TestTheImportToolTakesOneOptionalDestination is the destination rule this tool
// shares with the command's --output-dir and the editor session's outputDir,
// asserted over the process's own streams at the surface a caller reads.
//
// It is asserted at the declared schema and at the tree, because either alone
// would pass over a defect. The schema is what a caller composes a call from, so
// it offers the one argument and no second spelling of it; the tree is what a
// caller inspects afterwards, so each row reads where the drafts landed rather
// than what the answer claimed. On this transport a directory is taken as named:
// a relative one against the workspace root, and an absolute one outside the
// workspace drafted into, as the command drafts into its --output-dir.
//
// 🔴 PLANTED DEFECTS:
//   - drop OutputDir from the importops.Options literal in importInvoke
//     (internal/mcp/tool_import.go): the two rows naming a directory redden,
//     their drafts landing at the workspace root.
//   - resolve a blank output_dir rather than refusing it (remove the blank
//     check in importInvoke): the blank row reddens, drafting at the root.
func TestTheImportToolTakesOneOptionalDestination(t *testing.T) {
	spec, found := lookupTool(importToolName)
	if !found {
		t.Fatalf("no tool is registered as %q, so this assertion measures nothing", importToolName)
	}
	var declared struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(spec.inputSchema), &declared); err != nil {
		t.Fatalf("the declared schema does not parse: %v", err)
	}
	if _, offered := declared.Properties["output_dir"]; !offered {
		t.Errorf("the declared schema does not offer output_dir, so a caller cannot compose a call naming where " +
			"the drafts go")
	}
	for _, name := range []string{"output", "outputDir", "destination", "dir", "directory", "target_dir"} {
		if _, offered := declared.Properties[name]; offered {
			t.Errorf("the declared schema offers %q beside output_dir, a second spelling of one destination", name)
		}
	}

	pointImportSeamAtTheDemoGuest(t)
	for _, row := range []struct {
		name string
		// outputDir returns the value sent, given the root and a directory outside
		// the workspace; nil sends none.
		outputDir func(root, outside string) *string
		// wantDir returns the directory the drafts must land beneath, or "" where
		// the call must be refused with nothing written.
		wantDir func(root, outside string) string
	}{
		{
			name:      "naming no destination drafts beneath the workspace root",
			outputDir: func(string, string) *string { return nil },
			wantDir:   func(root, _ string) string { return root },
		},
		{
			name:      "a relative destination resolves against the workspace root",
			outputDir: func(string, string) *string { s := "drafts"; return &s },
			wantDir:   func(root, _ string) string { return filepath.Join(root, "drafts") },
		},
		{
			name:      "an absolute destination outside the workspace is drafted into",
			outputDir: func(_, outside string) *string { return &outside },
			wantDir:   func(_, outside string) string { return outside },
		},
		{
			name:      "a blank destination is refused rather than read as none",
			outputDir: func(string, string) *string { s := " "; return &s },
			wantDir:   func(string, string) string { return "" },
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			root, rel := importSourceWorkspace(t, "flow.json", readImportFixture(t, langflowFixture))
			outside := t.TempDir()
			rootBefore, outsideBefore := treeUnder(t, root), treeUnder(t, outside)

			args := map[string]any{"source": rel}
			if sent := row.outputDir(root, outside); sent != nil {
				args["output_dir"] = *sent
			}
			res := callTool(t, importToolServer(t, root), importToolName, args)

			want := row.wantDir(root, outside)
			if want == "" {
				if !res.IsError {
					t.Fatalf("a blank output_dir was answered rather than refused: %s", res.Content[0].Text)
				}
				if !strings.Contains(res.Content[0].Text, "'output_dir'") {
					t.Errorf("the refusal does not name the argument to correct: %q", res.Content[0].Text)
				}
				if !slices.Equal(treeUnder(t, root), rootBefore) || !slices.Equal(treeUnder(t, outside), outsideBefore) {
					t.Errorf("a refused call wrote a file")
				}
				return
			}
			p := importToolPayload(t, res)
			if told(p.OutputDir) != want {
				t.Errorf("the answer names output_dir %q; want %q", told(p.OutputDir), want)
			}
			if told(p.WorkspaceRoot) != root {
				t.Errorf("the answer names workspace_root %q; want the resolved workspace %q", told(p.WorkspaceRoot), root)
			}
			body, err := os.ReadFile(filepath.Join(want, "langflow.yaml"))
			if err != nil {
				t.Fatalf("the draft is not beneath %s: %v", want, err)
			}
			if string(body) != importDemoEchoBody("langflow") {
				t.Errorf("the draft carries %q, want %q", body, importDemoEchoBody("langflow"))
			}
			if want != outside && !slices.Equal(treeUnder(t, outside), outsideBefore) {
				t.Errorf("a call drafting beneath %s wrote outside the workspace", want)
			}
		})
	}
}

// TestTheImportToolReportsTheDirectoryItsDraftsAreRelativeTo pins the one fact
// the shared operation reports about where an import went — the directory every
// drafted path is relative to — on both transports and under both deliveries.
//
// The answer is read by its key, into a shape of this test's own rather than into
// the payload type, because the key is what a caller reads: an answer that
// stopped carrying it fails here whatever the Go type beside it declares. The key
// is then held against the tree rather than taken on trust. Under a write, every
// path the answer names as written is a file under that directory carrying what
// the importer drafted, and those are the only files the import added. Under a
// dry run nothing reaches disk and the directory is still reported, because each
// path handed back is still relative to it.
//
// Over the process's own streams the value is also required to equal the
// workspace root the same answer names. That is this tool's destination rule read
// off its own answer — a call naming no output_dir drafts into the workspace the
// call resolved. Over a
// listener both are that root, a directory on the host such a caller is not told
// about, so both are required to be absent and marked as kept back; the written
// paths are then held against the served root, which is the coordinate that
// caller's own paths are named in.
func TestTheImportToolReportsTheDirectoryItsDraftsAreRelativeTo(t *testing.T) {
	// Not parallel: points the shared import seam, a package-level variable.
	pointImportSeamAtTheDemoGuest(t)

	// importSession is one workspace seeded with the source, and the way a call
	// reaches the tool serving it.
	type importSession struct {
		root string
		call func(t *testing.T, args map[string]any) callToolResult
	}
	transports := []struct {
		name string
		open func(t *testing.T) importSession
		// keepsTheRootBack is whether this transport's caller is told where the
		// served workspace sits on the host.
		keepsTheRootBack bool
	}{
		{"over the process's own streams", func(t *testing.T) importSession {
			root, _ := importSourceWorkspace(t, "flow.json", readImportFixture(t, langflowFixture))
			s := importToolServer(t, root)
			return importSession{root: root, call: func(t *testing.T, args map[string]any) callToolResult {
				return *callTool(t, s, importToolName, args)
			}}
		}, false},
		{"over a listener", func(t *testing.T) importSession {
			f := newScopeGridFixture(t, false)
			importOverTheListener(t, f, "flow.json", readImportFixture(t, langflowFixture))
			credential := f.grantOf(t, scopeWorkspaceWrite)
			return importSession{root: f.root, call: func(t *testing.T, args map[string]any) callToolResult {
				return toolResultOf(t, importToolName, f.callTool(t, credential, importToolName, args))
			}}
		}, true},
	}
	deliveries := []struct {
		name   string
		dryRun bool
	}{
		{"a write", false},
		{"a dry run", true},
	}

	for _, transport := range transports {
		for _, delivery := range deliveries {
			t.Run(transport.name+", "+delivery.name, func(t *testing.T) {
				session := transport.open(t)
				before := treeUnder(t, session.root)

				res := session.call(t, map[string]any{"source": "flow.json", "dry_run": delivery.dryRun})
				if res.IsError {
					t.Fatalf("the import was refused: %s", res.Content[0].Text)
				}
				var answer struct {
					WorkspaceRoot         *string  `json:"workspace_root"`
					WorkspaceRootWithheld bool     `json:"workspace_root_withheld"`
					OutputDir             *string  `json:"output_dir"`
					OutputDirWithheld     bool     `json:"output_dir_withheld"`
					Written               []string `json:"written"`
					Drafts                []struct {
						Path    string `json:"path"`
						Content string `json:"content"`
					} `json:"drafts"`
				}
				if err := json.Unmarshal(res.StructuredContent, &answer); err != nil {
					t.Fatalf("decode the structured answer: %v (body %s)", err, res.StructuredContent)
				}

				// The directory the written paths below are resolved against: the one
				// the answer names where it names one, and the served root — the
				// coordinate the caller's own paths are in — where it keeps it back.
				outputDir := session.root
				if transport.keepsTheRootBack {
					if answer.OutputDir != nil || !answer.OutputDirWithheld {
						t.Errorf("over a listener the answer names output_dir %q (withheld=%v); it is the served "+
							"root, a directory on the host, and is kept back and marked: %s",
							told(answer.OutputDir), answer.OutputDirWithheld, res.StructuredContent)
					}
					if answer.WorkspaceRoot != nil || !answer.WorkspaceRootWithheld {
						t.Errorf("over a listener the answer names workspace_root %q (withheld=%v); it is kept "+
							"back and marked: %s", told(answer.WorkspaceRoot), answer.WorkspaceRootWithheld,
							res.StructuredContent)
					}
				} else {
					if answer.OutputDir == nil {
						t.Fatalf("the answer carries no output_dir, so a caller is not told the directory its drafted "+
							"paths are relative to, which the shared operation reports to every other surface: %s",
							res.StructuredContent)
					}
					outputDir = *answer.OutputDir
					if outputDir != session.root {
						t.Errorf("the answer names %q as the directory the drafts are relative to, want the resolved "+
							"workspace %q", outputDir, session.root)
					}
					if outputDir != told(answer.WorkspaceRoot) {
						t.Errorf("output_dir %q differs from the workspace_root %q the same answer names; a call "+
							"naming no output_dir drafts into the workspace the call resolved", outputDir,
							told(answer.WorkspaceRoot))
					}
					if answer.OutputDirWithheld || answer.WorkspaceRootWithheld {
						t.Errorf("over the process's own streams the answer marks a location as kept back: %s",
							res.StructuredContent)
					}
				}

				added := treeAdditions(before, treeUnder(t, session.root))
				if delivery.dryRun {
					if len(added) != 0 {
						t.Errorf("a dry run put %v into the workspace", added)
					}
					if len(answer.Drafts) != 2 || answer.Drafts[0].Path != "langflow.yaml" ||
						answer.Drafts[1].Path != importops.RecordFileName {
						t.Fatalf("the dry run handed back %+v, want the draft for langflow.yaml, then the record",
							answer.Drafts)
					}
					draft := filepath.Join(outputDir, filepath.FromSlash(answer.Drafts[0].Path))
					if _, err := os.Stat(draft); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("a dry run's draft exists under the directory the answer names: %v", err)
					}
					return
				}

				// The tree, which is the claim. Each written path, resolved against the
				// directory the answer names, is a file carrying the drafted bytes, and
				// the written set is exactly what the import added under the root.
				if !slices.Equal(answer.Written, []string{"langflow.yaml", importops.RecordFileName}) {
					t.Fatalf("the answer names %v as written, want exactly [langflow.yaml %s]", answer.Written,
						importops.RecordFileName)
				}
				for _, name := range answer.Written {
					got, err := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(name)))
					if err != nil {
						t.Fatalf("%s is not under the directory the answer names, %q: %v", name, outputDir, err)
					}
					if name != importops.RecordFileName && string(got) != importDemoEchoBody("langflow") {
						t.Errorf("%s under %q carries %q, want %q", name, outputDir, string(got),
							importDemoEchoBody("langflow"))
					}
				}
				if !slices.Equal(added, slices.Sorted(slices.Values(answer.Written))) {
					t.Errorf("the import added %v under the workspace and the answer names %v as written",
						added, answer.Written)
				}
			})
		}
	}
}

// treeAdditions names the paths present in after and absent from before, so a
// case states what an import PUT somewhere rather than restating the whole tree
// — which would pin the fixture's own layout and would then fail for a reason
// that has nothing to do with the import.
func treeAdditions(before, after []string) []string {
	had := make(map[string]bool, len(before))
	for _, p := range before {
		had[p] = true
	}
	var added []string
	for _, p := range after {
		if !had[p] {
			added = append(added, p)
		}
	}
	return added
}
