// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"fmt"
	"slices"

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
	// VerifyOnly marks a run that compares the lockfile against the resolution
	// instead of writing it. It moves no pin decision: a verifying run is handed
	// the reuse verification the writing run it stands in for is handed, so the
	// pins it honours — and what it reads from and fetches into the content cache —
	// are that run's, which is what lets it answer whether that run would change
	// the lockfile.
	//
	// That includes what counts as a cache entry. A slot holding content but no
	// manifest is an incomplete entry, which neither the pull's reuse check nor the
	// build's verifying read serves — the two decide it through one rule
	// (persistedManifest) — so a verifying run meets such a slot at a pinned
	// version exactly as the writing run does: as a miss, which it fetches again
	// where a registry is reachable and refuses where none is, as a build refuses
	// it.
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
// signatures, deprecations and external inputs. Reading it in one place and
// handing it back is what keeps those consumers from disagreeing about whether a
// prior lockfile is in play.
type PinReuse struct {
	Prior        *lockfile.Lockfile
	Verification *ReuseVerification
}

// PinReuseFromLockfile decides, for one pull, whether the prior lockfile at
// lockfilePath is read and which of its pins are honored — and performs that
// read, exactly once, so every consumer reads the returned PinReuse rather than
// the file.
//
// The outcomes below hold for a verify-only run exactly as for the writing run it
// stands in for, so a verification compares the lockfile against the resolution
// that run would perform — the recorded pins honoured, the complete cache slots
// they name read, and a slot holding content without its manifest treated as the
// miss it is — rather than against one no pull performs.
//
//   - A targeted upgrade holds every pin it did not name, so it requires a prior
//     lockfile: with none there is no pin set to reduce, and it halts. Reducing
//     that set is the whole mechanism — a URI dropped from the honored pins
//     floats to its highest satisfying version while every URI still present is
//     honored. A named target that is neither pinned nor vouched for by the
//     caller halts rather than silently upgrading nothing.
//   - Otherwise, when a prior lockfile was read, every pin it records is honored.
//     A bare upgrade reads no prior lockfile and so honors nothing: floating
//     every pin and carrying forward neither the recorded signatures and
//     deprecations nor the external inputs is what a bare upgrade means.
//
// releasePath names the Release in the remediation hint of the no-lockfile halt.
// warn receives the reuse notices the closure lookup emits when a cache slot has
// drifted from its pin; a nil warn discards them.
//
// unpinnedTargets names identities a targeted upgrade may legitimately name even
// though no pin exists for them, so naming one is accepted and reduces nothing.
// Not everything a resolution re-resolves is pinned: an artifact whose identity is
// recorded beside the lockfile rather than in it has no pin to drop, and a target
// checked against the pin set alone would be refused before the run could reach
// what it does govern. They arrive as plain strings, and this package stays
// unaware of where they come from — the same division the pin maps draw, so the
// composition has one shape rather than a second mechanism for each source.
//
// The lockfile is only ever read here, never written or removed: deciding what
// to honor leaves the recorded pins exactly as they were, so a run that cannot
// complete destroys nothing it never replaced. The read is held to the root a
// write of the lockfile is held to, for the workspace at workspaceRoot
// ([lockfile.CheckRead]): a prior lockfile that is a link leading out of that root
// halts the decision naming the link, rather than being read for its pins or
// passed over as absent — pins taken from elsewhere on the machine would decide
// what this run resolves.
func PinReuseFromLockfile(workspaceRoot, lockfilePath, releasePath string, mode PinReuseMode, unpinnedTargets []string, warn func(notice string)) (PinReuse, error) {
	var reuse PinReuse
	if !mode.FullRefresh() {
		// A prior lockfile that cannot be read is treated as absent: a default pull
		// re-establishes its pins from scratch, and a targeted upgrade halts below
		// because it has no pin set to reduce. One that leads out is not unreadable
		// but refused, and the refusal is the answer.
		lf, err := lockfile.LoadIfReadable(workspaceRoot, lockfilePath)
		if err != nil {
			return PinReuse{}, err
		}
		reuse.Prior = lf
	}
	if mode.Targeted() {
		if reuse.Prior == nil {
			return PinReuse{}, fmt.Errorf("no lockfile to upgrade at %s; run `aggrega pull --release %s` first to create one", lockfilePath, releasePath)
		}
		pins, err := PinsFromLockfile(reuse.Prior)
		if err != nil {
			return PinReuse{}, err
		}
		for _, target := range mode.UpgradeTargets {
			// A target names a reference, so it drops the pin of every kind recorded
			// under that reference: an upgrade of `compose/app` re-resolves the
			// Component and the Recipe of that name alike.
			dropped := false
			for key := range pins {
				if key.URI == target {
					delete(pins, key)
					dropped = true
				}
			}
			if dropped {
				continue
			}
			// An unpinned target reduces nothing — there is no pin to drop — so it is
			// accepted and left alone. What it does is reach the run: whatever governs
			// that identity outside the lockfile sees the target named, which is the
			// only way an unpinned identity can be re-resolved on purpose.
			if !slices.Contains(unpinnedTargets, target) {
				return PinReuse{}, fmt.Errorf("%q is not pinned in the lockfile, and it is not a plugin whose identity is recorded beside the lockfile; nothing to upgrade", target)
			}
		}
		reuse.Verification = &ReuseVerification{PriorPins: pins, Warn: warn}
		return reuse, nil
	}
	if reuse.Prior != nil {
		pins, err := PinsFromLockfile(reuse.Prior)
		if err != nil {
			return PinReuse{}, err
		}
		reuse.Verification = &ReuseVerification{PriorPins: pins, Warn: warn}
	}
	return reuse, nil
}
