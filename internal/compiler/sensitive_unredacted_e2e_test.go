// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof of the trace's unredacted mode: an
// operator debugging a build can see the values the trace normally withholds,
// every file that shows them says so, and nothing outside the trace moves.
//
// The two builds below differ in exactly one input, so every difference between
// their outputs is attributable to it. The control is not decoration: a test that
// only asserted the plaintext is present under the mode would pass equally if the
// mode had turned redaction off everywhere, which is the failure that matters.

package compiler_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// unredactedHeaderLead is the header's first line. Matching the lead rather than
// the whole block keeps the assertion from breaking on a rewording of the
// following lines while still proving the announcement is there.
const unredactedHeaderLead = "# WARNING: written with `aggrega build --trace-unredacted`."

// unredactedBuild is the derived-withholding fixture built with the mode on. It
// is the same Release the withholding tests use, so the two runs are comparable
// slot for slot.
func unredactedBuild(t *testing.T) *pipeline.Result {
	t.Helper()
	return pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:     "testdata/sensitive-redaction/locals-release.yaml",
		CatalogPaths:    []string{"testdata/sensitive-redaction/catalog/base"},
		IncludeTrace:    true,
		TraceUnredacted: true,
	})
}

func sortedTraceSlots(trace map[string][]byte) []string {
	out := make([]string, 0, len(trace))
	for path := range trace {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// TestTraceUnredacted_ShowsWithheldValuesAndAnnouncesEveryFile is the positive
// half. Both derived and declared withholding must lift — a mode that lifted only
// the leaf declarations would leave a computed secret hidden and mislead the
// operator who turned it on — and the announcement is asserted on EVERY entry the
// build produced rather than on a representative one, because the file that
// escapes into a bug report is not the one a test sampled.
func TestTraceUnredacted_ShowsWithheldValuesAndAnnouncesEveryFile(t *testing.T) {
	t.Parallel()
	result := unredactedBuild(t)

	if len(result.Trace) == 0 {
		t.Fatal("the unredacted build produced no trace; every assertion below would be vacuous")
	}
	for _, slot := range sortedTraceSlots(result.Trace) {
		body := string(result.Trace[slot])
		if !strings.HasPrefix(body, unredactedHeaderLead) {
			t.Errorf("%s carries no unredacted-capture header; a file found later must announce what it is\n--- slot ---\n%s", slot, body)
		}
		if strings.Contains(body, redact.Placeholder()) {
			t.Errorf("%s still withholds a value under the unredacted mode\n--- slot ---\n%s", slot, body)
		}
	}

	// The declared leaves and the values computed from them are both visible.
	// Each is checked in a slot that actually carries it, so a missing slot fails
	// as a missing slot rather than passing as an absent plaintext.
	for _, slot := range derivedPillarOnlySlots {
		body := derivedTraceSlot(t, result, slot)
		for _, want := range []string{derivedSecret, derivedAPIKey, derivedToken} {
			if !strings.Contains(body, want) {
				t.Errorf("%s must show the declared-sensitive value %q under the unredacted mode\n--- slot ---\n%s", slot, want, body)
			}
		}
	}
	for _, slot := range derivedLocalsSlots {
		body := derivedTraceSlot(t, result, slot)
		// zzDsn is a local whose template read a declared leaf: withheld by
		// derivation rather than by declaration, so it proves the mode reaches
		// the derived set too.
		if !strings.Contains(body, "zzDsn: ") || strings.Contains(body, "zzDsn: "+redact.Placeholder()) {
			t.Errorf("%s must show the computed local zzDsn under the unredacted mode\n--- slot ---\n%s", slot, body)
		}
	}
}

// TestTraceUnredacted_OffIsTheWithholdingControl is the control. It runs the same
// fixture with the mode off and asserts the opposite of every claim above, so the
// positive assertions are attributable to the mode rather than to the fixture.
func TestTraceUnredacted_OffIsTheWithholdingControl(t *testing.T) {
	t.Parallel()
	result := derivedRedactionBuild(t)

	if len(result.Trace) == 0 {
		t.Fatal("the withholding build produced no trace; every assertion below would be vacuous")
	}
	stamped := 0
	for _, slot := range sortedTraceSlots(result.Trace) {
		if strings.Contains(string(result.Trace[slot]), unredactedHeaderLead) {
			stamped++
			t.Errorf("%s carries the unredacted-capture header with the mode off", slot)
		}
	}
	if stamped == 0 {
		// Sanity on the needle itself: if the header text ever changed and this
		// file's copy went stale, the loop above would report zero either way.
		// The positive test is what proves the needle matches a real header, so
		// the two tests are read together.
		t.Logf("no entry carries the header, as required (%d entries checked)", len(result.Trace))
	}

	for _, slot := range derivedPillarOnlySlots {
		body := derivedTraceSlot(t, result, slot)
		for _, plaintext := range []string{derivedSecret, derivedAPIKey, derivedToken} {
			if strings.Contains(body, plaintext) {
				t.Errorf("%s leaked %q with the mode off\n--- slot ---\n%s", slot, plaintext, body)
			}
		}
		if !strings.Contains(body, redact.Placeholder()) {
			t.Errorf("%s must withhold something with the mode off\n--- slot ---\n%s", slot, body)
		}
	}
}

// TestTraceUnredacted_MovesNothingButTheTrace is the boundary assertion, and the
// reason the mode is scoped to one surface. Two builds differing only in the mode
// must produce the same rendered artifacts, byte for byte, and capture the same
// set of trace slots: the mode governs what a slot's body SHOWS, never what a
// build writes or which states it records.
func TestTraceUnredacted_MovesNothingButTheTrace(t *testing.T) {
	t.Parallel()
	off := derivedRedactionBuild(t)
	on := unredactedBuild(t)

	if len(off.Artifacts) == 0 {
		t.Fatal("the withholding build rendered nothing; the byte-identity claim would be vacuous")
	}
	if len(on.Artifacts) != len(off.Artifacts) {
		t.Fatalf("the mode changed the rendered file count: %d with it on, %d with it off", len(on.Artifacts), len(off.Artifacts))
	}
	for path, want := range off.Artifacts {
		got, present := on.Artifacts[path]
		if !present {
			t.Errorf("the mode dropped the rendered artifact %q", path)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("the mode moved rendered bytes at %q; it must reach the trace and nothing else\n--- with the mode off ---\n%s\n--- with it on ---\n%s", path, want, got)
		}
	}

	onSlots, offSlots := sortedTraceSlots(on.Trace), sortedTraceSlots(off.Trace)
	if strings.Join(onSlots, "\n") != strings.Join(offSlots, "\n") {
		t.Errorf("the mode changed which trace slots are captured\n--- with it off ---\n%v\n--- with it on ---\n%v", offSlots, onSlots)
	}
}

// TestTraceUnredacted_DiffersOnlyAtWithheldPositions is the per-line control. It
// is the assertion that distinguishes "the mode lifted the withholding" from "the
// mode changed the projection": stripping the header from an unredacted slot must
// leave a body with the same line count as the withholding one, and every line
// the withholding body printed in full must appear unchanged.
//
// A whole-body comparison cannot make that distinction, because the two bodies
// legitimately differ wherever a value was withheld. Comparing line by line is
// what isolates those positions.
func TestTraceUnredacted_DiffersOnlyAtWithheldPositions(t *testing.T) {
	t.Parallel()
	off := derivedRedactionBuild(t)
	on := unredactedBuild(t)

	checked := 0
	for _, slot := range sortedTraceSlots(off.Trace) {
		offBody := string(off.Trace[slot])
		onBody, present := on.Trace[slot]
		if !present {
			t.Errorf("slot %q absent from the unredacted build", slot)
			continue
		}
		onLines := strings.Split(strings.TrimSuffix(string(onBody), "\n"), "\n")
		offLines := strings.Split(strings.TrimSuffix(offBody, "\n"), "\n")

		// Drop the announcing header, which is the one addition the mode makes.
		headerLen := 0
		for _, line := range onLines {
			if !strings.HasPrefix(line, "#") {
				break
			}
			headerLen++
		}
		onLines = onLines[headerLen:]

		if len(onLines) != len(offLines) {
			t.Errorf("%s: the mode changed the projection's shape (%d lines with it on, %d with it off)\n--- with it off ---\n%s\n--- with it on ---\n%s", slot, len(onLines), len(offLines), offBody, onBody)
			continue
		}
		for i := range offLines {
			if strings.Contains(offLines[i], redact.Placeholder()) {
				// A withheld position is exactly where the two are allowed to
				// differ, and it must actually differ: an identical line here
				// would mean the mode failed to lift the withholding.
				if onLines[i] == offLines[i] {
					t.Errorf("%s line %d was withheld and is unchanged under the mode: %q", slot, i+1, onLines[i])
				}
				checked++
				continue
			}
			if onLines[i] != offLines[i] {
				t.Errorf("%s line %d changed at a position nothing was withheld at\n  with it off: %q\n  with it on:  %q", slot, i+1, offLines[i], onLines[i])
			}
		}
	}
	if checked == 0 {
		t.Error("no withheld line was found in the withholding build; the fixture no longer exercises the mode and this test is vacuous")
	}
}
