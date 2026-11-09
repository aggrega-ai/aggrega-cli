// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
)

// What a sync judges and what it writes, driven over the operation's own planning
// and record functions with drafts written here, so each row states exactly what
// the import last wrote, what an author did to it on disk, and what the source
// drafts now. The end-to-end proof through a real importer and through each
// surface is beside the surfaces.

// syncPrevious is a drafted platform Aggregate as an import last wrote it: a
// platform carrying a flow, a model part and a store part, each wired, with the
// comments an import writes above the keys they belong to.
const syncPrevious = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: imported/flow
spec:
  components:
    # Left for the Release: the image
    platform:
      type: core/ai-agent/langflow
      values:
        flow_definition: |
          {"nodes": ["a", "b"]}
      uses:
        model:
          model-a:
            target: model-a
        store:
          store-b:
            target: store-b
    # resembles core/ai-model/openai
    model-a:
      type: imported/ai-model/a
    store-b:
      type: imported/datastore/b
`

// syncFresh is what the source drafts now: it dropped the store part, gained a
// tool part, changed the flow, and the comment the import writes above the
// platform names the tool's address too.
const syncFresh = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: imported/flow
spec:
  components:
    # Left for the Release: the image and tool-c's endpoint
    platform:
      type: core/ai-agent/langflow
      values:
        flow_definition: |
          {"nodes": ["a", "c"]}
      uses:
        model:
          model-a:
            target: model-a
        tools:
          tool-c:
            target: tool-c
    # resembles core/ai-model/openai
    model-a:
      type: imported/ai-model/a
    tool-c:
      type: imported/ai-tool/c
`

// replaceOnce replaces old in text with new, failing the test unless text holds
// old exactly once: an anchor matching twice — one line's text is the tail of a
// deeper line's — would edit a part the case does not name, and the case would go
// on passing or failing for a reason other than its own.
func replaceOnce(t *testing.T, text, old, new string) string {
	t.Helper()
	if n := strings.Count(text, old); n != 1 {
		t.Fatalf("the draft holds %q %d times; the edit needs it once", old, n)
	}
	return strings.Replace(text, old, new, 1)
}

// syncFlowOnly is a source that changed the flow and nothing else.
func syncFlowOnly(t *testing.T) string {
	t.Helper()
	return replaceOnce(t, syncPrevious, `{"nodes": ["a", "b"]}`, `{"nodes": ["a", "b", "d"]}`)
}

// authorEdits applies what an author writes into a drafted Aggregate: a comment
// and a parameter of their own, a Feature under the drafted platform instance, a
// Component of their own, and a note separated from the next key by a blank line.
// It applies them to a draft the same way whatever the draft says, so the same
// edits on the fresh draft are what a sync of the edited previous draft must read
// as.
func authorEdits(t *testing.T, text string) string {
	t.Helper()
	text = replaceOnce(t, text, "\nspec:\n", "\nspec:\n  # mine\n  parameters:\n    region:\n      type: string\n")
	text = replaceOnce(t, text, "\n      type: core/ai-agent/langflow\n",
		"\n      type: core/ai-agent/langflow\n      features:\n        mine:\n          type: core/secret-value\n")
	text = replaceOnce(t, text, "\n    # resembles core/ai-model/openai\n",
		"\n    # a loose note\n\n    # resembles core/ai-model/openai\n")
	return text + "    cache:\n      type: core/datastore/redis\n"
}

// aggregateDraft is one drafted Aggregate at aggregate.yaml.
func aggregateDraft(content string) []abi.ScaffoldFile {
	return []abi.ScaffoldFile{{Path: "aggregate.yaml", Content: content}}
}

// planOver writes disk beneath a fresh output directory and plans the sync of
// fresh against the record of previous, as a sync over that directory would.
func planOver(t *testing.T, previous, fresh []abi.ScaffoldFile, disk map[string]string) (syncPlan, string) {
	t.Helper()
	out := t.TempDir()
	for path, content := range disk {
		place := filepath.Join(out, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(place), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(place, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	rec := recordFor("", draftedFiles(previous))
	plan, err := planSync(out, draftedFiles(fresh), &rec)
	if err != nil {
		t.Fatalf("plan the sync: %v", err)
	}
	return plan, out
}

// writtenAggregate is the content the plan writes for aggregate.yaml, or what is
// on disk where it writes none.
func writtenAggregate(plan syncPlan, disk string) string {
	for _, w := range plan.writes {
		if w.Path == "aggregate.yaml" {
			return w.Content
		}
	}
	return disk
}

// TestASyncBringsOnlyWhatTheImportWroteUpToDate is the sync's whole contract on
// one Aggregate an author edited: every part the source gained, dropped or
// changed is brought up to date — the comment the import writes above the
// platform among them — and every key, comment and note the author wrote stays
// exactly where it was, so the synced text is byte for byte the fresh draft with
// the same edits made. The drift names each part, in the document's order.
//
// 🔴 PLANTED DEFECT: have judge (internal/importops/sync.go) treat a part on disk
// that the record does not list as the import's — skip the inRecord check in the
// dropped case — and the author's Component is removed, so the comparison reddens.
func TestASyncBringsOnlyWhatTheImportWroteUpToDate(t *testing.T) {
	t.Parallel()
	edited := authorEdits(t, syncPrevious)
	plan, _ := planOver(t, aggregateDraft(syncPrevious), aggregateDraft(syncFresh),
		map[string]string{"aggregate.yaml": edited})
	if len(plan.conflicts) != 0 {
		t.Fatalf("the sync conflicts: %+v", plan.conflicts)
	}
	if got, want := writtenAggregate(plan, edited), authorEdits(t, syncFresh); got != want {
		t.Errorf("the synced Aggregate reads\n%s\nwant the fresh draft with the author's edits\n%s", got, want)
	}
	want := []Drift{
		{Change: DriftChanged, File: "aggregate.yaml", Key: []string{"spec", "components", "platform"}},
		{Change: DriftChanged, File: "aggregate.yaml", Key: []string{"spec", "components", "platform", "values", "flow_definition"}},
		{Change: DriftAdded, File: "aggregate.yaml", Key: []string{"spec", "components", "platform", "uses", "tools"}},
		{Change: DriftRemoved, File: "aggregate.yaml", Key: []string{"spec", "components", "platform", "uses", "store"}},
		{Change: DriftAdded, File: "aggregate.yaml", Key: []string{"spec", "components", "tool-c"}},
		{Change: DriftRemoved, File: "aggregate.yaml", Key: []string{"spec", "components", "store-b"}},
	}
	if !reflect.DeepEqual(plan.drift, want) {
		t.Errorf("the drift is %+v\nwant %+v", plan.drift, want)
	}
	if len(plan.removes) != 0 {
		t.Errorf("the sync removes the files %v; the source drafts the same file", plan.removes)
	}
}

// TestASyncInStepWritesNothing holds the in-step case: a source drafting what
// the import last wrote leaves every byte on disk as it is, edited or not, and
// names no drift.
func TestASyncInStepWritesNothing(t *testing.T) {
	t.Parallel()
	edited := authorEdits(t, replaceOnce(t, syncPrevious, "type: imported/ai-model/a\n", "type: core/ai-model/openai\n"))
	plan, _ := planOver(t, aggregateDraft(syncPrevious), aggregateDraft(syncPrevious),
		map[string]string{"aggregate.yaml": edited})
	if len(plan.conflicts) != 0 || len(plan.writes) != 0 || len(plan.drift) != 0 || len(plan.removes) != 0 {
		t.Errorf("a sync in step planned %+v", plan)
	}
}

// TestAnEditTheSourceLeavesAloneStays is the provisional ruling's case: an author
// changed a part the import owns, the source changed another, and the sync brings
// that other part up to date and leaves the edited one byte for byte.
func TestAnEditTheSourceLeavesAloneStays(t *testing.T) {
	t.Parallel()
	edit := func(s string) string {
		return replaceOnce(t, s, "type: imported/ai-model/a\n", "type: core/ai-model/openai\n")
	}
	flowOnly := syncFlowOnly(t)
	plan, _ := planOver(t, aggregateDraft(syncPrevious), aggregateDraft(flowOnly),
		map[string]string{"aggregate.yaml": edit(syncPrevious)})
	if len(plan.conflicts) != 0 {
		t.Fatalf("an edit the source leaves alone conflicts: %+v", plan.conflicts)
	}
	if got, want := writtenAggregate(plan, edit(syncPrevious)), edit(flowOnly); got != want {
		t.Errorf("the synced Aggregate reads\n%s\nwant\n%s", got, want)
	}
	want := []Drift{{Change: DriftChanged, File: "aggregate.yaml",
		Key: []string{"spec", "components", "platform", "values", "flow_definition"}}}
	if !reflect.DeepEqual(plan.drift, want) {
		t.Errorf("the drift is %+v, want %+v", plan.drift, want)
	}
}

// TestAFeatureAnAuthorAddsBesideTheImportsOwnStays holds the case of a drafted
// instance whose features the import writes too: an author's Feature added under
// the same features key is theirs, so a sync that changes the import's Feature
// changes that one and leaves the author's byte for byte.
func TestAFeatureAnAuthorAddsBesideTheImportsOwnStays(t *testing.T) {
	t.Parallel()
	const anchor = "\n      type: core/ai-agent/langflow\n"
	withFeature := func(s, allow string) string {
		return replaceOnce(t, s, anchor, anchor+"      features:\n        egress:\n          type: core/egress-allowlist\n"+
			"          values:\n            hosts: "+allow+"\n")
	}
	mine := "        mine:\n          type: core/secret-value\n"
	previous := withFeature(syncPrevious, "[a]")
	fresh := withFeature(syncFlowOnly(t), "[a, b]")
	plan, _ := planOver(t, aggregateDraft(previous), aggregateDraft(fresh),
		map[string]string{"aggregate.yaml": replaceOnce(t, previous, "\n            hosts: [a]\n",
			"\n            hosts: [a]\n"+mine)})
	if len(plan.conflicts) != 0 {
		t.Fatalf("the sync conflicts: %+v", plan.conflicts)
	}
	want := replaceOnce(t, fresh, "\n            hosts: [a, b]\n", "\n            hosts: [a, b]\n"+mine)
	if got := writtenAggregate(plan, ""); got != want {
		t.Errorf("the synced Aggregate reads\n%s\nwant the import's Feature updated and the author's kept\n%s", got, want)
	}
}

// TestASyncThatWouldRewriteWhatAnAuthorWroteConflicts is one row per way an
// author's work meets a part the sync would change, remove or add. Each row
// carries one fault, and the conflicts are pinned whole: the part, its keys and
// why — and nothing is planned for writing.
//
// Every edit is made through replaceOnce at an anchor opening with a line feed,
// so it lands on the one line it names: the components' "    store-b:" is also
// the tail of the connection's deeper "          store-b:", and an anchor
// without the line feed edits the connection first.
func TestASyncThatWouldRewriteWhatAnAuthorWroteConflicts(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		// disk is the Aggregate on disk, made from the previous draft.
		disk func(t *testing.T) string
		// changesOnlyTheFlow says the source changed the flow alone; otherwise it
		// is syncFresh.
		changesOnlyTheFlow bool
		want               []SyncConflict
	}{
		{
			name: "an edited part the source changes",
			disk: func(t *testing.T) string {
				return replaceOnce(t, syncPrevious, `{"nodes": ["a", "b"]}`, `{"nodes": ["a", "x"]}`)
			},
			changesOnlyTheFlow: true,
			want: []SyncConflict{{File: "aggregate.yaml", Key: []string{"spec", "components", "platform", "values",
				"flow_definition"}, Reason: conflictEditedChanged}},
		},
		{
			name: "an edited part the source drops",
			disk: func(t *testing.T) string {
				return replaceOnce(t, syncPrevious, "\n      type: imported/datastore/b\n", "\n      type: core/datastore/pgvector\n")
			},
			want: []SyncConflict{{File: "aggregate.yaml", Key: []string{"spec", "components", "store-b", "type"},
				Reason: conflictEditedDropped}},
		},
		{
			name: "a Feature an author added under a part the source drops",
			disk: func(t *testing.T) string {
				return replaceOnce(t, syncPrevious, "\n      type: imported/datastore/b\n",
					"\n      type: imported/datastore/b\n      features:\n        mine:\n          type: core/secret-value\n")
			},
			want: []SyncConflict{{File: "aggregate.yaml", Key: []string{"spec", "components", "store-b", "features"},
				Reason: conflictAuthorInside}},
		},
		{
			name: "a note an author wrote inside a part the source drops",
			disk: func(t *testing.T) string {
				return replaceOnce(t, syncPrevious, "\n    store-b:\n", "\n    store-b:\n      # my note\n\n")
			},
			want: []SyncConflict{{File: "aggregate.yaml", Key: []string{"spec", "components", "store-b"},
				Reason: conflictAuthorLines}},
		},
		{
			name: "a part the source gains, at a key an author wrote",
			disk: func(*testing.T) string { return syncPrevious + "    tool-c:\n      type: core/ai-tool/mcp\n" },
			want: []SyncConflict{{File: "aggregate.yaml", Key: []string{"spec", "components", "tool-c"},
				Reason: conflictAuthorGained}},
		},
		{
			name: "a comment an author wrote directly above a part the source changes",
			disk: func(t *testing.T) string {
				return replaceOnce(t, syncPrevious, "\n        flow_definition: |\n",
					"\n        # mine\n        flow_definition: |\n")
			},
			changesOnlyTheFlow: true,
			want: []SyncConflict{{File: "aggregate.yaml", Key: []string{"spec", "components", "platform", "values",
				"flow_definition"}, Reason: conflictEditedChanged}},
		},
		{
			name:               "an Aggregate that no longer reads as YAML, where the source changes it",
			disk:               func(*testing.T) string { return "spec: [\n" },
			changesOnlyTheFlow: true,
			want:               []SyncConflict{{File: "aggregate.yaml", Reason: conflictUnreadable}},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			fresh := syncFresh
			if row.changesOnlyTheFlow {
				fresh = syncFlowOnly(t)
			}
			plan, _ := planOver(t, aggregateDraft(syncPrevious), aggregateDraft(fresh),
				map[string]string{"aggregate.yaml": row.disk(t)})
			if !reflect.DeepEqual(plan.conflicts, row.want) {
				t.Errorf("the conflicts are %+v\nwant %+v", plan.conflicts, row.want)
			}
		})
	}
}

// TestAFileOwnedWholeIsJudgedWhole holds the drafted files that are not an
// Aggregate: each is the import's while it is byte for byte what the import wrote,
// so one the source no longer drafts is removed only then, one the source changes
// is written anew only then, and one the source gains where an author already
// has a file is a conflict.
func TestAFileOwnedWholeIsJudgedWhole(t *testing.T) {
	t.Parallel()
	const comp = "kind: Component\nmetadata:\n  name: imported/b\n"
	for _, row := range []struct {
		name      string
		previous  []abi.ScaffoldFile
		fresh     []abi.ScaffoldFile
		disk      map[string]string
		removes   []string
		writes    []string
		drift     []Drift
		conflicts []SyncConflict
	}{
		{
			name:     "a file the source no longer drafts, untouched, is removed",
			previous: []abi.ScaffoldFile{{Path: "components/b.yaml", Content: comp}},
			disk:     map[string]string{"components/b.yaml": comp},
			removes:  []string{"components/b.yaml"},
			drift:    []Drift{{Change: DriftRemoved, File: "components/b.yaml"}},
		},
		{
			name:      "a file the source no longer drafts, edited, conflicts",
			previous:  []abi.ScaffoldFile{{Path: "components/b.yaml", Content: comp}},
			disk:      map[string]string{"components/b.yaml": comp + "# mine\n"},
			conflicts: []SyncConflict{{File: "components/b.yaml", Reason: conflictEditedDropped}},
		},
		{
			name:     "a file the source changes, untouched, is written anew",
			previous: []abi.ScaffoldFile{{Path: "components/b.yaml", Content: comp}},
			fresh:    []abi.ScaffoldFile{{Path: "components/b.yaml", Content: comp + "spec: {}\n"}},
			disk:     map[string]string{"components/b.yaml": comp},
			writes:   []string{"components/b.yaml"},
			drift:    []Drift{{Change: DriftChanged, File: "components/b.yaml"}},
		},
		{
			name:      "a file the source changes, edited, conflicts",
			previous:  []abi.ScaffoldFile{{Path: "components/b.yaml", Content: comp}},
			fresh:     []abi.ScaffoldFile{{Path: "components/b.yaml", Content: comp + "spec: {}\n"}},
			disk:      map[string]string{"components/b.yaml": comp + "# mine\n"},
			conflicts: []SyncConflict{{File: "components/b.yaml", Reason: conflictEditedChanged}},
		},
		{
			name:      "a file the source gains, where an author has one",
			fresh:     []abi.ScaffoldFile{{Path: "components/c.yaml", Content: comp}},
			disk:      map[string]string{"components/c.yaml": "kind: Component\n"},
			conflicts: []SyncConflict{{File: "components/c.yaml", Reason: conflictAuthorGained}},
		},
		{
			name:   "a file the source gains, where nothing is, is added",
			fresh:  []abi.ScaffoldFile{{Path: "components/c.yaml", Content: comp}},
			writes: []string{"components/c.yaml"},
			drift:  []Drift{{Change: DriftAdded, File: "components/c.yaml"}},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			plan, _ := planOver(t, row.previous, row.fresh, row.disk)
			var writes []string
			for _, w := range plan.writes {
				writes = append(writes, w.Path)
			}
			if !slices.Equal(writes, row.writes) || !slices.Equal(plan.removes, row.removes) ||
				!reflect.DeepEqual(plan.drift, row.drift) || !reflect.DeepEqual(plan.conflicts, row.conflicts) {
				t.Errorf("the plan writes %v, removes %v, drifts %+v and conflicts %+v\n"+
					"want writes %v, removes %v, drift %+v and conflicts %+v",
					writes, plan.removes, plan.drift, plan.conflicts, row.writes, row.removes, row.drift, row.conflicts)
			}
		})
	}
}

// TestASyncKeepsTheLineEndingsOnDisk holds a file an author's editor saved with a
// carriage return before every line feed: the lines the sync writes are ended
// the same way, so the file does not come out with two kinds of line ending.
func TestASyncKeepsTheLineEndingsOnDisk(t *testing.T) {
	t.Parallel()
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	plan, _ := planOver(t, aggregateDraft(syncPrevious), aggregateDraft(syncFresh),
		map[string]string{"aggregate.yaml": crlf(syncPrevious)})
	if len(plan.conflicts) != 0 {
		t.Fatalf("the sync conflicts: %+v", plan.conflicts)
	}
	if got, want := writtenAggregate(plan, ""), crlf(syncFresh); got != want {
		t.Errorf("the synced Aggregate reads %q\nwant %q", got, want)
	}
}

// TestTheSamePlanTwice holds the sync to the deterministic-output rule: two plans
// over the same inputs write the same bytes and name the same drift.
func TestTheSamePlanTwice(t *testing.T) {
	t.Parallel()
	edited := authorEdits(t, syncPrevious)
	a, _ := planOver(t, aggregateDraft(syncPrevious), aggregateDraft(syncFresh), map[string]string{"aggregate.yaml": edited})
	b, _ := planOver(t, aggregateDraft(syncPrevious), aggregateDraft(syncFresh), map[string]string{"aggregate.yaml": edited})
	if !reflect.DeepEqual(a, b) {
		t.Errorf("two plans over the same inputs differ:\n%+v\n%+v", a, b)
	}
	if recordBytes(recordFor("compose", draftedFiles(aggregateDraft(syncFresh)))) !=
		recordBytes(recordFor("compose", draftedFiles(aggregateDraft(syncFresh)))) {
		t.Error("two records of one draft differ")
	}
}

// TestTheRecordListsEveryEntryOfADraftedAggregate pins what the record owns in a
// drafted Aggregate and a drafted Release: every mapping entry at every depth, in
// document order, by the one text rule — and no file of another kind entry by
// entry, a Component and a Manifest each recorded whole.
//
// 🔴 PLANTED DEFECT: have draftedFiles (internal/importops/record.go) select by
// isAggregateBytes again: the Release row reddens, recorded whole.
func TestTheRecordListsEveryEntryOfADraftedAggregate(t *testing.T) {
	t.Parallel()
	rec := recordFor("", draftedFiles([]abi.ScaffoldFile{
		{Path: "components/b.yaml", Content: "kind: Component\nmetadata:\n  name: b\n"},
		{Path: "aggregate.yaml", Content: "kind: Aggregate\nspec:\n  components:\n    a:\n      type: x\n"},
		{Path: "release.yaml", Content: "kind: Release\nmetadata:\n  # environment: left for the operator\n  name: r\n" +
			"spec:\n  components:\n    a:\n      infra:\n        port: 80\n"},
		{Path: "manifest.yaml", Content: "kind: Manifest\nspec:\n  mappings: {}\n"},
	}))
	if len(rec.Files) != 4 {
		t.Fatalf("the record lists %d files, want 4: %+v", len(rec.Files), rec.Files)
	}
	for _, i := range []int{0, 3} {
		if rec.Files[i].Digest == "" || len(rec.Files[i].Parts) != 0 {
			t.Errorf("%s is not recorded whole: %+v", rec.Files[i].Path, rec.Files[i])
		}
	}
	for _, row := range []struct {
		at   int
		want []string
	}{
		{1, []string{"kind", "spec", "spec.components", "spec.components.a", "spec.components.a.type"}},
		{2, []string{"kind", "metadata", "metadata.name", "spec", "spec.components", "spec.components.a",
			"spec.components.a.infra", "spec.components.a.infra.port"}},
	} {
		f := rec.Files[row.at]
		if f.Digest != "" {
			t.Errorf("%s is recorded whole; want it entry by entry", f.Path)
		}
		var keys []string
		for _, p := range f.Parts {
			keys = append(keys, RenderKey(p.Key))
		}
		if !slices.Equal(keys, row.want) {
			t.Errorf("the record of %s lists %v, want %v", f.Path, keys, row.want)
		}
	}
	// The comment written directly above a key is that key's text, in a Release as
	// in an Aggregate: replacing it with a key of the author's own edits the key it
	// sat above.
	doc, err := indexYAML("kind: Release\nmetadata:\n  # environment: left for the operator\n  name: r\n")
	if err != nil {
		t.Fatalf("index the Release: %v", err)
	}
	if u := doc.byKey[joinKey([]string{"metadata", "name"})]; u == nil || u.start != 2 {
		t.Errorf("the comment above metadata.name is not that key's: %+v", u)
	}
}

// draftedRelease is a Release as an import drafts it beside its Aggregate: the
// environment left for its author in a comment, one server's settings.
const draftedRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: imported/flow
  # environment: left for the operator, since no source names it.
  version: 0.1.0
spec:
  aggregate: imported/flow
  components:
    filesystem:
      infra:
        stdio_command:
          - "npx"
        stdio_runtime: npm
`

// gitServer is the server entry the source gains in draftedRelease.
const gitServer = "    git:\n      infra:\n        stdio_command:\n          - \"uvx\"\n        stdio_runtime: python\n"

// filledIn is draftedRelease with the environment its comment asks for written
// where the comment was, as its author fills it in.
func filledIn(t *testing.T, release string) string {
	t.Helper()
	return replaceOnce(t, release, "\n  # environment: left for the operator, since no source names it.\n",
		"\n  environment: staging\n")
}

// releaseDraft is one drafted Release at release.yaml.
func releaseDraft(content string) []abi.ScaffoldFile {
	return []abi.ScaffoldFile{{Path: "release.yaml", Content: content}}
}

// writtenFile is the content the plan writes for path, or what is on disk where
// it writes none.
func writtenFile(plan syncPlan, path, disk string) string {
	for _, w := range plan.writes {
		if w.Path == path {
			return w.Content
		}
	}
	return disk
}

// TestAReleaseItsAuthorFilledInIsSyncedEntryByEntry is the filled-in Release's
// case: its author wrote the environment where the import left a comment for it,
// and the source gained a server. The sync adds the server's entry and keeps the
// environment byte for byte, because the record lists the Release entry by entry
// and the environment is a key the author wrote; the drift names the one entry
// the source gained.
//
// 🔴 PLANTED DEFECT: have draftedFiles (internal/importops/record.go) record a
// Release whole: the sync conflicts on release.yaml as a file edited since the
// import wrote it, and this test reddens.
func TestAReleaseItsAuthorFilledInIsSyncedEntryByEntry(t *testing.T) {
	t.Parallel()
	fresh := draftedRelease + gitServer
	disk := filledIn(t, draftedRelease)
	plan, _ := planOver(t, releaseDraft(draftedRelease), releaseDraft(fresh), map[string]string{"release.yaml": disk})
	if len(plan.conflicts) != 0 {
		t.Fatalf("the sync of a filled-in Release conflicts: %+v", plan.conflicts)
	}
	if got, want := writtenFile(plan, "release.yaml", disk), filledIn(t, fresh); got != want {
		t.Errorf("the synced Release reads\n%s\nwant the fresh draft with the author's environment\n%s", got, want)
	}
	want := []Drift{{Change: DriftAdded, File: "release.yaml", Key: []string{"spec", "components", "git"}}}
	if !reflect.DeepEqual(plan.drift, want) {
		t.Errorf("the drift is %+v, want %+v", plan.drift, want)
	}
}

// TestAnEditedReleaseEntryTheSourceChangesConflicts holds a filled-in Release to
// the conflict rule an Aggregate is held to: an author's edit to an entry the
// source also changes refuses the sync, naming the entry and the file, and
// nothing is planned for writing.
func TestAnEditedReleaseEntryTheSourceChangesConflicts(t *testing.T) {
	t.Parallel()
	previous := draftedRelease + gitServer
	fresh := replaceOnce(t, previous, "\n          - \"uvx\"\n", "\n          - \"uvx\"\n          - \"mcp-server-git\"\n")
	disk := replaceOnce(t, filledIn(t, previous), "\n          - \"uvx\"\n", "\n          - \"pipx\"\n")
	plan, _ := planOver(t, releaseDraft(previous), releaseDraft(fresh), map[string]string{"release.yaml": disk})
	want := []SyncConflict{{File: "release.yaml", Key: []string{"spec", "components", "git", "infra", "stdio_command"},
		Reason: conflictEditedChanged}}
	if !reflect.DeepEqual(plan.conflicts, want) {
		t.Errorf("the conflicts are %+v\nwant %+v", plan.conflicts, want)
	}
	if len(plan.writes) != 0 {
		t.Errorf("a conflicting sync plans writes: %+v", plan.writes)
	}
}

// TestAReleaseRecordedWholeIsJudgedWhole holds the record an import wrote while a
// drafted Release was owned whole: a sync judges that Release as the file it was
// recorded as, writing the entry-by-entry draft where the Release is untouched
// and refusing where it was edited, rather than reading the old record as
// something it is not.
func TestAReleaseRecordedWholeIsJudgedWhole(t *testing.T) {
	t.Parallel()
	fresh := draftedRelease + gitServer
	whole := importRecord{Files: []recordFile{{Path: "release.yaml", Digest: digestOf(draftedRelease)}}}
	for _, row := range []struct {
		name      string
		disk      string
		writes    bool
		conflicts []SyncConflict
	}{
		{name: "untouched", disk: draftedRelease, writes: true},
		{name: "filled in", disk: filledIn(t, draftedRelease),
			conflicts: []SyncConflict{{File: "release.yaml", Reason: conflictEditedChanged}}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			out := t.TempDir()
			if err := os.WriteFile(filepath.Join(out, "release.yaml"), []byte(row.disk), 0o644); err != nil {
				t.Fatal(err)
			}
			rec := whole
			plan, err := planSync(out, draftedFiles(releaseDraft(fresh)), &rec)
			if err != nil {
				t.Fatalf("plan the sync: %v", err)
			}
			if !reflect.DeepEqual(plan.conflicts, row.conflicts) {
				t.Errorf("the conflicts are %+v, want %+v", plan.conflicts, row.conflicts)
			}
			if got := len(plan.writes) == 1 && plan.writes[0].Content == fresh; got != row.writes {
				t.Errorf("the plan writes %+v; want the fresh Release written: %v", plan.writes, row.writes)
			}
		})
	}
}

// TestTheRecordCarriesTheTarget pins where the target an import was given sits in
// the record: first, under target, for an import given one, and absent for an
// import given none; and a record carrying one reads back with it.
//
// 🔴 PLANTED DEFECT: drop omitempty from importRecord.Target
// (internal/importops/record.go): the no-target row reddens on an empty target
// written into the record.
func TestTheRecordCarriesTheTarget(t *testing.T) {
	t.Parallel()
	files := draftedFiles(aggregateDraft("kind: Aggregate\nspec: {}\n"))
	with := recordBytes(recordFor("compose", files))
	if !strings.HasPrefix(with, "{\n  \"target\": \"compose\",\n  \"files\": [") {
		t.Errorf("the record of an import given the target compose opens\n%s", with)
	}
	rec, err := parseRecord([]byte(with))
	if err != nil || rec.Target != "compose" {
		t.Errorf("the record reads back with the target %q (%v); want compose", rec.Target, err)
	}
	if without := recordBytes(recordFor("", files)); strings.Contains(without, "target") {
		t.Errorf("the record of an import given no target names one:\n%s", without)
	}
}

// TestARecordThatIsNotOneAnImportWritesIsRefused holds the record to its own
// shape before a sync reads anything through it, and holds every path it lists
// to the output directory before anything is read or removed through it.
func TestARecordThatIsNotOneAnImportWritesIsRefused(t *testing.T) {
	t.Parallel()
	digest := digestOf("x")
	for _, row := range []struct{ name, body string }{
		{"not JSON", "files: []\n"},
		{"an unknown field", `{"files": [], "extra": 1}`},
		{"a file listed twice", `{"files": [{"path": "a", "digest": "` + digest + `"}, {"path": "a", "digest": "` + digest + `"}]}`},
		{"a file with neither a digest nor parts", `{"files": [{"path": "a"}]}`},
		{"a digest of another shape", `{"files": [{"path": "a", "digest": "md5:00"}]}`},
		{"a path climbing out of the output directory", `{"files": [{"path": "../a", "digest": "` + digest + `"}]}`},
		{"a path naming the record itself", `{"files": [{"path": "` + RecordFileName + `", "digest": "` + digest + `"}]}`},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			out := filepath.Join(t.TempDir(), "out")
			if err := os.MkdirAll(out, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, RecordFileName), []byte(row.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := readRecord(out, out)
			var refused *ImportRecordError
			if !errors.As(err, &refused) || refused.Err == nil {
				t.Fatalf("the record was not refused as one that cannot be read: %v", err)
			}
			if !strings.Contains(err.Error(), RecordFileName) {
				t.Errorf("the refusal does not name the record: %q", err.Error())
			}
		})
	}
	t.Run("no record at all reads as none", func(t *testing.T) {
		t.Parallel()
		out := t.TempDir()
		rec, onDisk, err := readRecord(out, out)
		if err != nil || rec != nil || onDisk != "" {
			t.Errorf("readRecord over a directory holding none = %v, %q, %v; want nil, empty, nil", rec, onDisk, err)
		}
	})
}

// TestTheSentencesEverySurfacePrints pins the shared wording of a drift line, a
// conflict line and a part's keys, with a key that holds a full stop quoted so
// two parts never read alike.
func TestTheSentencesEverySurfacePrints(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ got, want string }{
		{DriftLine(Drift{Change: DriftAdded, File: "aggregate.yaml", Key: []string{"spec", "components", "search"}}),
			"added spec.components.search in aggregate.yaml"},
		{DriftLine(Drift{Change: DriftRemoved, File: "components/x.yaml"}), "removed components/x.yaml"},
		{ConflictLine(SyncConflict{File: "aggregate.yaml", Key: []string{"spec", "a"}, Reason: conflictEditedChanged}),
			"aggregate.yaml spec.a: edited since the import wrote it, and the source changes it"},
		{ConflictLine(SyncConflict{File: "components/x.yaml", Reason: conflictEditedDropped}),
			"components/x.yaml: edited since the import wrote it, and the source no longer has it"},
		{RenderKey([]string{"metadata", "labels", "component.aggrega.ai/category", ""}),
			`metadata.labels."component.aggrega.ai/category".""`},
	} {
		if row.got != row.want {
			t.Errorf("got %q, want %q", row.got, row.want)
		}
	}
}

// TestTheEntriesOfADocumentAreReadFromItsText pins how an entry's text is found,
// which is what decides what an author's comment belongs to: a comment directly
// above a key at the key's own column is that key's, one at another column or
// separated by a blank line is no key's, a literal block's lines are its content
// whatever they hold, and a sequence written at its key's column belongs to it.
func TestTheEntriesOfADocumentAreReadFromItsText(t *testing.T) {
	t.Parallel()
	doc, err := indexYAML("a:\n  # owned by b\n  b: |\n    # content\n    more\n  # not c's: a blank line follows\n\n" +
		"  c:\n  - one\n  - two\n    # deeper, no key's\nd: 1\n")
	if err != nil {
		t.Fatalf("index the document: %v", err)
	}
	for _, row := range []struct {
		key        string
		start, end int
	}{
		{"a", 0, 9},
		{"a\x00b", 1, 4},
		{"a\x00c", 7, 9},
		{"d", 11, 11},
	} {
		u, ok := doc.byKey[row.key]
		if !ok {
			t.Fatalf("no entry %q", row.key)
		}
		if u.start != row.start || u.end != row.end {
			t.Errorf("entry %q spans lines %d-%d, want %d-%d", row.key, u.start, u.end, row.start, row.end)
		}
	}
	owned := doc.ownedLines()
	for _, loose := range []int{5, 10} {
		if owned[loose] {
			t.Errorf("line %d is owned by an entry; it is no key's", loose)
		}
	}
}
