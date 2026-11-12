// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// seedRowInSection appends one row to the named lockfile section, carrying uri as
// its identity, and reports whether the section exists and could be seeded. It
// works over any section whose element type declares a URI string field, which is
// every pinned section, so it needs no per-section arm and cannot fall behind a
// newly added one.
func seedRowInSection(lf *Lockfile, section, uri string) bool {
	field := reflect.ValueOf(lf).Elem().FieldByName(section)
	if !field.IsValid() || field.Kind() != reflect.Slice {
		return false
	}
	row := reflect.New(field.Type().Elem()).Elem()
	uriField := row.FieldByName("URI")
	if !uriField.IsValid() || uriField.Kind() != reflect.String {
		return false
	}
	uriField.SetString(uri)
	field.Set(reflect.Append(field, row))
	return true
}

// TestVerifiedFactRowsCoverEveryVerifiableClosureAxis is the lockfile half of the
// kind-independence property. The closure side asserts that every closure section
// is either verifiable or named exempt; this asserts the join lands: for every
// closure section the verify pass walks, the lockfile declares a section of the
// same name AND VerifiedFactRows yields its rows.
//
// Without it a newly covered closure axis could pass its own coverage assertion
// while its facts had nowhere to land: the pass would discover a signature and a
// withdrawal marker for the reference, find no row to record them on, and complete
// silently. Nothing else observes that, because a fact recorded nowhere looks
// exactly like an artifact with no fact.
//
// The check is fail-safe rather than a census: each axis must POSITIVELY produce
// its seeded identity, so a broken enumeration makes this fail rather than pass
// vacuously.
func TestVerifiedFactRowsCoverEveryVerifiableClosureAxis(t *testing.T) {
	axes := closure.Closure{}.VerifiableAxes()
	if len(axes) == 0 {
		t.Fatal("the closure declares no verifiable axes; the assertion would pass vacuously")
	}

	lf := &Lockfile{}
	wantURIs := make(map[string]string, len(axes))
	for _, axis := range axes {
		uri := "seeded-" + axis.Axis
		if !seedRowInSection(lf, axis.Axis, uri) {
			t.Errorf("the verify pass walks closure section %q, but the lockfile declares no pinned "+
				"section of that name to record its verified facts on", axis.Axis)
			continue
		}
		wantURIs[uri] = axis.Axis
	}

	got := map[string]bool{}
	for _, row := range lf.VerifiedFactRows() {
		got[row.URI] = true
	}
	for uri, section := range wantURIs {
		if !got[uri] {
			t.Errorf("VerifiedFactRows does not enumerate the %q section, so a fact discovered for a "+
				"reference on it would be recorded nowhere and the omission would be silent", section)
		}
	}
}

// TestVerifiedFactRowRecordsThroughToTheRow proves the view addresses rows in
// place. A view that copied its row out would accept every recorded fact and
// persist none — the failure mode with no symptom, since the pull would report the
// discovery it made and the lockfile would carry nothing.
func TestVerifiedFactRowRecordsThroughToTheRow(t *testing.T) {
	lf := &Lockfile{
		Entities:          []Entity{{URI: "core/compute/api-server", Digest: "sha256:e"}},
		SchemaLibraries:   []SchemaLibrary{{URI: "PortSpec", Digest: "sha256:s"}},
		MappingLibraries:  []MappingLibrary{{URI: "HelmValues", Digest: "sha256:m"}},
		WorkloadLibraries: []WorkloadLibrary{{URI: "Deployments", Digest: "sha256:w"}},
		ArtifactLibraries: []ArtifactLibrary{{URI: "Bundles", Digest: "sha256:a"}},
		Plugins:           []Plugin{{URI: "provider/helm", Digest: "sha256:p"}},
	}

	for _, row := range lf.VerifiedFactRows() {
		row.RecordDeprecation(&DeprecationRecord{Severity: "retracted", Message: "withdrawn"})
		row.RecordSignature(&VerifiedSignature{Mode: "keyed", BundleDigest: "sha256:b"})
	}

	// Read back through the sections themselves rather than through the view, so the
	// assertion cannot be satisfied by the view's own bookkeeping.
	check := func(section, uri string, dep *DeprecationRecord, sig *VerifiedSignature) {
		if dep == nil {
			t.Errorf("%s row %q recorded no marker; the view did not write through to the row", section, uri)
		}
		if sig == nil {
			t.Errorf("%s row %q recorded no signature; the view did not write through to the row", section, uri)
		}
	}
	check("entities", lf.Entities[0].URI, lf.Entities[0].Deprecation, lf.Entities[0].Signature)
	check("schema_libraries", lf.SchemaLibraries[0].URI, lf.SchemaLibraries[0].Deprecation, lf.SchemaLibraries[0].Signature)
	check("mapping_libraries", lf.MappingLibraries[0].URI, lf.MappingLibraries[0].Deprecation, lf.MappingLibraries[0].Signature)
	check("workload_libraries", lf.WorkloadLibraries[0].URI, lf.WorkloadLibraries[0].Deprecation, lf.WorkloadLibraries[0].Signature)
	check("artifact_libraries", lf.ArtifactLibraries[0].URI, lf.ArtifactLibraries[0].Deprecation, lf.ArtifactLibraries[0].Signature)
	check("plugins", lf.Plugins[0].URI, lf.Plugins[0].Deprecation, lf.Plugins[0].Signature)

	// Deprecation() reads the slot rather than a snapshot taken when the view was
	// built, so a fact recorded through one view is visible through the next.
	for _, row := range lf.VerifiedFactRows() {
		if row.Deprecation() == nil {
			t.Errorf("row %q reads back no marker through the view", row.URI)
		}
	}
}

// TestCanonicaliseClonesLibraryRecordedFacts pins the writer's no-aliasing
// contract on the new library slots. The canonical copy shares scalar fields by
// struct-copy but must clone every pointer, or a caller mutating the written output
// reaches back into the lockfile it was built from.
func TestCanonicaliseClonesLibraryRecordedFacts(t *testing.T) {
	sig := &VerifiedSignature{Mode: "keyed", BundleDigest: "sha256:b", Identity: "held-key"}
	dep := &DeprecationRecord{
		Severity: "retracted",
		Message:  "withdrawn",
		Signer:   &VerifiedSignature{Mode: "keyed", BundleDigest: "sha256:mb"},
	}
	lf := &Lockfile{
		SchemaLibraries:   []SchemaLibrary{{URI: "PortSpec", Signature: sig, Deprecation: dep}},
		MappingLibraries:  []MappingLibrary{{URI: "HelmValues", Signature: sig, Deprecation: dep}},
		WorkloadLibraries: []WorkloadLibrary{{URI: "Deployments", Signature: sig, Deprecation: dep}},
		ArtifactLibraries: []ArtifactLibrary{{URI: "Bundles", Signature: sig, Deprecation: dep}},
	}

	out := canonicalise(lf)

	pairs := []struct {
		section   string
		outSig    *VerifiedSignature
		outDep    *DeprecationRecord
		outSigner *VerifiedSignature
	}{
		{"schema_libraries", out.SchemaLibraries[0].Signature, out.SchemaLibraries[0].Deprecation, out.SchemaLibraries[0].Deprecation.Signer},
		{"mapping_libraries", out.MappingLibraries[0].Signature, out.MappingLibraries[0].Deprecation, out.MappingLibraries[0].Deprecation.Signer},
		{"workload_libraries", out.WorkloadLibraries[0].Signature, out.WorkloadLibraries[0].Deprecation, out.WorkloadLibraries[0].Deprecation.Signer},
		{"artifact_libraries", out.ArtifactLibraries[0].Signature, out.ArtifactLibraries[0].Deprecation, out.ArtifactLibraries[0].Deprecation.Signer},
	}
	for _, p := range pairs {
		if p.outSig == sig {
			t.Errorf("%s: the canonical copy aliases the input signature pointer", p.section)
		}
		if p.outDep == dep {
			t.Errorf("%s: the canonical copy aliases the input deprecation pointer", p.section)
		}
		if p.outSigner == dep.Signer {
			t.Errorf("%s: the canonical copy aliases the input marker-signer pointer", p.section)
		}
		// The clone must carry the same values — an unaliased copy of the wrong
		// content would pass the checks above and lose the fact.
		if p.outSig == nil || *p.outSig != *sig {
			t.Errorf("%s: the cloned signature does not match the input: %+v", p.section, p.outSig)
		}
		if p.outDep == nil || p.outDep.Severity != dep.Severity || p.outDep.Message != dep.Message {
			t.Errorf("%s: the cloned marker does not match the input: %+v", p.section, p.outDep)
		}
	}
}

// TestLibraryRecordedFactsRoundTripThroughTheValidatingLoad is the enforcement
// proof for the library row shape, not merely a declaration check. A schema
// rejects nothing by existing: what enforces it is the validating load, which
// checks a lockfile against the embedded schema and refuses an unknown key
// anywhere in it. So a library row carrying a recorded signature and marker is
// written, loaded back through that gate, and its facts read from the typed value.
//
// The calibration arm follows: the same load is given a library row with a key the
// schema does not declare and must REFUSE it. Without that, a clean load would only
// show that the loader accepted the bytes, which an unenforced schema also does.
func TestLibraryRecordedFactsRoundTripThroughTheValidatingLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.lock")
	lf := &Lockfile{
		Version:     1,
		Release:     "./release.yaml",
		GeneratedAt: "2026-08-07T09:30:00Z",
		SchemaLibraries: []SchemaLibrary{{
			URI:             "PortSpec",
			ResolvedVersion: "1.4.0",
			Digest:          "sha256:" + strings.Repeat("c", 64),
			ManifestDigest:  "sha256:" + strings.Repeat("d", 64),
			Signature:       &VerifiedSignature{Mode: "keyed", BundleDigest: "sha256:" + strings.Repeat("b", 64)},
			Deprecation: &DeprecationRecord{
				Severity: "retracted",
				Message:  "withdrawn: unsound port range",
				Signer:   &VerifiedSignature{Mode: "keyed", BundleDigest: "sha256:" + strings.Repeat("a", 64)},
			},
		}},
	}
	if err := WriteLockfile(path, lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	loaded, _, err := LoadLockfile(path)
	if err != nil {
		t.Fatalf("the validating load refused a library row carrying its recorded facts: %v", err)
	}
	if len(loaded.SchemaLibraries) != 1 {
		t.Fatalf("schema_libraries = %d rows; want 1", len(loaded.SchemaLibraries))
	}
	row := loaded.SchemaLibraries[0]
	if row.ManifestDigest != lf.SchemaLibraries[0].ManifestDigest {
		t.Errorf("manifest digest did not survive the round trip: got %q", row.ManifestDigest)
	}
	if row.Signature == nil || row.Signature.Mode != "keyed" {
		t.Errorf("the recorded signature did not survive the round trip: %+v", row.Signature)
	}
	if row.Deprecation == nil || row.Deprecation.Severity != "retracted" {
		t.Errorf("the recorded marker did not survive the round trip: %+v", row.Deprecation)
	}
	if row.Deprecation == nil || row.Deprecation.Signer == nil {
		t.Errorf("the marker's verified signer did not survive the round trip: %+v", row.Deprecation)
	}
}

// TestValidatingLoadRefusesAnUndeclaredLibraryKey calibrates the gate the test
// above relies on. A load that accepts everything would make that test pass while
// proving nothing about the schema, so this plants a key the library row shape does
// not declare and requires the load to refuse it.
func TestValidatingLoadRefusesAnUndeclaredLibraryKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.lock")
	body := "version: 1\n" +
		"release: ./release.yaml\n" +
		"generated_at: \"2026-08-07T09:30:00Z\"\n" +
		"schema_libraries:\n" +
		"  - uri: PortSpec\n" +
		"    resolved_version: 1.4.0\n" +
		"    digest: sha256:" + strings.Repeat("c", 64) + "\n" +
		"    planted_key: this key is declared nowhere in the library row shape\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write probe lockfile: %v", err)
	}

	if _, _, err := LoadLockfile(path); !errors.Is(err, ErrLockfileInvalid()) {
		t.Fatalf("the validating load must refuse an undeclared key on a library row; got %v", err)
	}
}
