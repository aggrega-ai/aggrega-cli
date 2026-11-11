// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryindex

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// The fixture the pinned-withdrawal rows run over: one artifact, two of whose
// published versions carry a verified withdrawal — one with a reason recorded and
// one without — and one that carries none.
const (
	pinnedRef         = "registry.example.test/plugin/linter/conv"
	pinnedWithReason  = "2.0.0"
	pinnedNoReason    = "1.5.0"
	pinnedOrdinary    = "1.2.0"
	pinnedReason      = "unsound rule set"
	pinnedOtherRef    = "registry.example.test/plugin/linter/other"
	pinnedIndexClause = " (recorded in the local registry index as of 2026-11-11T08:00:00Z; run `aggrega registry update` to refresh it)"
	pinnedLiveClause  = " (read from the registry just now)"
)

// pinnedDeprecations is the fixture's withdrawal record, each entry signed, because
// only a signature-verified record screens a version: an unsigned one would exercise
// a screen that holds nothing while reading as one that holds a withdrawal.
func pinnedDeprecations() []VersionDeprecation {
	signer := &lockfile.VerifiedSignature{Identity: "publisher", Mode: "keyed"}
	return []VersionDeprecation{
		{Version: pinnedWithReason, Marker: deprecation.Marker{Severity: deprecation.SeverityRetracted, Reason: pinnedReason}, Signer: signer},
		{Version: pinnedNoReason, Marker: deprecation.Marker{Severity: deprecation.SeverityRetracted}, Signer: signer},
	}
}

// foreignScreen is a screen this package did not build. It records the pinned
// announcements it is asked for, so a row can assert that ReportPinnedWithdrawal
// still delivered the announcement to it while reporting no fact of its own.
type foreignScreen struct {
	pinned []string
}

func (f *foreignScreen) ScreenVersions(_ string, tags []string) ([]string, []catalog.VersionExclusion) {
	return tags, nil
}

func (f *foreignScreen) ReportSelection(string, string, []catalog.VersionExclusion) {}

func (f *foreignScreen) ReportPinned(reference, version string) {
	f.pinned = append(f.pinned, reference+":"+version)
}

// TestReportPinnedWithdrawalAnswersFromTheLookupItAnnouncesFrom holds the value a
// caller answering with values reads and the sentence a terminal reader is shown to
// one determination.
//
// Every row asserts the boolean AND the bytes written, together, because the
// property under test is that they cannot split. The defects it catches:
//   - a second withdrawal lookup returning the value while the sentence comes from
//     the screen (or the reverse) — a row where one moves and the other does not;
//   - reportPinned returning true on a path that wrote nothing, or false on one that
//     wrote the sentence — the not-withdrawn and other-reference rows, and the two
//     withdrawn rows;
//   - the reason-less sentence regressing into the reasoned one or dropping its
//     provenance clause — the expected bytes are written out in full;
//   - a nil screen dereferenced rather than reported as no fact;
//   - a screen this package did not build reported as withdrawn, or not told at all
//     — the foreign row asserts false AND that its own ReportPinned ran once.
//
// The expected sentences are independent literals rather than renderings by the
// code under test, so a rewording is a diff a reviewer reads.
func TestReportPinnedWithdrawalAnswersFromTheLookupItAnnouncesFrom(t *testing.T) {
	t.Parallel()

	observed := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	indexScreen := func(out io.Writer) catalog.RetractionScreen {
		return NewScreenFromRecords([]Record{{
			URI:          URIForReference(pinnedRef),
			Versions:     []string{pinnedOrdinary, pinnedNoReason, pinnedWithReason},
			Deprecations: pinnedDeprecations(),
		}}, IndexProvenance(observed, true), out)
	}
	liveScreen := func(out io.Writer) catalog.RetractionScreen {
		return NewLiveScreen(pinnedRef, pinnedDeprecations(), out)
	}

	for _, tc := range []struct {
		name      string
		screen    func(io.Writer) catalog.RetractionScreen
		reference string
		version   string
		want      bool
		wantProse string
	}{
		{
			name:      "an index screen, a withdrawn version with a reason",
			screen:    indexScreen,
			reference: pinnedRef,
			version:   pinnedWithReason,
			want:      true,
			wantProse: "  retraction: " + pinnedRef + ":" + pinnedWithReason + " is retracted (" + pinnedReason +
				"); resolving it because the reference names it explicitly" + pinnedIndexClause + "\n",
		},
		{
			name:      "an index screen, a withdrawn version with no reason",
			screen:    indexScreen,
			reference: pinnedRef,
			version:   pinnedNoReason,
			want:      true,
			wantProse: "  retraction: " + pinnedRef + ":" + pinnedNoReason +
				" is retracted; resolving it because the reference names it explicitly" + pinnedIndexClause + "\n",
		},
		{
			name:      "an index screen, a published version nothing withdrew",
			screen:    indexScreen,
			reference: pinnedRef,
			version:   pinnedOrdinary,
			want:      false,
			wantProse: "",
		},
		{
			name:      "an index screen, a reference it holds nothing for",
			screen:    indexScreen,
			reference: pinnedOtherRef,
			version:   pinnedWithReason,
			want:      false,
			wantProse: "",
		},
		{
			name:      "a live screen, a withdrawn version with a reason",
			screen:    liveScreen,
			reference: pinnedRef,
			version:   pinnedWithReason,
			want:      true,
			wantProse: "  retraction: " + pinnedRef + ":" + pinnedWithReason + " is retracted (" + pinnedReason +
				"); resolving it because the reference names it explicitly" + pinnedLiveClause + "\n",
		},
		{
			name:      "no screen",
			screen:    func(io.Writer) catalog.RetractionScreen { return nil },
			reference: pinnedRef,
			version:   pinnedWithReason,
			want:      false,
			wantProse: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			got := ReportPinnedWithdrawal(tc.screen(&out), tc.reference, tc.version)
			if got != tc.want || out.String() != tc.wantProse {
				t.Errorf("ReportPinnedWithdrawal(%s:%s) = %v and wrote %q; want %v and %q — the value and the sentence must come from one lookup",
					tc.reference, tc.version, got, out.String(), tc.want, tc.wantProse)
			}
		})
	}

	t.Run("a screen this package did not build", func(t *testing.T) {
		t.Parallel()
		foreign := &foreignScreen{}
		if ReportPinnedWithdrawal(foreign, pinnedRef, pinnedWithReason) {
			t.Error("a screen this package did not build was reported as holding a withdrawal; it states no fact to report")
		}
		if len(foreign.pinned) != 1 || foreign.pinned[0] != pinnedRef+":"+pinnedWithReason {
			t.Errorf("the screen's own ReportPinned received %q; want exactly one announcement of %s:%s",
				foreign.pinned, pinnedRef, pinnedWithReason)
		}
	})
}
