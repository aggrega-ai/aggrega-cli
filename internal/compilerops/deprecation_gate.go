// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// DeprecationFinding is one entry of the withdrawal report over both stores: the
// notice a surface renders, in the words of the store that recorded the marker,
// and the severity that marker recorded, which is what a verdict compares against
// the workspace's halt threshold.
//
// It carries only those two because they are all any surface reads. The two
// stores' own finding types differ in what they identify — a pinned reference, a
// trusted plugin — and each renders its own notice, so the report holds the
// rendered sentence rather than a third spelling of either.
type DeprecationFinding struct {
	// Notice is the store's own one-line sentence for the marker, with no channel
	// prefix and no policy clause; a front door supplies its own prefix.
	Notice string
	// Severity is the recorded severity verbatim: warning, error or retracted.
	Severity deprecation.Severity
}

// DeprecationReport is the one report of recorded withdrawal markers over the two
// stores a marker can be recorded in: the lockfile, for every pinned reference,
// followed by the plugin trust record beside it, for every plugin attached at a
// verdict role. Each store's findings arrive in that store's own order — sorted by
// reference and by plugin coordinate — so the report's order is deterministic.
//
// Every surface that says anything about a withdrawn reference or plugin reads
// this one slice — the build's verdict, release-scoped validate and lint, the
// editor-facing daemon's validation and closure listing, the assistant's describe,
// validate and build, and the closure listing at a terminal. Which store holds a
// marker is decided by the attachment's role rather than by anything the reader
// chose, so a surface reading one store and not the other reports a withdrawal for
// some dependencies and stays silent for others, and that silence reads as
// "nothing is withdrawn". Composing the two here, once, is what keeps every
// surface reporting the same set; only the VERDICT differs between them — a
// surface producing an artifact may refuse on a finding, a surface producing a
// report enumerates it.
//
// The two stores keep separate producers for the reasons the trust record's own
// producer states; what is single-sourced here is the reading of both.
//
// A nil lockfile and a nil record both contribute nothing: where there is no store
// there are no recorded markers in it either.
func DeprecationReport(lf *lockfile.Lockfile, trust *pluginstrust.Trust) []DeprecationFinding {
	var out []DeprecationFinding
	for _, f := range lf.DeprecationFindings() {
		out = append(out, DeprecationFinding{Notice: f.Notice(), Severity: deprecation.Severity(f.Severity)})
	}
	for _, f := range trust.DeprecationFindings() {
		out = append(out, DeprecationFinding{Notice: f.Notice(), Severity: deprecation.Severity(f.Severity)})
	}
	return out
}

// ReleaseDeprecationReport is DeprecationReport for the Release at releasePath:
// lf is the lockfile the caller already loaded for that Release (nil where it has
// none), and the plugin trust record is read from beside the Release.
//
// A record that is absent contributes nothing, which is the workspace that has
// recorded no verdict attachment. A record that is there and cannot be read is
// returned as an error, beside the lockfile's own findings: an unreadable record
// leaves the surface unable to say whether any plugin was withdrawn, which is not
// the same as saying none was, so it must not pass as an empty one. The lockfile's
// findings still come back so a surface that reports what it could read before it
// refuses keeps the order it reports in.
func ReleaseDeprecationReport(lf *lockfile.Lockfile, releasePath string) ([]DeprecationFinding, error) {
	trust, _, err := pluginstrust.LoadOrStart(pluginstrust.PathFor(releasePath))
	if err != nil {
		return DeprecationReport(lf, nil), err
	}
	return DeprecationReport(lf, trust), nil
}

// DeprecationVerdict reports every finding of the withdrawal report and halts only
// where the workspace asked for a halt. It reads only what the two stores
// recorded, so it never touches a registry, preserving the ahead-of-time boundary
// that a render is offline.
//
// Reporting and halting are independent axes, and keeping them independent is the
// point of this gate:
//
//   - Reporting is unconditional. Every finding is reported, at every severity, in
//     the words of the store that recorded it. No configuration silences it, so
//     raising a halt threshold never reduces what the surface says.
//   - Halting is governed by configuration alone. A recorded marker halts when
//     build.halt_on_deprecated_severity names a severity AND the recorded severity
//     meets or exceeds it. The dial is not set by default, so a default workspace
//     is advised and never stopped. That default is deliberate: a recorded marker
//     is mutable registry state applied after the pin was taken, so halting on it
//     would fail today a run that succeeded yesterday with no input of the user's
//     having changed. It is one dial over both stores, so one workspace
//     configuration cannot mean one thing for a pinned reference and another for a
//     trusted plugin.
//
// An unset threshold is tested for explicitly rather than left to rank below every
// severity, so the default behaviour does not depend on how an unrecognised
// severity happens to compare.
//
// The walk always completes before a halt is returned, so a configured halt never
// costs the reader the notices for findings after the halting one, in either
// store; the first halting finding in report order owns the returned error.
//
// The gate is kind-independent, and that is the property to preserve: the
// findings come from each store's own enumeration of its marker-bearing entries,
// so no section is named here that a later one could be missing from.
//
// report renders each notice in the calling surface's own voice. The gate does not
// format for a terminal, because its callers are a terminal, a structured tool
// payload and an editor channel.
func DeprecationVerdict(findings []DeprecationFinding, buildCfg workspaceconfig.BuildConfig, report func(string)) error {
	threshold := deprecation.Severity(buildCfg.HaltOnDeprecatedSeverity)

	// A halt is recorded rather than returned mid-walk: returning here would drop
	// the notices for every finding after the halting one, which is the one way a
	// stricter dial could make the surface say LESS.
	var halt error
	for _, f := range findings {
		report(f.Notice)
		if halt == nil {
			halt = deprecation.HaltFor(f.Notice, f.Severity, threshold)
		}
	}
	return halt
}
