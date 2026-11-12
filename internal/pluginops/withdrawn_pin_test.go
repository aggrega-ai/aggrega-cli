// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginops

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The sentence an addition states for a named version a withdrawal covers, written
// out rather than rendered by the code under test. The live clause is the one a
// reading taken during the operation carries; the index rows match the snapshot's
// clause by its opening, since its instant is the fixture's.
const (
	withdrawnPinLine = "  retraction: " + retractedRefFull + ":" + retractedNewest + " is retracted (" + retractedReason +
		"); resolving it because the reference names it explicitly"
	withdrawnPinLiveClause  = " (read from the registry just now)\n"
	withdrawnPinIndexClause = " (recorded in the local registry index as of "
	unscreenedPinLine       = "  retraction: " + retractedRefFull + ": markers could not be read (" + unreadableFault +
		"); published versions were not screened for retraction\n"
	unreadableFault = "the registry refused the referrer listing"
)

// TestANamedWithdrawnVersionIsReportedOnTheResult drives the addition through Add
// over the live read (no local snapshot) and asserts the value a surface answering
// with values reads, beside the prose a terminal reader is shown.
//
// The planted defects each row catches:
//   - "named withdrawn": dropping Withdrawn from Add's result, or a second lookup that
//     disagrees with the sentence — the value and the line are asserted together;
//   - "named ordinary": a Withdrawn set unconditionally on a named pin;
//   - "latest": a Withdrawn set on the latest path — and, as the control, the
//     substitution still recorded on Retraction, so the fixture does withdraw 2.0.0;
//   - "named withdrawn, no notices writer": the early return on a nil writer, which
//     left the fact unread wherever nobody watched a terminal (removing the
//     io.Discard fallback fails this row);
//   - "named, markers unreadable": a named pin's read failure left unrecorded, or a
//     Withdrawn claimed over a read that never happened — Retraction.Unscreened
//     carries the fault and Withdrawn is false.
func TestANamedWithdrawnVersionIsReportedOnTheResult(t *testing.T) {
	// Not parallel: the registry seams this drives are process-global.
	for _, tc := range []struct {
		name           string
		query          string
		noNotices      bool
		failMarkers    bool
		wantPin        string
		wantWithdrawn  bool
		wantProse      string
		wantExcluded   string
		wantUnscreened string
	}{
		{
			name:          "named withdrawn",
			query:         retractedQuery + ":" + retractedNewest,
			wantPin:       retractedNewest,
			wantWithdrawn: true,
			wantProse:     withdrawnPinLine + withdrawnPinLiveClause,
		},
		{
			name:    "named ordinary",
			query:   retractedQuery + ":" + retractedOlder,
			wantPin: retractedOlder,
		},
		{
			name:         "latest",
			query:        retractedQuery,
			wantPin:      retractedOlder,
			wantExcluded: retractedNewest,
		},
		{
			name:          "named withdrawn, no notices writer",
			query:         retractedQuery + ":" + retractedNewest,
			noNotices:     true,
			wantPin:       retractedNewest,
			wantWithdrawn: true,
		},
		{
			name:           "named, markers unreadable",
			query:          retractedQuery + ":" + retractedNewest,
			failMarkers:    true,
			wantPin:        retractedNewest,
			wantProse:      unscreenedPinLine,
			wantUnscreened: unreadableFault,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rw := resolveScratch(t, scratchConfig)
			withFakeRegistryWithdrawing(t, []string{retractedRepo}, []string{retractedOlder, retractedNewest},
				map[string]string{retractedNewest: retractedReason})
			if tc.failMarkers {
				// Set after the fixture, so the fixture's own restore puts the true lister back.
				RepoWithdrawalLister = failingWithdrawalLister(unreadableFault)
			}
			var notices bytes.Buffer
			opts := AddOptions{Socket: retractedSocket, Query: tc.query, Notices: &notices}
			if tc.noNotices {
				opts.Notices = nil
			}

			res, err := Add(context.Background(), rw, opts)
			if err != nil {
				t.Fatalf("Add(%q): %v", tc.query, err)
			}
			if res.Identity != retractedEntity+":"+tc.wantPin {
				t.Fatalf("pinned %q; want %q — the fixture is wrong, not the operation", res.Identity, retractedEntity+":"+tc.wantPin)
			}
			if res.Withdrawn != tc.wantWithdrawn {
				t.Errorf("Withdrawn = %v; want %v", res.Withdrawn, tc.wantWithdrawn)
			}
			if !tc.noNotices && notices.String() != tc.wantProse && tc.wantExcluded == "" {
				t.Errorf("the addition wrote %q; want %q — the value and the sentence come from one lookup",
					notices.String(), tc.wantProse)
			}

			switch {
			case tc.wantExcluded != "":
				if res.Retraction == nil || len(res.Retraction.Excluded) != 1 || res.Retraction.Excluded[0].Version != tc.wantExcluded {
					t.Fatalf("Retraction = %#v; want exactly %s excluded — without it this row controls nothing", res.Retraction, tc.wantExcluded)
				}
			case tc.wantUnscreened != "":
				if res.Retraction == nil || res.Retraction.Unscreened != tc.wantUnscreened || res.Retraction.Reference != retractedRefFull {
					t.Fatalf("Retraction = %#v; want unscreened %q on %s — a named pin's failed read must reach a caller answering with values",
						res.Retraction, tc.wantUnscreened, retractedRefFull)
				}
			default:
				if res.Retraction != nil {
					t.Errorf("a named pin carries an advisory %#v; there was no selection for it to describe", res.Retraction)
				}
			}
		})
	}
}

// TestANamedPinsUnreadableMarkersAreToldOnceInTheSameWords is the proof that
// recording a named pin's failed read changed nothing for a reader watching a
// terminal.
//
// The command line hands the operation a discarding substitution writer and states
// the advisory itself, from the result (WriteRetractionNotice). So the line a
// terminal shows comes from the advisory now rather than from the read as it
// failed, and this compares the two byte for byte: a caller that lets the operation
// speak (no substitution writer of its own) against one shaped like the command line.
// The defects it catches: the line lost because it went to the discarded writer and
// was never recorded; the line told twice because it stayed on the notices writer
// while also being recorded; and the advisory rendering in other words than the read.
func TestANamedPinsUnreadableMarkersAreToldOnceInTheSameWords(t *testing.T) {
	// Not parallel: the registry seams this drives are process-global.
	withFakeRegistryWithdrawing(t, []string{retractedRepo}, []string{retractedOlder, retractedNewest}, nil)
	RepoWithdrawalLister = failingWithdrawalLister(unreadableFault)
	query := retractedQuery + ":" + retractedNewest

	var spoken bytes.Buffer
	if _, err := Add(context.Background(), resolveScratch(t, scratchConfig), AddOptions{
		Socket: retractedSocket, Query: query, Notices: &spoken,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	var rendered bytes.Buffer
	res, err := Add(context.Background(), resolveScratch(t, scratchConfig), AddOptions{
		Socket: retractedSocket, Query: query, Notices: &rendered, RetractionNotices: io.Discard,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if rendered.Len() != 0 {
		t.Fatalf("with the substitution writer discarded, the operation still wrote %q — a caller rendering the advisory would tell the reader twice", rendered.String())
	}
	WriteRetractionNotice(&rendered, res.Retraction)

	if spoken.String() != unscreenedPinLine {
		t.Fatalf("the operation wrote %q; want %q — the comparison below needs the line present", spoken.String(), unscreenedPinLine)
	}
	if rendered.String() != spoken.String() {
		t.Errorf("rendered from the advisory:\n%q\nas the read failed:\n%q", rendered.String(), spoken.String())
	}
}

// TestANamedWithdrawnVersionIsReportedFromTheLocalIndex is the index half: a local
// snapshot carrying the named version answers with no registry round trip, so the
// marker read is made to fail here — a row that took the live path instead would
// report the failure and no withdrawal, and fail.
//
// The planted defect it catches is the index screen's fact going unreported: the
// sentence printed from the snapshot while Withdrawn stays false.
func TestANamedWithdrawnVersionIsReportedFromTheLocalIndex(t *testing.T) {
	// Not parallel: the registry seams this drives are process-global.
	rw := resolveScratch(t, scratchConfig)
	withFakeRegistryWithdrawing(t, []string{retractedRepo}, []string{retractedOlder, retractedNewest}, nil)
	RepoWithdrawalLister = failingWithdrawalLister(unreadableFault)
	plantWithdrawingSnapshot(t, rw)

	var notices bytes.Buffer
	res, err := Add(context.Background(), rw, AddOptions{
		Socket: retractedSocket, Query: retractedQuery + ":" + retractedNewest, Notices: &notices,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !res.Withdrawn {
		t.Errorf("Withdrawn = false for a named version the local index records as withdrawn; prose was %q", notices.String())
	}
	if !strings.HasPrefix(notices.String(), withdrawnPinLine+withdrawnPinIndexClause) {
		t.Errorf("the addition wrote %q; want the snapshot's sentence %q…", notices.String(), withdrawnPinLine+withdrawnPinIndexClause)
	}
	if res.Retraction != nil {
		t.Errorf("a named pin answered from the index carries an advisory %#v; nothing was substituted and nothing failed", res.Retraction)
	}
}

// plantWithdrawingSnapshot writes a local registry-index snapshot into the
// workspace rw resolves, recording the fixture's two versions with the newer
// withdrawn under a verified signer. It addresses the snapshot through the path
// helpers and the record type the index reader uses, so the fixture cannot
// disagree with production about where a snapshot lives or what one holds.
func plantWithdrawingSnapshot(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) {
	t.Helper()
	root, cacheDir := rw.Root(), rw.Config().ResolvedArtifactCacheDir()
	body, err := json.Marshal(registryindex.Record{
		URI:      registryindex.URIForReference(retractedRefFull),
		Name:     retractedQuery,
		Version:  retractedNewest,
		Versions: []string{retractedOlder, retractedNewest},
		Deprecations: []registryindex.VersionDeprecation{{
			Version: retractedNewest,
			Marker:  deprecation.Marker{Severity: deprecation.SeverityRetracted, Reason: retractedReason},
			Signer:  &lockfile.VerifiedSignature{Identity: "publisher", Mode: "keyed"},
		}},
	})
	if err != nil {
		t.Fatalf("marshal the snapshot record: %v", err)
	}
	path := oci.IndexRepoPath(root, cacheDir, retractedHost, retractedRepo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("make the snapshot directory: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write the snapshot record: %v", err)
	}
	stamp := oci.IndexBasePath(root, cacheDir, retractedHost) + ".updated"
	if err := os.WriteFile(stamp, []byte("2026-11-11T08:00:00Z\n"), 0o600); err != nil {
		t.Fatalf("write the snapshot timestamp: %v", err)
	}
}
