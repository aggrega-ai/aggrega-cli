// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// mutatorSampleLockfile returns a minimal schema-valid lockfile carrying the
// supplied mutator attachments, so a write→load roundtrip exercises the
// mutators section through the real writer and the schema-validating loader.
func mutatorSampleLockfile(mutators []lockfile.MutatorAttachment) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version:     1,
		Release:     "release.yaml",
		GeneratedAt: "2026-07-17T00:00:00Z",
		Mutators:    mutators,
	}
}

// TestMutators_OrderPreservedThroughRoundTrip proves the mutators section is
// carried through WriteLockfile → LoadLockfile in AUTHOR order — never sorted by
// coordinate, unlike the URI-sorted plugins / providers sections. The input
// order is deliberately reverse-lexicographic by coordinate so a stray sort
// would be observable, and the loader validates against the embedded schema so
// the roundtrip also proves the schema admits the new section.
func TestMutators_OrderPreservedThroughRoundTrip(t *testing.T) {
	in := []lockfile.MutatorAttachment{
		{Plugin: "mutator/zeta", Function: "custom_mutate"},
		{Plugin: "mutator/alpha"},
		{Plugin: "mutator/middle", Function: "mutate"},
	}
	path := filepath.Join(t.TempDir(), "release.lock")
	if err := lockfile.WriteLockfile(path, mutatorSampleLockfile(in)); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile (schema validation is part of the load): %v", err)
	}
	if len(lf.Mutators) != len(in) {
		t.Fatalf("mutators len = %d, want %d: %+v", len(lf.Mutators), len(in), lf.Mutators)
	}
	for i := range in {
		if lf.Mutators[i] != in[i] {
			t.Errorf("mutators[%d] = %+v, want %+v (author order must be preserved, not sorted)",
				i, lf.Mutators[i], in[i])
		}
	}
}

// TestMutators_EmptyFunctionOmitted proves an attachment with no export override
// serializes without a function key (the default mutate export) and reloads with
// an empty Function, while a non-empty override roundtrips verbatim.
func TestMutators_EmptyFunctionOmitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.lock")
	if err := lockfile.WriteLockfile(path, mutatorSampleLockfile([]lockfile.MutatorAttachment{
		{Plugin: "mutator/tag-injector"},
	})); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(raw); strings.Contains(got, "function:") {
		t.Errorf("default-export attachment must omit the function key; got:\n%s", got)
	}

	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	if len(lf.Mutators) != 1 || lf.Mutators[0].Plugin != "mutator/tag-injector" || lf.Mutators[0].Function != "" {
		t.Fatalf("attachment roundtrip: got %+v", lf.Mutators)
	}
}

// TestMutators_WriteIsDeterministic proves two independent writes of the same
// in-memory lockfile (with a multi-attachment mutators section) produce
// byte-identical output — the determinism contract, holding for the
// order-preserving section as it does for the sorted ones.
func TestMutators_WriteIsDeterministic(t *testing.T) {
	in := []lockfile.MutatorAttachment{
		{Plugin: "mutator/second", Function: "mutate"},
		{Plugin: "mutator/first"},
	}
	dir := t.TempDir()
	p1 := filepath.Join(dir, "a.lock")
	p2 := filepath.Join(dir, "b.lock")
	if err := lockfile.WriteLockfile(p1, mutatorSampleLockfile(in)); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := lockfile.WriteLockfile(p2, mutatorSampleLockfile(in)); err != nil {
		t.Fatalf("write b: %v", err)
	}
	a, _ := os.ReadFile(p1)
	b, _ := os.ReadFile(p2)
	if string(a) != string(b) {
		t.Fatalf("two writes of the same lockfile diverged:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
}
