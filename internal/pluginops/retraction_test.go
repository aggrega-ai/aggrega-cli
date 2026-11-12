// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginops

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The fixture every test here runs over: two published versions, of which the
// newer is withdrawn and the older is good.
//
// The constants are what the assertions are derived from, and that is deliberate.
// An expectation read back off the operation's own answer, or off the screen that
// produced it, is computed by the thing under test and agrees with it whatever it
// does; these are what the test PLANTED, so the comparison has a side the
// implementation does not read.
const (
	retractedRepo    = "plugin/linter/style"
	retractedHost    = "hub.aggrega.ai"
	retractedNewest  = "2.0.0"
	retractedOlder   = "1.2.0"
	retractedReason  = "withdrawn in favour of a successor"
	retractedSocket  = "linter"
	retractedQuery   = "style"
	retractedEntity  = "linter/style"
	retractedRefFull = retractedHost + "/" + retractedRepo
)

// TestAnAttachmentAnswersWithTheSubstitutionItMade is the invocation-level proof
// that the screen's conclusion survives from the operation to a caller answering
// with values, and the control that keeps it from passing vacuously.
//
// The two arms run the SAME attachment over the SAME two published versions and
// differ in one thing: whether the newer of them carries a withdrawal. Neither arm
// proves anything alone — "an advisory is present" passes on an operation that
// reports one unconditionally, and "the older version was pinned" passes on a
// fixture that never published the newer one — so only the difference between them
// is evidence.
func TestAnAttachmentAnswersWithTheSubstitutionItMade(t *testing.T) {
	// Not parallel: the registry seams this drives are process-global.
	t.Run("a withdrawn newest version is reported as a substitution", func(t *testing.T) {
		rw := resolveScratch(t, scratchConfig)
		withFakeRegistryWithdrawing(t, []string{retractedRepo}, []string{retractedOlder, retractedNewest},
			map[string]string{retractedNewest: retractedReason})

		res, err := Add(context.Background(), rw, AddOptions{
			Socket:  retractedSocket,
			Query:   retractedQuery,
			Notices: &bytes.Buffer{},
		})
		if err != nil {
			t.Fatalf("Add over a repository with one good version: %v", err)
		}

		if res.Identity != retractedEntity+":"+retractedOlder {
			t.Fatalf("pinned %q; want %q — the withdrawn %s must not be attached",
				res.Identity, retractedEntity+":"+retractedOlder, retractedNewest)
		}
		advisory := res.Retraction
		if advisory == nil {
			t.Fatal("the result carries no retraction advisory; a reader handed an older version than the newest published one is told nothing about why")
		}
		if advisory.Reference != retractedRefFull {
			t.Errorf("advisory reference = %q; want %q", advisory.Reference, retractedRefFull)
		}
		if advisory.Selected != retractedOlder {
			t.Errorf("advisory selected = %q; want %q", advisory.Selected, retractedOlder)
		}
		if advisory.Unscreened != "" {
			t.Errorf("advisory reports selection unscreened (%q) on a run whose markers read cleanly", advisory.Unscreened)
		}
		// A count and an equality rather than a containment check: an assertion that
		// the advisory "mentions 2.0.0" passes on one that also carried versions the
		// screen never withdrew, which is the shape a re-derivation produces.
		if len(advisory.Excluded) != 1 {
			t.Fatalf("advisory withdrew %d versions; want exactly 1 (%s): %#v",
				len(advisory.Excluded), retractedNewest, advisory.Excluded)
		}
		if advisory.Excluded[0].Version != retractedNewest {
			t.Errorf("advisory withdrew %q; want %q", advisory.Excluded[0].Version, retractedNewest)
		}
		if advisory.Excluded[0].Reason != retractedReason {
			t.Errorf("advisory reason = %q; want %q — the recorded reason is the compiler's own text and is carried, not reworded",
				advisory.Excluded[0].Reason, retractedReason)
		}
		// The screen's own evidence, captured where the screen is in hand. This fixture
		// builds no local snapshot, so resolution falls through to a reading taken while
		// the operation ran — and a recorder that stopped asking the screen would leave
		// the empty source here, which is the shape a selection nothing screened carries.
		//
		// It compares against the named source rather than against its spelling, because
		// what this layer owes is the capture; the spelling is a wire contract and is
		// asserted against a literal where that contract lives.
		if advisory.Provenance.Source != registryindex.ProvenanceRegistry {
			t.Errorf("advisory provenance source = %q; want %q — these facts were read while the operation ran",
				advisory.Provenance.Source, registryindex.ProvenanceRegistry)
		}
		if advisory.Provenance.HasObservedAt {
			t.Errorf("advisory provenance reports an observation time (%s) for facts read during the operation; there is no snapshot age to report",
				advisory.Provenance.ObservedAt)
		}
	})

	t.Run("nothing withdrawn leaves the advisory absent", func(t *testing.T) {
		rw := resolveScratch(t, scratchConfig)
		withFakeRegistryWithdrawing(t, []string{retractedRepo}, []string{retractedOlder, retractedNewest}, nil)

		res, err := Add(context.Background(), rw, AddOptions{
			Socket:  retractedSocket,
			Query:   retractedQuery,
			Notices: &bytes.Buffer{},
		})
		if err != nil {
			t.Fatalf("Add over a repository with no withdrawal: %v", err)
		}
		if res.Identity != retractedEntity+":"+retractedNewest {
			t.Fatalf("pinned %q; want %q — with nothing withdrawn the newest version is the one attached",
				res.Identity, retractedEntity+":"+retractedNewest)
		}
		if res.Retraction != nil {
			t.Errorf("an attachment that screened cleanly carries an advisory: %#v — an empty one reads to a caller as a withdrawal it cannot see",
				res.Retraction)
		}
	})
}

// TestAnUnreadableMarkerIsReportedRatherThanPassingAsCleanScreening pins the other
// condition the advisory carries, and it is the one that would otherwise be
// silence: a failed withdrawal read leaves selection unscreened, which produces no
// exclusions at all and is therefore indistinguishable from a clean screening to
// anything that only looks at those.
func TestAnUnreadableMarkerIsReportedRatherThanPassingAsCleanScreening(t *testing.T) {
	// Not parallel: the registry seams this drives are process-global.
	rw := resolveScratch(t, scratchConfig)
	withFakeRegistryWithdrawing(t, []string{retractedRepo}, []string{retractedOlder, retractedNewest}, nil)

	const fault = "the registry refused the referrer listing"
	prev := RepoWithdrawalLister
	RepoWithdrawalLister = failingWithdrawalLister(fault)
	t.Cleanup(func() { RepoWithdrawalLister = prev })

	res, err := Add(context.Background(), rw, AddOptions{
		Socket:  retractedSocket,
		Query:   retractedQuery,
		Notices: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("a failed withdrawal read degrades rather than halting; got: %v", err)
	}
	advisory := res.Retraction
	if advisory == nil {
		t.Fatal("selection ran unscreened and the result says nothing; a caller cannot tell a degraded answer from a screened one")
	}
	if advisory.Unscreened != fault {
		t.Errorf("advisory unscreened = %q; want %q — the fault is what a reader can act on", advisory.Unscreened, fault)
	}
	if len(advisory.Excluded) != 0 {
		t.Errorf("an unscreened selection reported %d exclusions; nothing was screened, so there is nothing to have withdrawn: %#v",
			len(advisory.Excluded), advisory.Excluded)
	}
	// The control for the assertion in the test above: no screen was formed here, so
	// there is no evidence to name where it came from. Without this pair, an operation
	// that reported a source unconditionally would satisfy that one.
	if advisory.Provenance.Source != "" {
		t.Errorf("an unscreened selection named %q as its source; nothing was consulted, so there is none to name",
			advisory.Provenance.Source)
	}
}

// TestTheRecordingScreenLeavesTheCommandLinesProseByteIdentical is the proof that
// carrying the substitution as values changed nothing for a reader watching a
// terminal.
//
// It is a byte comparison of two runs over one fixture: the attachment, which
// records, against the version resolution the attachment routes through, which does
// not. The recorder is the only difference between the two paths, so a decorator
// that reordered, dropped or added a line would show up here as a diff — which a
// containment check over either run's output could not see.
func TestTheRecordingScreenLeavesTheCommandLinesProseByteIdentical(t *testing.T) {
	// Not parallel: the registry seams this drives are process-global.
	withFakeRegistryWithdrawing(t, []string{retractedRepo}, []string{retractedOlder, retractedNewest},
		map[string]string{retractedNewest: retractedReason})

	// A resolution per run, so the write the attachment performs is not a setting the
	// second run's resolution has to account for.
	var recorded bytes.Buffer
	if _, err := Add(context.Background(), resolveScratch(t, scratchConfig), AddOptions{
		Socket:  retractedSocket,
		Query:   retractedQuery,
		Notices: &recorded,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	var plain bytes.Buffer
	if _, err := ResolveLatestVersion(context.Background(), resolveScratch(t, scratchConfig),
		retractedHost, retractedRepo, &plain); err != nil {
		t.Fatalf("ResolveLatestVersion: %v", err)
	}

	if recorded.String() != plain.String() {
		t.Errorf("recording the substitution changed the prose\nwith a recorder:\n%s\nwithout one:\n%s", recorded.String(), plain.String())
	}
	// A positive control on the comparison itself: two empty buffers are byte-identical
	// too, so without this the assertion above would pass on a fixture that announced
	// nothing at all.
	if recorded.Len() == 0 {
		t.Fatal("the fixture announced nothing, so the byte comparison above compared two empty buffers")
	}
	t.Logf("the prose both paths produce:\n%s", recorded.String())
}

// failingWithdrawalLister is a withdrawal read that always faults, so the degraded
// posture can be driven. The fault text is the subject: what the advisory must
// carry is the reason the read failed, not the bare fact that it did.
func failingWithdrawalLister(fault string) func(context.Context, workspaceconfig.Config, string, string, []string, io.Writer) ([]registryindex.VersionDeprecation, error) {
	return func(context.Context, workspaceconfig.Config, string, string, []string, io.Writer) ([]registryindex.VersionDeprecation, error) {
		return nil, errString(fault)
	}
}

// errString is an error whose text is exactly the string it was built from, so the
// assertion above compares against the fixture's own constant rather than against
// whatever wrapping an error helper would add.
type errString string

func (e errString) Error() string { return string(e) }
