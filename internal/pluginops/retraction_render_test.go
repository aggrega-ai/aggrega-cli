// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginops

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestWriteRetractionNoticeStatesEachConditionInItsOwnWords pins the sentences a
// surface rendering the advisory puts in front of a reader.
//
// The expectations are spelled out rather than assembled from the renderer's own
// pieces, because an expectation built the way the renderer builds it agrees with
// the renderer whatever it does. These are the bytes a person reads, so they are
// the bytes written here.
//
// The two filled rows are the pair the advisory exists to keep apart: one says the
// screen ran and withdrew what it names, the other says nothing was read at all.
// The empty rows are the control — a renderer that stated something unconditionally
// would pass both filled rows and fail these.
func TestWriteRetractionNoticeStatesEachConditionInItsOwnWords(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		advisory *RetractionAdvisory
		want     string
	}{
		{
			name:     "no advisory states nothing",
			advisory: nil,
			want:     "",
		},
		{
			name:     "an advisory that concluded nothing states nothing",
			advisory: &RetractionAdvisory{Reference: retractedRefFull},
			want:     "",
		},
		{
			name: "a screened selection names what was withdrawn and what was taken",
			advisory: &RetractionAdvisory{
				Reference: retractedRefFull,
				Selected:  retractedOlder,
				Excluded: []catalog.VersionExclusion{
					{Version: retractedNewest, Reason: retractedReason},
				},
				Provenance: registryindex.LiveProvenance(),
			},
			want: "  retraction: " + retractedRefFull + ":" + retractedNewest +
				" is retracted and was not selected: " + retractedReason + "\n" +
				"  retraction: " + retractedRefFull + " resolved to " + retractedOlder +
				" instead (read from the registry just now)\n",
		},
		{
			name: "a withdrawal with no recorded reason states the withdrawal alone",
			advisory: &RetractionAdvisory{
				Reference:  retractedRefFull,
				Selected:   retractedOlder,
				Excluded:   []catalog.VersionExclusion{{Version: retractedNewest}},
				Provenance: registryindex.LiveProvenance(),
			},
			want: "  retraction: " + retractedRefFull + ":" + retractedNewest +
				" is retracted and was not selected\n" +
				"  retraction: " + retractedRefFull + " resolved to " + retractedOlder +
				" instead (read from the registry just now)\n",
		},
		{
			name: "an unscreened selection names the fault the read failed with",
			advisory: &RetractionAdvisory{
				Reference:  retractedRefFull,
				Unscreened: "the registry refused the referrer listing",
			},
			want: "  retraction: " + retractedRefFull +
				": markers could not be read (the registry refused the referrer listing); " +
				"published versions were not screened for retraction\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			WriteRetractionNotice(&out, c.advisory)
			if out.String() != c.want {
				t.Errorf("rendered:\n%q\nwant:\n%q", out.String(), c.want)
			}
		})
	}
}

// TestTheSubstitutionWordingHasOneHome is the cross-check that the sentence a
// screen announces as selection runs and the sentence a surface renders from the
// advisory are one sentence rather than two that happen to agree.
//
// It drives both producers over one fixture and compares their bytes. A second
// composition of the wording anywhere would show up here the first time either
// moved, which no containment check over either output could see.
func TestTheSubstitutionWordingHasOneHome(t *testing.T) {
	t.Parallel()

	// The screen's own announcement, produced by the screen the index build makes.
	deps := []registryindex.VersionDeprecation{{
		Version: retractedNewest,
		Marker:  deprecation.Marker{Severity: deprecation.SeverityRetracted, Reason: retractedReason},
		Signer:  &lockfile.VerifiedSignature{Identity: "publisher", Mode: "keyed"},
	}}
	var announced bytes.Buffer
	screen := registryindex.NewLiveScreen(retractedRefFull, deps, &announced)
	if screen == nil {
		t.Fatal("the fixture built no screen, so there is no announcement to compare against")
	}
	excluded := []catalog.VersionExclusion{{Version: retractedNewest, Reason: retractedReason}}
	screen.ReportSelection(retractedRefFull, retractedOlder, excluded)

	// The same conclusion as values, rendered for a reader.
	var rendered bytes.Buffer
	WriteRetractionNotice(&rendered, &RetractionAdvisory{
		Reference:  retractedRefFull,
		Selected:   retractedOlder,
		Excluded:   excluded,
		Provenance: registryindex.LiveProvenance(),
	})

	if announced.Len() == 0 {
		t.Fatal("the screen announced nothing, so the comparison below compares two empty buffers")
	}
	if announced.String() != rendered.String() {
		t.Errorf("the screen and the advisory state one substitution differently\nannounced:\n%s\nrendered:\n%s",
			announced.String(), rendered.String())
	}
	t.Logf("the one sentence both produce:\n%s", announced.String())
}

// TestRedirectingTheSubstitutionLeavesTheMarkerAccountOnNotices is the
// invocation-level proof of the channel split, and of the safety property a naive
// split would break.
//
// Three things are asserted together because separating them would let each pass
// while the whole was wrong. The substitution leaves the notices writer, so a
// caller rendering it itself does not state it twice. The marker account stays,
// because it is a different fact and a caller silencing the substitution has not
// asked to lose it. And selection is still SCREENED — the withdrawn version is not
// what was pinned — which is what a caller that silenced the channel with a nil
// writer instead of a discarding one would have destroyed, silently and in the one
// direction nothing else here would notice.
func TestRedirectingTheSubstitutionLeavesTheMarkerAccountOnNotices(t *testing.T) {
	// Not parallel: the registry seams this drives are process-global.
	rw := resolveScratch(t, scratchConfig)
	withFakeRegistryWithdrawing(t, []string{retractedRepo}, []string{retractedOlder, retractedNewest},
		map[string]string{retractedNewest: retractedReason})

	// A withdrawal read that reports on the writer it is handed, standing in for the
	// production read's account of each marker it examined. The account is what must
	// survive the redirect, so the fixture has to produce one.
	const account = "  deprecation: the marker read reported here\n"
	prev := RepoWithdrawalLister
	RepoWithdrawalLister = accountingWithdrawalLister(prev, account)
	t.Cleanup(func() { RepoWithdrawalLister = prev })

	var notices bytes.Buffer
	res, err := Add(context.Background(), rw, AddOptions{
		Socket:            retractedSocket,
		Query:             retractedQuery,
		Notices:           &notices,
		RetractionNotices: io.Discard,
	})
	if err != nil {
		t.Fatalf("Add with the substitution redirected: %v", err)
	}

	if got := notices.String(); !strings.Contains(got, account) {
		t.Errorf("the marker account did not reach the notices writer; it carried:\n%q", got)
	}
	if strings.Contains(notices.String(), "retraction:") {
		t.Errorf("the substitution still reached the notices writer, so a caller rendering it states it twice; it carried:\n%s",
			notices.String())
	}
	if res.Retraction == nil {
		t.Fatal("redirecting the substitution left nothing to render it from, so the reader learns nothing at all")
	}
	if res.Identity != retractedEntity+":"+retractedOlder {
		t.Fatalf("pinned %q; want %q — redirecting where the substitution is announced must not stop selection being screened",
			res.Identity, retractedEntity+":"+retractedOlder)
	}
}

// accountingWithdrawalLister wraps a withdrawal read so it reports on the writer it
// is handed before answering, which is what the production read does and what the
// fake behind this fixture does not.
func accountingWithdrawalLister(
	inner func(context.Context, workspaceconfig.Config, string, string, []string, io.Writer) ([]registryindex.VersionDeprecation, error),
	account string,
) func(context.Context, workspaceconfig.Config, string, string, []string, io.Writer) ([]registryindex.VersionDeprecation, error) {
	return func(ctx context.Context, cfg workspaceconfig.Config, host, repoPath string, versions []string, out io.Writer) ([]registryindex.VersionDeprecation, error) {
		if out != nil {
			_, _ = io.WriteString(out, account)
		}
		return inner(ctx, cfg, host, repoPath, versions, out)
	}
}
