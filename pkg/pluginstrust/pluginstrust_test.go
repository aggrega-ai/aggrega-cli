// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginstrust

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

const (
	testSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testDigest = "sha256:" + testSHA256
)

// write is the common arrange step: a record written into a fresh temporary
// directory, with the path returned.
func write(t *testing.T, tr *Trust) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := Write(path, tr); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return path
}

// TestWriteThenLoadRoundTrips pins that a written record reads back with the
// same content. It is the baseline the ordering and idempotence tests below
// refine: without it, a byte-identity assertion could hold over bytes that no
// loader accepts.
func TestWriteThenLoadRoundTrips(t *testing.T) {
	t.Parallel()

	want := &Trust{
		Version: CurrentVersion,
		Plugins: []TrustedPlugin{{
			Plugin:          "governance/aggrega-security-baseline",
			ResolvedVersion: "1.0.0",
			Digest:          testDigest,
		}},
	}
	got, err := Load(write(t, want))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != want.Version {
		t.Errorf("version = %d; want %d", got.Version, want.Version)
	}
	if len(got.Plugins) != 1 {
		t.Fatalf("got %d record(s); want 1", len(got.Plugins))
	}
	if got.Plugins[0] != want.Plugins[0] {
		t.Errorf("record = %+v; want %+v", got.Plugins[0], want.Plugins[0])
	}
}

// TestWriteOrdersRecordsByCoordinate pins the canonical ordering. The input is
// deliberately in descending order, so an implementation that copied the slice
// without sorting would emit the input order and fail — an already-sorted input
// could not tell the two apart.
func TestWriteOrdersRecordsByCoordinate(t *testing.T) {
	t.Parallel()

	in := &Trust{
		Version: CurrentVersion,
		Plugins: []TrustedPlugin{
			{Plugin: "linter/naming", ResolvedVersion: "2.0.0", Digest: testDigest},
			{Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: testDigest},
		},
	}
	got, err := Load(write(t, in))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"governance/baseline", "linter/naming"}
	if len(got.Plugins) != len(want) {
		t.Fatalf("got %d record(s); want %d", len(got.Plugins), len(want))
	}
	for i, coordinate := range want {
		if got.Plugins[i].Plugin != coordinate {
			t.Errorf("record %d = %q; want %q", i, got.Plugins[i].Plugin, coordinate)
		}
	}
}

// TestWriteIsIdempotentOverTime pins the determinism scope that distinguishes
// this record from the lockfile beside it: the record carries no wall-clock
// stamp, so re-writing an unchanged resolution reproduces the same bytes rather
// than a one-line diff. That is what makes a committed record diffable — every
// diff is a real change.
//
// The two writes go to the same path, which is what a re-pull actually does.
func TestWriteIsIdempotentOverTime(t *testing.T) {
	t.Parallel()

	tr := &Trust{
		Version: CurrentVersion,
		Plugins: []TrustedPlugin{{Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: testDigest}},
	}
	path := write(t, tr)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read first write: %v", err)
	}
	// Calibration: an empty file would make the comparison below "" == "" and
	// the assertion would hold while measuring nothing.
	if len(first) == 0 {
		t.Fatal("the first write produced no bytes, so the comparison below asserts nothing")
	}

	if err := Write(path, tr); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read second write: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("re-writing an unchanged record moved the bytes:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestWriteDoesNotMutateTheCaller pins the deep copy in canonicalise. Without
// it the sort would reorder the caller's own slice, which is the failure the
// maintenance hazard recorded beside canonicalise warns about — silent, because
// the written file would still be correct.
func TestWriteDoesNotMutateTheCaller(t *testing.T) {
	t.Parallel()

	tr := &Trust{
		Version: CurrentVersion,
		Plugins: []TrustedPlugin{
			{Plugin: "linter/naming", ResolvedVersion: "2.0.0", Digest: testDigest},
			{Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: testDigest},
		},
	}
	write(t, tr)
	if tr.Plugins[0].Plugin != "linter/naming" {
		t.Errorf("Write reordered the caller's slice: first record is now %q", tr.Plugins[0].Plugin)
	}
}

// TestWrittenRecordCarriesTheGeneratedHeader pins that the explanation ships
// with the file. It is asserted by content rather than by presence alone,
// because a header that had lost its substance would still be a header: each
// clause below is a question a reader of an unfamiliar generated file has to
// answer from somewhere, and the file answering them itself is the point.
func TestWrittenRecordCarriesTheGeneratedHeader(t *testing.T) {
	t.Parallel()

	path := write(t, &Trust{Version: CurrentVersion})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body := string(raw)

	for _, want := range []string{
		"aggrega pull",              // who writes it
		"runs one of those plugins", // who reads it
		"Commit this",               // why committing it matters
		"verdict_inputs",            // where the rule-input record lives instead
		"\"signature\" block",       // the verified fact it carries about each artifact
		"carries no signer",         // what it does not carry
		"lockfile",                  // where the reproducibility pins live
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the generated header does not mention %q:\n%s", want, body)
		}
	}
}

// TestLoadToleratesTheGeneratedHeader pins that the header does not make the
// file unreadable — the header is emitted by the same package that parses the
// file, so a change breaking one and not the other is exactly the drift worth a
// test. Asserted through Load rather than a raw parse so the schema-validation
// step is covered too: a comment is not a property, so it must not trip the
// closed root.
func TestLoadToleratesTheGeneratedHeader(t *testing.T) {
	t.Parallel()

	path := write(t, &Trust{
		Version: CurrentVersion,
		Plugins: []TrustedPlugin{{Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: testDigest}},
	})
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load a file carrying the generated header: %v", err)
	}
	if len(got.Plugins) != 1 {
		t.Fatalf("got %d record(s); want 1", len(got.Plugins))
	}
}

// TestLoadRefusesASchemaViolation walks one violating document per constraint
// class the schema states, each asserted against the envelope condition rather
// than against "any error" — a loose assertion would keep passing once some
// earlier step rejected a different fault.
func TestLoadRefusesASchemaViolation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		doc  string
	}{
		{
			name: "the required version key is absent",
			doc:  "plugins: []\n",
		},
		{
			name: "an unknown top-level key",
			doc:  "version: 1\nsignatures: []\n",
		},
		{
			name: "an unknown key inside a record",
			doc:  "version: 1\nplugins:\n  - plugin: governance/baseline\n    resolved_version: 1.0.0\n    digest: " + testDigest + "\n    registry: registry.example.com\n",
		},
		{
			name: "a record missing its digest",
			doc:  "version: 1\nplugins:\n  - plugin: governance/baseline\n    resolved_version: 1.0.0\n",
		},
		{
			name: "a version the schema does not pin",
			doc:  "version: 2\n",
		},
		{
			name: "a document root that is not an object",
			doc:  "- governance/baseline\n",
		},
		{
			name: "a signature fact missing its mode",
			doc:  "version: 1\nplugins:\n  - plugin: governance/baseline\n    resolved_version: 1.0.0\n    digest: " + testDigest + "\n    signature:\n      bundle_digest: " + testDigest + "\n",
		},
		{
			name: "a signature fact naming a mode the schema does not admit",
			doc:  "version: 1\nplugins:\n  - plugin: governance/baseline\n    resolved_version: 1.0.0\n    digest: " + testDigest + "\n    signature:\n      bundle_digest: " + testDigest + "\n      mode: detached\n",
		},
		{
			name: "a signature fact whose bundle digest is not a sha256 digest",
			doc:  "version: 1\nplugins:\n  - plugin: governance/baseline\n    resolved_version: 1.0.0\n    digest: " + testDigest + "\n    signature:\n      bundle_digest: ../escape\n      mode: keyed\n",
		},
		{
			name: "an unknown key inside a signature fact",
			doc:  "version: 1\nplugins:\n  - plugin: governance/baseline\n    resolved_version: 1.0.0\n    digest: " + testDigest + "\n    signature:\n      bundle_digest: " + testDigest + "\n      mode: keyed\n      bundle: inline\n",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), FileName)
			if err := os.WriteFile(path, []byte(tc.doc), 0o644); err != nil {
				t.Fatalf("arrange: %v", err)
			}
			got, err := Load(path)
			if err == nil {
				t.Fatalf("Load accepted a document that %s: %+v", tc.name, got)
			}
			if !errors.Is(err, envelope.ErrEntityEnvelope()) {
				t.Errorf("Load refused a document that %s, but not as an envelope violation: %v", tc.name, err)
			}
		})
	}
}

// TestLoadAcceptsAMinimalRecord is the other direction, and it is not
// redundant: a loader refusing every document satisfies the table above
// completely. The minimal shape is the one a first write produces before any
// attachment has resolved, so it must be readable.
func TestLoadAcceptsAMinimalRecord(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("version: 1\nplugins: []\n"), 0o644); err != nil {
		t.Fatalf("arrange: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load a record covering no attachments yet: %v", err)
	}
	if len(got.Plugins) != 0 {
		t.Errorf("got %d record(s); want 0", len(got.Plugins))
	}
}

// TestLoadSurfacesAbsenceAsNotExist pins that absence is recognisable as
// absence. A caller has to distinguish "nothing has been recorded yet", which is
// a first-run state, from a damaged file — and it can only do that if the
// underlying condition survives the wrapping.
func TestLoadSurfacesAbsenceAsNotExist(t *testing.T) {
	t.Parallel()

	_, err := Load(filepath.Join(t.TempDir(), FileName))
	if err == nil {
		t.Fatal("Load of a missing file returned no error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load of a missing file: %v; want an error matching fs.ErrNotExist", err)
	}
}

// TestUpsertAddressesByCoordinate is the structural point of addressing records
// by coordinate: a version change edits the record that exists rather than
// adding a second one. Were the address the authored reference instead, the
// re-versioned entry would land under a new address and a changed version would
// be indistinguishable from a first sighting.
func TestUpsertAddressesByCoordinate(t *testing.T) {
	t.Parallel()

	tr := &Trust{Version: CurrentVersion}
	tr.Upsert(TrustedPlugin{Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: testDigest})
	tr.Upsert(TrustedPlugin{Plugin: "governance/baseline", ResolvedVersion: "2.0.0", Digest: testDigest})

	if len(tr.Plugins) != 1 {
		t.Fatalf("re-versioning produced %d record(s); want 1 — the coordinate is the address, so a version change is a field edit", len(tr.Plugins))
	}
	if tr.Plugins[0].ResolvedVersion != "2.0.0" {
		t.Errorf("resolved_version = %q; want %q", tr.Plugins[0].ResolvedVersion, "2.0.0")
	}
}

// TestUpsertDropsAnEmptyCoordinate pins the one input Upsert refuses. An empty
// address collides with every other empty one, so admitting it would make Find's
// answer depend on which empty record came first.
func TestUpsertDropsAnEmptyCoordinate(t *testing.T) {
	t.Parallel()

	tr := &Trust{Version: CurrentVersion}
	tr.Upsert(TrustedPlugin{ResolvedVersion: "1.0.0", Digest: testDigest})
	if len(tr.Plugins) != 0 {
		t.Errorf("a record with no coordinate was admitted: %+v", tr.Plugins)
	}
}

// TestFind covers the lookup in both directions plus the nil receiver, which a
// caller reaches before any file has been loaded.
func TestFind(t *testing.T) {
	t.Parallel()

	tr := &Trust{
		Version: CurrentVersion,
		Plugins: []TrustedPlugin{{Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: testDigest}},
	}

	got, ok := tr.Find("governance/baseline")
	if !ok {
		t.Fatal("Find missed a record that is present")
	}
	if got.ResolvedVersion != "1.0.0" {
		t.Errorf("resolved_version = %q; want %q", got.ResolvedVersion, "1.0.0")
	}

	// The socket segment is part of the address: the same bare name in another
	// socket is a different plugin, so it must not match.
	if _, ok := tr.Find("linter/baseline"); ok {
		t.Error("Find matched a record across sockets; the socket segment is part of the address")
	}

	var absent *Trust
	if _, ok := absent.Find("governance/baseline"); ok {
		t.Error("a nil Trust reported a record")
	}
}

// TestPathForPlacesTheRecordBesideItsRelease pins the sibling relationship. The
// name is fixed rather than derived from the Release stem, so two Releases in one
// directory share one record — which is correct, because what the record covers
// is resolved per directory rather than per Release.
func TestPathForPlacesTheRecordBesideItsRelease(t *testing.T) {
	t.Parallel()

	cases := []struct {
		release string
		want    string
	}{
		{filepath.Join("envs", "prod", "release.yaml"), filepath.Join("envs", "prod", FileName)},
		{filepath.Join("envs", "prod", "release-eu.yml"), filepath.Join("envs", "prod", FileName)},
		{"release.yaml", FileName},
	}
	for _, tc := range cases {
		if got := PathFor(tc.release); got != tc.want {
			t.Errorf("PathFor(%q) = %q; want %q", tc.release, got, tc.want)
		}
	}
}

// TestCurrentVersionMatchesTheSchemaConstant holds the version number's two
// homes together. The schema pins it as a `const` and this package writes it, so
// without this check the two could disagree and the product would emit records
// its own schema refuses. The schema's value is READ rather than restated: a
// restated copy would agree on the day it was written.
func TestCurrentVersionMatchesTheSchemaConstant(t *testing.T) {
	t.Parallel()

	var doc struct {
		Properties struct {
			Version struct {
				Const *int `json:"const"`
			} `json:"version"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schemas.PluginsTrustV1beta1), &doc); err != nil {
		t.Fatalf("decode the embedded schema: %v", err)
	}
	if doc.Properties.Version.Const == nil {
		t.Fatal("the schema pins no const at properties.version, so this check would compare against nothing")
	}
	if *doc.Properties.Version.Const != CurrentVersion {
		t.Errorf("the schema pins version %d but this package writes %d", *doc.Properties.Version.Const, CurrentVersion)
	}
}

// TestWriteRefusesANilRecord pins the guard rather than a panic: the contract
// this package holds everywhere else is that invalid input comes back as a
// wrapped error, and a panic emits nothing a caller can act on.
func TestWriteRefusesANilRecord(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Write panicked on a nil record: %v", r)
		}
	}()
	if err := Write(filepath.Join(t.TempDir(), FileName), nil); err == nil {
		t.Error("Write accepted a nil record")
	}
}

// TestRemoveDropsExactlyTheAddressedRecord covers the only removal in both
// directions, plus the two edges its siblings also guard.
//
// The neighbour row is the discriminating one: a record removed by coordinate must
// leave the same bare name in another socket alone, because the socket segment is
// part of the address. A removal matching on the bare name would pass a
// single-record test and quietly delete a second plugin here.
func TestRemoveDropsExactlyTheAddressedRecord(t *testing.T) {
	t.Parallel()

	tr := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{
		{Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: testDigest},
		{Plugin: "linter/baseline", ResolvedVersion: "2.0.0", Digest: testDigest},
	}}

	if !tr.Remove("governance/baseline") {
		t.Fatal("Remove reported nothing removed for a record that is present")
	}
	if _, held := tr.Find("governance/baseline"); held {
		t.Error("the removed record is still held")
	}
	if _, held := tr.Find("linter/baseline"); !held {
		t.Error("Remove reached the same bare name in another socket; the socket segment is part of the address")
	}

	if tr.Remove("governance/baseline") {
		t.Error("removing an absent record must report false")
	}
	if tr.Remove("") {
		t.Error("an empty coordinate addresses nothing and must remove nothing")
	}
	var absent *Trust
	if absent.Remove("governance/baseline") {
		t.Error("a nil Trust removes nothing")
	}
}

// TestRemoveEmptiesTheRecordWithoutDamagingIt is the load-bearing half of "an
// emptied record behaves as an absent one": removing the last entry leaves a
// record that still satisfies its schema and still holds, pins and refuses
// nothing.
//
// It is written against the emitted BYTES rather than against the in-memory value,
// because the failure it guards is the file: a record emptied into a shape the
// schema rejects would turn the one workspace that needs to clear its record into
// one whose record can no longer be read at all — a worse dead end than the one
// clearing it exists to escape.
func TestRemoveEmptiesTheRecordWithoutDamagingIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	tr := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{
		{Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: testDigest},
	}}
	if !tr.Remove("governance/baseline") {
		t.Fatal("Remove reported nothing removed for the only record")
	}
	if err := Write(path, tr); err != nil {
		t.Fatalf("write the emptied record: %v", err)
	}

	// It reads back through the same loader every consumer uses, so a shape the
	// schema rejects fails here rather than at the next build.
	reloaded, started, err := LoadOrStart(path)
	if err != nil {
		t.Fatalf("an emptied record must still satisfy its schema; got %v", err)
	}
	if started {
		t.Error("the file is on disk, so it was read rather than started")
	}
	if len(reloaded.Plugins) != 0 {
		t.Fatalf("the emptied record holds %d record(s); want none", len(reloaded.Plugins))
	}

	// The three empty-collection contracts that make emptied equivalent to absent.
	if _, held := reloaded.Find("governance/baseline"); held {
		t.Error("an emptied record must hold nothing")
	}
	if pins := reloaded.PinMaps(); len(pins.Pins) != 0 || len(pins.PinVersions) != 0 {
		t.Errorf("an emptied record must pin nothing; got %+v", pins)
	}
	if err := reloaded.HoldConfigurationToRecord(filepath.Join(dir, "release.yaml"), nil, func(string) (string, bool) {
		return "", false
	}); err != nil {
		t.Errorf("an emptied record must refuse nothing, exactly as an absent one does; got %v", err)
	}
}

// TestUnaccountedIsTheOneAnswerBothConsumersRead pins the derivation the refusal
// and the prune share, on the four inputs that behave differently.
//
// The incomplete row is the one that matters: an attachment whose plugin could not
// be identified might be the very one accounting for an entry, so an incomplete
// answer must name nothing. Reported as unaccounted for, it would refuse a build
// over a gate that is attached — or, on an upgrade, delete that gate's reviewed
// identity — on the strength of a read that failed.
func TestUnaccountedIsTheOneAnswerBothConsumersRead(t *testing.T) {
	t.Parallel()

	record := canonicalise(recordOf("governance/opa-gate", "linter/house-style"))

	cases := []struct {
		name      string
		accounted []string
		complete  bool
		want      string
	}{
		{
			name:      "everything_accounted_for_names_nothing",
			accounted: []string{"governance/opa-gate", "linter/house-style"},
			complete:  true,
			want:      "",
		},
		{
			name:      "one_unaccounted_for_names_that_one",
			accounted: []string{"governance/opa-gate"},
			complete:  true,
			want:      "linter/house-style",
		},
		{
			name:      "nothing_accounted_for_names_all_in_record_order",
			accounted: nil,
			complete:  true,
			want:      "governance/opa-gate,linter/house-style",
		},
		{
			name:      "an_incomplete_answer_names_nothing_however_little_it_covers",
			accounted: nil,
			complete:  false,
			want:      "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(record.Unaccounted(tc.accounted, tc.complete), ",")
			if got != tc.want {
				t.Errorf("Unaccounted(%v, %t) = %q; want %q", tc.accounted, tc.complete, got, tc.want)
			}
		})
	}

	var absent *Trust
	if got := absent.Unaccounted(nil, true); got != nil {
		t.Errorf("a nil Trust accounts for nothing and names nothing; got %v", got)
	}
}
