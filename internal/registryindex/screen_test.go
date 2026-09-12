// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryindex

import (
	"io"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestAProvenanceStatesItsObservationTimeInUTCOrNotAtAll holds the two claims the
// rendering makes, each against its own control.
//
// The zone arm is the one that would otherwise pass on a machine whose local zone
// happens to be UTC, which is where a test is usually written: its instant is stated
// in a zone two hours ahead, so a rendering that took the instant as it stands would
// produce a different hour and a different offset.
//
// The zero-instant arm is the control for the absent arm. "The empty string means
// there is no observation time" is satisfied by a rendering that returns the empty
// string for the zero instant too, and those two must not read alike — a snapshot
// whose build time was never recorded is a different fact from one built at the
// epoch.
func TestAProvenanceStatesItsObservationTimeInUTCOrNotAtAll(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		provenance RetractionProvenance
		want       string
	}{
		{
			name:       "no observation time recorded",
			provenance: RetractionProvenance{Source: ProvenanceLocalIndex},
			want:       "",
		},
		{
			name:       "an instant recorded in another zone",
			provenance: IndexProvenance(time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("two hours ahead", 2*60*60)), true),
			want:       "2026-03-04T03:06:07Z",
		},
		{
			name:       "the zero instant, recorded",
			provenance: IndexProvenance(time.Time{}, true),
			want:       "0001-01-01T00:00:00Z",
		},
		{
			name:       "facts read while the operation ran",
			provenance: LiveProvenance(),
			want:       "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.provenance.ObservedAtStated(); got != tc.want {
				t.Errorf("ObservedAtStated() = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestEveryProvenanceRendersItsOwnClause holds the sentences a reader watching a
// terminal is shown, verbatim and per source.
//
// They are quoted in full rather than matched on a fragment, because what a reader
// acts on is the whole sentence: the snapshot clauses name the command that refreshes
// the snapshot, and a fragment check would keep passing over a clause that had lost
// it. They are the one place those bytes are asserted against a written-out
// expectation rather than against another run of the same renderer, so a rewording is
// a diff a reviewer reads rather than a silent change to what a reader is told.
//
// The unknown-source arm is the control the other three need. "Every source renders a
// clause" is satisfied by a renderer that guesses, and a notice that cannot say where
// its evidence came from must stay silent on the question rather than be wrong about
// it.
func TestEveryProvenanceRendersItsOwnClause(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		provenance RetractionProvenance
		want       string
	}{
		{
			name:       "a reading taken during the operation",
			provenance: LiveProvenance(),
			want:       " (read from the registry just now)",
		},
		{
			name:       "a snapshot recording when it was built",
			provenance: IndexProvenance(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC), true),
			want:       " (recorded in the local registry index as of 2026-03-04T05:06:07Z; run `aggrega registry update` to refresh it)",
		},
		{
			name:       "a snapshot carrying no build time",
			provenance: IndexProvenance(time.Time{}, false),
			want:       " (recorded in the local registry index, which carries no build time; run `aggrega registry update` to refresh it)",
		},
		{
			name:       "a selection nothing screened",
			provenance: RetractionProvenance{},
			want:       "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.provenance.Clause(); got != tc.want {
				t.Errorf("Clause() = %q;\nwant %q", got, tc.want)
			}
		})
	}
}

// TestProvenanceOfAnswersOnlyForAScreenThisPackageBuilt pins the accessor's two
// answers, and the second is what the first would otherwise rest on unexamined.
//
// A screen built elsewhere — a fake standing in for one, which is what the contract
// admits — must report no provenance rather than a guess, and the empty value it
// reports then is the same one a selection that was never screened carries. Without
// the second arm, "the accessor returns the screen's provenance" would pass on an
// accessor that returned one for anything it was handed.
func TestProvenanceOfAnswersOnlyForAScreenThisPackageBuilt(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	built := NewScreenFromRecords([]Record{{
		URI: URIForReference("hub.aggrega.ai/plugin/linter/style"),
		Deprecations: []VersionDeprecation{{
			Version: "2.0.0",
			Marker:  deprecation.Marker{Severity: deprecation.SeverityRetracted, Reason: "withdrawn in favour of a successor"},
			// Only a signature-verified record screens a version, so a record without a
			// signer would leave no screen to read provenance off and this would fail for
			// the wrong reason.
			Signer: &lockfile.VerifiedSignature{Identity: "publisher", Mode: "keyed"},
		}},
	}}, IndexProvenance(observedAt, true), io.Discard)
	if built == nil {
		t.Fatal("no screen was formed over a record carrying a verified withdrawal, so there is nothing to read provenance off")
	}

	provenance, known := ProvenanceOf(built)
	if !known {
		t.Fatal("a screen this package built reports no provenance")
	}
	if provenance.Source != ProvenanceLocalIndex {
		t.Errorf("source = %q; want %q — the screen was built from index records", provenance.Source, ProvenanceLocalIndex)
	}
	if !provenance.ObservedAt.Equal(observedAt) {
		t.Errorf("observed at %s; want %s", provenance.ObservedAt, observedAt)
	}
	if !provenance.HasObservedAt {
		t.Error("the screen was built with an observation time and reports none")
	}

	if provenance, known := ProvenanceOf(screenBuiltElsewhere{}); known {
		t.Errorf("a screen this package did not build reported provenance %#v; it cannot answer the question, and a guess here would name evidence nothing consulted", provenance)
	} else if provenance != (RetractionProvenance{}) {
		t.Errorf("an unknown screen reported %#v alongside its refusal; the empty value is what the refusal means", provenance)
	}
}

// screenBuiltElsewhere satisfies the screen contract and is not one of this
// package's screens, which is the case the accessor's contract admits: the contract
// is what a selection primitive calls, and widening it with a provenance method would
// oblige every implementation to answer a question only a screen built from index or
// registry facts can answer.
type screenBuiltElsewhere struct{}

func (screenBuiltElsewhere) ScreenVersions(_ string, tags []string) ([]string, []catalog.VersionExclusion) {
	return tags, nil
}

func (screenBuiltElsewhere) ReportSelection(_, _ string, _ []catalog.VersionExclusion) {}

func (screenBuiltElsewhere) ReportPinned(_, _ string) {}
