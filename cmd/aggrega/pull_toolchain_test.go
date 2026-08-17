// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestPrintLockfileToolchainRendersRecordedStamp pins the reader's output shape,
// one row per case, including the two shapes that must print nothing but a header
// or nothing at all.
//
// The nil case is load-bearing rather than defensive: a lockfile written before
// the stamp existed carries none, and the stamp produces no halt, no warning and
// no verdict — so it must produce no notice about being missing either.
func TestPrintLockfileToolchainRendersRecordedStamp(t *testing.T) {
	cases := []struct {
		name     string
		stamp    *lockfile.ToolchainStamp
		want     []string
		wantNone bool
	}{
		{
			name:     "no_stamp_prints_nothing",
			stamp:    nil,
			wantNone: true,
		},
		{
			name: "compiler_and_core_plugins",
			stamp: &lockfile.ToolchainStamp{
				CompilerVersion: "1.2.0",
				CorePlugins: []lockfile.CorePluginVersion{
					{Plugin: "governance/label-injector", Version: "1.2.0"},
					{Plugin: "provider/helm", Version: "1.2.0"},
				},
			},
			want: []string{
				"compiler 1.2.0",
				"reported, not verified",
				"governance/label-injector",
				"provider/helm",
			},
		},
		{
			name: "compiler_only_when_no_core_plugins_are_carried",
			stamp: &lockfile.ToolchainStamp{
				CompilerVersion: "(devel)",
				CorePlugins:     nil,
			},
			want: []string{"compiler (devel)", "reported, not verified"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printLockfileToolchain(&buf, tc.stamp)
			out := buf.String()

			if tc.wantNone {
				if out != "" {
					t.Fatalf("a lockfile with no stamp must print nothing; got %q", out)
				}
				return
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			// Idempotent render: no map-order nondeterminism, matching the
			// deterministic-ordering property the pull detail surface states.
			var second bytes.Buffer
			printLockfileToolchain(&second, tc.stamp)
			if second.String() != out {
				t.Errorf("non-deterministic render:\n--- first ---\n%s\n--- second ---\n%s", out, second.String())
			}
		})
	}
}

// TestPrintLockfileToolchainPrintsRowsInRecordedOrder confirms the reader prints
// the recorded order rather than imposing one of its own. Ordering belongs to the
// write (which canonicalises by plugin coordinate), so a reader that re-sorted
// would be a second home for the same rule and could disagree with the file.
func TestPrintLockfileToolchainPrintsRowsInRecordedOrder(t *testing.T) {
	var buf bytes.Buffer
	printLockfileToolchain(&buf, &lockfile.ToolchainStamp{
		CompilerVersion: "1.2.0",
		CorePlugins: []lockfile.CorePluginVersion{
			{Plugin: "zzz/last", Version: "1.2.0"},
			{Plugin: "aaa/first", Version: "1.2.0"},
		},
	})
	out := buf.String()
	if iz, ia := strings.Index(out, "zzz/last"), strings.Index(out, "aaa/first"); iz > ia {
		t.Errorf("reader re-sorted the recorded order:\n%s", out)
	}
}

// TestPullWritesToolchainStampAndVerifyReadsItBack is the end-to-end proof that
// the field and its reader ship as one unit: a real pull writes the stamp into the
// lockfile on disk, and `pull --verify` — reading that file, writing nothing —
// reports it on stdout.
//
// It also asserts the negative half of the recording rule directly against the
// bytes: the stamp records versions, so the section must carry no resolution tier
// and no source word, which is what keeps two machines that resolved the same
// closure writing the same stamp.
func TestPullWritesToolchainStampAndVerifyReadsItBack(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	catalogDirs := []string{filepath.Join(root, "catalog")}

	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: catalogDirs,
	}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}

	lockfilePath := filepath.Join(root, "release.lock")
	raw, err := os.ReadFile(lockfilePath)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	if !bytes.Contains(raw, []byte("\ntoolchain:\n")) {
		t.Fatalf("the pull wrote no toolchain section:\n%s", raw)
	}

	// Read the section back out of the file and assert on it alone, so a token
	// appearing elsewhere in the lockfile cannot make this pass or fail.
	section := toolchainSection(t, raw)
	if !strings.Contains(section, "compiler_version:") {
		t.Errorf("toolchain section records no compiler version:\n%s", section)
	}
	for _, forbidden := range []string{
		"tier-0-embed", "tier-1-catalog", "tier-2-cache", "tier-3-pull",
		"in-binary", "fetched-fresh", "cache-reuse",
		"source:", "resolution_path:", "digest:",
	} {
		if strings.Contains(section, forbidden) {
			t.Errorf("the toolchain stamp records %q — it must record versions only, "+
				"never where the bytes came from:\n%s", forbidden, section)
		}
	}

	// The reader: verify loads the file and reports the stamp, writing nothing.
	before, err := os.ReadFile(lockfilePath)
	if err != nil {
		t.Fatalf("read lockfile before verify: %v", err)
	}
	var out bytes.Buffer
	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: catalogDirs,
		Verify:      true,
		Out:         &out,
	}); err != nil {
		t.Fatalf("pull --verify: %v", err)
	}
	printed := out.String()
	if !strings.Contains(printed, "toolchain recorded by the pull that wrote it") {
		t.Errorf("verify did not report the recorded toolchain:\n%s", printed)
	}
	if !strings.Contains(printed, "reported, not verified") {
		t.Errorf("verify reported the toolchain without saying it was not checked:\n%s", printed)
	}

	after, err := os.ReadFile(lockfilePath)
	if err != nil {
		t.Fatalf("read lockfile after verify: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("verify wrote to the lockfile; it must only read")
	}
}

// TestPullVerifyTreatsAToolchainDifferenceAsNoDrift is the executable form of the
// prohibition. The stamp reaches no comparison that decides pass or fail, so a
// lockfile recording a compiler version this binary does not have still verifies
// clean — no error, no warning in the reported output.
//
// Asserting it rather than documenting it is the point: a recorded version invites
// enforcement by increments, and an advisory that nothing tests would acquire a
// gate the first time somebody found the field convenient.
func TestPullVerifyTreatsAToolchainDifferenceAsNoDrift(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	catalogDirs := []string{filepath.Join(root, "catalog")}

	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: catalogDirs,
	}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}

	lockfilePath := filepath.Join(root, "release.lock")
	raw, err := os.ReadFile(lockfilePath)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}

	// Rewrite the recorded compiler version to one this binary certainly is not,
	// and every recorded core-plugin version with it, so the whole stamp differs.
	rewritten := regexp.MustCompile(`(?m)^(\s*)(compiler_version|version): .*$`).
		ReplaceAllString(string(raw), "${1}${2}: 99.99.99")
	if rewritten == string(raw) {
		t.Fatalf("could not rewrite a recorded version in:\n%s", raw)
	}
	// `version: 1` at column 0 is the lockfile schema version, which the rewrite
	// above would also have hit; restore it, since changing it is a different
	// fault and would make this fixture carry two.
	rewritten = regexp.MustCompile(`(?m)^version: 99\.99\.99$`).
		ReplaceAllString(rewritten, "version: 1")
	if err := os.WriteFile(lockfilePath, []byte(rewritten), 0o644); err != nil {
		t.Fatalf("write rewritten lockfile: %v", err)
	}

	// Positive control: the rewrite really did move the stamp, so a green below
	// means the stamp was ignored rather than that nothing changed.
	section := toolchainSection(t, []byte(rewritten))
	if !strings.Contains(section, "99.99.99") {
		t.Fatalf("the rewrite did not reach the toolchain section:\n%s", section)
	}

	var out bytes.Buffer
	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: catalogDirs,
		Verify:      true,
		Out:         &out,
	}); err != nil {
		t.Fatalf("a toolchain difference must not be drift; verify returned: %v", err)
	}
	printed := out.String()
	if !strings.Contains(printed, "matches current dependency resolution") {
		t.Errorf("verify did not report a match:\n%s", printed)
	}
	if !strings.Contains(printed, "compiler 99.99.99") {
		t.Errorf("verify did not report the recorded (differing) compiler version:\n%s", printed)
	}
	for _, complaint := range []string{"warn", "WARN", "mismatch", "drift", "expected"} {
		if strings.Contains(printed, complaint) {
			t.Errorf("verify complained about the toolchain difference (%q); the stamp "+
				"produces no halt, no warning and no verdict:\n%s", complaint, printed)
		}
	}
}

// toolchainSection returns just the toolchain block out of canonical lockfile
// bytes, so an assertion about the stamp cannot be satisfied or broken by a token
// living in some other section. It is the complement of lockfile.ParityWaived,
// which removes the same block; deliberately a separate, local derivation, because
// a test that extracted the section through the production helper would be asking
// the code under test to define the subject of its own assertion.
func toolchainSection(t *testing.T, b []byte) string {
	t.Helper()
	var section []string
	inside := false
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "toolchain:") {
			inside = true
			section = append(section, line)
			continue
		}
		if !inside {
			continue
		}
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			section = append(section, line)
			continue
		}
		break
	}
	if len(section) == 0 {
		t.Fatalf("no toolchain section found in:\n%s", b)
	}
	return strings.Join(section, "\n")
}
