// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
)

// How this surface words a sync: the drift it answers with, the text lines it
// renders, and the refusals it re-words by type. The sync itself — what it
// judges and writes — is the shared operation's, proven there and end to end
// beside the command.

// TestTheToolAnswersEachDriftedPartWithItsKeysAsAList pins the drift's shape on
// this surface: each part's keys as a list, empty for a whole file, so a key
// holding a full stop is read back as the one key it is, and the whole drift an
// empty list rather than an absent field where nothing drifted.
func TestTheToolAnswersEachDriftedPartWithItsKeysAsAList(t *testing.T) {
	t.Parallel()
	got, err := json.Marshal(importDriftFor([]importops.Drift{
		{Change: importops.DriftChanged, File: "aggregate.yaml", Key: []string{"metadata", "labels", "a.b/c"}},
		{Change: importops.DriftRemoved, File: "components/x.yaml"},
	}))
	if err != nil {
		t.Fatalf("encode the drift: %v", err)
	}
	want := `[{"change":"changed","file":"aggregate.yaml","key":["metadata","labels","a.b/c"]},` +
		`{"change":"removed","file":"components/x.yaml","key":[]}]`
	if string(got) != want {
		t.Errorf("the drift encodes as %s, want %s", got, want)
	}
	if empty, _ := json.Marshal(importDriftFor(nil)); string(empty) != "[]" {
		t.Errorf("no drift encodes as %s, want an empty list", empty)
	}
}

// TestTheToolsTextNamesTheSyncByWhatItDid pins the text answer of a sync: the
// drift lines in the command line's wording, the files removed, and a sync in
// step said to be in step rather than read as an importer that drafted nothing.
func TestTheToolsTextNamesTheSyncByWhatItDid(t *testing.T) {
	t.Parallel()
	root := "/srv/ws"
	payload := importPayload{Source: "flow.json", Format: "langflow", Written: []string{"aggregate.yaml"},
		OutputDir: &root}
	drift := []importops.Drift{{Change: importops.DriftRemoved, File: "components/x.yaml"}}
	text := renderImportText(payload, importops.Result{Drift: drift, Removed: []string{"components/x.yaml"}},
		authoredcontent.DeliveryFor(false), true)
	for _, want := range []string{"  wrote aggregate.yaml, relative to ", "  removed components/x.yaml, relative to ",
		"  " + importops.DriftLine(drift[0])} {
		if !strings.Contains(text, want) {
			t.Errorf("the text answer does not carry %q:\n%s", want, text)
		}
	}
	inStep := renderImportText(importPayload{Source: "flow.json", Format: "langflow", Written: []string{}},
		importops.Result{}, authoredcontent.DeliveryFor(true), true)
	if !strings.Contains(inStep, "the drafts are in step with the source") {
		t.Errorf("a sync in step does not say so:\n%s", inStep)
	}
}

// TestTheToolRewordsASyncConflictForItsCaller pins the conflict refusal: the
// argument a caller here sent, each part in the shared wording, the directory the
// parts are relative to as this caller is told it, and no flag of the command
// line's. A caller at the keyboard is told the directory in full; a caller
// reaching a listener is told a directory inside the served workspace by its name
// relative to the root, and the root itself as the served workspace.
func TestTheToolRewordsASyncConflictForItsCaller(t *testing.T) {
	t.Parallel()
	conflict := importops.SyncConflict{File: "aggregate.yaml", Key: []string{"spec", "components", "a", "type"},
		Reason: "edited since the import wrote it, and the source changes it"}
	for _, tc := range []struct {
		name      string
		transport transportMode
		outputDir string
		names     string
		keptBack  []string
	}{
		{"at the keyboard, the root", transportStdio, "/srv/ws", "beneath /srv/ws up to date", nil},
		{"over a listener, the root", transportHTTP, "/srv/ws", "beneath " + servedWorkspaceWords + " up to date",
			[]string{"/srv/ws"}},
		{"over a listener, a directory inside the root", transportHTTP, "/srv/ws/drafts", "beneath drafts up to date",
			[]string{"/srv/ws"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tw := &toolWorkspace{root: "/srv/ws", transport: tc.transport}
			text := importConflictRefusal(tw, &importops.SyncConflictError{OutputDir: tc.outputDir,
				Conflicts: []importops.SyncConflict{conflict}})
			for _, want := range []string{"'sync'", "nothing was written", tc.names,
				"\n  " + importops.ConflictLine(conflict)} {
				if !strings.Contains(text, want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, text)
				}
			}
			for _, unwanted := range append([]string{"--sync"}, tc.keptBack...) {
				if strings.Contains(text, unwanted) {
					t.Errorf("the refusal names %q, which this caller neither sends nor is told:\n%s", unwanted, text)
				}
			}
		})
	}
}

// TestImportRecordFormatsRefusalsNameTheToolsArguments pins how this surface
// words the refusals of an import into a directory other formats' imports
// drafted in: an entity another format's import drafted, with and without
// 'sync', naming that import and never offering 'sync' where it would not help; a
// sync the record's entry naming no format does not answer for; and a source no
// importer recognises before the record's entry is chosen, naming the record's
// formats rather than every format the importers advertise. No command-line flag
// is named.
//
// 🔴 PLANTED DEFECT: in importRecordRefusal (tool_import.go), drop the branch for
// an entry naming no format: the unclaimed row reads as an entity no record
// lists, and reddens.
func TestImportRecordFormatsRefusalsNameTheToolsArguments(t *testing.T) {
	t.Parallel()
	tw := &toolWorkspace{root: "/srv/ws", transport: transportStdio}
	for _, tc := range []struct {
		name   string
		text   string
		want   []string
		absent []string
	}{
		{"an Aggregate another format's import drafted, without sync",
			importExistingEntityRefusal(tw, &importops.ExistingEntityError{Path: "aggregate.yaml",
				OutputDir: "/srv/ws/drafts", Kind: "Aggregate", Format: "compose"}),
			[]string{`"aggregate.yaml"`, "Aggregate", "the compose import", "'output_dir'", "Nothing was written"},
			[]string{"'sync'"}},
		{"a sync over an Aggregate another format's entry lists",
			importRecordRefusal(tw, &importops.ImportRecordError{Record: importops.RecordFileName,
				OutputDir: "/srv/ws/drafts", Path: "aggregate.yaml", Kind: "Aggregate", Format: "compose"}),
			[]string{"'sync'", `"aggregate.yaml"`, "the compose import", importops.RecordFileName, "Nothing was written"},
			nil},
		{"a sync the entry naming no format does not answer for",
			importRecordRefusal(tw, &importops.ImportRecordError{Record: importops.RecordFileName,
				OutputDir: "/srv/ws/drafts", Unclaimed: true}),
			[]string{"'sync'", importops.RecordFileName, "names no format", "without 'sync'", "Nothing was written"},
			[]string{`""`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, want := range tc.want {
				if !strings.Contains(tc.text, want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, tc.text)
				}
			}
			for _, absent := range append([]string{"--"}, tc.absent...) {
				if strings.Contains(tc.text, absent) {
					t.Errorf("the refusal carries %q:\n%s", absent, tc.text)
				}
			}
		})
	}

	text, unsettled := importFormatRefusal(&importops.UndetectedFormatError{Source: "mystery.json",
		Advertised: []string{"langflow"}, Recorded: []string{"compose", "mcp"}})
	if !unsettled {
		t.Fatal("a source no importer recognises over several entries was not recognised by its type")
	}
	for _, want := range []string{"E0350: ", "mystery.json", "'format'", `one of "compose", "mcp"`, "Nothing was written"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, text)
		}
	}
	for _, absent := range []string{"langflow", "--format"} {
		if strings.Contains(text, absent) {
			t.Errorf("the refusal carries %q:\n%s", absent, text)
		}
	}
}
