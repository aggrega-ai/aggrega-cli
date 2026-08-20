// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// PinReuseMode is the pull invocation the prior-pin reuse decision reads:
// whether an upgrade was requested, which entities it named, and whether the run
// only verifies. It holds flags alone — the lockfile and Release paths are the
// decision's own arguments — so it is constructible from a command's flags with
// nothing resolved first.
type PinReuseMode struct {
	// Upgrade requests fresh resolution. With no targets it means every pin.
	Upgrade bool
	// UpgradeTargets names the entity URIs to upgrade. Read only when Upgrade is set.
	UpgradeTargets []string
	// VerifyOnly is a read-only run: it writes no lockfile and re-fetches nothing.
	VerifyOnly bool
}

// Targeted reports whether the upgrade names specific entities. A targeted
// upgrade holds every pin it did not name, so it needs a prior pin set to hold
// them from.
func (m PinReuseMode) Targeted() bool { return m.Upgrade && len(m.UpgradeTargets) > 0 }

// FullRefresh reports whether the run re-resolves every entity from current
// registry state. A bare upgrade — one naming no targets — floats every pin and
// carries nothing forward from the previous pin set, so it reads no prior
// lockfile at all.
func (m PinReuseMode) FullRefresh() bool { return m.Upgrade && !m.Targeted() }

// PinReuse is one pull's resolved prior-pin reuse: the prior lockfile the run
// read (nil when it read none), together with the reuse verification to hand
// ClosureLookupWithReuse (nil when there is nothing to verify against).
//
// Prior is the value every prior-lockfile consumer reads — the reuse check
// carried in Verification, and the caller's own carry-forwards of the recorded
// deprecations and external inputs. Reading it in one place and handing it back
// is what keeps those consumers from disagreeing about whether a prior lockfile
// is in play.
type PinReuse struct {
	Prior        *lockfile.Lockfile
	Verification *ReuseVerification
}

// PinReuseFromLockfile decides, for one pull, whether the prior lockfile at
// lockfilePath is read and which of its pins are honored — and performs that
// read, exactly once, so every consumer reads the returned PinReuse rather than
// the file.
//
// The outcomes:
//
//   - A verify-only run establishes no reuse verification. It compares the
//     lockfile against the freshly-computed closure instead of re-fetching, so
//     there is nothing to re-pin.
//   - A targeted upgrade holds every pin it did not name, so it requires a prior
//     lockfile: with none there is no pin set to reduce, and it halts. Reducing
//     that set is the whole mechanism — a URI dropped from the honored pins
//     floats to its highest satisfying version while every URI still present is
//     honored. A named target that is not pinned halts rather than silently
//     upgrading nothing.
//   - Otherwise, when a prior lockfile was read, every pin it records is honored.
//     A bare upgrade reads no prior lockfile and so honors nothing: floating
//     every pin and carrying forward neither the recorded deprecations nor the
//     external inputs is what a bare upgrade means.
//
// releasePath names the Release in the remediation hint of the no-lockfile halt.
// warn receives the reuse notices the closure lookup emits when a cache slot has
// drifted from its pin; a nil warn discards them.
//
// The lockfile is only ever read here, never written or removed: deciding what
// to honor leaves the recorded pins exactly as they were, so a run that cannot
// complete destroys nothing it never replaced.
func PinReuseFromLockfile(lockfilePath, releasePath string, mode PinReuseMode, warn func(notice string)) (PinReuse, error) {
	var reuse PinReuse
	if !mode.FullRefresh() {
		// A prior lockfile that cannot be read is treated as absent: a default pull
		// re-establishes its pins from scratch, and a targeted upgrade halts below
		// because it has no pin set to reduce.
		if lf, _, err := lockfile.LoadLockfile(lockfilePath); err == nil {
			reuse.Prior = lf
		}
	}
	if mode.VerifyOnly {
		return reuse, nil
	}
	if mode.Targeted() {
		if reuse.Prior == nil {
			return PinReuse{}, fmt.Errorf("no lockfile to upgrade at %s; run `aggrega pull --release %s` first to create one", lockfilePath, releasePath)
		}
		pins := PinsFromLockfile(reuse.Prior)
		for _, target := range mode.UpgradeTargets {
			if _, ok := pins[target]; !ok {
				return PinReuse{}, fmt.Errorf("entity %q is not pinned in the lockfile; nothing to upgrade", target)
			}
			delete(pins, target)
		}
		reuse.Verification = &ReuseVerification{PriorPins: pins, Warn: warn}
		return reuse, nil
	}
	if reuse.Prior != nil {
		reuse.Verification = &ReuseVerification{PriorPins: PinsFromLockfile(reuse.Prior), Warn: warn}
	}
	return reuse, nil
}
