// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// noPullNote is the substring identifying the --detail no-pull note. Held in one
// place so the two sinks are matched against the same text: the assertions below
// are only meaningful if "present on stdout" and "absent from stderr" test for
// exactly the same line.
const noPullNote = "resolution detail is a pull-time surface"

// TestBuildDetail_LockfilePresent_NoPullNote pins the --detail no-pull path:
// when a lockfile already exists, build does not pull, so there is no resolution
// to report. --detail says so in one line (rather than silence or a fabricated
// tier) and reports the lockfile pre-existed; without --detail the path is
// silent.
//
// Both sinks are asserted on every row, because the note's stream is the
// contract: --detail's help promises stdout, so landing the note on the
// diagnostic sink would be a defect that a single-sink assertion — "the text
// appears somewhere" — cannot distinguish from correct behavior.
func TestBuildDetail_LockfilePresent_NoPullNote(t *testing.T) {
	tests := []struct {
		name     string
		detail   bool
		wantNote bool
	}{
		{name: "detail on: the note is written to stdout", detail: true, wantNote: true},
		{name: "detail off: neither sink is written", detail: false, wantNote: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			rel := filepath.Join(root, "release.yaml")
			writeFixture(t, rel, "kind: Release\n")
			// A present lockfile makes acquireLockfile take the no-pull branch; its
			// content is irrelevant (only os.Stat is consulted there).
			writeFixture(t, lockfile.PathFor(rel), "{}\n")

			var diag, out bytes.Buffer
			pre, err := acquireLockfile(nil, rel, []string{root}, workspaceconfig.Config{},
				false, false, tc.detail, &diag, &out)
			if err != nil {
				t.Fatalf("acquireLockfile: %v", err)
			}
			if !pre {
				t.Fatal("expected lockfilePreexisted=true when the lockfile is present")
			}

			if got := strings.Contains(out.String(), noPullNote); got != tc.wantNote {
				t.Errorf("no-pull note on the stdout sink: got present=%v, want %v (stdout=%q)",
					got, tc.wantNote, out.String())
			}
			// The stream is the contract, so the negative half is asserted
			// explicitly: the note never reaches the diagnostic sink, which in
			// production is os.Stderr.
			if strings.Contains(diag.String(), noPullNote) {
				t.Errorf("no-pull note leaked onto the diagnostic sink: %q", diag.String())
			}
			if strings.TrimSpace(diag.String()) != "" {
				t.Errorf("expected no diagnostic output on this path, got %q", diag.String())
			}
		})
	}
}
