// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The workspace documents the test validates, as closureFindingsWorkspace writes
// them.
const (
	mappingFixtureManifest  = "app-manifest.yaml"
	mappingFixtureAggregate = "app-agg.yaml"
)

// validateWorkspaceDocument drives one aggrega/validate request end to end, naming
// release, over the document at file (relative to the workspace root) with content
// as its buffer, and returns the diagnostics the reply carries.
func validateWorkspaceDocument(t *testing.T, ws, release, file, content string) []Diagnostic {
	t.Helper()
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	return validateContentOverTheWire(t, srv, file, content, release).Diagnostics
}

// TestServeValidatePublishesAManifestMappingFindingOnTheManifest drives the editor
// validation of a Release whose Manifest maps a type onto a Recipe implementing an
// unrelated one, over the Manifest and over the Aggregate, and requires the mapping
// finding on the Manifest — on its entry's own line where the buffer declares the
// entry, at the head of the document where it does not — and on the Aggregate not
// at all. A closure-level class naming no document, the withdrawal notice the
// fixture's record of pinned versions carries, stays on the Aggregate and is not
// published on the Manifest.
//
// Each row is one workspace with one mapping fault. The rows differ in the entry's
// key — written bare, and written with a version, whose dots a path walk would read
// as separators — and in whether the buffer sent for the Manifest still declares
// the entry the finding is about.
func TestServeValidatePublishesAManifestMappingFindingOnTheManifest(t *testing.T) {
	mismatch, ok := cerrors.ShortCodeOf(resolver.ErrManifestImplementsMismatch())
	if !ok || mismatch == "" {
		t.Fatal("the mapping-mismatch condition carries no short code, so nothing below can recognise its finding")
	}

	tests := []struct {
		name string
		// faultyKey is the key the inconsistent mapping entry is written under.
		faultyKey string
		// dropEntryFromBuffer sends the Manifest's buffer without the faulty
		// entry, as an unsaved edit would, while the file on disk keeps it.
		dropEntryFromBuffer bool
	}{
		{name: "a bare key is drawn on its entry", faultyKey: "demo/other"},
		{name: "a versioned key is drawn on its entry", faultyKey: "demo/other:1.0.0"},
		{name: "an entry the buffer does not declare is drawn at the head", faultyKey: "demo/other", dropEntryFromBuffer: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws, release := closureFindingsWorkspace(t, true)
			manifestPath := filepath.Join(ws, mappingFixtureManifest)
			raw, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatalf("read the fixture Manifest: %v", err)
			}
			const faultyLine = "        demo/other: demo-unrelated:1.0.0\n"
			if !strings.Contains(string(raw), faultyLine) {
				t.Fatalf("the fixture Manifest no longer writes the inconsistent entry this test moves:\n%s", raw)
			}
			onDisk := strings.Replace(string(raw), faultyLine,
				"        "+tc.faultyKey+": demo-unrelated:1.0.0\n", 1)
			writeFixtureFile(t, manifestPath, onDisk)
			buffer := onDisk
			if tc.dropEntryFromBuffer {
				buffer = strings.Replace(onDisk, "        "+tc.faultyKey+": demo-unrelated:1.0.0\n", "", 1)
			}

			// The withdrawal notices, read from the shared report over the record
			// the fixture wrote, and calibrated so the Aggregate half cannot pass
			// over an empty expectation.
			lf, err := lockfile.LoadIfReadable(ws, lockfile.PathFor(filepath.Join(ws, release)))
			if err != nil || lf == nil {
				t.Fatalf("read the fixture's record of pinned versions: lockfile=%v err=%v", lf, err)
			}
			var notices []string
			for _, f := range compilerops.DeprecationReport(lf, nil) {
				notices = append(notices, f.Notice)
			}
			if len(notices) == 0 {
				t.Fatal("the fixture records no withdrawal, so the class that stays on the Aggregate is not exercised")
			}

			manifestDiags := validateWorkspaceDocument(t, ws, release, mappingFixtureManifest, buffer)
			aggBody, err := os.ReadFile(filepath.Join(ws, mappingFixtureAggregate))
			if err != nil {
				t.Fatalf("read the fixture Aggregate: %v", err)
			}
			aggDiags := validateWorkspaceDocument(t, ws, release, mappingFixtureAggregate, string(aggBody))
			t.Logf("Manifest answer: %+v", manifestDiags)
			t.Logf("Aggregate answer: %+v", aggDiags)

			wantLine, wantColumn := 1, 1
			if !tc.dropEntryFromBuffer {
				// The key's own colon is part of the prefix matched, so a bare key
				// is not found on a line writing a longer key that starts with it.
				wantLine, wantColumn = keyPosition(t, buffer, tc.faultyKey+":")
			}

			var onManifest []Diagnostic
			for _, d := range manifestDiags {
				if d.ShortCode == mismatch {
					onManifest = append(onManifest, d)
				}
				for _, n := range notices {
					if strings.Contains(d.Message, n) {
						t.Errorf("a withdrawal notice names no document and stays on the Aggregate, yet the "+
							"Manifest carries it: %+v", d)
					}
				}
			}
			if len(onManifest) != 1 {
				t.Fatalf("the Manifest must carry the mapping finding exactly once; it carries %d: %+v",
					len(onManifest), manifestDiags)
			}
			got := onManifest[0]
			if got.Line != wantLine || got.Column != wantColumn {
				t.Errorf("the mapping finding is drawn at %d:%d; want %d:%d (%+v)",
					got.Line, got.Column, wantLine, wantColumn, got)
			}
			if !tc.dropEntryFromBuffer && got.EndColumn != wantColumn+len(tc.faultyKey) {
				t.Errorf("the mapping finding spans to column %d; want the end of the key %q at %d",
					got.EndColumn, tc.faultyKey, wantColumn+len(tc.faultyKey))
			}
			if got.Severity != editorSeverityError {
				t.Errorf("the mapping finding is published at %q; a mapping that does not hold is an error", got.Severity)
			}
			if !strings.Contains(got.Message, tc.faultyKey) {
				t.Errorf("the mapping finding does not name the entry's key %q: %s", tc.faultyKey, got.Message)
			}

			for _, d := range aggDiags {
				if d.ShortCode == mismatch {
					t.Errorf("the mapping finding is the Manifest's to carry, yet the Aggregate carries it too: %+v", d)
				}
			}
			for _, n := range notices {
				found := false
				for _, d := range aggDiags {
					if strings.Contains(d.Message, n) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("a withdrawal notice names no document and stays on the Aggregate, yet the Aggregate "+
						"does not carry it.\n  notice: %s\n  answer: %+v", n, aggDiags)
				}
			}
		})
	}
}
