// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// TestMutators_SettingsAndSourcesRoundTrip proves an attachment's pinned settings
// and content-addressed source files survive WriteLockfile → LoadLockfile (the
// schema-validating load admits the new keys), and that MutatorAttachments
// reconstructs them onto the ref's With — the settings inline and each pinned
// source as a PinnedSource carrying its base name and content — so a build reads
// settings and source content from the lockfile rather than live config or the
// host filesystem.
func TestMutators_SettingsAndSourcesRoundTrip(t *testing.T) {
	const content = "package rules\n"
	sourceDigest := fixtureDigest(content)
	in := []lockfile.MutatorAttachment{{
		Plugin:   "governance/tag-injector",
		Role:     string(plugintype.RoleLabelTransform),
		Settings: map[string]any{"team": "checkout"},
		Sources: []lockfile.MutatorSourcePin{{
			Path:    "rules.rego",
			Content: content,
			Digest:  sourceDigest,
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
		got.Sources[0].Content != content || got.Sources[0].Digest != sourceDigest {
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

// TestMutators_RoleRoundTrip proves each transform role survives WriteLockfile ->
// LoadLockfile -> MutatorAttachments so the build reconstructs an attachment at
// the runner (and facet) it was authored for. Both transform roles round-trip
// verbatim: the annotation-transform attachment reconstructs as
// RoleAnnotationTransform, the label-transform attachment as RoleLabelTransform.
// The role is recorded on every attachment — never inferred at reconstruction —
// so an attachment cannot come back at a role other than the one authored, the
// mis-route class the earlier omit-then-re-synthesize pair could hide.
func TestMutators_RoleRoundTrip(t *testing.T) {
	in := []lockfile.MutatorAttachment{
		{Plugin: "governance/annotation-injector", Role: string(plugintype.RoleAnnotationTransform)},
		{Plugin: "governance/tag-injector", Role: string(plugintype.RoleLabelTransform)},
	}
	path := filepath.Join(t.TempDir(), "release.lock")
	if err := lockfile.WriteLockfile(path, mutatorSampleLockfile(in)); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(raw); strings.Count(got, "role:") != 2 {
		t.Errorf("expected a role key on every attachment (role is required, never inferred); got:\n%s", got)
	}

	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile (schema validation is part of the load): %v", err)
	}
	refs := lockfile.MutatorAttachments(lf)
	if len(refs) != 2 {
		t.Fatalf("MutatorAttachments len = %d, want 2: %+v", len(refs), refs)
	}
	byURI := map[string]plugintype.Role{}
	for _, r := range refs {
		byURI[r.URI] = r.Role
	}
	if got := byURI["governance/annotation-injector"]; got != plugintype.RoleAnnotationTransform {
		t.Errorf("annotation attachment reconstructed role = %q, want %q", got, plugintype.RoleAnnotationTransform)
	}
	if got := byURI["governance/tag-injector"]; got != plugintype.RoleLabelTransform {
		t.Errorf("label attachment reconstructed role = %q, want %q", got, plugintype.RoleLabelTransform)
	}
}

// TestMutators_RolelessAttachmentFailsLoudly proves a lockfile whose transform
// attachment omits role is rejected at load rather than silently defaulting.
// The mutators section requires role on every attachment, so the
// schema-validating load halts with ErrLockfileInvalid — reconstruction never
// sees a role-less attachment, so it can never infer a role the author did not
// write.
func TestMutators_RolelessAttachmentFailsLoudly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.lock")
	roleless := "version: 1\n" +
		"release: release.yaml\n" +
		"generated_at: \"2026-07-17T00:00:00Z\"\n" +
		"mutators:\n" +
		"    - plugin: governance/tag-injector\n"
	if err := os.WriteFile(path, []byte(roleless), 0o644); err != nil {
		t.Fatalf("write role-less lockfile: %v", err)
	}
	_, _, err := lockfile.LoadLockfile(path)
	if err == nil {
		t.Fatal("expected a role-less transform attachment to fail schema validation, got nil")
	}
	if !errors.Is(err, lockfile.ErrLockfileInvalid()) {
		t.Fatalf("expected ErrLockfileInvalid for a role-less transform attachment, got: %v", err)
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
	lt := string(plugintype.RoleLabelTransform)
	in := []lockfile.MutatorAttachment{
		{Plugin: "governance/zeta", Role: lt, Function: "custom_mutate"},
		{Plugin: "governance/alpha", Role: lt},
		{Plugin: "governance/middle", Role: lt, Function: "mutate"},
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
		{Plugin: "governance/tag-injector", Role: string(plugintype.RoleLabelTransform)},
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
	if len(lf.Mutators) != 1 || lf.Mutators[0].Plugin != "governance/tag-injector" || lf.Mutators[0].Function != "" {
		t.Fatalf("attachment roundtrip: got %+v", lf.Mutators)
	}
}

// TestMutators_WriteIsDeterministic proves two independent writes of the same
// in-memory lockfile (with a multi-attachment mutators section) produce
// byte-identical output — the determinism contract, holding for the
// order-preserving section as it does for the sorted ones.
func TestMutators_WriteIsDeterministic(t *testing.T) {
	lt := string(plugintype.RoleLabelTransform)
	in := []lockfile.MutatorAttachment{
		{Plugin: "governance/second", Role: lt, Function: "mutate"},
		{Plugin: "governance/first", Role: lt},
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
