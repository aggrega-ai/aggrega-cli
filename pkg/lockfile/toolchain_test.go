// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// stampedLockfile returns a minimal lockfile carrying a toolchain stamp, with the
// core plugins deliberately out of coordinate order so a canonicalisation
// assertion has something to reorder.
func stampedLockfile() *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version:     1,
		Release:     "release-prod.yaml",
		GeneratedAt: "2026-06-06T00:00:00Z",
		Toolchain: &lockfile.ToolchainStamp{
			CompilerVersion: "1.2.0",
			CorePlugins: []lockfile.CorePluginVersion{
				{Plugin: "provider/helm", Version: "1.2.0"},
				{Plugin: "governance/label-injector", Version: "1.2.0"},
				{Plugin: "parser/yaml", Version: "1.2.0"},
			},
		},
	}
}

// TestToolchainStampRoundTrip proves the recorded toolchain survives a
// write/load round-trip with both halves intact, and — the load-bearing negative
// — that a lockfile carrying no stamp loads with a nil stamp, no error, and no
// diagnostic. The absence case is the one that keeps the stamp advisory: a
// tolerated-but-noted absence would be the warning the stamp must never produce.
func TestToolchainStampRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "release-prod.lock")
	if err := lockfile.WriteLockfile(path, stampedLockfile()); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	got, diags, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	if got.Toolchain == nil {
		t.Fatalf("stamp lost across the round-trip")
	}
	if got.Toolchain.CompilerVersion != "1.2.0" {
		t.Errorf("compiler version = %q; want 1.2.0", got.Toolchain.CompilerVersion)
	}
	// Canonical order is by coordinate, so the write reorders the input.
	wantOrder := []string{"governance/label-injector", "parser/yaml", "provider/helm"}
	if len(got.Toolchain.CorePlugins) != len(wantOrder) {
		t.Fatalf("core plugins = %d entries; want %d", len(got.Toolchain.CorePlugins), len(wantOrder))
	}
	for i, want := range wantOrder {
		if got.Toolchain.CorePlugins[i].Plugin != want {
			t.Errorf("core plugin %d = %q; want %q", i, got.Toolchain.CorePlugins[i].Plugin, want)
		}
		if got.Toolchain.CorePlugins[i].Version != "1.2.0" {
			t.Errorf("core plugin %d version = %q; want 1.2.0", i, got.Toolchain.CorePlugins[i].Version)
		}
	}

	// A lockfile with no stamp: nil, silent, and no diagnostic about the absence.
	bare := writeFixture(t, `version: 1
release: ./release-prod.yaml
generated_at: 2026-06-06T00:00:00Z
schema_libraries: []
mapping_libraries: []
source_artifacts: []
`)
	lf, bareDiags, err := lockfile.LoadLockfile(bare)
	if err != nil {
		t.Fatalf("LoadLockfile on a stampless lockfile: %v", err)
	}
	if lf.Toolchain != nil {
		t.Errorf("stampless lockfile loaded a non-nil stamp: %+v", lf.Toolchain)
	}
	for _, d := range append(append([]lockfile.Diagnostic(nil), diags...), bareDiags...) {
		if strings.Contains(d.Field, "toolchain") || strings.Contains(d.Message, "toolchain") {
			t.Errorf("the toolchain stamp produced a diagnostic, which it must never do: %+v", d)
		}
	}
}

// TestToolchainStampByteIdentity proves the deterministic-output invariant
// extends to the stamp: two independent writes of the same in-memory lockfile
// produce byte-identical output, and the canonical form does not depend on the
// order the core plugins were supplied in.
func TestToolchainStampByteIdentity(t *testing.T) {
	t.Parallel()

	write := func(lf *lockfile.Lockfile) []byte {
		t.Helper()
		path := filepath.Join(t.TempDir(), "release-prod.lock")
		if err := lockfile.WriteLockfile(path, lf); err != nil {
			t.Fatalf("WriteLockfile: %v", err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return b
	}

	first := write(stampedLockfile())
	second := write(stampedLockfile())
	if !bytes.Equal(first, second) {
		t.Errorf("two writes of one stamp differ:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}

	// Same set, supplied in a different order: the canonical form must coincide.
	shuffled := stampedLockfile()
	shuffled.Toolchain.CorePlugins = []lockfile.CorePluginVersion{
		{Plugin: "parser/yaml", Version: "1.2.0"},
		{Plugin: "provider/helm", Version: "1.2.0"},
		{Plugin: "governance/label-injector", Version: "1.2.0"},
	}
	if reordered := write(shuffled); !bytes.Equal(first, reordered) {
		t.Errorf("canonical form depends on the supplied core-plugin order:\n--- sorted ---\n%s\n--- shuffled ---\n%s",
			first, reordered)
	}
}

// TestCanonicaliseDoesNotAliasToolchain proves the write deep-copies the stamp
// pointer and its core-plugin slice, so sorting the canonical form cannot reorder
// the caller's slice — the maintenance hazard the canonicaliser guards against for
// every pointer- or slice-bearing field.
func TestCanonicaliseDoesNotAliasToolchain(t *testing.T) {
	t.Parallel()

	lf := stampedLockfile()
	before := make([]lockfile.CorePluginVersion, len(lf.Toolchain.CorePlugins))
	copy(before, lf.Toolchain.CorePlugins)

	path := filepath.Join(t.TempDir(), "release-prod.lock")
	if err := lockfile.WriteLockfile(path, lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	for i := range before {
		if lf.Toolchain.CorePlugins[i] != before[i] {
			t.Fatalf("the write mutated the caller's core-plugin slice at %d: %+v, was %+v",
				i, lf.Toolchain.CorePlugins[i], before[i])
		}
	}
}

// TestLoadRejectsMalformedToolchain is the negative corpus for the stamp's own
// schema constraints, one fault per fixture, each asserted against the sentinel
// the load raises rather than against "any error".
//
// It is the boundary the no-enforcement rule does NOT cover, and the distinction
// matters: nothing compares a well-formed stamp against this binary, and its
// absence is silent — but a stamp that is present and structurally wrong is a
// malformed document, and the loader rejects malformed documents whatever section
// they are in.
func TestLoadRejectsMalformedToolchain(t *testing.T) {
	t.Parallel()

	const head = `version: 1
release: ./release-prod.yaml
generated_at: 2026-06-06T00:00:00Z
`
	cases := []struct {
		name string
		body string
	}{
		{
			name: "unknown_key_inside_the_stamp",
			body: `toolchain:
    compiler_version: 1.2.0
    core_plugins: []
    resolution_tier: tier-0-embed
`,
		},
		{
			name: "missing_compiler_version",
			body: `toolchain:
    core_plugins: []
`,
		},
		{
			name: "missing_core_plugins",
			body: `toolchain:
    compiler_version: 1.2.0
`,
		},
		{
			name: "core_plugin_entry_missing_version",
			body: `toolchain:
    compiler_version: 1.2.0
    core_plugins:
        - plugin: provider/helm
`,
		},
		{
			name: "core_plugin_entry_carries_a_source",
			body: `toolchain:
    compiler_version: 1.2.0
    core_plugins:
        - plugin: provider/helm
          version: 1.2.0
          source: in-binary
`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := lockfile.LoadLockfile(writeFixture(t, head+tc.body))
			if err == nil {
				t.Fatalf("expected the load to reject %s; got nil", tc.name)
			}
			if !errors.Is(err, lockfile.ErrLockfileInvalid()) {
				t.Errorf("error should wrap ErrLockfileInvalid; got %v", err)
			}
		})
	}

	// Positive control: the same head with a well-formed stamp loads cleanly, so
	// each row above fails on its own fault rather than on the shared fixture.
	if _, _, err := lockfile.LoadLockfile(writeFixture(t, head+`toolchain:
    compiler_version: 1.2.0
    core_plugins:
        - plugin: provider/helm
          version: 1.2.0
`)); err != nil {
		t.Fatalf("positive control: a well-formed stamp must load; got %v", err)
	}
}

// TestParityWaivedRemovesWholeSections pins the waiver's structural rule: a
// waived column-0 key takes its indented continuation with it and stops at the
// next column-0 key. Each row carries one shape, including the shapes that must
// SURVIVE — a key merely resembling a waived one, an indented key that happens to
// share a waived name, and the top-level key immediately following a waived block.
//
// The survivors are the point. A strip that over-consumes silently widens the
// tolerance, and a widened tolerance is invisible from a passing comparison: the
// two sides simply agree about less.
func TestParityWaivedRemovesWholeSections(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "scalar_valued_waived_section",
			in:   "entities: []\ngenerated_at: 2026-06-06T00:00:00Z\nversion: 1\n",
			want: "entities: []\nversion: 1\n",
		},
		{
			name: "mapping_valued_waived_section_with_a_nested_list",
			in: `source_artifacts: []
toolchain:
    compiler_version: 1.2.0
    core_plugins:
        - plugin: provider/helm
          version: 1.2.0
verdict_inputs: []
`,
			want: "source_artifacts: []\nverdict_inputs: []\n",
		},
		{
			name: "both_waived_sections_at_once",
			in: `entities: []
generated_at: 2026-06-06T00:00:00Z
toolchain:
    compiler_version: 1.2.0
    core_plugins: []
version: 1
`,
			want: "entities: []\nversion: 1\n",
		},
		{
			name: "a_key_merely_resembling_a_waived_one_survives",
			in:   "generated_at_source: pull\ntoolchain_pinned: false\nversion: 1\n",
			want: "generated_at_source: pull\ntoolchain_pinned: false\nversion: 1\n",
		},
		{
			name: "an_indented_key_sharing_a_waived_name_survives",
			in: `entities:
    - uri: core/api
      generated_at: 2026-06-06T00:00:00Z
version: 1
`,
			want: "entities:\n    - uri: core/api\n      generated_at: 2026-06-06T00:00:00Z\nversion: 1\n",
		},
		{
			name: "tab_indented_continuation_is_consumed",
			in:   "toolchain:\n\tcompiler_version: 1.2.0\nversion: 1\n",
			want: "version: 1\n",
		},
		{
			name: "a_waived_section_at_the_end_of_the_document",
			in: `version: 1
toolchain:
    compiler_version: 1.2.0
    core_plugins: []
`,
			want: "version: 1\n",
		},
		{
			name: "nothing_waived_is_a_passthrough",
			in:   "entities: []\nversion: 1\n",
			want: "entities: []\nversion: 1\n",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := string(lockfile.ParityWaived([]byte(tc.in))); got != tc.want {
				t.Errorf("ParityWaived mismatch\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}

// TestParityWaivedSectionCountDetectsAWidenedTolerance calibrates the instrument
// the parity comparisons pre-assert with. A count that cannot see a duplicated
// key would report every lockfile clean and the widening guard would be inert, so
// the needle is planted deliberately: a document carrying a waived key twice at
// column 0 must report two.
func TestParityWaivedSectionCountDetectsAWidenedTolerance(t *testing.T) {
	t.Parallel()

	sections := lockfile.ParityWaivedSections()
	if len(sections) == 0 {
		t.Fatalf("no waived section is declared, so every parity comparison waives nothing " +
			"and this assertion checks nothing")
	}
	for _, key := range sections {
		single := key + ": x\nversion: 1\n"
		if n := lockfile.ParityWaivedSectionCount([]byte(single), key); n != 1 {
			t.Errorf("count of %q in a single-section document = %d; want 1", key, n)
		}
		doubled := key + ": x\nversion: 1\n" + key + ": y\n"
		if n := lockfile.ParityWaivedSectionCount([]byte(doubled), key); n != 2 {
			t.Errorf("count of %q in a doubled document = %d; want 2 — the widening guard is blind", key, n)
		}
		indented := "entities:\n    - " + key + ": x\n"
		if n := lockfile.ParityWaivedSectionCount([]byte(indented), key); n != 0 {
			t.Errorf("count of %q counted an indented occurrence (%d); it must count column-0 sections only", key, n)
		}
	}
}

// TestWritePreservingTimestampDoesNotWaiveTheToolchain pins the deliberate
// asymmetry between the two waivers. The idempotent write asks "would re-writing
// this closure change the file?", and a moved toolchain stamp IS such a change:
// it must therefore refresh the wall-clock stamp beside it rather than reuse the
// prior one. Folding the two waivers together would hide the stamp's own movement
// behind an unchanged timestamp, which is why parity.go keeps them apart.
func TestWritePreservingTimestampDoesNotWaiveTheToolchain(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "release-prod.lock")

	prior := stampedLockfile()
	prior.GeneratedAt = "2026-06-06T00:00:00Z"
	if err := lockfile.WriteLockfile(path, prior); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	// An unchanged closure AND an unchanged stamp: the prior timestamp is reused.
	same := stampedLockfile()
	same.GeneratedAt = "2027-01-01T00:00:00Z"
	if err := lockfile.WriteLockfilePreservingTimestamp(path, same); err != nil {
		t.Fatalf("re-write unchanged: %v", err)
	}
	if same.GeneratedAt != "2026-06-06T00:00:00Z" {
		t.Errorf("an unchanged closure and stamp should reuse the prior timestamp; got %q", same.GeneratedAt)
	}

	// A moved stamp, everything else unchanged: the new timestamp stands, so the
	// diff shows both the new stamp and the moment it arrived.
	upgraded := stampedLockfile()
	upgraded.GeneratedAt = "2027-01-01T00:00:00Z"
	upgraded.Toolchain.CompilerVersion = "1.3.0"
	if err := lockfile.WriteLockfilePreservingTimestamp(path, upgraded); err != nil {
		t.Fatalf("re-write upgraded: %v", err)
	}
	if upgraded.GeneratedAt != "2027-01-01T00:00:00Z" {
		t.Errorf("a moved toolchain stamp must keep its own timestamp; got %q", upgraded.GeneratedAt)
	}
}
