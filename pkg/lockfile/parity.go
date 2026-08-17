// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import "bytes"

// This file is the single home for the parity waiver: the set of top-level
// lockfile sections a byte-identity comparison between two pulls of the same
// inputs ignores, and the one function that removes them.
//
// It exists as a chokepoint rather than as a helper per comparison because the
// waiver is a tolerance, and a tolerance re-implemented per call site widens
// silently — one site learns about a newly waived section and another does not,
// so the two comparisons stop asking the same question while both stay green.
// Every comparison that waives anything routes through ParityWaived.
//
// Deliberately NOT unified with stripGeneratedAt in writer.go, which serves a
// different question and must keep a narrower waiver. That one asks "would
// re-writing this closure change the file?", and a changed toolchain stamp IS a
// change that should refresh the wall-clock stamp beside it; folding the two
// together would make a compiler upgrade reuse the old timestamp and hide the
// stamp's own movement. The near-duplication is the correct shape here.

// parityWaivedSections names every top-level lockfile key a parity comparison
// waives, in a fixed order so a report over them is deterministic.
//
//   - generated_at is wall-clock provenance: it differs between any two pulls by
//     construction.
//   - toolchain records the versions of the compiler and its core plugins. It is
//     invariant for a given binary, but it is not a property of the closure, so a
//     comparison asking whether two pulls resolved the same dependencies must not
//     read it. Enforcing it would pin the compiler by proxy.
var parityWaivedSections = []string{"generated_at", "toolchain"}

// ParityWaivedSections returns the waived top-level keys in a fixed order. It is
// exported so a comparison can name in its own failure output what it chose not
// to read, rather than a reader having to infer the tolerance from a diff.
func ParityWaivedSections() []string {
	out := make([]string, len(parityWaivedSections))
	copy(out, parityWaivedSections)
	return out
}

// ParityWaivedSectionCount reports how many times key opens a top-level section
// in canonical lockfile bytes — that is, how many lines begin with `key:` at
// column 0.
//
// It exists so a comparison can pre-assert the width of its own tolerance before
// relying on it. ParityWaived removes every occurrence it finds, so the tolerance
// is only as narrow as the number of sections it can match: were a lockfile ever
// to carry a second `generated_at:` at column 0, the waiver would silently start
// ignoring content it was never meant to ignore. Asserting the expected count
// first is what keeps that from happening quietly — a required section is exactly
// one, an optional one is at most one.
func ParityWaivedSectionCount(b []byte, key string) int {
	prefix := []byte(key + ":")
	n := 0
	for _, line := range bytes.SplitAfter(b, []byte("\n")) {
		if bytes.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// ParityWaived returns canonical lockfile bytes with every waived top-level
// section removed, so two lockfiles can be compared for byte equality over the
// content that is supposed to match.
//
// A section is removed as a whole block: the column-0 `key:` line, plus every
// following line that is indented, which is the only shape a nested value takes
// in the canonical output — yaml.v3 indents every mapping entry, sequence entry
// and wrapped scalar continuation under its key, and emits no blank line inside a
// mapping. The block therefore ends at the next column-0 line, which is the next
// top-level key and is kept. A scalar-valued section, whose value sits on the key
// line itself, is a block of one line and needs no special case.
//
// The input is expected to be the canonical bytes marshalCanonical produces.
// Bytes from another source are returned with whatever the same structural rule
// removes; nothing here parses YAML, so a non-canonical document is neither
// rejected nor repaired.
func ParityWaived(b []byte) []byte {
	out := make([]byte, 0, len(b))
	waived := false
	for _, line := range bytes.SplitAfter(b, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		indented := line[0] == ' ' || line[0] == '\t'
		if waived && indented {
			// A continuation of the waived section's value.
			continue
		}
		waived = false
		if !indented && opensWaivedSection(line) {
			waived = true
			continue
		}
		out = append(out, line...)
	}
	return out
}

// opensWaivedSection reports whether a column-0 line opens one of the waived
// sections. Matching is against the declared set rather than a pattern, so a key
// merely resembling a waived one is untouched.
func opensWaivedSection(line []byte) bool {
	for _, key := range parityWaivedSections {
		if bytes.HasPrefix(line, []byte(key+":")) {
			return true
		}
	}
	return false
}
