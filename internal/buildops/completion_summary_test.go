// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package buildops

import "testing"

// TestCompletionSummaryCountsInWords holds the sentence a finished run closes on
// to its count: one file reads "1 file" and every other count "files", in the
// build's sentence, in both of a dry run's, and in the clause counting the files
// a build found beside its own.
func TestCompletionSummaryCountsInWords(t *testing.T) {
	t.Parallel()
	const dir = "/w/dist"
	for _, tc := range []struct {
		name         string
		written      int
		dryRun       bool
		policyInputs bool
		unwritten    int
		want         string
	}{
		{name: "a build writing none", written: 0, want: "Build completed: 0 files written to /w/dist"},
		{name: "a build writing one", written: 1, want: "Build completed: 1 file written to /w/dist"},
		{name: "a build writing two", written: 2, want: "Build completed: 2 files written to /w/dist"},
		{name: "one written beside one it did not write", written: 1, unwritten: 1, want: "Build completed: 1 file written to /w/dist, beside 1 file this build did not write"},
		{name: "two written beside two it did not write", written: 2, unwritten: 2, want: "Build completed: 2 files written to /w/dist, beside 2 files this build did not write"},
		{name: "a dry run that would write none", written: 0, dryRun: true, want: "Dry run: 0 files would be written to /w/dist; nothing was written"},
		{name: "a dry run that would write one", written: 1, dryRun: true, want: "Dry run: 1 file would be written to /w/dist; nothing was written"},
		{name: "a dry run that would write two", written: 2, dryRun: true, want: "Dry run: 2 files would be written to /w/dist; nothing was written"},
		{name: "a dry run writing the policy inputs, one file", written: 1, dryRun: true, policyInputs: true, want: "Dry run: 1 file would be written to /w/dist; only the policy inputs were written"},
		{name: "a dry run writing the policy inputs, two files", written: 2, dryRun: true, policyInputs: true, want: "Dry run: 2 files would be written to /w/dist; only the policy inputs were written"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := completionSummary(tc.written, dir, tc.dryRun, tc.policyInputs, tc.unwritten); got != tc.want {
				t.Errorf("completionSummary(%d, %q, dryRun=%v, policyInputs=%v, %d)\n got %q\nwant %q",
					tc.written, dir, tc.dryRun, tc.policyInputs, tc.unwritten, got, tc.want)
			}
		})
	}
}
