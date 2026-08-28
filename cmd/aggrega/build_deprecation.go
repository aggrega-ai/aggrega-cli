// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// enforceLockfileDeprecation reports the deprecation status of every pinned
// reference under one build-time gate — catalog entities, the four library
// sections, and Wasm plugins — and halts only where the workspace asked for a
// halt. Its source is the shared report over the lockfile's recorded
// Deprecation records — retroactive markers discovered and verified at pull
// time, each carrying a severity in the ordered set
// warning < error < retracted. It reads ONLY the lockfile, so it never touches a
// registry, preserving the ahead-of-time boundary that build is offline and
// lockfile-only.
//
// The report itself is produced by Lockfile.DeprecationFindings and is not
// composed here, because it is the same report on every surface that says
// anything about a deprecated reference. What belongs here is the BUILD's
// verdict on it, which is what this gate is: a notice per finding, and a halt
// where the workspace configured one. The gate's own godoc for why the recorded
// rows are the consumed references, and for what those rows cannot reach, sits
// with the producer.
//
// Reporting and halting are independent axes, and keeping them independent is
// the point of this gate:
//
//   - Reporting is unconditional. Every finding produces a notice on diagOut
//     naming the reference, its severity and its message, at every severity. No
//     configuration silences it, so raising a halt threshold never reduces what
//     the build says.
//   - Halting is governed by configuration alone. A recorded marker halts when
//     build.halt_on_deprecated_severity names a severity AND the recorded
//     severity meets or exceeds it. The dial is not set by default, so a default
//     workspace is advised and never stopped. That default is deliberate: a
//     recorded marker is mutable registry state applied after the pin was taken,
//     so halting on it would fail today a build that succeeded yesterday with no
//     input of the user's having changed.
//
// An unset threshold is tested for explicitly rather than left to rank below
// every severity, so the default behaviour does not depend on how an
// unrecognised severity happens to compare.
//
// The walk always completes before a halt is returned, so a configured halt
// never costs the reader the notices for references sorted after the halting one;
// the first halting finding in the walk owns the returned error. The producer
// sorts by URI, so the surfaced order is deterministic.
//
// The gate is kind-independent, and that is the property to preserve. It names
// no lockfile section: the findings come from the lockfile's own enumeration of
// its marker-bearing rows. A gate that named the sections instead reported on
// exactly the sections someone remembered, and a reference on an unnamed section
// built silently against a deprecated artifact — with the gate's own silence
// reading as "nothing is deprecated". Adding a section to the lockfile is a
// build-time question rather than a silent omission, because the coverage
// assertion beside that enumeration requires every declared section to be
// classified.
func enforceLockfileDeprecation(lf *lockfile.Lockfile, buildCfg workspaceconfig.BuildConfig, diagOut io.Writer) error {
	threshold := deprecation.Severity(buildCfg.HaltOnDeprecatedSeverity)

	// A halt is recorded rather than returned mid-walk: returning here would
	// drop the notices for every finding sorted after the halting one, which is
	// the one way a stricter dial could make the build say LESS.
	var halt error

	for _, f := range lf.DeprecationFindings() {
		fmt.Fprintf(diagOut, "  deprecation: %s\n", f.Notice())
		// The halting rule and the halt's wording are the shared ones every
		// artifact-producing surface applies, so this verb and the AI-native build
		// cannot come to halt on different markers under the same configuration.
		if halt == nil {
			halt = deprecation.HaltFor(f.Notice(), deprecation.Severity(f.Severity), threshold)
		}
	}
	return halt
}
