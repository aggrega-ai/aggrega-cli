// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package docsplice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpliceAppendsWhenMarkersAbsent(t *testing.T) {
	got, err := Splice("curated prose\n", "id1", "generated")
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	want := "curated prose\n\n<!-- BEGIN GENERATED: id1 -->\ngenerated\n<!-- END GENERATED: id1 -->\n"
	if got != want {
		t.Fatalf("append mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestSpliceIsIdempotent(t *testing.T) {
	first, err := Splice("curated\n", "id1", "gen v1")
	if err != nil {
		t.Fatalf("first splice: %v", err)
	}
	second, err := Splice(first, "id1", "gen v1")
	if err != nil {
		t.Fatalf("second splice: %v", err)
	}
	if first != second {
		t.Fatalf("splice not idempotent:\n first  %q\n second %q", first, second)
	}
}

func TestSpliceRefreshesInPlaceAndPreservesSurroundingProse(t *testing.T) {
	page := "# Title\n\nintro\n\n<!-- BEGIN GENERATED: id1 -->\nold\n<!-- END GENERATED: id1 -->\n\ntrailing curated section\n"
	got, err := Splice(page, "id1", "new content")
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	if !strings.Contains(got, "# Title\n\nintro\n") {
		t.Errorf("leading prose lost:\n%s", got)
	}
	if !strings.Contains(got, "\n\ntrailing curated section\n") {
		t.Errorf("trailing prose lost:\n%s", got)
	}
	if strings.Contains(got, "old") {
		t.Errorf("stale generated content retained:\n%s", got)
	}
	if !strings.Contains(got, "\nnew content\n") {
		t.Errorf("new generated content missing:\n%s", got)
	}
}

func TestSpliceRejectsUnbalancedMarkers(t *testing.T) {
	if _, err := Splice("<!-- BEGIN GENERATED: id1 -->\nx\n", "id1", "y"); err == nil {
		t.Fatal("expected error for missing end marker")
	}
	if _, err := Splice("<!-- END GENERATED: id1 -->\n", "id1", "y"); err == nil {
		t.Fatal("expected error for missing begin marker")
	}
}

// TestSpliceRejectsReversedMarkers covers the third refusal, which the splice
// tests inherited from their previous home did not reach: markers present but in
// the wrong order. Without it, the reversed-order branch is unexercised, and a
// refusal nobody has watched fire is not known to fire.
func TestSpliceRejectsReversedMarkers(t *testing.T) {
	page := "<!-- END GENERATED: id1 -->\nbody\n<!-- BEGIN GENERATED: id1 -->\n"
	if _, err := Splice(page, "id1", "y"); err == nil {
		t.Fatal("expected error when the end marker precedes the begin marker")
	}
}

// TestWriteFileSkipsAnIdenticalWrite pins the no-op property the freshness stamps
// depend on: regenerating identical content must leave the file untouched, so a
// gate keyed on the page's modification time does not invalidate itself.
func TestWriteFileSkipsAnIdenticalWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.md")
	initial := "<!-- BEGIN GENERATED: id1 -->\nsame\n<!-- END GENERATED: id1 -->\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	changed, err := WriteFile(path, initial, "id1", "same")
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if changed {
		t.Error("identical content reported as changed")
	}

	changed, err = WriteFile(path, initial, "id1", "different")
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !changed {
		t.Error("differing content reported as unchanged")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(got), "\ndifferent\n") {
		t.Errorf("new content not written:\n%s", got)
	}
}
