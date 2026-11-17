// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
)

// TestImportAcceptsSyncAsARequestField is the request half of a sync over this
// surface: a request sending sync is read past the parameter decode, which
// refuses every field the method does not declare by name.
//
// The fixture wires no plugin resolver, so the request stops at that wiring
// rather than at its parameters — a different code and a sentence naming
// neither a missing field nor sync. The control row sends a field the method
// does not declare, so the refusal this test rules out is shown to fire.
//
// 🔴 PLANTED DEFECT: remove the Sync field from importParams
// (internal/serve/import.go) and the sync row is refused as an undeclared
// field, which reddens it.
func TestImportAcceptsSyncAsARequestField(t *testing.T) {
	t.Parallel()
	srv := importFixture(t)

	resp := callDaemonMethod(t, srv, "aggrega/import", map[string]any{
		"source": "flow.json", "outputDir": "drafts", "sync": true, "dryRun": true,
	})
	if resp.Error == nil {
		t.Fatal("the fixture wires no importer, so this request cannot have succeeded")
	}
	if resp.Error.Code == jsonRPCInvalidParams || strings.Contains(resp.Error.Message, "does not accept") {
		t.Fatalf("a request sending sync was refused at its parameters: %d %q", resp.Error.Code, resp.Error.Message)
	}

	control := callDaemonMethod(t, srv, "aggrega/import", map[string]any{
		"source": "flow.json", "outputDir": "drafts", "resync": true,
	})
	if control.Error == nil || !strings.Contains(control.Error.Message, `does not accept "resync"`) {
		t.Fatalf("an undeclared field was not refused at the parameters, so the row above proves nothing: %+v",
			control.Error)
	}
	_, accepted, _ := strings.Cut(control.Error.Message, "it accepts: ")
	if !slices.Contains(strings.Split(accepted, ", "), "sync") {
		t.Errorf("the refusal's list of accepted fields does not name sync: %q", control.Error.Message)
	}
}

// TestImportDriftIsAlwaysAListInTheOperationsOrder pins the wire shape of the
// drift the answer carries: an empty list rather than an absent key or null for
// a request that is not a sync, the operation's order kept, and each part's keys
// a list — empty, never null, for a whole file — so a key holding a full stop is
// one entry.
//
// 🔴 PLANTED DEFECTS:
//   - have importDrifts (internal/serve/import.go) return nil for no drift: the
//     first row answers null and reddens.
//   - have it carry d.Key as it is: the whole-file row answers "key":null and
//     reddens.
func TestImportDriftIsAlwaysAListInTheOperationsOrder(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		drift []importops.Drift
		want  string
	}{
		{name: "a request that is not a sync", drift: nil, want: `[]`},
		{
			name: "a sync, in the operation's order, a whole file among the parts",
			drift: []importops.Drift{
				{Change: importops.DriftChanged, File: "aggregate.yaml", Key: []string{"spec", "components", "langflow"}},
				{Change: importops.DriftAdded, File: "aggregate.yaml", Key: []string{"spec", "components", "search"}},
				{Change: importops.DriftRemoved, File: "components/imported-datastore-pgvector.yaml"},
				{Change: importops.DriftChanged, File: "aggregate.yaml", Key: []string{"spec", "a.b"}},
			},
			want: `[{"change":"changed","file":"aggregate.yaml","key":["spec","components","langflow"]},` +
				`{"change":"added","file":"aggregate.yaml","key":["spec","components","search"]},` +
				`{"change":"removed","file":"components/imported-datastore-pgvector.yaml","key":[]},` +
				`{"change":"changed","file":"aggregate.yaml","key":["spec","a.b"]}]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, err := json.Marshal(importDrifts(tc.drift))
			if err != nil {
				t.Fatalf("marshal the drift: %v", err)
			}
			if string(first) != tc.want {
				t.Errorf("drift on the wire = %s\nwant %s", first, tc.want)
			}
			again, err := json.Marshal(importDrifts(tc.drift))
			if err != nil {
				t.Fatalf("marshal the drift again: %v", err)
			}
			if string(again) != string(first) {
				t.Errorf("two projections of one drift differ:\n%s\n%s", first, again)
			}
		})
	}

	// The answer carries the field whatever else it holds, so a client reading it
	// finds a list on every answer.
	answer, err := json.Marshal(importResult{Drift: importDrifts(nil)})
	if err != nil {
		t.Fatalf("marshal an answer: %v", err)
	}
	if !strings.Contains(string(answer), `"drift":[]`) {
		t.Errorf("an answer with no drift does not carry an empty list: %s", answer)
	}
}

// TestImportSyncRefusalsNameTheRequestField holds each refusal a sync can draw
// to this surface's words: the request field a caller here sends, never the
// command line's flag, each conflicting part in the one sentence every surface
// prints for it, and the Aggregate and the record where they sit.
//
// 🔴 PLANTED DEFECTS:
//   - have sessionSyncConflict return "import: " + e.Error(), the operation's
//     own sentence: it names --sync and the conflict row reddens.
//   - drop the "send sync" remedy from sessionExistingEntity: the
//     existing-Aggregate row reddens.
func TestImportSyncRefusalsNameTheRequestField(t *testing.T) {
	t.Parallel()

	outputDir := filepath.Join(t.TempDir(), "drafts")
	conflicts := []importops.SyncConflict{
		{File: "aggregate.yaml", Key: []string{"spec", "components", "openaimodel-m1", "type"},
			Reason: "edited since the import wrote it, and the source no longer has it"},
		{File: "components/imported-ai-model-openaimodel.yaml",
			Reason: "edited since the import wrote it, and the source changes it"},
	}

	for _, tc := range []struct {
		name    string
		message string
		want    []string
	}{
		{
			name: "an Aggregate at a drafted path, without sync",
			message: sessionExistingEntity(&importops.ExistingEntityError{Path: "aggregate.yaml", OutputDir: outputDir,
				Kind: entitykind.Aggregate}),
			want: []string{"import: ", "the Aggregate at " + filepath.Join(outputDir, "aggregate.yaml"), "nothing was written",
				"send sync to bring it up to date with the source", "outputDir"},
		},
		{
			name: "a Component at a drafted path, without sync",
			message: sessionExistingEntity(&importops.ExistingEntityError{Path: "catalog/c.yaml", OutputDir: outputDir,
				Kind: entitykind.Component}),
			want: []string{"import: ", "the Component at " + filepath.Join(outputDir, "catalog", "c.yaml"),
				"nothing was written", "send sync to bring it up to date with the source", "outputDir"},
		},
		{
			name:    "a sync that would rewrite what an author wrote",
			message: sessionSyncConflict(&importops.SyncConflictError{OutputDir: outputDir, Conflicts: conflicts}),
			want: []string{"import: sync was sent", outputDir, "nothing was written",
				"\n  " + importops.ConflictLine(conflicts[0]), "\n  " + importops.ConflictLine(conflicts[1])},
		},
		{
			name: "a sync over an Aggregate no record lists",
			message: sessionImportRecord(&importops.ImportRecordError{Record: importops.RecordFileName,
				OutputDir: outputDir, Path: "aggregate.yaml", Kind: entitykind.Aggregate}),
			want: []string{"import: sync was sent", "the Aggregate at " + filepath.Join(outputDir, "aggregate.yaml"),
				filepath.Join(outputDir, importops.RecordFileName), "nothing was written"},
		},
		{
			name: "a sync over a Recipe no record lists",
			message: sessionImportRecord(&importops.ImportRecordError{Record: importops.RecordFileName,
				OutputDir: outputDir, Path: "catalog/r.yaml", Kind: entitykind.Recipe}),
			want: []string{"import: sync was sent", "the Recipe at " + filepath.Join(outputDir, "catalog", "r.yaml"),
				filepath.Join(outputDir, importops.RecordFileName), "nothing was written"},
		},
		{
			name: "a sync whose record cannot be read",
			message: sessionImportRecord(&importops.ImportRecordError{Record: importops.RecordFileName,
				OutputDir: outputDir, Err: errors.New("it lists itself")}),
			want: []string{"import: sync was sent", filepath.Join(outputDir, importops.RecordFileName),
				"cannot be read: it lists itself", "nothing was written"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, want := range tc.want {
				if !strings.Contains(tc.message, want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, tc.message)
				}
			}
			if strings.Contains(tc.message, "--sync") || strings.Contains(tc.message, "--output-dir") {
				t.Errorf("the refusal names a command-line flag a request cannot send:\n%s", tc.message)
			}
		})
	}

	// The conflicting parts keep the operation's order.
	message := sessionSyncConflict(&importops.SyncConflictError{OutputDir: outputDir, Conflicts: conflicts})
	if first, second := strings.Index(message, importops.ConflictLine(conflicts[0])),
		strings.Index(message, importops.ConflictLine(conflicts[1])); first < 0 || second < first {
		t.Errorf("the conflicting parts are not named in the operation's order:\n%s", message)
	}
}

// TestImportRecordFormatsRefusalsNameTheRequestField holds the refusals of an
// import into a directory other formats' imports drafted in to this surface's
// words: an entity another format's import drafted, with and without sync,
// naming that import and never offering sync where it would not help; a sync the
// record's entry naming no format does not answer for; and a source no importer
// recognises before the record's entry is chosen, naming the record's formats
// rather than every format the importers advertise. No command-line flag is
// named.
//
// 🔴 PLANTED DEFECT: in sessionExistingEntity (internal/serve/import.go), drop the
// branch for another format's entity: the first row offers sync, and reddens.
func TestImportRecordFormatsRefusalsNameTheRequestField(t *testing.T) {
	t.Parallel()
	outputDir := filepath.Join(t.TempDir(), "drafts")
	record := filepath.Join(outputDir, importops.RecordFileName)
	for _, tc := range []struct {
		name    string
		message string
		want    []string
		absent  []string
	}{
		{
			name: "an Aggregate another format's import drafted, without sync",
			message: sessionExistingEntity(&importops.ExistingEntityError{Path: "aggregate.yaml", OutputDir: outputDir,
				Kind: entitykind.Aggregate, Format: "compose"}),
			want: []string{"import: ", "the Aggregate at " + filepath.Join(outputDir, "aggregate.yaml"),
				"the compose import", "nothing was written", "outputDir"},
			absent: []string{"send sync"},
		},
		{
			name: "a sync over an Aggregate another format's entry lists",
			message: sessionImportRecord(&importops.ImportRecordError{Record: importops.RecordFileName,
				OutputDir: outputDir, Path: "aggregate.yaml", Kind: entitykind.Aggregate, Format: "compose"}),
			want: []string{"import: sync was sent", "the Aggregate at " + filepath.Join(outputDir, "aggregate.yaml"),
				"the compose import", record, "nothing was written"},
		},
		{
			name: "a sync the entry naming no format does not answer for",
			message: sessionImportRecord(&importops.ImportRecordError{Record: importops.RecordFileName,
				OutputDir: outputDir, Unclaimed: true}),
			want:   []string{"import: sync was sent", record, "names no format", "without sync", "nothing was written"},
			absent: []string{"the  at"},
		},
		{
			name: "a source no importer recognises over several entries",
			message: sessionUndetectedFormat(&importops.UndetectedFormatError{Source: "mystery.json",
				Advertised: []string{"langflow"}, Recorded: []string{"compose", "mcp"}}),
			want:   []string{"import: ", "mystery.json", "send format", "compose or mcp"},
			absent: []string{"langflow"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, want := range tc.want {
				if !strings.Contains(tc.message, want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, tc.message)
				}
			}
			for _, absent := range append([]string{"--sync", "--output-dir", "--format"}, tc.absent...) {
				if strings.Contains(tc.message, absent) {
					t.Errorf("the refusal carries %q:\n%s", absent, tc.message)
				}
			}
		})
	}
}
