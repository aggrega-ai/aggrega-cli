// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog/catalogtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// priorPinFixture writes a prior lockfile carrying pins across several
// closure-walked kinds and returns its path. Several kinds rather than one so a
// targeted upgrade of a single URI leaves a non-empty honored set behind — an
// honored set that could be empty would make the reduction unobservable.
func priorPinFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.lock")
	// Every digest is a real content digest, the one spelling the lockfile loader
	// admits, and the plugin row records its content digest as its resolved
	// version, because a plugin artifact is pinned by its bytes.
	pluginDigest := digest.ContentDigest([]byte("eee"))
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "release.yaml",
		GeneratedAt: "2026-08-20T09:00:00Z",
		Entities: []lockfile.Entity{
			{Kind: "component", URI: "core/database", ResolvedVersion: "1.0.0", Digest: digest.ContentDigest([]byte("aaa"))},
			{Kind: "recipe", URI: "core/database-recipe", ResolvedVersion: "1.2.0", Digest: digest.ContentDigest([]byte("bbb"))},
			{Kind: "component", URI: "core/cache", ResolvedVersion: "2.0.0", Digest: digest.ContentDigest([]byte("ccc"))},
		},
		SchemaLibraries: []lockfile.SchemaLibrary{
			{URI: "core/DbSizing", ResolvedVersion: "1.0.0", Digest: digest.ContentDigest([]byte("ddd"))},
		},
		Plugins: []lockfile.Plugin{
			{URI: "core/helm-renderer", ResolvedVersion: pluginDigest, Digest: pluginDigest},
		},
	}
	if err := lockfile.WriteLockfile(path, lf); err != nil {
		t.Fatalf("write prior lockfile fixture: %v", err)
	}
	return path
}

// inlineReuseDecision is an independent re-derivation of the fill-vs-refuse
// policy, written out longhand. It is the oracle
// TestPinReuseFromLockfile_ParityWithIndependentDerivation compares against.
//
// DELIBERATE NEAR-DUPLICATE — do not "unify" it with PinReuseFromLockfile. A
// cross-check that delegates to the implementation it checks proves only that a
// function equals itself, so the branch structure, the pin reduction, the
// accept-an-unpinned-target arm and both halt messages are spelled out here on
// purpose — including the membership test over the vouched-for targets, which is
// written longhand rather than through the standard-library helper the
// implementation reaches for, so a wrong containment answer cannot be shared by
// both sides. It does share
// PinsFromLockfile, and that sharing is equally deliberate: the projection from
// a lockfile to a pin set is settled mechanics with its own guard, while what is
// under test here is the policy over it — which branch is taken, which pins
// survive, and which halt fires.
func inlineReuseDecision(lockfilePath, releasePath string, mode catalog.PinReuseMode, unpinnedTargets []string) (*lockfile.Lockfile, *catalog.ReuseVerification, error) {
	vouchedFor := func(target string) bool {
		for _, t := range unpinnedTargets {
			if t == target {
				return true
			}
		}
		return false
	}
	targeted := mode.Upgrade && len(mode.UpgradeTargets) > 0
	fullRefresh := mode.Upgrade && !targeted

	var prior *lockfile.Lockfile
	if !fullRefresh {
		if lf, _, lerr := lockfile.LoadLockfile(lockfilePath); lerr == nil {
			prior = lf
		}
	}

	// mode.VerifyOnly is deliberately not read: a verifying run is handed exactly
	// the verification the writing run it stands in for is handed.
	var reuse *catalog.ReuseVerification
	if targeted {
		if prior == nil {
			return nil, nil, fmt.Errorf("no lockfile to upgrade at %s; run `aggrega pull --release %s` first to create one", lockfilePath, releasePath)
		}
		pins, err := catalog.PinsFromLockfile(prior)
		if err != nil {
			return nil, nil, err
		}
		for _, target := range mode.UpgradeTargets {
			// A target names a reference: every kind's pin under it is dropped.
			pinned := false
			for key := range pins {
				if key.URI == target {
					delete(pins, key)
					pinned = true
				}
			}
			if !pinned && !vouchedFor(target) {
				return nil, nil, fmt.Errorf("%q is not pinned in the lockfile, and it is not a plugin whose identity is recorded beside the lockfile; nothing to upgrade", target)
			}
		}
		reuse = &catalog.ReuseVerification{PriorPins: pins}
	} else if prior != nil {
		pins, err := catalog.PinsFromLockfile(prior)
		if err != nil {
			return nil, nil, err
		}
		reuse = &catalog.ReuseVerification{PriorPins: pins}
	}
	return prior, reuse, nil
}

func pinKeys(pins catalog.Pins) []string {
	out := make([]string, 0, len(pins))
	for k := range pins {
		out = append(out, k.URI)
	}

	slices.Sort(out)
	return out
}

// TestPinReuseFromLockfile_ParityWithIndependentDerivation is the
// behaviour-identity proof for making the fill-vs-refuse decision callable:
// across every pull mode, PinReuseFromLockfile decides exactly what the
// longhand derivation in inlineReuseDecision decides — same prior-lockfile
// read, same honored pin set, same halt message.
//
// The parity is anti-needled two ways, because a comparison of two empty sets
// passes while proving nothing:
//
//   - The fixture is asserted to yield a NON-EMPTY pin set before any row runs,
//     so a row expecting no honored pins is a real decision rather than an empty
//     fixture.
//   - Every row that expects honored pins asserts the compared set is non-empty
//     BEFORE comparing it, so the set equality can never be a vacuous pass.
//
// The rows that most need this are the two the committed pin goldens cannot
// reach: they regenerate under a bare upgrade, which is the full-refresh branch
// where the honored set is empty by construction.
func TestPinReuseFromLockfile_ParityWithIndependentDerivation(t *testing.T) {
	const releasePath = "release.yaml"

	// Anti-needle, fixture tier: the prior lockfile must project to a non-empty
	// pin set, or every set comparison below is empty-vs-empty.
	fixtureProbe := priorPinFixture(t)
	probeLf, _, err := lockfile.LoadLockfile(fixtureProbe)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	fixturePins := catalogtest.MustPinsFromLockfile(t, probeLf)
	if len(fixturePins) == 0 {
		t.Fatalf("fixture projects to an EMPTY pin set: every comparison below would be a vacuous empty-vs-empty pass")
	}
	const wantFixturePins = 5
	if len(fixturePins) != wantFixturePins {
		t.Fatalf("fixture pin set = %v (%d); want %d pins so a single-target reduction leaves a non-empty honored set",
			pinKeys(fixturePins), len(fixturePins), wantFixturePins)
	}

	cases := []struct {
		name            string
		mode            catalog.PinReuseMode
		unpinnedTargets []string
		priorExists     bool
		wantTargeted    bool
		wantFullRefresh bool
		wantPrior       bool
		wantPins        []string // nil means: no reuse verification at all
		wantErr         func(lockfilePath string) string
	}{
		{
			name:        "default pull with a prior lockfile honors every recorded pin",
			mode:        catalog.PinReuseMode{},
			priorExists: true,
			wantPrior:   true,
			wantPins:    []string{"core/DbSizing", "core/cache", "core/database", "core/database-recipe", "core/helm-renderer"},
		},
		{
			name:        "first pull has nothing to honor",
			mode:        catalog.PinReuseMode{},
			priorExists: false,
		},
		{
			name:            "bare upgrade is a full refresh and reads no prior lockfile",
			mode:            catalog.PinReuseMode{Upgrade: true},
			priorExists:     true, // present on disk, and deliberately not read
			wantFullRefresh: true,
		},
		{
			name:         "targeted upgrade drops only the named pin",
			mode:         catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"core/database-recipe"}},
			priorExists:  true,
			wantTargeted: true,
			wantPrior:    true,
			wantPins:     []string{"core/DbSizing", "core/cache", "core/database", "core/helm-renderer"},
		},
		{
			name:         "targeted upgrade drops every named pin",
			mode:         catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"core/database-recipe", "core/helm-renderer"}},
			priorExists:  true,
			wantTargeted: true,
			wantPrior:    true,
			wantPins:     []string{"core/DbSizing", "core/cache", "core/database"},
		},
		{
			name:         "targeted upgrade with no prior lockfile halts",
			mode:         catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"core/database-recipe"}},
			priorExists:  false,
			wantTargeted: true,
			wantErr: func(lockfilePath string) string {
				return "no lockfile to upgrade at " + lockfilePath + "; run `aggrega pull --release release.yaml` first to create one"
			},
		},
		{
			name:         "targeted upgrade of an unpinned entity halts",
			mode:         catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"core/absent"}},
			priorExists:  true,
			wantTargeted: true,
			wantErr: func(string) string {
				return `"core/absent" is not pinned in the lockfile, and it is not a plugin whose identity is recorded beside the lockfile; nothing to upgrade`
			},
		},
		{
			name:            "targeted upgrade of an unpinned target the caller vouches for is accepted and drops no pin",
			mode:            catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"governance/policy-probe"}},
			unpinnedTargets: []string{"governance/policy-probe"},
			priorExists:     true,
			wantTargeted:    true,
			wantPrior:       true,
			wantPins:        []string{"core/DbSizing", "core/cache", "core/database", "core/database-recipe", "core/helm-renderer"},
		},
		{
			name:            "targeted upgrade mixing a pinned target and a vouched-for one drops only the pinned one",
			mode:            catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"core/database-recipe", "governance/policy-probe"}},
			unpinnedTargets: []string{"governance/policy-probe"},
			priorExists:     true,
			wantTargeted:    true,
			wantPrior:       true,
			wantPins:        []string{"core/DbSizing", "core/cache", "core/database", "core/helm-renderer"},
		},
		{
			name:            "a vouched-for set that does not name the target still halts",
			mode:            catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"governance/absent-probe"}},
			unpinnedTargets: []string{"governance/policy-probe"},
			priorExists:     true,
			wantTargeted:    true,
			wantErr: func(string) string {
				return `"governance/absent-probe" is not pinned in the lockfile, and it is not a plugin whose identity is recorded beside the lockfile; nothing to upgrade`
			},
		},
		{
			name:        "verify-only with a prior lockfile honors every recorded pin, as the reconcile it stands in for does",
			mode:        catalog.PinReuseMode{VerifyOnly: true},
			priorExists: true,
			wantPrior:   true,
			wantPins:    []string{"core/DbSizing", "core/cache", "core/database", "core/database-recipe", "core/helm-renderer"},
		},
		{
			name:        "verify-only with no prior lockfile has nothing to honor",
			mode:        catalog.PinReuseMode{VerifyOnly: true},
			priorExists: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lockfilePath := filepath.Join(t.TempDir(), "release.lock")
			if tc.priorExists {
				lockfilePath = priorPinFixture(t)
			}

			if got := tc.mode.Targeted(); got != tc.wantTargeted {
				t.Errorf("Targeted() = %v; want %v", got, tc.wantTargeted)
			}
			if got := tc.mode.FullRefresh(); got != tc.wantFullRefresh {
				t.Errorf("FullRefresh() = %v; want %v", got, tc.wantFullRefresh)
			}

			got, gotErr := catalog.PinReuseFromLockfile("", lockfilePath, releasePath, tc.mode, tc.unpinnedTargets, nil)
			wantPrior, wantReuse, wantErr := inlineReuseDecision(lockfilePath, releasePath, tc.mode, tc.unpinnedTargets)

			// Halt parity: the lifted decision and the longhand derivation must
			// refuse in the same cases with the same user-facing message.
			switch {
			case tc.wantErr != nil:
				want := tc.wantErr(lockfilePath)
				if gotErr == nil {
					t.Fatalf("PinReuseFromLockfile: want halt %q; got nil", want)
				}
				if gotErr.Error() != want {
					t.Errorf("halt message =\n  %q\nwant\n  %q", gotErr.Error(), want)
				}
				if wantErr == nil || wantErr.Error() != gotErr.Error() {
					t.Errorf("halt diverges from the independent derivation: lifted %v; derived %v", gotErr, wantErr)
				}
				return
			default:
				if gotErr != nil {
					t.Fatalf("PinReuseFromLockfile: unexpected halt: %v", gotErr)
				}
				if wantErr != nil {
					t.Fatalf("independent derivation halted where the lifted decision did not: %v", wantErr)
				}
			}

			// The prior-lockfile read: taken, or deliberately not taken.
			if (got.Prior != nil) != tc.wantPrior {
				t.Errorf("prior lockfile read = %v; want %v", got.Prior != nil, tc.wantPrior)
			}
			if (got.Prior != nil) != (wantPrior != nil) {
				t.Errorf("prior-read decision diverges from the independent derivation: lifted %v; derived %v",
					got.Prior != nil, wantPrior != nil)
			}

			// The honored pin set.
			if tc.wantPins == nil {
				if got.Verification != nil {
					t.Errorf("reuse verification = %v; want none", pinKeys(got.Verification.PriorPins))
				}
				if wantReuse != nil {
					t.Errorf("independent derivation established a verification where the lifted decision did not: %v",
						pinKeys(wantReuse.PriorPins))
				}
				return
			}
			if got.Verification == nil {
				t.Fatalf("reuse verification = nil; want the pins %v honored", tc.wantPins)
			}
			// Anti-needle, row tier: assert the compared set is non-empty BEFORE
			// comparing, so no equality below can pass on two empty sets.
			if len(got.Verification.PriorPins) == 0 {
				t.Fatalf("honored pin set is EMPTY; want %v — an empty-vs-empty comparison would pass while proving nothing", tc.wantPins)
			}
			if diff := pinKeys(got.Verification.PriorPins); !slices.Equal(diff, tc.wantPins) {
				t.Errorf("honored pins = %v; want %v", diff, tc.wantPins)
			}
			if wantReuse == nil {
				t.Fatalf("independent derivation established no verification; lifted honored %v", pinKeys(got.Verification.PriorPins))
			}
			if !slices.Equal(pinKeys(got.Verification.PriorPins), pinKeys(wantReuse.PriorPins)) {
				t.Errorf("honored pins diverge from the independent derivation: lifted %v; derived %v",
					pinKeys(got.Verification.PriorPins), pinKeys(wantReuse.PriorPins))
			}
			// Pin contents, not just the key set: a reduction that kept the right
			// URIs at the wrong versions or digests would pass a key comparison.
			for uri, pin := range got.Verification.PriorPins {
				if pin != wantReuse.PriorPins[uri] {
					t.Errorf("pin %+v = %+v; independent derivation has %+v", uri, pin, wantReuse.PriorPins[uri])
				}
			}
		})
	}
}

// TestPinReuseFromLockfile_VerifyOnlyHonoursWhatTheReconcileHonours pins the one
// pin policy the verify-only run shares with the reconcile it answers for: over
// the same prior lockfile, the verify-only decision reads the same prior, honours
// the same pins at the same versions and digests, and threads the same notice
// sink. A verification handed fewer pins than the reconcile would compare the
// lockfile against a resolution no pull performs — a floated bare reference read
// as drift, a cached plugin artifact nothing can address — so each row fails if
// the two decisions part.
//
// Each row is asserted to honour a non-empty set before the sets are compared,
// where the fixture has pins to honour, so equality cannot pass on two empties.
func TestPinReuseFromLockfile_VerifyOnlyHonoursWhatTheReconcileHonours(t *testing.T) {
	unparsable := func(t *testing.T) string {
		path := filepath.Join(t.TempDir(), "release.lock")
		if err := os.WriteFile(path, []byte("this: is: not: a: lockfile\n"), 0o644); err != nil {
			t.Fatalf("seed unparsable lockfile: %v", err)
		}
		return path
	}
	cases := []struct {
		name     string
		lockfile func(t *testing.T) string
		wantPins bool
	}{
		{name: "a prior lockfile", lockfile: priorPinFixture, wantPins: true},
		{name: "no prior lockfile", lockfile: func(t *testing.T) string { return filepath.Join(t.TempDir(), "release.lock") }},
		{name: "an unreadable prior lockfile", lockfile: unparsable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.lockfile(t)
			var reconcileNotices, verifyNotices []string
			reconcile, rerr := catalog.PinReuseFromLockfile("", path, "release.yaml", catalog.PinReuseMode{}, nil,
				func(n string) { reconcileNotices = append(reconcileNotices, n) })
			verify, verr := catalog.PinReuseFromLockfile("", path, "release.yaml", catalog.PinReuseMode{VerifyOnly: true}, nil,
				func(n string) { verifyNotices = append(verifyNotices, n) })
			if rerr != nil || verr != nil {
				t.Fatalf("decision halted: reconcile %v, verify-only %v", rerr, verr)
			}
			if (reconcile.Prior != nil) != (verify.Prior != nil) {
				t.Errorf("prior lockfile read: reconcile %t, verify-only %t", reconcile.Prior != nil, verify.Prior != nil)
			}
			if (reconcile.Verification != nil) != (verify.Verification != nil) {
				t.Fatalf("reuse verification established: reconcile %t, verify-only %t — the verify-only run would compare against a resolution the reconcile does not perform",
					reconcile.Verification != nil, verify.Verification != nil)
			}
			if !tc.wantPins {
				if verify.Verification != nil {
					t.Errorf("verify-only honoured %v over a fixture with nothing to honour", pinKeys(verify.Verification.PriorPins))
				}
				return
			}
			if verify.Verification == nil || len(verify.Verification.PriorPins) == 0 {
				t.Fatalf("verify-only honours no pins over a prior lockfile that records some")
			}
			if !slices.Equal(pinKeys(reconcile.Verification.PriorPins), pinKeys(verify.Verification.PriorPins)) {
				t.Errorf("honoured pins: reconcile %v, verify-only %v",
					pinKeys(reconcile.Verification.PriorPins), pinKeys(verify.Verification.PriorPins))
			}
			for uri, pin := range reconcile.Verification.PriorPins {
				if got := verify.Verification.PriorPins[uri]; got != pin {
					t.Errorf("pin %+v: reconcile %+v, verify-only %+v", uri, pin, got)
				}
			}
			if verify.Verification.Warn == nil {
				t.Fatalf("verify-only dropped the notice sink: a drifted cache slot it refetches would be refetched silently")
			}
			verify.Verification.Warn("probe")
			reconcile.Verification.Warn("probe")
			if len(verifyNotices) != 1 || len(reconcileNotices) != 1 {
				t.Errorf("notices reaching each sink: reconcile %v, verify-only %v; want one each", reconcileNotices, verifyNotices)
			}
		})
	}
}

// TestPinReuseFromLockfile_ThreadsTheNoticeSink proves the notice sink handed to
// the decision reaches the reuse verification the closure lookup consumes: a
// dropped sink would silence the cache-drift notices without failing anything.
func TestPinReuseFromLockfile_ThreadsTheNoticeSink(t *testing.T) {
	var notices []string
	got, err := catalog.PinReuseFromLockfile("", priorPinFixture(t), "release.yaml",
		catalog.PinReuseMode{}, nil, func(notice string) { notices = append(notices, notice) })
	if err != nil {
		t.Fatalf("PinReuseFromLockfile: %v", err)
	}
	if got.Verification == nil || got.Verification.Warn == nil {
		t.Fatalf("notice sink dropped: verification = %+v", got.Verification)
	}
	got.Verification.Warn("cache entry refreshed")
	if len(notices) != 1 || notices[0] != "cache entry refreshed" {
		t.Errorf("notices = %v; want exactly the one handed to the sink", notices)
	}
}

// TestPinReuseFromLockfile_UnreadablePriorIsAbsent proves a prior lockfile that
// cannot be parsed is treated as absent rather than halting a default pull: the
// pull re-establishes its pins from scratch, and a targeted upgrade halts with
// the no-lockfile message because it has no pin set to reduce.
func TestPinReuseFromLockfile_UnreadablePriorIsAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.lock")
	if err := os.WriteFile(path, []byte("this: is: not: a: lockfile\n"), 0o644); err != nil {
		t.Fatalf("seed unparsable lockfile: %v", err)
	}

	got, err := catalog.PinReuseFromLockfile("", path, "release.yaml", catalog.PinReuseMode{}, nil, nil)
	if err != nil {
		t.Fatalf("default pull over an unparsable prior lockfile: %v", err)
	}
	if got.Prior != nil || got.Verification != nil {
		t.Errorf("unparsable prior lockfile honored something: prior=%v verification=%+v", got.Prior != nil, got.Verification)
	}

	_, err = catalog.PinReuseFromLockfile("", path, "release.yaml",
		catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"core/database"}}, nil, nil)
	if err == nil {
		t.Fatal("targeted upgrade over an unparsable prior lockfile: want a halt; got nil")
	}
	want := fmt.Sprintf("no lockfile to upgrade at %s; run `aggrega pull --release release.yaml` first to create one", path)
	if err.Error() != want {
		t.Errorf("halt message =\n  %q\nwant\n  %q", err.Error(), want)
	}
}

// TestPinReuseFromLockfile_APriorLeadingOutHalts proves the prior lockfile is
// read for its pins only where it lies inside the root its write is held to: a
// lockfile beside a Release in the workspace that is a link to a lockfile kept
// outside halts the decision naming the link — rather than honouring pins read
// from elsewhere on the machine, or passing over the file as absent — while a run
// that reads no prior lockfile at all is not held up by it.
//
// The control is the same lockfile beside the Release itself, whose pins are
// honoured, so a halting row's halt is the link's doing.
func TestPinReuseFromLockfile_APriorLeadingOutHalts(t *testing.T) {
	for _, row := range []struct {
		name   string
		linked bool
		mode   catalog.PinReuseMode
		halts  bool
	}{
		{name: "control: the lockfile beside the Release is read for its pins", mode: catalog.PinReuseMode{}},
		{name: "a lockfile that is a link leading out halts a default pull", linked: true, mode: catalog.PinReuseMode{}, halts: true},
		{name: "a lockfile that is a link leading out halts a verify", linked: true,
			mode: catalog.PinReuseMode{VerifyOnly: true}, halts: true},
		{name: "a lockfile that is a link leading out halts a targeted upgrade", linked: true,
			mode: catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"core/database"}}, halts: true},
		{name: "a bare upgrade reads no prior lockfile, so the link holds nothing up", linked: true,
			mode: catalog.PinReuseMode{Upgrade: true}},
	} {
		t.Run(row.name, func(t *testing.T) {
			ws := t.TempDir()
			lockPath := filepath.Join(ws, "release.lock")
			prior := priorPinFixture(t)
			if row.linked {
				if err := os.Symlink(prior, lockPath); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			} else {
				data, err := os.ReadFile(prior)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(lockPath, data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := catalog.PinReuseFromLockfile(ws, lockPath, "release.yaml", row.mode, nil, nil)
			if row.halts {
				if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
					t.Fatalf("want the read-leads-out-of-workspace halt, got %v", err)
				}
				if !strings.Contains(err.Error(), "the link "+lockPath+" leads to "+prior) {
					t.Errorf("the halt does not name the link and where it leads: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("PinReuseFromLockfile: %v", err)
			}
			if wantPrior := !row.mode.FullRefresh(); (got.Prior != nil) != wantPrior {
				t.Errorf("prior read = %v, want %v", got.Prior != nil, wantPrior)
			}
		})
	}
}
