// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// enforceClosureDeprecation reports each closure entity's and plugin's
// deprecation status under one build-time gate, and halts only where the
// workspace asked for a halt. Its source is the lockfile Deprecation record — a
// retroactive marker discovered and verified at pull time, carrying a severity
// in the ordered set warning < error < retracted. It reads ONLY the lockfile, so
// it never touches a registry, preserving the ahead-of-time boundary that build
// is offline and lockfile-only.
//
// Reporting and halting are independent axes, and keeping them independent is
// the point of this gate:
//
//   - Reporting is unconditional. Every entity carrying a recorded marker
//     produces a notice on diagOut naming the entity, its severity and its
//     message, at every severity. No configuration silences it, so raising a
//     halt threshold never reduces what the build says.
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
// The notice reports an observation moment: a recorded marker states what a
// registry reported at some past pull, so how stale it might be is part of the
// fact.
//
// The walk always completes before a halt is returned, so a configured halt
// never costs the reader the notices for entities sorted after the halting one;
// the first halting marker in the walk owns the returned error. Iteration is
// over the sorted closure-entity and plugin URIs, so the surfaced order is
// deterministic and independent of closure order.
func enforceClosureDeprecation(cl closure.Closure, lf *lockfile.Lockfile, buildCfg workspaceconfig.BuildConfig, diagOut io.Writer) error {
	threshold := deprecation.Severity(buildCfg.HaltOnDeprecatedSeverity)
	// One stamp governs every recorded marker in this lockfile, so it is rendered
	// once rather than per entity.
	asOf := lockfileAsOfSuffix(lf.GeneratedAt)

	recByURI := make(map[string]*lockfile.DeprecationRecord)
	for i := range lf.Entities {
		if lf.Entities[i].Deprecation != nil {
			recByURI[lf.Entities[i].URI] = lf.Entities[i].Deprecation
		}
	}
	for i := range lf.Plugins {
		if lf.Plugins[i].Deprecation != nil {
			recByURI[lf.Plugins[i].URI] = lf.Plugins[i].Deprecation
		}
	}

	// Evaluate catalog entities AND Wasm plugins under one gate: both are
	// consumed by the build and both can carry either deprecation source. Their
	// URI namespaces are disjoint, so a single sorted walk is unambiguous and
	// deterministic.
	refs := make([]closure.ResolvedRef, 0, len(cl.Entities)+len(cl.Plugins))
	refs = append(refs, cl.Entities...)
	refs = append(refs, cl.Plugins...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].URI < refs[j].URI })

	// A halt is recorded rather than returned mid-walk: returning here would
	// drop the notices for every entity sorted after the halting one, which is
	// the one way a stricter dial could make the build say LESS.
	var halt error

	for _, e := range refs {
		// The moment qualifies the recorded fact, so it sits with the message
		// rather than after the threshold clause, which would read as though the
		// policy were what had been recorded.
		if rec, ok := recByURI[e.URI]; ok {
			fmt.Fprintf(diagOut, "  deprecation: %s is deprecated (%s): %s%s\n", e.URI, rec.Severity, rec.Message, asOf)
			// The empty threshold is read as "no halt configured" here rather
			// than left to MeetsThreshold's unranked arm: that arm exists for a
			// malformed value, and resting the shipped default on it would let a
			// later change in how an unrecognised severity ranks silently invert
			// the default from advising to halting.
			if halt == nil && threshold != "" && deprecation.MeetsThreshold(deprecation.Severity(rec.Severity), threshold) {
				halt = fmt.Errorf("%w: %s is deprecated (%s): %s%s: %s",
					deprecation.ErrDeprecatedEntity, e.URI, rec.Severity, rec.Message, asOf, haltThresholdClause(threshold))
			}
		}
	}
	return halt
}

// haltThresholdClause renders the threshold half of a halt message, naming the
// policy that triggered the halt. It returns a string rather than an error, and
// the halt message interpolates it, so the message wraps exactly ONE error: its
// sentinel, ErrDeprecatedEntity. A second wrapped error would make the message a
// multi-error, and a multi-error renders as its wrapped errors alone — dropping
// the format string's own text, which is where the entity, the severity, the
// message and the observation moment all live.
func haltThresholdClause(threshold deprecation.Severity) string {
	return fmt.Sprintf("halt_on_deprecated_severity is %q", threshold)
}

// lockfileAsOfSuffix renders when a recorded marker was observed — the moment the
// lockfile carrying it was generated — together with the command that refreshes it,
// so a reader can tell a stale record from a fresh one. A withdrawal is mutable
// registry state, so the record says what the registry reported at that pull and
// never what it reports now; the suffix exists to stop the message implying a
// currency the offline build cannot provide.
//
// The stamp can only lag the true observation, never lead it: it is set solely when
// a pull generates the lockfile, a re-pull that changes nothing keeps the earlier
// stamp, and the build preserves whatever it loaded. So the suffix errs toward
// reporting the record as older than it is, which is the safe direction.
//
// generatedAt is the lockfile's own stamp, already RFC 3339 when present; empty
// means the lockfile carries none, which is reported as such rather than rendered
// as a zero moment.
//
// This is deliberately NOT shared with the as-of wording on the index-served
// surfaces (the pull-time withdrawal notices and the inspect deprecation block).
// The two report different facts: a different provenance (the local registry index
// versus the lockfile), a different refresh command, and a different input (a parsed
// instant plus a presence flag versus an already-formatted string, so no formatting
// rule is duplicated here). Folding them together would take a template with every
// distinguishing part injected, which would single-source nothing.
func lockfileAsOfSuffix(generatedAt string) string {
	if generatedAt == "" {
		return " (recorded in the lockfile at pull, which carries no generation time; re-run `aggrega pull` to refresh it)"
	}
	return fmt.Sprintf(" (recorded in the lockfile at pull, as of %s; re-run `aggrega pull` to refresh it)", generatedAt)
}
