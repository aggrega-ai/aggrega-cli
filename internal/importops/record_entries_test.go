// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"strings"
	"testing"
)

func entryOf(format, target string, paths ...string) recordEntry {
	e := recordEntry{Format: format, Target: target, Files: []recordFile{}}
	for _, p := range paths {
		e.Files = append(e.Files, recordFile{Path: p, Digest: digestOf(p)})
	}
	return e
}

func TestRecordEntriesReadOlderRecordAsOneEntry(t *testing.T) {
	t.Parallel()
	older := `{"target": "compose", "files": [{"path": "a.yaml", "digest": "` + digestOf("a") + `"}]}`
	rec, err := parseRecord([]byte(older))
	if err != nil {
		t.Fatalf("an older record is refused: %v", err)
	}
	if len(rec.Imports) != 1 {
		t.Fatalf("an older record reads as %d entries, want 1: %+v", len(rec.Imports), rec)
	}
	e := rec.Imports[0]
	if e.Format != "" || e.Target != "compose" || len(e.Files) != 1 || e.Files[0].Path != "a.yaml" {
		t.Errorf("the older record's entry = %+v; want no format, target compose, file a.yaml", e)
	}
	if _, ok := rec.entry(""); !ok {
		t.Errorf("entry(\"\") finds nothing in an older record")
	}
}

func TestRecordEntriesSortedByFormat(t *testing.T) {
	t.Parallel()
	rec := importRecord{Imports: []recordEntry{entryOf("terraform", "", "t.yaml"), entryOf("", "", "o.yaml"),
		entryOf("bicep", "", "b.yaml")}}
	got := recordBytes(rec)
	at := func(s string) int { return strings.Index(got, s) }
	if !(at("o.yaml") < at(`"bicep"`) && at(`"bicep"`) < at(`"terraform"`)) {
		t.Errorf("entries are not sorted by format, no format first:\n%s", got)
	}
	if !strings.HasPrefix(got, "{\n  \"imports\": [\n    {\n      \"files\"") {
		t.Errorf("the entry with no format does not open the record with its files:\n%s", got)
	}
	if !strings.Contains(got, "\"format\": \"bicep\",\n") || !strings.HasSuffix(got, "}\n") || strings.HasSuffix(got, "\n\n") {
		t.Errorf("the record is not the declared shape with one trailing newline:\n%s", got)
	}
	if f, tg := strings.Index(got, `"format": "terraform"`), strings.LastIndex(got, `"files"`); f > tg {
		t.Errorf("format does not precede files:\n%s", got)
	}
}

func TestRecordEntriesWithKeepsOtherEntries(t *testing.T) {
	t.Parallel()
	rec := importRecord{Imports: []recordEntry{entryOf("bicep", "", "b.yaml"), entryOf("terraform", "", "t.yaml")}}
	next := rec.with(entryOf("bicep", "compose", "b2.yaml"))
	if len(next.Imports) != 2 {
		t.Fatalf("replacing an entry gave %d entries, want 2", len(next.Imports))
	}
	if b, _ := next.entry("bicep"); b.Target != "compose" || b.Files[0].Path != "b2.yaml" {
		t.Errorf("the bicep entry was not replaced: %+v", b)
	}
	if tf, _ := next.entry("terraform"); tf.Files[0].Path != "t.yaml" {
		t.Errorf("the terraform entry changed: %+v", tf)
	}
	if o, _ := rec.entry("bicep"); o.Target != "" {
		t.Errorf("with changed the receiver: %+v", o)
	}
	added := next.with(entryOf("cloudformation", "", "c.yaml"))
	if len(added.Imports) != 3 {
		t.Fatalf("adding an entry gave %d entries, want 3", len(added.Imports))
	}
	if _, ok := added.entry("missing"); ok {
		t.Errorf("entry found a format the record does not hold")
	}
}

func TestRecordEntriesRefuseMixedShape(t *testing.T) {
	t.Parallel()
	d := digestOf("x")
	file := `{"path": "a.yaml", "digest": "` + d + `"}`
	for _, row := range []struct{ name, doc, want string }{
		{"imports and top-level files", `{"imports": [{"format": "a", "files": [` + file + `]}], "files": [` + file + `]}`,
			"both entries under"},
		{"imports and a top-level target", `{"imports": [], "target": "compose"}`, "both entries under"},
		{"two entries of one format", `{"imports": [{"format": "a", "files": [` + file + `]}, {"format": "a", "files": []}]}`,
			`more than one entry for the format "a"`},
		{"two entries with no format", `{"imports": [{"files": []}, {"files": []}]}`,
			`more than one entry for the format ""`},
		{"an entry file with no path", `{"imports": [{"format": "a", "files": [{"digest": "` + d + `"}]}]}`,
			"a file with no path"},
		{"an entry file named twice", `{"imports": [{"format": "a", "files": [` + file + `, ` + file + `]}]}`,
			`the file "a.yaml" more than once`},
		{"an entry snapshot not a digest", `{"imports": [{"format": "a", "catalogSnapshot": "nope", "files": []}]}`,
			`catalog snapshot "nope"`},
		{"an unknown entry field", `{"imports": [{"format": "a", "extra": 1, "files": []}]}`,
			`unknown field "extra"`},
	} {
		_, err := parseRecord([]byte(row.doc))
		if err == nil {
			t.Errorf("%s: the record is accepted", row.name)
		} else if !strings.Contains(err.Error(), row.want) {
			t.Errorf("%s: refused with %q; want it to name %q", row.name, err, row.want)
		}
	}
}

func TestRecordEntriesBytesStable(t *testing.T) {
	t.Parallel()
	rec := importRecord{Imports: []recordEntry{entryOf("b", "x", "1"), entryOf("a", "", "2")}}
	first := recordBytes(rec)
	for range 5 {
		if got := recordBytes(rec); got != first {
			t.Fatalf("the record's bytes differ between runs:\n%s\n%s", first, got)
		}
	}
	back, err := parseRecord([]byte(first))
	if err != nil || recordBytes(back) != first {
		t.Errorf("the record does not round-trip (%v):\n%s", err, first)
	}
	if rec.Imports[0].Format != "b" {
		t.Errorf("writing the record reordered the caller's entries")
	}
}

// sole is the record's first entry, for a case whose record holds the entry of
// one import, and a zero entry where it holds none.
func (r importRecord) sole() recordEntry {
	if len(r.Imports) == 0 {
		return recordEntry{}
	}
	return r.Imports[0]
}
