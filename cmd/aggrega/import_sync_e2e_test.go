// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// `aggrega import --sync` end to end, through the real command tree and the
// assistant's import tool, over a workspace whose catalog folder holds the
// reference importers: a drafted platform an author has edited is brought up to
// date with a changed source, the author's work stays byte for byte, and each
// refusal and exit status is read off the run.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// syncSources writes the Langflow fixture into root as it was first imported,
// and as it reads after the flow changed, and returns both paths relative to
// root. The change is one value of one node, which only the flow the platform
// carries holds.
func syncSources(t *testing.T, root string) (first, changed string) {
	t.Helper()
	body := readCatalogFolderSource(t, catalogFolderImportSources[0].path)
	const from, to = `"value": 0.2`, `"value": 0.7`
	if bytes.Count(body, []byte(from)) != 1 {
		t.Fatalf("the fixture holds %q %d times; the change needs it once", from, bytes.Count(body, []byte(from)))
	}
	// The draft with no target leaves its Release's spec.manifest empty, a value it
	// owes; the answers file beside both sources answers it, so the imports below
	// owe nothing and each run's outcome is the sync's alone. The workspace is
	// offline, so a Release owing nothing is not pulled.
	for name, content := range map[string][]byte{
		"flow.json":               body,
		"flow-changed.json":       bytes.Replace(body, []byte(from), []byte(to), 1),
		importops.AnswersFileName: []byte("release.yaml:\n  spec.manifest: drafted\n"),
	} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0o644); err != nil {
			t.Fatalf("write the source %s: %v", name, err)
		}
	}
	t.Setenv("AGGREGA_OFFLINE", "1")
	return "flow.json", "flow-changed.json"
}

// authorEditsTo applies what an author writes into the drafted Aggregate: a
// parameter of their own under a comment of their own, and a Component of their
// own at the end. It edits any draft the same way, so the same edits on a fresh
// draft are what a sync of the edited one must read as.
func authorEditsTo(t *testing.T, text string) string {
	t.Helper()
	if strings.Count(text, "\nspec:\n") != 1 {
		t.Fatalf("the draft does not open its spec once:\n%s", text)
	}
	text = strings.Replace(text, "\nspec:\n", "\nspec:\n  # mine\n  parameters:\n    region:\n      type: string\n", 1)
	return text + "    cache:\n      type: core/datastore/redis\n"
}

// sharedDraftOf is the Aggregate and the record the shared operation drafts from
// the source at rel beneath root, reported rather than written, into a directory
// of its own so the Aggregate already in root does not refuse it.
func sharedDraftOf(t *testing.T, root, rel string) (aggregate, record string) {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	res, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    filepath.Join(root, rel),
		SourceName:    rel,
		OutputDir:     t.TempDir(),
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if err != nil {
		t.Fatalf("the shared import of %s was refused: %v", rel, err)
	}
	for _, f := range res.Files {
		switch f.Path {
		case "aggregate.yaml":
			aggregate = f.Content
		case importops.RecordFileName:
			record = f.Content
		}
	}
	if aggregate == "" || record == "" {
		t.Fatalf("the shared import of %s drafted no Aggregate or no record: %v", rel, res.Files)
	}
	return aggregate, record
}

// handEditTheCarriedFlow is an author's edit inside the flow the drafted platform
// carries: the changed node's value written by hand. The value must stand in the
// Aggregate once, so the edit lands in the carried flow and nowhere else.
func handEditTheCarriedFlow(t *testing.T, text string) string {
	t.Helper()
	const from, to = `"value": 0.7`, `"value": 0.9`
	if n := strings.Count(text, from); n != 1 {
		t.Fatalf("the synced Aggregate holds %q %d times; the hand edit needs it once:\n%s", from, n, text)
	}
	return strings.Replace(text, from, to, 1)
}

// readRooted reads a file beneath root.
func readRooted(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// TestImportSyncThroughTheCommand drives the whole of --sync at the command line
// over one workspace, in order: a plain import, an author's edits, the refusal
// without --sync, the dry run's three exit statuses, the write, a second sync in
// step, a conflict and a missing record. Each step reads both the streams and the
// workspace's tree.
//
// 🔴 PLANTED DEFECT: return nil instead of the exit status in the command's
// --sync --dry-run branch (cmd/aggrega/import.go) and the drifted dry run exits 0,
// which this reddens.
func TestImportSyncThroughTheCommand(t *testing.T) {
	embeddedentities.Require(t)
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	root, _ := catalogFolderWorkspace(t, true)
	first, changed := syncSources(t, root)
	wantSynced, wantRecord := sharedDraftOf(t, root, changed)

	run := func(args ...string) (int, string, string, string) {
		t.Helper()
		return runImportAtTheRoot(t, root, append([]string{filepath.Join(root, args[0])}, args[1:]...)...)
	}
	unchanged := func(step string, before map[string]string) {
		t.Helper()
		if diffs := fingerprintDifferences(before, treeFingerprint(t, root)); len(diffs) != 0 {
			t.Errorf("%s changed the workspace:\n  %s", step, strings.Join(diffs, "\n  "))
		}
	}

	if code, _, _, process := run(first); code != 0 {
		t.Fatalf("the plain import exited %d: %s", code, process)
	}
	edited := authorEditsTo(t, readRooted(t, root, "aggregate.yaml"))
	if err := os.WriteFile(filepath.Join(root, "aggregate.yaml"), []byte(edited), 0o644); err != nil {
		t.Fatalf("write the author's edits: %v", err)
	}

	before := treeFingerprint(t, root)
	if code, _, _, process := run(changed); code != 1 || !strings.Contains(process, "--sync") {
		t.Errorf("an import over the edited Aggregate without --sync exited %d: %s", code, process)
	}
	unchanged("the refused import", before)

	var inStep bytes.Buffer
	if err := reportImportedFiles(&inStep, nil); err != nil {
		t.Fatalf("compose the empty report: %v", err)
	}
	if code, stdout, stderr, process := run(first, "--sync", "--dry-run"); code != 0 || stdout != inStep.String() ||
		strings.Contains(stderr, " in aggregate.yaml") {
		t.Errorf("a dry run in step exited %d, printed %q and drifted %q (%s)", code, stdout, stderr, process)
	}
	unchanged("the dry run in step", before)

	code, _, stderr, process := run(changed, "--sync", "--dry-run")
	if code != importDriftExitStatus {
		t.Errorf("a drifted dry run exited %d, want %d: %s", code, importDriftExitStatus, process)
	}
	if !strings.Contains(stderr, "values.flow_definition in aggregate.yaml\n") {
		t.Errorf("the drifted dry run does not name the carried flow:\n%s", stderr)
	}
	unchanged("the drifted dry run", before)

	if code, stdout, stderr, process := run(changed, "--sync"); code != 0 {
		t.Fatalf("the sync exited %d\nstdout:\n%s\nstderr:\n%s\nprocess:\n%s", code, stdout, stderr, process)
	}
	if got, want := readRooted(t, root, "aggregate.yaml"), authorEditsTo(t, wantSynced); got != want {
		t.Errorf("the synced Aggregate reads\n%s\nwant the fresh draft with the author's edits\n%s", got, want)
	}
	if got := readRooted(t, root, importops.RecordFileName); got != wantRecord {
		t.Errorf("the record after the sync is not the fresh draft's:\n%s\nwant\n%s", got, wantRecord)
	}

	before = treeFingerprint(t, root)
	if code, stdout, _, process := run(changed, "--sync"); code != 0 || stdout != "" {
		t.Errorf("a sync in step exited %d and wrote %q (%s)", code, stdout, process)
	}
	unchanged("the sync in step", before)

	// The author now edits the carried flow, which the source changes back.
	handEdited := handEditTheCarriedFlow(t, readRooted(t, root, "aggregate.yaml"))
	if err := os.WriteFile(filepath.Join(root, "aggregate.yaml"), []byte(handEdited), 0o644); err != nil {
		t.Fatalf("write the hand edit: %v", err)
	}
	before = treeFingerprint(t, root)
	for _, dryRun := range []bool{false, true} {
		args := []string{first, "--sync"}
		if dryRun {
			args = append(args, "--dry-run")
		}
		code, _, _, process := run(args...)
		if code != 1 || !strings.Contains(process, "values.flow_definition: edited since the import wrote it, and the "+
			"source changes it") {
			t.Errorf("a sync over an edited carried flow (dry run %v) exited %d: %s", dryRun, code, process)
		}
	}
	unchanged("the refused sync", before)

	if err := os.Remove(filepath.Join(root, importops.RecordFileName)); err != nil {
		t.Fatalf("remove the record: %v", err)
	}
	if code, _, _, process := run(changed, "--sync", "--dry-run"); code != 1 ||
		!strings.Contains(process, "no record of what an import wrote there lists it") {
		t.Errorf("a sync with no record exited %d: %s", code, process)
	}
}

// TestTheImportToolSyncsAsTheCommandDoes holds the assistant's import tool to
// the command: the same edits and the same changed source leave the same
// Aggregate and the same record, the answer's drift names the carried flow, a dry
// run writes nothing, and a conflict is refused naming the part.
func TestTheImportToolSyncsAsTheCommandDoes(t *testing.T) {
	embeddedentities.Require(t)
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	root, _ := catalogFolderWorkspace(t, true)
	first, changed := syncSources(t, root)
	wantSynced, wantRecord := sharedDraftOf(t, root, changed)

	if answer := callAssistantTool(t, root, "import", map[string]any{"source": first}); answer.isError {
		t.Fatalf("the plain import was refused: %s", answer.text)
	}
	edited := authorEditsTo(t, readRooted(t, root, "aggregate.yaml"))
	if err := os.WriteFile(filepath.Join(root, "aggregate.yaml"), []byte(edited), 0o644); err != nil {
		t.Fatalf("write the author's edits: %v", err)
	}

	type toolDrift struct {
		Change string   `json:"change"`
		File   string   `json:"file"`
		Key    []string `json:"key"`
	}
	driftOf := func(answer assistantToolAnswer) []toolDrift {
		t.Helper()
		var payload struct {
			Drift []toolDrift `json:"drift"`
		}
		if err := json.Unmarshal(answer.structured, &payload); err != nil {
			t.Fatalf("decode the answer: %v (body %s)", err, answer.structured)
		}
		return payload.Drift
	}
	namesTheFlow := func(drift []toolDrift) bool {
		return slices.ContainsFunc(drift, func(d toolDrift) bool {
			return d.Change == importops.DriftChanged && d.File == "aggregate.yaml" &&
				len(d.Key) > 0 && d.Key[len(d.Key)-1] == "flow_definition"
		})
	}

	before := treeFingerprint(t, root)
	answer := callAssistantTool(t, root, "import", map[string]any{"source": changed, "sync": true, "dry_run": true})
	if answer.isError || !namesTheFlow(driftOf(answer)) {
		t.Errorf("the dry run's answer does not name the carried flow as drifted: %s %s", answer.text, answer.structured)
	}
	if diffs := fingerprintDifferences(before, treeFingerprint(t, root)); len(diffs) != 0 {
		t.Errorf("the dry run changed the workspace: %v", diffs)
	}

	answer = callAssistantTool(t, root, "import", map[string]any{"source": changed, "sync": true})
	if answer.isError || !namesTheFlow(driftOf(answer)) {
		t.Fatalf("the sync was refused or named no drift: %s %s", answer.text, answer.structured)
	}
	if got, want := readRooted(t, root, "aggregate.yaml"), authorEditsTo(t, wantSynced); got != want {
		t.Errorf("the tool's synced Aggregate reads\n%s\nwant what the command's reads\n%s", got, want)
	}
	if got := readRooted(t, root, importops.RecordFileName); got != wantRecord {
		t.Errorf("the tool's record is not the fresh draft's:\n%s\nwant\n%s", got, wantRecord)
	}
	if drift := driftOf(callAssistantTool(t, root, "import",
		map[string]any{"source": changed, "sync": true, "dry_run": true})); len(drift) != 0 {
		t.Errorf("a dry run in step names drift: %+v", drift)
	}

	handEdited := handEditTheCarriedFlow(t, readRooted(t, root, "aggregate.yaml"))
	if err := os.WriteFile(filepath.Join(root, "aggregate.yaml"), []byte(handEdited), 0o644); err != nil {
		t.Fatalf("write the hand edit: %v", err)
	}
	answer = callAssistantTool(t, root, "import", map[string]any{"source": first, "sync": true})
	if !answer.isError || !strings.Contains(answer.text, "'sync'") ||
		!strings.Contains(answer.text, "values.flow_definition: edited since the import wrote it") {
		t.Errorf("a sync over an edited carried flow was not refused naming it: %s", answer.text)
	}
}
