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

// TestMutators_SettingsAndSourcesRoundTrip proves an attachment's pinned settings
// and content-addressed source files survive WriteLockfile → LoadLockfile (the
// schema-validating load admits the new keys), and that MutatorAttachments
// reconstructs them onto the ref's With — the settings inline and each pinned
// source as a PinnedSource carrying its base name and content — so a build reads
// settings and source content from the lockfile rather than live config or the
// host filesystem.
func TestMutators_SettingsAndSourcesRoundTrip(t *testing.T) {
	in := []lockfile.MutatorAttachment{{
		Plugin:   "mutator/tag-injector",
		Settings: map[string]any{"team": "checkout"},
		Sources: []lockfile.MutatorSourcePin{{
			Path:    "rules.rego",
			Content: "package rules\n",
			Digest:  "sha256:deadbeef",
		}},
	}}
	path := filepath.Join(t.TempDir(), "release.lock")
	if err := lockfile.WriteLockfile(path, mutatorSampleLockfile(in)); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile (schema validation is part of the load): %v", err)
	}
	if len(lf.Mutators) != 1 {
		t.Fatalf("mutators len = %d, want 1: %+v", len(lf.Mutators), lf.Mutators)
	}
	got := lf.Mutators[0]
	if got.Settings["team"] != "checkout" {
		t.Errorf("settings roundtrip: got %+v, want team=checkout", got.Settings)
	}
	if len(got.Sources) != 1 || got.Sources[0].Path != "rules.rego" ||
		got.Sources[0].Content != "package rules\n" || got.Sources[0].Digest != "sha256:deadbeef" {
		t.Errorf("source pin roundtrip: got %+v", got.Sources)
	}

	refs := lockfile.MutatorAttachments(lf)
	if len(refs) != 1 || refs[0].With == nil {
		t.Fatalf("MutatorAttachments reconstruction: got %+v", refs)
	}
	with := refs[0].With
	if with.Settings["team"] != "checkout" {
		t.Errorf("reconstructed settings: got %+v", with.Settings)
	}
	if len(with.PinnedSources) != 1 || with.PinnedSources[0].Path != "rules.rego" ||
		with.PinnedSources[0].Content != "package rules\n" {
		t.Errorf("reconstructed pinned sources: got %+v", with.PinnedSources)
	}
}

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
		if lf.Mutators[i].Plugin != in[i].Plugin || lf.Mutators[i].Function != in[i].Function {
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
