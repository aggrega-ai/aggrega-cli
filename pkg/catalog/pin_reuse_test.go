// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// priorPinFixture writes a prior lockfile carrying pins across several
// closure-walked kinds and returns its path. Several kinds rather than one so a
// targeted upgrade of a single URI leaves a non-empty honored set behind — an
// honored set that could be empty would make the reduction unobservable.
func priorPinFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.lock")
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "release.yaml",
		GeneratedAt: "2026-08-20T09:00:00Z",
		Entities: []lockfile.Entity{
			{URI: "core/database", ResolvedVersion: "1.0.0", Digest: "sha256:aaa"},
			{URI: "core/database-recipe", ResolvedVersion: "1.2.0", Digest: "sha256:bbb"},
			{URI: "core/cache", ResolvedVersion: "2.0.0", Digest: "sha256:ccc"},
		},
		SchemaLibraries: []lockfile.SchemaLibrary{
			{URI: "core/DbSizing", ResolvedVersion: "1.0.0", Digest: "sha256:ddd"},
		},
		Plugins: []lockfile.Plugin{
			{URI: "core/helm-renderer", ResolvedVersion: "1.0.0", Digest: "sha256:eee"},
		},
	}
	if err := lockfile.WriteLockfile(path, lf); err != nil {
		t.Fatalf("write prior lockfile fixture: %v", err)
	}
	return path
}

// inlineReuseDecision is an independent re-derivation of the fill-vs-refuse
// policy, written out longhand as the pull command's own control flow expressed
// it before the decision became callable. It is the oracle
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

	var reuse *catalog.ReuseVerification
	if !mode.VerifyOnly {
		if targeted {
			if prior == nil {
				return nil, nil, fmt.Errorf("no lockfile to upgrade at %s; run `aggrega pull --release %s` first to create one", lockfilePath, releasePath)
			}
			pins := catalog.PinsFromLockfile(prior)
			for _, target := range mode.UpgradeTargets {
				_, pinned := pins[target]
				if !pinned && !vouchedFor(target) {
					return nil, nil, fmt.Errorf("%q is not pinned in the lockfile, and it is not a plugin whose identity is recorded beside the lockfile; nothing to upgrade", target)
				}
				if pinned {
					delete(pins, target)
				}
			}
			reuse = &catalog.ReuseVerification{PriorPins: pins}
		} else if prior != nil {
			reuse = &catalog.ReuseVerification{PriorPins: catalog.PinsFromLockfile(prior)}
		}
	}
	return prior, reuse, nil
}

func pinKeys(pins map[string]catalog.Pin) []string {
	out := make([]string, 0, len(pins))
	for k := range pins {
		out = append(out, k)
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
	fixturePins := catalog.PinsFromLockfile(probeLf)
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
			name:        "verify-only reads the prior lockfile and honors nothing",
			mode:        catalog.PinReuseMode{VerifyOnly: true},
			priorExists: true,
			wantPrior:   true,
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

			got, gotErr := catalog.PinReuseFromLockfile(lockfilePath, releasePath, tc.mode, tc.unpinnedTargets, nil)
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
					t.Errorf("pin %q = %+v; independent derivation has %+v", uri, pin, wantReuse.PriorPins[uri])
				}
			}
		})
	}
}

// TestPinReuseFromLockfile_ThreadsTheNoticeSink proves the notice sink handed to
// the decision reaches the reuse verification the closure lookup consumes: a
// dropped sink would silence the cache-drift notices without failing anything.
func TestPinReuseFromLockfile_ThreadsTheNoticeSink(t *testing.T) {
	var notices []string
	got, err := catalog.PinReuseFromLockfile(priorPinFixture(t), "release.yaml",
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

	got, err := catalog.PinReuseFromLockfile(path, "release.yaml", catalog.PinReuseMode{}, nil, nil)
	if err != nil {
		t.Fatalf("default pull over an unparsable prior lockfile: %v", err)
	}
	if got.Prior != nil || got.Verification != nil {
		t.Errorf("unparsable prior lockfile honored something: prior=%v verification=%+v", got.Prior != nil, got.Verification)
	}

	_, err = catalog.PinReuseFromLockfile(path, "release.yaml",
		catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"core/database"}}, nil, nil)
	if err == nil {
		t.Fatal("targeted upgrade over an unparsable prior lockfile: want a halt; got nil")
	}
	want := fmt.Sprintf("no lockfile to upgrade at %s; run `aggrega pull --release release.yaml` first to create one", path)
	if err.Error() != want {
		t.Errorf("halt message =\n  %q\nwant\n  %q", err.Error(), want)
	}
}
