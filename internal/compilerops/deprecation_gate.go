// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// LockfileDeprecationVerdict reports the deprecation status of every pinned
// reference under one gate — catalog entities, the four library sections, and
// Wasm plugins — and halts only where the workspace asked for a halt. Its source
// is the shared report over the lockfile's recorded Deprecation records:
// retroactive markers discovered and verified at pull time, each carrying a
// severity in the ordered set warning < error < retracted. It reads ONLY the
// lockfile, so it never touches a registry, preserving the ahead-of-time
// boundary that a render is offline and lockfile-only.
//
// The report itself is produced by Lockfile.DeprecationFindings and is not
// composed here, because it is the same report on every surface that says
// anything about a deprecated reference. What belongs here is the VERDICT on it:
// a notice per finding, and a halt where the workspace configured one.
//
// Reporting and halting are independent axes, and keeping them independent is
// the point of this gate:
//
//   - Reporting is unconditional. Every finding is reported, naming the
//     reference, its severity and its message, at every severity. No
//     configuration silences it, so raising a halt threshold never reduces what
//     the surface says.
//   - Halting is governed by configuration alone. A recorded marker halts when
//     build.halt_on_deprecated_severity names a severity AND the recorded
//     severity meets or exceeds it. The dial is not set by default, so a default
//     workspace is advised and never stopped. That default is deliberate: a
//     recorded marker is mutable registry state applied after the pin was taken,
//     so halting on it would fail today a run that succeeded yesterday with no
//     input of the user's having changed.
//
// An unset threshold is tested for explicitly rather than left to rank below
// every severity, so the default behaviour does not depend on how an
// unrecognised severity happens to compare.
//
// The walk always completes before a halt is returned, so a configured halt
// never costs the reader the notices for references sorted after the halting
// one; the first halting finding in the walk owns the returned error. The
// producer sorts by URI, so the surfaced order is deterministic.
//
// The gate is kind-independent, and that is the property to preserve. It names
// no lockfile section: the findings come from the lockfile's own enumeration of
// its marker-bearing rows. A gate that named the sections instead reported on
// exactly the sections someone remembered, and a reference on an unnamed section
// built silently against a deprecated artifact — with the gate's own silence
// reading as "nothing is deprecated".
//
// report renders each notice in the calling surface's own voice. The gate does
// not format for a terminal, because its callers are a terminal, a structured
// tool payload and an editor channel.
func LockfileDeprecationVerdict(lf *lockfile.Lockfile, buildCfg workspaceconfig.BuildConfig, report func(string)) error {
	threshold := deprecation.Severity(buildCfg.HaltOnDeprecatedSeverity)

	// A halt is recorded rather than returned mid-walk: returning here would
	// drop the notices for every finding sorted after the halting one, which is
	// the one way a stricter dial could make the surface say LESS.
	var halt error

	if lf == nil {
		return nil
	}
	for _, f := range lf.DeprecationFindings() {
		report(f.Notice())
		if halt == nil {
			halt = deprecation.HaltFor(f.Notice(), deprecation.Severity(f.Severity), threshold)
		}
	}
	return halt
}

// TrustDeprecationVerdict reports every plugin trusted for an accept-or-reject
// verdict whose recorded artifact carries a withdrawal, halting only where the
// workspace asked for a halt.
//
// It is the sibling of the lockfile gate above, over the other store, and the
// two are separate for the reason the trust record's own producer states at
// length: the stores differ in scope, in subject, and in two inverted axes —
// lifecycle and semantics. What is deliberately NOT separate is the part a
// reader would be harmed by differing: the reporting rule is the same
// (unconditional, at every severity, no configuration silences it) and the
// halting rule is literally the same function against the same dial, so one
// workspace configuration cannot mean one thing for a pinned reference and
// another for a trusted plugin.
//
// Why a gate here at all, rather than leaving it to the pull that discovered the
// withdrawal: a pull happens once and a render happens continually, so a
// pull-time notice alone would say it to whoever ran the pull and to nobody
// afterwards. The artifact is fetched, cached and RUN at render, which is where
// the reader needs to be told.
//
// It reads only the record, so it touches no registry — the same ahead-of-time
// boundary the lockfile gate keeps. A nil record holds nothing and reports
// nothing, which is the workspace that has recorded no verdict attachment.
func TrustDeprecationVerdict(trust *pluginstrust.Trust, buildCfg workspaceconfig.BuildConfig, report func(string)) error {
	threshold := deprecation.Severity(buildCfg.HaltOnDeprecatedSeverity)

	// Recorded rather than returned mid-walk, for the reason the gate above
	// records: returning here would drop the notices for every finding sorted
	// after the halting one, which is the one way a stricter dial could make the
	// surface say LESS.
	var halt error

	if trust == nil {
		return nil
	}
	for _, f := range trust.DeprecationFindings() {
		report(f.Notice())
		if halt == nil {
			halt = deprecation.HaltFor(f.Notice(), deprecation.Severity(f.Severity), threshold)
		}
	}
	return halt
}
