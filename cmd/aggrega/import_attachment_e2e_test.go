// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation,
// for a workspace attaching two importers: two importer entities, one generated
// from the Langflow reference importer and so advertising the Langflow format,
// the other from the Flowise one — beside the JSON parser they call back to, each
// attached by a plugins.importer entry handing it a different namespace to draft
// under. `aggrega import`, the assistant's import tool through
// `aggrega mcp`, and the session's aggrega/import method through `aggrega serve`
// are each driven at the real command tree, with the real import seam and the
// plugin resolution this package supplies, for a source of each format.
//
// Each surface is held to the shared operation's own result over the same
// workspace, and that result is held to the namespace the attachment naming the
// importer that ran hands it — and to not carrying the other attachment's — so
// each importer is shown to receive its own attachment's settings and only its
// own, from each surface's own entry.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// attachedImportRows are the two imports every surface runs over the attached
// workspace: the source of each format, the file it is laid down as at the
// workspace root, and the namespace the attachment naming the importer of that
// format hands it — the other row's namespace being the one it must not carry.
var attachedImportRows = []struct {
	format, file, namespace, other string
}{
	{format: "langflow", file: "langflow-flow.json", namespace: "north", other: "south"},
	{format: "flowise", file: "flowise-flow.json", namespace: "south", other: "north"},
}

// attachedImportWorkspace lays the attached workspace down at a fresh root and
// returns it: the two importers and the parser in its catalog folder, its
// configuration attaching each importer by identity and version with the
// namespace its row names, and the committed source of each format at the root.
// A case calls isolateMachineConfiguration first.
func attachedImportWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	laid, _ := importopstest.LayImporters(t, root, filepath.Join(root, workspaceconfig.ConventionalCatalogDir),
		importopstest.Importer{Name: "import-alpha", Format: "langflow"},
		importopstest.Importer{Name: "import-beta", Format: "flowise"},
	)
	importopstest.WriteConfiguration(t, root,
		importopstest.Attachment{URI: laid[0].Identity, Settings: map[string]any{"target_namespace": "north"}},
		importopstest.Attachment{URI: laid[1].Identity, Settings: map[string]any{"target_namespace": "south"}},
	)
	for _, src := range catalogFolderImportSources {
		body := readCatalogFolderSource(t, src.path)
		if err := os.WriteFile(filepath.Join(root, filepath.Base(src.path)), body, 0o644); err != nil {
			t.Fatalf("lay the %s source down at the workspace root: %v", src.format, err)
		}
	}
	return root
}

// sharedAttachedImport is the expectation a surface is held to for one row: the
// shared operation over the attached workspace at root, through the real seam
// and this package's own plugin resolution, reporting rather than writing. It is
// itself held to the row's namespace first — every drafted component is named in
// it, the drafted Aggregate is, and the other attachment's namespace appears in
// nothing drafted — so an expectation that measured the wrong importer, or the
// right one handed the wrong settings, refuses rather than being compared with.
func sharedAttachedImport(t *testing.T, root, file, namespace, other string) importops.Result {
	t.Helper()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	source := readCatalogFolderSource(t, filepath.Join(root, file))
	res, err := importops.Import(context.Background(), rw, importops.Options{
		Source:        source,
		SourceName:    file,
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if err != nil {
		t.Fatalf("the shared import of %s was refused, so there is no expectation: %v", file, err)
	}
	if res.Halted() || len(res.Files) == 0 {
		t.Fatalf("the shared import of %s halted or drafted nothing: %+v", file, res.Diagnostics)
	}
	for _, f := range res.Files {
		if f.Path != "aggregate.yaml" && !strings.HasPrefix(f.Path, "components/"+namespace+"-") {
			t.Fatalf("the shared import of %s drafted %s, which is not named in the namespace %q its "+
				"importer's attachment hands it", file, f.Path, namespace)
		}
		if f.Path == "aggregate.yaml" && !strings.Contains(f.Content, "name: "+namespace+"/flow") {
			t.Fatalf("the shared import of %s drafted an Aggregate not named in the namespace %q:\n%s",
				file, namespace, f.Content)
		}
		if strings.Contains(f.Path, other) || strings.Contains(f.Content, other+"/") {
			t.Fatalf("the shared import of %s drafted %s carrying the namespace %q another attachment hands "+
				"another importer", file, f.Path, other)
		}
	}
	return res
}

// draftedFile is one drafted file as both protocol surfaces answer it: the path
// it is for and its content.
type draftedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// draftedFilesOf renders a result's drafted files as path-and-content pairs, in
// the importer's order.
func draftedFilesOf(res importops.Result) []draftedFile {
	files := make([]draftedFile, 0, len(res.Files))
	for _, f := range res.Files {
		files = append(files, draftedFile{Path: f.Path, Content: f.Content})
	}
	return files
}

// TestImportCommandHandsEachAttachedImporterOnlyItsOwnSettings drives
// `aggrega import --dry-run` over the attached workspace for a source of each
// format and pins the standard output stream whole against the shared result
// for that row, put through the reported form's own writer.
func TestImportCommandHandsEachAttachedImporterOnlyItsOwnSettings(t *testing.T) {
	// Not parallel, rows included: captureStderr swaps process-global os.Stderr,
	// and the environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	root := attachedImportWorkspace(t)

	for _, row := range attachedImportRows {
		t.Run(row.format, func(t *testing.T) {
			shared := sharedAttachedImport(t, root, row.file, row.namespace, row.other)
			docs := make([]authoredcontent.Document, 0, len(shared.Files))
			for _, f := range shared.Files {
				docs = append(docs, authoredcontent.Document{Path: f.Path, Content: f.Content})
			}
			var want bytes.Buffer
			if err := authoredcontent.Report(&want, docs); err != nil {
				t.Fatalf("compose the reported expectation: %v", err)
			}
			code, stdout, stderr, process := runImportAtTheRoot(t, root, filepath.Join(root, row.file), "--dry-run")
			if code != 0 {
				t.Fatalf("aggrega import exited %d\nstdout:\n%s\ncommand error writer:\n%s\nprocess error stream:\n%s",
					code, stdout, stderr, process)
			}
			if stdout != want.String() {
				t.Errorf("the standard output stream carried\n%s\nwant the shared result's drafts\n%s", stdout, want.String())
			}
		})
	}
}

// TestTheAssistantImportToolHandsEachAttachedImporterOnlyItsOwnSettings drives
// the assistant's import tool at `aggrega mcp`, the real entry point, over its
// own transport, with one dry-run call per row in one session, and holds the
// drafts each answer carries to the shared result for that row.
func TestTheAssistantImportToolHandsEachAttachedImporterOnlyItsOwnSettings(t *testing.T) {
	// Not parallel: the environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	root := attachedImportWorkspace(t)

	var stdin, stdout, stderr bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	ids := make([]int, 0, len(attachedImportRows))
	for i, row := range attachedImportRows {
		args, err := json.Marshal(map[string]any{"name": "import",
			"arguments": map[string]any{"source": row.file, "dry_run": true}})
		if err != nil {
			t.Fatalf("encode the call: %v", err)
		}
		stdin.WriteString(rpcLine(2+i, "tools/call", string(args)))
		ids = append(ids, 2+i)
	}
	cmd := newRootCmd()
	cmd.SetIn(&stdin)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"mcp", "--workspace-root", root})
	if code := executeRoot(context.Background(), cmd); code != 0 {
		t.Fatalf("aggrega mcp exited %d\nstderr:\n%s", code, stderr.String())
	}
	answers := answersByID(t, stdout.String(), append([]int{1}, ids...)...)

	for i, row := range attachedImportRows {
		t.Run(row.format, func(t *testing.T) {
			answer := answers[ids[i]]
			if answer.Error != nil {
				t.Fatalf("the import call was not dispatched: %s", answer.Error.Message)
			}
			var call struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				StructuredContent struct {
					Format string        `json:"format"`
					Drafts []draftedFile `json:"drafts"`
					Halted bool          `json:"halted"`
				} `json:"structuredContent"`
				IsError bool `json:"isError"`
			}
			if err := json.Unmarshal(answer.Result, &call); err != nil {
				t.Fatalf("decode the import answer: %v (body %s)", err, answer.Result)
			}
			if call.IsError {
				t.Fatalf("the import was refused: %+v", call.Content)
			}
			if call.StructuredContent.Format != row.format {
				t.Errorf("the source was read as the %q format, want %q", call.StructuredContent.Format, row.format)
			}
			shared := sharedAttachedImport(t, root, row.file, row.namespace, row.other)
			if want := draftedFilesOf(shared); !slices.Equal(call.StructuredContent.Drafts, want) {
				t.Errorf("the tool answered the drafts\n%+v\nwant the shared result's\n%+v", call.StructuredContent.Drafts, want)
			}
		})
	}
}

// TestTheSessionImportMethodHandsEachAttachedImporterOnlyItsOwnSettings drives
// the session's aggrega/import method at `aggrega serve`, the real entry point,
// over its own framing, with one dry-run request per row, and holds the files
// each answer carries to the shared result for that row.
func TestTheSessionImportMethodHandsEachAttachedImporterOnlyItsOwnSettings(t *testing.T) {
	// Not parallel: the environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	root := attachedImportWorkspace(t)

	for _, row := range attachedImportRows {
		t.Run(row.format, func(t *testing.T) {
			request, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "aggrega/import",
				"params": map[string]any{"workspaceRoot": root, "source": row.file, "outputDir": ".", "dryRun": true},
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
				Format string        `json:"format"`
				Files  []draftedFile `json:"files"`
				Halted bool          `json:"halted"`
			}
			if err := json.Unmarshal(frame["result"], &result); err != nil {
				t.Fatalf("decode the import answer: %v\n%s", err, frame["result"])
			}
			if result.Format != row.format {
				t.Errorf("the source was read as the %q format, want %q", result.Format, row.format)
			}
			shared := sharedAttachedImport(t, root, row.file, row.namespace, row.other)
			if want := draftedFilesOf(shared); !slices.Equal(result.Files, want) {
				t.Errorf("the method answered the files\n%+v\nwant the shared result's\n%+v", result.Files, want)
			}
		})
	}
}
