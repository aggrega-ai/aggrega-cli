// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs that imports of different formats into one output directory
// each keep their own entry in the record beside the drafts, through the real
// Import with the reference Compose and MCP importers of a catalog folder:
//
//   - a Compose import and an MCP import each write their own entry, the record
//     reading the same bytes whichever ran first, and each syncs reading and
//     writing its own entry and touching only its own files;
//   - a record an older version wrote — one import's fields at its top level, no
//     format — is kept as written by an import that is not a sync, taken over by
//     the sync whose drafts it lists, and refused by any other sync, which
//     removes nothing;
//   - an Aggregate another format's entry lists refuses an import of this one,
//     naming that format's import, with or without sync, under a dry run too;
//   - a sync naming no format over a record of several entries recognises the
//     source before the published catalog is read, and asks for the format,
//     naming the record's, where no importer recognises it there;
//   - a record that cannot be read refuses a sync, and an import that is not a
//     sync writes it anew, warning that the entries it held are lost.
//
// The workspace names no default registry, so no published catalog is read. The
// Compose importer's attachment asks for its decisions alone (render_mode llm),
// so its draft is import-decisions.json, one decision per service, and shares no
// path with the MCP draft, an Aggregate and a Release; the case of a shared path
// attaches it with no settings, so it drafts an Aggregate at the path the MCP
// import drafts one.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// decisionsOnly is the setting that has an importer draft its decisions alone,
// as import-decisions.json.
var decisionsOnly = map[string]any{"render_mode": "llm"}

// formatSource is a source a case imports: where it is written and the name it
// is handed to the import under, which recognition reads.
type formatSource struct{ path, name string }

// writeFormatSource writes body as a source named name in a directory of its own.
func writeFormatSource(t *testing.T, name, body string) formatSource {
	t.Helper()
	return formatSource{path: importopstest.WriteSource(t, filepath.Join(t.TempDir(), name), body), name: name}
}

// rewrite replaces the source's content, as an author changing it between an
// import and a sync does.
func (s formatSource) rewrite(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(s.path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// composeSourceBody is the authored Compose application.
func composeSourceBody(t *testing.T) string {
	t.Helper()
	return string(readComposeFixture(t, "authored/compose.yaml"))
}

// mcpSourceBody is Claude Code's MCP configuration.
func mcpSourceBody(t *testing.T) string {
	t.Helper()
	return string(readMCPFixture(t, "claude-code.mcp.json"))
}

// recordFormatsWorkspace is a workspace whose catalog folder holds the reference
// importers, each attached, the Compose importer's attachment handing it
// composeSettings, and naming no default registry.
func recordFormatsWorkspace(t *testing.T, composeSettings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	folder := importopstest.LayCatalogFolder(t, wsDir, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir), true)
	attachments := folder.Attachments(nil)
	compose := folder.ImporterFor(t, "compose").Identity
	for i := range attachments {
		if attachments[i].URI == compose {
			attachments[i].Settings = composeSettings
		}
	}
	importopstest.WriteConfiguration(t, wsDir, attachments...)
	return resolveWorkspaceAt(t, wsDir)
}

// draftsDir is where the cases draft, beneath the workspace root.
func draftsDir(rw *workspaceconfig.ResolvedWorkspace) string {
	return filepath.Join(rw.Root(), "drafts")
}

// importInto imports src into draftsDir(rw) with opts.
func importInto(rw *workspaceconfig.ResolvedWorkspace, src formatSource, opts Options) (Result, error) {
	opts.SourcePath, opts.SourceName = src.path, src.name
	opts.OutputDir = draftsDir(rw)
	opts.ResolvePlugin = (&recordingCascade{}).resolve
	return Import(context.Background(), rw, opts)
}

// mustImport is importInto failing the test where the import is refused.
func mustImport(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, src formatSource, opts Options) Result {
	t.Helper()
	res, err := importInto(rw, src, opts)
	if err != nil {
		t.Fatalf("the import of %s was refused: %v", src.name, err)
	}
	return res
}

// recordBeside reads the record beside the drafts beneath dir, and its bytes.
func recordBeside(t *testing.T, dir string) (importRecord, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, RecordFileName))
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	rec, err := parseRecord(data)
	if err != nil {
		t.Fatalf("the record does not read back: %v\n%s", err, data)
	}
	return rec, string(data)
}

// recordFormats is the format of each entry of rec, in its order.
func recordFormats(rec importRecord) []string {
	out := make([]string, 0, len(rec.Imports))
	for _, e := range rec.Imports {
		out = append(out, e.Format)
	}
	return out
}

// entryIn returns rec's entry for format, failing the test where it holds none.
func entryIn(t *testing.T, rec importRecord, format string) recordEntry {
	t.Helper()
	e, ok := rec.entry(format)
	if !ok {
		t.Fatalf("the record holds no entry for the format %q: %v", format, recordFormats(rec))
	}
	return e
}

// entryPaths is every path an entry lists, in its order.
func entryPaths(e recordEntry) []string {
	out := make([]string, 0, len(e.Files))
	for _, f := range e.Files {
		out = append(out, f.Path)
	}
	return out
}

// filesOf reads every file the entry lists beneath dir, failing the test where
// one is not there.
func filesOf(t *testing.T, dir string, e recordEntry) map[string]string {
	t.Helper()
	out := make(map[string]string, len(e.Files))
	for _, p := range entryPaths(e) {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			t.Fatalf("read %s, which the record lists: %v", p, err)
		}
		out[p] = string(data)
	}
	return out
}

// writeOlderRecord rewrites the record beside the drafts beneath dir in the
// shape an older version writes — its only entry's fields at the top level, and
// no format — and returns that entry, its format left out.
func writeOlderRecord(t *testing.T, dir string) recordEntry {
	t.Helper()
	rec, _ := recordBeside(t, dir)
	if len(rec.Imports) != 1 {
		t.Fatalf("the record holds %d entries; the older shape holds one", len(rec.Imports))
	}
	older := rec.Imports[0]
	older.Format = ""
	data, err := json.MarshalIndent(older, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "imports") || !strings.HasPrefix(string(data), "{\n  \"files\"") {
		t.Fatalf("the older record is not the older shape:\n%s", data)
	}
	if err := os.WriteFile(filepath.Join(dir, RecordFileName), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return older
}

// TestRecordFormatsTwoImportsKeepTheirEntries imports a Compose application and
// an MCP configuration into one directory and requires the record to hold an
// entry for each, sorted by format and the same bytes whichever import ran
// first, the second import leaving the first's entry as it was. Each source then
// changes and is synced naming no format: each sync drifts, writes only its own
// drafts and the record, removes nothing, and leaves the other's entry and
// files byte for byte.
//
// 🔴 PLANTED DEFECT: have Import (internal/importops/import.go) write the record
// of its own entry alone, dropping every other: the MCP import's record holds
// one entry, and the case reddens.
func TestRecordFormatsTwoImportsKeepTheirEntries(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	rw := recordFormatsWorkspace(t, decisionsOnly)
	dir := draftsDir(rw)
	compose := writeFormatSource(t, "compose.yaml", composeSourceBody(t))
	mcp := writeFormatSource(t, ".mcp.json", mcpSourceBody(t))

	if res := mustImport(t, rw, compose, Options{}); res.Format != "compose" {
		t.Fatalf("the Compose source was read as %q", res.Format)
	}
	afterCompose, _ := recordBeside(t, dir)
	composeEntry := entryIn(t, afterCompose, "compose")
	if got := entryPaths(composeEntry); !slices.Equal(got, []string{"import-decisions.json"}) {
		t.Fatalf("the Compose entry lists %v, want import-decisions.json alone", got)
	}
	if res := mustImport(t, rw, mcp, Options{}); res.Format != "mcp" {
		t.Fatalf("the MCP source was read as %q", res.Format)
	}
	both, bothBytes := recordBeside(t, dir)
	if got := recordFormats(both); !slices.Equal(got, []string{"compose", "mcp"}) {
		t.Fatalf("the record holds the entries %q, want compose and mcp", got)
	}
	if got := entryIn(t, both, "compose"); !reflect.DeepEqual(got, composeEntry) {
		t.Errorf("the MCP import changed the Compose entry:\n%+v\nwant\n%+v", got, composeEntry)
	}
	mcpEntry := entryIn(t, both, "mcp")
	if got := entryPaths(mcpEntry); !slices.Equal(got, []string{"aggregate.yaml", "release.yaml"}) {
		t.Fatalf("the MCP entry lists %v, want aggregate.yaml and release.yaml", got)
	}

	// The same two imports the other way round write the same record.
	reverse := recordFormatsWorkspace(t, decisionsOnly)
	mustImport(t, reverse, writeFormatSource(t, ".mcp.json", mcpSourceBody(t)), Options{})
	mustImport(t, reverse, writeFormatSource(t, "compose.yaml", composeSourceBody(t)), Options{})
	if _, reversed := recordBeside(t, draftsDir(reverse)); reversed != bothBytes {
		t.Errorf("the record depends on which import ran first:\n%s\nwant\n%s", reversed, bothBytes)
	}

	// The Compose sync decides a service more and leaves the MCP drafts alone.
	mcpFiles := filesOf(t, dir, mcpEntry)
	compose.rewrite(t, strings.Replace(composeSourceBody(t), "\nvolumes:\n",
		"\n  worker:\n    image: ghcr.io/example/shop-worker:2.0.0\nvolumes:\n", 1))
	synced := mustImport(t, rw, compose, Options{Sync: true})
	if synced.Format != "compose" {
		t.Fatalf("the Compose sync read the format %q", synced.Format)
	}
	if want := []string{"import-decisions.json", RecordFileName}; !slices.Equal(synced.Written, want) ||
		len(synced.Removed) != 0 {
		t.Errorf("the Compose sync wrote %v and removed %v; want %v, nothing removed", synced.Written,
			synced.Removed, want)
	}
	afterSync, _ := recordBeside(t, dir)
	composeNow := entryIn(t, afterSync, "compose")
	if reflect.DeepEqual(composeNow, composeEntry) {
		t.Errorf("the Compose sync left its entry as it was, though its draft changed")
	}
	if got := entryIn(t, afterSync, "mcp"); !reflect.DeepEqual(got, mcpEntry) {
		t.Errorf("the Compose sync changed the MCP entry:\n%+v\nwant\n%+v", got, mcpEntry)
	}
	if got := filesOf(t, dir, mcpEntry); !reflect.DeepEqual(got, mcpFiles) {
		t.Errorf("the Compose sync changed the MCP drafts")
	}

	// The MCP sync drafts a server more and leaves the Compose draft alone.
	composeFiles := filesOf(t, dir, composeNow)
	mcp.rewrite(t, strings.Replace(mcpSourceBody(t), "\n  \"mcpServers\": {\n",
		"\n  \"mcpServers\": {\n    \"fetch\": {\"command\": \"uvx\", \"args\": [\"mcp-server-fetch\"]},\n", 1))
	synced = mustImport(t, rw, mcp, Options{Sync: true})
	if synced.Format != "mcp" || len(synced.Drift) == 0 {
		t.Fatalf("the MCP sync read the format %q and drifted %+v; want mcp, drifted", synced.Format, synced.Drift)
	}
	last, _ := recordBeside(t, dir)
	mcpNow := entryIn(t, last, "mcp")
	for _, p := range synced.Written {
		if p != RecordFileName && !recordLists(mcpNow, p) {
			t.Errorf("the MCP sync wrote %s, which its entry does not list", p)
		}
	}
	if !slices.Contains(synced.Written, RecordFileName) || len(synced.Removed) != 0 {
		t.Errorf("the MCP sync wrote %v and removed %v; want the record among the writes, nothing removed",
			synced.Written, synced.Removed)
	}
	if got := entryIn(t, last, "compose"); !reflect.DeepEqual(got, composeNow) {
		t.Errorf("the MCP sync changed the Compose entry:\n%+v\nwant\n%+v", got, composeNow)
	}
	if got := filesOf(t, dir, composeNow); !reflect.DeepEqual(got, composeFiles) {
		t.Errorf("the MCP sync changed the Compose draft")
	}
}

// TestRecordFormatsOlderRecordKeptByPlainImport holds a record an older version
// wrote beside an MCP draft as written when a Compose import, not a sync, adds
// its own entry beside it: the older entry reads back unchanged, with no format,
// and the MCP drafts are untouched.
//
// 🔴 PLANTED DEFECT: have the import's record drop the entry naming no format
// (internal/importops/import.go): the older entry is gone, and the case reddens.
func TestRecordFormatsOlderRecordKeptByPlainImport(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	rw := recordFormatsWorkspace(t, decisionsOnly)
	dir := draftsDir(rw)
	mustImport(t, rw, writeFormatSource(t, ".mcp.json", mcpSourceBody(t)), Options{})
	older := writeOlderRecord(t, dir)
	mcpFiles := filesOf(t, dir, older)

	mustImport(t, rw, writeFormatSource(t, "compose.yaml", composeSourceBody(t)), Options{})
	rec, _ := recordBeside(t, dir)
	if got := recordFormats(rec); !slices.Equal(got, []string{"", "compose"}) {
		t.Fatalf("the record holds the entries %q, want the older one and compose", got)
	}
	if got := entryIn(t, rec, ""); !reflect.DeepEqual(got, older) {
		t.Errorf("the older entry changed:\n%+v\nwant\n%+v", got, older)
	}
	if got := entryPaths(entryIn(t, rec, "compose")); !slices.Equal(got, []string{"import-decisions.json"}) {
		t.Errorf("the Compose entry lists %v, want import-decisions.json", got)
	}
	if got := filesOf(t, dir, older); !reflect.DeepEqual(got, mcpFiles) {
		t.Errorf("the Compose import changed the MCP drafts")
	}
}

// TestRecordFormatsOlderRecordTakenOverBySyncThatListsIt syncs the MCP source
// over the record an older version wrote of its own import: the sync reads that
// entry as its own — in step, it judges nothing drifted and writes no draft —
// and writes it back under its format, its every other field as it was.
//
// 🔴 PLANTED DEFECT: have the sync write its record with no format
// (internal/importops/import.go): the entry reads back with none, and the case
// reddens.
func TestRecordFormatsOlderRecordTakenOverBySyncThatListsIt(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	rw := recordFormatsWorkspace(t, decisionsOnly)
	dir := draftsDir(rw)
	mcp := writeFormatSource(t, ".mcp.json", mcpSourceBody(t))
	mustImport(t, rw, mcp, Options{})
	older := writeOlderRecord(t, dir)
	mcpFiles := filesOf(t, dir, older)

	res := mustImport(t, rw, mcp, Options{Sync: true})
	if res.Format != "mcp" || len(res.Drift) != 0 || len(res.Removed) != 0 ||
		!slices.Equal(res.Written, []string{RecordFileName}) {
		t.Fatalf("the sync read %q, drifted %+v, wrote %v and removed %v; want mcp, in step, the record alone "+
			"written", res.Format, res.Drift, res.Written, res.Removed)
	}
	rec, _ := recordBeside(t, dir)
	if got := recordFormats(rec); !slices.Equal(got, []string{"mcp"}) {
		t.Fatalf("the record holds the entries %q, want mcp alone", got)
	}
	took := entryIn(t, rec, "mcp")
	took.Format = ""
	if !reflect.DeepEqual(took, older) {
		t.Errorf("the entry taken over is not the older one under its format:\n%+v\nwant\n%+v", took, older)
	}
	if got := filesOf(t, dir, older); !reflect.DeepEqual(got, mcpFiles) {
		t.Errorf("the sync in step changed the MCP drafts")
	}
}

// TestRecordFormatsOlderRecordRefusedBySyncThatDoesNot syncs a Compose source
// over the record an older version wrote of an MCP import: the record lists none
// of the files the Compose import drafts, so the sync is refused naming the
// record, under a dry run too, and nothing is written or removed — the MCP
// drafts the record lists are not taken for files the Compose source dropped.
//
// 🔴 PLANTED DEFECT: have the sync take over an entry naming no format whatever
// it lists (internal/importops/import.go): the sync removes the MCP drafts as
// dropped by the source, and the case reddens.
func TestRecordFormatsOlderRecordRefusedBySyncThatDoesNot(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	rw := recordFormatsWorkspace(t, decisionsOnly)
	dir := draftsDir(rw)
	mustImport(t, rw, writeFormatSource(t, ".mcp.json", mcpSourceBody(t)), Options{})
	older := writeOlderRecord(t, dir)
	mcpFiles := filesOf(t, dir, older)
	_, before := recordBeside(t, dir)
	compose := writeFormatSource(t, "compose.yaml", composeSourceBody(t))

	for _, delivery := range []authoredcontent.Delivery{authoredcontent.DeliveryReport, authoredcontent.DeliveryWrite} {
		_, err := importInto(rw, compose, Options{Sync: true, Delivery: delivery})
		var refused *ImportRecordError
		if !errors.As(err, &refused) {
			t.Fatalf("the sync returned %v; want an ImportRecordError", err)
		}
		if !refused.Unclaimed || refused.Path != "" || refused.Err != nil || refused.Format != "" {
			t.Errorf("the refusal is %+v; want the record's entry naming no format unclaimed", refused)
		}
		if !strings.Contains(err.Error(), RecordFileName) || !strings.Contains(err.Error(), "--sync") {
			t.Errorf("the refusal %q does not name the record and --sync", err.Error())
		}
	}
	if _, after := recordBeside(t, dir); after != before {
		t.Errorf("the refused sync changed the record:\n%s", after)
	}
	if got := filesOf(t, dir, older); !reflect.DeepEqual(got, mcpFiles) {
		t.Errorf("the refused sync changed or removed an MCP draft")
	}
	if _, err := os.Stat(filepath.Join(dir, "import-decisions.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refused sync wrote the Compose draft (%v)", err)
	}
}

// TestRecordFormatsPathOfAnotherFormatRefuses imports an MCP configuration whose
// importer drafts an Aggregate at the path a Compose import drafted one: an
// import that is not a sync, a dry run among them, is refused naming the Compose
// import and drafting beneath another directory — not a sync, which would not
// bring that Aggregate up to date — and a sync of the MCP format is refused naming
// the Compose import; nothing is written.
//
// 🔴 PLANTED DEFECT: drop the reading of the other formats' entries from
// checkDraftPlaces (internal/importops/import.go): both refusals name no format,
// and the case reddens.
func TestRecordFormatsPathOfAnotherFormatRefuses(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	rw := recordFormatsWorkspace(t, nil)
	dir := draftsDir(rw)
	mustImport(t, rw, writeFormatSource(t, "compose.yaml", composeSourceBody(t)), Options{})
	rec, before := recordBeside(t, dir)
	composeFiles := filesOf(t, dir, entryIn(t, rec, "compose"))
	mcp := writeFormatSource(t, ".mcp.json", mcpSourceBody(t))

	for _, delivery := range []authoredcontent.Delivery{authoredcontent.DeliveryReport, authoredcontent.DeliveryWrite} {
		_, err := importInto(rw, mcp, Options{Delivery: delivery})
		var existing *ExistingEntityError
		if !errors.As(err, &existing) {
			t.Fatalf("the import returned %v; want an ExistingEntityError", err)
		}
		if existing.Path != "aggregate.yaml" || existing.Kind != entitykind.Aggregate || existing.Format != "compose" {
			t.Errorf("the refusal names %q as %s of the format %q; want aggregate.yaml as an Aggregate of compose",
				existing.Path, existing.Kind, existing.Format)
		}
		for _, want := range []string{"compose import", "aggregate.yaml", "--output-dir"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal %q does not name %q", err.Error(), want)
			}
		}
		if strings.Contains(err.Error(), "--sync") {
			t.Errorf("the refusal %q offers --sync, which would not bring another import's Aggregate up to date",
				err.Error())
		}
	}

	_, err := importInto(rw, mcp, Options{Sync: true, Format: "mcp"})
	var refused *ImportRecordError
	if !errors.As(err, &refused) {
		t.Fatalf("the sync returned %v; want an ImportRecordError", err)
	}
	if refused.Path != "aggregate.yaml" || refused.Kind != entitykind.Aggregate || refused.Format != "compose" ||
		refused.Err != nil || refused.Unclaimed {
		t.Errorf("the refusal is %+v; want aggregate.yaml as an Aggregate the compose entry lists", refused)
	}
	if !strings.Contains(err.Error(), "compose import") {
		t.Errorf("the refusal %q does not name the compose import", err.Error())
	}

	if _, after := recordBeside(t, dir); after != before {
		t.Errorf("a refused import changed the record:\n%s", after)
	}
	if got := filesOf(t, dir, entryIn(t, rec, "compose")); !reflect.DeepEqual(got, composeFiles) {
		t.Errorf("a refused import changed a Compose draft")
	}
}

// TestRecordFormatsSyncWithoutFormatOverSeveralEntries syncs over a record
// holding a Compose entry and an MCP entry, naming no format. The Compose source
// is recognised by the importers the workspace's catalog folder holds before
// the published catalog is read, and syncs in step. A source no such importer
// recognises is refused before the published catalog is read, asking for the
// format and naming the record's, and nothing is written.
//
// It points BuildSeam to count the reads of the published catalog, so it does
// not run in parallel.
//
// 🔴 PLANTED DEFECT: have Import read the published catalog before it settles the
// format over a record of several entries (internal/importops/import.go): the
// refused sync reads it once, and the case reddens.
func TestRecordFormatsSyncWithoutFormatOverSeveralEntries(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	rw := recordFormatsWorkspace(t, decisionsOnly)
	dir := draftsDir(rw)
	compose := writeFormatSource(t, "compose.yaml", composeSourceBody(t))
	mustImport(t, rw, compose, Options{})
	mustImport(t, rw, writeFormatSource(t, ".mcp.json", mcpSourceBody(t)), Options{})

	var reads atomic.Int32
	realSeam := BuildSeam
	t.Cleanup(func() { BuildSeam = realSeam })
	BuildSeam = func(rw *workspaceconfig.ResolvedWorkspace, resolve PluginBytesResolver) (Seam, error) {
		seam, err := realSeam(rw, resolve)
		if err != nil || seam.readSnapshot == nil {
			return seam, err
		}
		read := seam.readSnapshot
		seam.readSnapshot = func(ctx context.Context, r catalogSnapshotRead) (Seam, catalogSnapshot, error) {
			reads.Add(1)
			return read(ctx, r)
		}
		return seam, nil
	}

	res, err := importInto(rw, compose, Options{Sync: true})
	if err != nil {
		t.Fatalf("the Compose sync naming no format was refused: %v", err)
	}
	if res.Format != "compose" || len(res.Written) != 0 || len(res.Drift) != 0 {
		t.Errorf("the Compose sync read %q, wrote %v and drifted %+v; want compose, in step", res.Format, res.Written,
			res.Drift)
	}
	if got := reads.Load(); got != 1 {
		t.Errorf("the Compose sync read the published catalog %d times, want once", got)
	}

	_, before := recordBeside(t, dir)
	reads.Store(0)
	mystery := writeFormatSource(t, "mystery.json", "{\"unrecognised\": true}\n")
	_, err = importInto(rw, mystery, Options{Sync: true})
	var undetected *UndetectedFormatError
	if !errors.As(err, &undetected) {
		t.Fatalf("the sync of an unrecognised source returned %v; want an UndetectedFormatError", err)
	}
	if !slices.Equal(undetected.Recorded, []string{"compose", "mcp"}) {
		t.Errorf("the refusal names the recorded formats %q, want compose and mcp", undetected.Recorded)
	}
	for _, want := range []string{"--format compose", "--format mcp", "mystery.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err.Error(), want)
		}
	}
	if got := reads.Load(); got != 0 {
		t.Errorf("the refused sync read the published catalog %d times; it is refused before reading it", got)
	}
	if _, after := recordBeside(t, dir); after != before {
		t.Errorf("the refused sync changed the record:\n%s", after)
	}
}

// TestRecordFormatsUnreadableRecord replaces the record beside an MCP draft with
// bytes that do not read as one: a sync is refused naming why, and leaves it as
// it is; a Compose import that is not a sync writes the record anew, holding its
// own entry alone, with a warning that the entries it held are lost.
//
// 🔴 PLANTED DEFECT: drop the warning composed for a record that cannot be read
// (internal/importops/import.go): the import warns of nothing, and the case
// reddens.
func TestRecordFormatsUnreadableRecord(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	rw := recordFormatsWorkspace(t, decisionsOnly)
	dir := draftsDir(rw)
	mcp := writeFormatSource(t, ".mcp.json", mcpSourceBody(t))
	mustImport(t, rw, mcp, Options{})
	place := filepath.Join(dir, RecordFileName)
	const unreadable = "not a record\n"
	if err := os.WriteFile(place, []byte(unreadable), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := importInto(rw, mcp, Options{Sync: true})
	var refused *ImportRecordError
	if !errors.As(err, &refused) || refused.Err == nil {
		t.Fatalf("the sync over a record that cannot be read returned %v; want an ImportRecordError naming why", err)
	}
	if got, _ := os.ReadFile(place); string(got) != unreadable {
		t.Errorf("the refused sync changed the record: %q", got)
	}

	res := mustImport(t, rw, writeFormatSource(t, "compose.yaml", composeSourceBody(t)), Options{})
	var warned []abi.Diagnostic
	for _, d := range res.Diagnostics {
		if d.Severity == abi.SeverityWarn && strings.Contains(d.Message, RecordFileName) {
			warned = append(warned, d)
		}
	}
	if len(warned) != 1 || !strings.Contains(warned[0].Message, "cannot be read") ||
		!strings.Contains(warned[0].Message, "is lost") {
		t.Errorf("the import warned %+v; want one warning that the record cannot be read and what it held is lost",
			warned)
	}
	rec, _ := recordBeside(t, dir)
	if got := recordFormats(rec); !slices.Equal(got, []string{"compose"}) {
		t.Errorf("the record written anew holds the entries %q, want compose alone", got)
	}
}
