// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// misdeclaredRecord loads a catalog directory holding one document that declares
// the given kind, name and version, through the tolerant load a build annotates
// from, and returns the record that load keeps — so each row here hands the
// decoration exactly what a build hands it.
func misdeclaredRecord(t *testing.T, kind, name, version string) []catalog.SkippedEntity {
	t.Helper()
	ws, dir := misdeclaredCatalog(t, kind, name, version)
	_, skipped, err := catalog.LoadPassOneTolerant(ws, "", dir)
	if err != nil {
		t.Fatalf("the tolerant load failed: %v", err)
	}
	if len(skipped) != 1 || !skipped[0].KindNotHeld() {
		t.Fatalf("the load did not record the document as one of a kind no catalog holds: %+v", skipped)
	}
	return skipped
}

// misdeclaredCatalog writes a catalog directory holding one document that
// declares the given kind, name and version, and returns the workspace and the
// directory.
func misdeclaredCatalog(t *testing.T, kind, name, version string) (ws, dir string) {
	t.Helper()
	ws = t.TempDir()
	dir = filepath.Join(ws, "catalog")
	p := filepath.Join(dir, "feature", "nightly-backup", "feature.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "apiVersion: aggrega.ai/v1alpha1\nkind: " + kind + "\nmetadata:\n  name: " + name + "\n  version: " + version + "\nspec: {}\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws, dir
}

// TestMisdeclaredNoteIsOneAccountOnEveryBacking holds the account of a file of a
// kind no catalog holds to one answer whichever catalog backing recorded it: the
// eager load behind a build that reads a lockfile, and the lazy view behind a
// build that fills one, a pull, a Release-scoped validate, the assistant tools
// and the editor. The same failure annotated from either record reads the same,
// so a reader is told the same thing however the build reached the reference.
//
// It also holds the one-line rendering a single-line message carries to the
// same sentence and the same refusal, spelled with the surface's own path, on
// one line; to nothing for a failure carrying no reference or a name no such
// file declares; and the multi-line rendering to the shape the workspace's
// account takes, opening with a line break.
func TestMisdeclaredNoteIsOneAccountOnEveryBacking(t *testing.T) {
	t.Parallel()
	miss := answeringLookup{res: closure.Resolution{NotFound: true}}
	failure := walkFailure(t, miss)
	ws, dir := misdeclaredCatalog(t, "Featur", "local/nightly-backup", "1.0.0")
	_, eager, err := catalog.LoadPassOneTolerant(ws, "", dir)
	if err != nil {
		t.Fatalf("the tolerant load failed: %v", err)
	}
	lazy := catalog.NewLazyLookup(ws, filepath.Join(ws, "cache"), []string{dir}, nil).SkippedEntities()
	if len(lazy) != 1 || !lazy[0].KindNotHeld() || !stderrors.Is(lazy[0].Err, catalog.ErrUnknownCatalogDocumentKind()) {
		t.Fatalf("the lazy view did not record the document as one of a kind no catalog holds: %+v", lazy)
	}
	fromEager := AnnotateSkipContext(failure, eager, localCatalogNoteFixture)
	fromLazy := AnnotateSkipContext(failure, lazy, localCatalogNoteFixture)
	if fromEager.Error() != fromLazy.Error() {
		t.Errorf("the two backings tell the same failure differently\n--- eager ---\n%s\n--- lazy ---\n%s", fromEager, fromLazy)
	}
	if !strings.Contains(fromLazy.Error(), "a kind a catalog does not hold") {
		t.Errorf("the lazy record did not name the file beneath the failure:\n%s", fromLazy)
	}

	report := func(string) string { return "catalog/feature/nightly-backup/feature.yaml" }
	line := MisdeclaredFailureLine(failure, lazy, report)
	wantHead := "note: a catalog file declares the name this reference asks for under a kind a catalog does not hold, " +
		"so the load skipped it: [CATALOG_ERROR E0371] catalog/feature/nightly-backup/feature.yaml: " +
		"catalog document declares a kind a catalog does not hold (kind \"Featur\")"
	if !strings.HasPrefix(line, wantHead) || strings.Contains(line, "\n") {
		t.Errorf("the one-line note reads\n  %s\nwant one line opening\n  %s", line, wantHead)
	}
	if byName := MisdeclaredDocumentsLine(lazy, "local/nightly-backup", "", report); byName != line {
		t.Errorf("the note for the name differs from the note for the failure carrying it:\n  %s\n  %s", byName, line)
	}
	if got := MisdeclaredDocumentsLine(lazy, "local/other", "", report); got != "" {
		t.Errorf("a name no such file declares got a note: %s", got)
	}
	if got := MisdeclaredFailureLine(closure.ErrUnresolvableURI(), lazy, report); got != "" {
		t.Errorf("a failure carrying no reference got a note: %s", got)
	}
	if note := MisdeclaredDocumentsNote(lazy, "local/nightly-backup", ""); !strings.HasPrefix(note, "\n  note: a catalog file declares") {
		t.Errorf("the multi-line note does not take the account's shape: %q", note)
	}
}

// TestAnnotateSkipContextNamesAKindNoCatalogHoldsOnlyWhereItExplains holds the
// decoration of an unresolvable reference to its account of a file the load
// skipped for declaring a kind no catalog holds.
//
// Where that file declares the name the reference asks for, the failure keeps
// leading — nothing is promoted ahead of it — the note names the file and the
// kind it declares, and the account of where local entities are read from is
// left out, because the file is already where the catalog reads. Where it
// declares another name or another version, it is left out and the report is
// byte-for-byte the one an empty record produces.
func TestAnnotateSkipContextNamesAKindNoCatalogHoldsOnlyWhereItExplains(t *testing.T) {
	t.Parallel()
	miss := answeringLookup{res: closure.Resolution{NotFound: true}}
	for _, tc := range []struct {
		name    string
		kind    string
		docName string
		version string
		listed  bool
	}{
		{name: "a misspelled kind declaring the name and version referenced", kind: "Featur", docName: "local/nightly-backup", version: "1.0.0", listed: true},
		{name: "a kind in the wrong case, declaring the name referenced", kind: "feature", docName: "local/nightly-backup", version: "1.0.0", listed: true},
		{name: "a misspelled kind declaring another name", kind: "Featur", docName: "local/other", version: "1.0.0"},
		{name: "a misspelled kind declaring another version", kind: "Featur", docName: "local/nightly-backup", version: "2.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			failure := walkFailure(t, miss)
			skipped := misdeclaredRecord(t, tc.kind, tc.docName, tc.version)
			got := AnnotateSkipContext(failure, skipped, localCatalogNoteFixture)
			if !stderrors.Is(got, closure.ErrUnresolvableURI()) {
				t.Fatalf("the decoration lost the unresolvable-reference condition: %v", got)
			}
			if !tc.listed {
				want := AnnotateSkipContext(failure, nil, localCatalogNoteFixture)
				if got.Error() != want.Error() {
					t.Errorf("a file naming nothing this reference asks for changed the report\n--- got ---\n%s\n--- want ---\n%s", got, want)
				}
				return
			}
			sce, ok := got.(*SkipContextError)
			if !ok {
				t.Fatalf("the decoration is not a skip-context error: %T", got)
			}
			if _, promoted := sce.PromotedRefusal(); promoted {
				t.Error("a file of a kind no catalog holds was promoted ahead of the failure")
			}
			if sce.Leading() != failure {
				t.Errorf("the failure does not lead the report: %v", sce.Leading())
			}
			note := sce.Note()
			for _, want := range []string{skipped[0].Path, `"` + tc.kind + `"`, "E0371", "a kind a catalog does not hold"} {
				if !strings.Contains(note, want) {
					t.Errorf("the note does not carry %q:\n%s", want, note)
				}
			}
			if strings.Contains(note, localCatalogNoteFixture) {
				t.Errorf("the account of where local entities are read from was kept beside a file the catalog read:\n%s", note)
			}
		})
	}
}
