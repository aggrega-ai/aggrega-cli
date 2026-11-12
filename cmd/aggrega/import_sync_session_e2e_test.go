// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// The editor session's aggrega/import under sync, end to end through the real
// command tree, held to the command and to the assistant's import tool: the same
// edited draft and the same changed source leave the same Aggregate and the same
// record on disk whichever surface syncs it, and the session's drift is the
// tool's, entry for entry.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
)

// sessionSyncAnswer is the part of the session's import answer these cases
// read, or the error it was refused with.
type sessionSyncAnswer struct {
	result struct {
		Written []string `json:"written"`
		Files   []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
		Drift  []importDriftEntry `json:"drift"`
		Halted bool               `json:"halted"`
	}
	// raw is the result exactly as the session wrote it.
	raw     json.RawMessage
	refused bool
	code    int
	message string
}

// importDriftEntry is one drift entry as the session and the assistant's tool
// both answer it.
type importDriftEntry struct {
	Change string   `json:"change"`
	File   string   `json:"file"`
	Key    []string `json:"key"`
}

// callSessionImport sends the session one aggrega/import over root, drafting into
// the workspace root itself, and reads its answer.
func callSessionImport(t *testing.T, root string, params map[string]any) sessionSyncAnswer {
	t.Helper()
	sent := map[string]any{"outputDir": "."}
	for k, v := range params {
		sent[k] = v
	}
	frame, stderr := callSession(t, root, "aggrega/import", sent)
	var answer sessionSyncAnswer
	if raw, refused := frame["error"]; refused {
		var failure struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &failure); err != nil {
			t.Fatalf("decode the refusal: %v\n%s", err, raw)
		}
		answer.refused, answer.code, answer.message = true, failure.Code, failure.Message
		return answer
	}
	answer.raw = frame["result"]
	if err := json.Unmarshal(answer.raw, &answer.result); err != nil {
		t.Fatalf("decode the import answer: %v\n%s\nstderr:\n%s", err, answer.raw, stderr)
	}
	return answer
}

// draftEditedBySync lays a workspace the way every surface below is driven
// over: the fixture's sources, a plain import of the first through the command,
// and an author's edits on the drafted Aggregate. It returns the root and both
// sources.
func draftEditedBySync(t *testing.T) (root, first, changed string) {
	t.Helper()
	root, _ = catalogFolderWorkspace(t, true)
	first, changed = syncSources(t, root)
	if code, _, _, process := runImportAtTheRoot(t, root, filepath.Join(root, first)); code != 0 {
		t.Fatalf("the plain import exited %d: %s", code, process)
	}
	edited := authorEditsTo(t, readRooted(t, root, "aggregate.yaml"))
	if err := os.WriteFile(filepath.Join(root, "aggregate.yaml"), []byte(edited), 0o644); err != nil {
		t.Fatalf("write the author's edits: %v", err)
	}
	return root, first, changed
}

// TestTheSessionSyncsAsTheCommandDoes drives sync through the editor session over
// one workspace and holds every step to the command and the tool: the refusal
// without sync names the field; the dry run writes nothing, hands back the
// Aggregate the sync would write — the shared draft with the author's edits —
// and answers the drift the assistant's tool answers for the same call, byte
// for byte on a second run; the sync writes the same Aggregate and the same
// record the command writes in a workspace of its own; a second sync is in step;
// a conflict and a missing record are refused in the session's words.
//
// 🔴 PLANTED DEFECTS:
//   - drop Sync from the importops.Options literal in dispatchImport
//     (internal/serve/import.go): every sync request is refused as an import
//     over an existing Aggregate, and the dry-run step reddens.
//   - drop Drift from the importResult literal in dispatchImport: the answer
//     carries "drift":null, the drift equals no tool's, and the dry-run step
//     reddens.
//   - remove the SyncConflictError branch in dispatchImport: the conflict is
//     refused in the operation's own words, which name --sync, and the conflict
//     step reddens.
func TestTheSessionSyncsAsTheCommandDoes(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	root, first, changed := draftEditedBySync(t)
	wantSynced, wantRecord := sharedDraftOf(t, root, changed)

	unchanged := func(step string, before map[string]string) {
		t.Helper()
		if diffs := fingerprintDifferences(before, treeFingerprint(t, root)); len(diffs) != 0 {
			t.Errorf("%s changed the workspace:\n  %s", step, strings.Join(diffs, "\n  "))
		}
	}

	before := treeFingerprint(t, root)
	refused := callSessionImport(t, root, map[string]any{"source": changed})
	if !refused.refused || !strings.Contains(refused.message, "send sync") || strings.Contains(refused.message, "--sync") {
		t.Errorf("an import over the edited Aggregate without sync was not refused naming the field: %+v", refused)
	}
	unchanged("the refused import", before)

	// The dry run: nothing written, the content the sync would write, and the
	// tool's drift for the same call.
	dryRun := callSessionImport(t, root, map[string]any{"source": changed, "sync": true, "dryRun": true})
	if dryRun.refused {
		t.Fatalf("the session's sync dry run was refused: %s", dryRun.message)
	}
	unchanged("the session's sync dry run", before)
	if len(dryRun.result.Written) != 0 {
		t.Errorf("a dry run names written files: %v", dryRun.result.Written)
	}
	handedBack := map[string]string{}
	for _, f := range dryRun.result.Files {
		handedBack[f.Path] = f.Content
	}
	if got, want := handedBack["aggregate.yaml"], authorEditsTo(t, wantSynced); got != want {
		t.Errorf("the dry run hands back the Aggregate\n%s\nwant the shared draft with the author's edits\n%s", got, want)
	}
	if got := handedBack[importops.RecordFileName]; got != wantRecord {
		t.Errorf("the dry run hands back the record\n%s\nwant the shared draft's\n%s", got, wantRecord)
	}
	if !namesTheCarriedFlow(dryRun.result.Drift) {
		t.Errorf("the dry run's drift does not name the carried flow: %+v", dryRun.result.Drift)
	}
	var tool struct {
		Drift []importDriftEntry `json:"drift"`
	}
	toolAnswer := callAssistantTool(t, root, "import", map[string]any{"source": changed, "sync": true, "dry_run": true})
	if toolAnswer.isError {
		t.Fatalf("the tool's sync dry run was refused: %s", toolAnswer.text)
	}
	if err := json.Unmarshal(toolAnswer.structured, &tool); err != nil {
		t.Fatalf("decode the tool's answer: %v (%s)", err, toolAnswer.structured)
	}
	if !reflect.DeepEqual(dryRun.result.Drift, tool.Drift) {
		t.Errorf("the session's drift\n%+v\ndiffers from the tool's\n%+v", dryRun.result.Drift, tool.Drift)
	}
	if again := callSessionImport(t, root, map[string]any{"source": changed, "sync": true, "dryRun": true}); !bytes.Equal(again.raw, dryRun.raw) {
		t.Errorf("two dry runs answer differently:\n%s\n%s", dryRun.raw, again.raw)
	}
	unchanged("the tool's dry run and the second dry run", before)

	// The command syncs a workspace of its own, laid out the same way.
	commandRoot, _, commandChanged := draftEditedBySync(t)
	if code, _, _, process := runImportAtTheRoot(t, commandRoot, filepath.Join(commandRoot, commandChanged), "--sync"); code != 0 {
		t.Fatalf("the command's sync exited %d: %s", code, process)
	}

	synced := callSessionImport(t, root, map[string]any{"source": changed, "sync": true})
	if synced.refused {
		t.Fatalf("the session's sync was refused: %s", synced.message)
	}
	if !namesTheCarriedFlow(synced.result.Drift) || !reflect.DeepEqual(synced.result.Drift, dryRun.result.Drift) {
		t.Errorf("the sync's drift\n%+v\nis not the dry run's\n%+v", synced.result.Drift, dryRun.result.Drift)
	}
	for _, rel := range []string{"aggregate.yaml", importops.RecordFileName} {
		if got, want := readRooted(t, root, rel), readRooted(t, commandRoot, rel); got != want {
			t.Errorf("the session's synced %s reads\n%s\nwant what the command's sync wrote\n%s", rel, got, want)
		}
	}
	if got, want := readRooted(t, root, "aggregate.yaml"), authorEditsTo(t, wantSynced); got != want {
		t.Errorf("the session's synced Aggregate reads\n%s\nwant the shared draft with the author's edits\n%s", got, want)
	}

	before = treeFingerprint(t, root)
	inStep := callSessionImport(t, root, map[string]any{"source": changed, "sync": true})
	if inStep.refused || len(inStep.result.Drift) != 0 || len(inStep.result.Files) != 0 || len(inStep.result.Written) != 0 {
		t.Errorf("a sync in step answered %+v (%s)", inStep.result, inStep.message)
	}
	if !bytes.Contains(inStep.raw, []byte(`"drift":[]`)) {
		t.Errorf("a sync in step does not answer drift as an empty list: %s", inStep.raw)
	}
	unchanged("the sync in step", before)

	// The author now edits the carried flow, which the first source changes back.
	handEdited := handEditTheCarriedFlow(t, readRooted(t, root, "aggregate.yaml"))
	if err := os.WriteFile(filepath.Join(root, "aggregate.yaml"), []byte(handEdited), 0o644); err != nil {
		t.Fatalf("write the hand edit: %v", err)
	}
	before = treeFingerprint(t, root)
	for _, reporting := range []bool{false, true} {
		answer := callSessionImport(t, root, map[string]any{"source": first, "sync": true, "dryRun": reporting})
		if !answer.refused || !strings.Contains(answer.message, "sync was sent") ||
			!strings.Contains(answer.message, "values.flow_definition: edited since the import wrote it, and the source changes it") ||
			strings.Contains(answer.message, "--sync") {
			t.Errorf("a sync over an edited carried flow (dry run %v) was not refused in the session's words: %+v",
				reporting, answer)
		}
	}
	unchanged("the refused sync", before)

	if err := os.Remove(filepath.Join(root, importops.RecordFileName)); err != nil {
		t.Fatalf("remove the record: %v", err)
	}
	missing := callSessionImport(t, root, map[string]any{"source": changed, "sync": true, "dryRun": true})
	if !missing.refused || !strings.Contains(missing.message, "no record of what an import wrote lists the Aggregate at") ||
		strings.Contains(missing.message, "--sync") {
		t.Errorf("a sync with no record was not refused in the session's words: %+v", missing)
	}
}

// namesTheCarriedFlow reports whether a drift names the flow the drafted
// platform carries as changed.
func namesTheCarriedFlow(drift []importDriftEntry) bool {
	for _, d := range drift {
		if d.Change == importops.DriftChanged && d.File == "aggregate.yaml" && len(d.Key) > 0 &&
			d.Key[len(d.Key)-1] == "flow_definition" {
			return true
		}
	}
	return false
}
