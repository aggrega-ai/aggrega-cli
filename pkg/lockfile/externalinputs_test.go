// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

func externalInputsLockfile(stamp string) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version:     1,
		Release:     "release.yaml",
		GeneratedAt: stamp,
		ExternalInputs: []lockfile.ExternalInput{
			// Deliberately out of URL order, with unsorted and duplicated
			// provenance, so canonicalisation has real work to do.
			{
				URL:        "oci://localhost:5000/inputs/chart:1.0.0",
				Sha256:     strings.Repeat("b", 64),
				Size:       2048,
				RequiredBy: []string{"recipe.helm.z", "recipe.helm.a", "recipe.helm.z"},
			},
			{
				URL:        "https://example.com/schemas/base.json",
				Sha256:     strings.Repeat("a", 64),
				Size:       512,
				RequiredBy: []string{"provider.kubernetes"},
			},
		},
	}
}

// TestExternalInputsByteIdentity proves two independent WriteLockfile
// invocations over the same in-memory lockfile carrying external-input pins
// produce byte-identical output — the deterministic-output invariant extends
// to the external_inputs section.
func TestExternalInputsByteIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const stamp = "2026-07-01T00:00:00Z"
	pathA := filepath.Join(dir, "a.lock")
	pathB := filepath.Join(dir, "b.lock")

	if err := lockfile.WriteLockfile(pathA, externalInputsLockfile(stamp)); err != nil {
		t.Fatalf("write A: %v", err)
	}
	if err := lockfile.WriteLockfile(pathB, externalInputsLockfile(stamp)); err != nil {
		t.Fatalf("write B: %v", err)
	}
	a, _ := os.ReadFile(pathA)
	b, _ := os.ReadFile(pathB)
	if !bytes.Equal(a, b) {
		t.Fatalf("lockfile bytes differ across independent writes:\n--- A ---\n%s\n--- B ---\n%s", a, b)
	}
}

// TestExternalInputsRoundTrip proves an external-input pin survives a
// write/load round-trip (including the schema-validation gate inside
// LoadLockfile) with entries sorted by URL and provenance sorted + deduped.
func TestExternalInputsRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "rt.lock")
	if err := lockfile.WriteLockfile(path, externalInputsLockfile("2026-07-01T00:00:00Z")); err != nil {
		t.Fatalf("write: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(lf.ExternalInputs) != 2 {
		t.Fatalf("external_inputs round-trip lost entries: %+v", lf.ExternalInputs)
	}
	// Canonical order is by URL: "https://..." sorts before "oci://...".
	first, second := lf.ExternalInputs[0], lf.ExternalInputs[1]
	if first.URL != "https://example.com/schemas/base.json" ||
		second.URL != "oci://localhost:5000/inputs/chart:1.0.0" {
		t.Errorf("external_inputs not sorted by URL: [%q, %q]", first.URL, second.URL)
	}
	if first.Sha256 != strings.Repeat("a", 64) || first.Size != 512 {
		t.Errorf("https entry round-trip lost data: %+v", first)
	}
	if len(first.RequiredBy) != 1 || first.RequiredBy[0] != "provider.kubernetes" {
		t.Errorf("https entry required_by round-trip lost data: %+v", first.RequiredBy)
	}
	if second.Sha256 != strings.Repeat("b", 64) || second.Size != 2048 {
		t.Errorf("oci entry round-trip lost data: %+v", second)
	}
	want := []string{"recipe.helm.a", "recipe.helm.z"}
	if len(second.RequiredBy) != len(want) ||
		second.RequiredBy[0] != want[0] || second.RequiredBy[1] != want[1] {
		t.Errorf("required_by should be sorted and deduped to %v, got %v", want, second.RequiredBy)
	}
}

// TestCanonicaliseDoesNotAliasExternalInputRequiredBy proves WriteLockfile's
// canonicalise reallocates the RequiredBy slice so the caller's value is
// neither reordered nor deduplicated in place.
func TestCanonicaliseDoesNotAliasExternalInputRequiredBy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lf := externalInputsLockfile("2026-07-01T00:00:00Z")
	if err := lockfile.WriteLockfile(filepath.Join(dir, "c.lock"), lf); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := lf.ExternalInputs[0].RequiredBy
	want := []string{"recipe.helm.z", "recipe.helm.a", "recipe.helm.z"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("WriteLockfile mutated the caller's RequiredBy slice: %v", got)
	}
}
