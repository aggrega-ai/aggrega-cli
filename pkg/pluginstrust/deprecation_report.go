// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginstrust

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
)

// DeprecationFinding reports one trusted plugin whose artifact carries a recorded
// withdrawal. It is the unit every surface that reports a withdrawn verdict plugin
// renders, so the wording of that report has one home and the surfaces cannot come
// to say different things about the same fact.
//
// Severity is the recorded severity verbatim, in the ordered set
// warning < error < retracted, so a caller comparing it against a configured
// threshold reads the same string the withdrawal recorded rather than a
// re-classification. Message is the withdrawal's own recorded message.
type DeprecationFinding struct {
	// Plugin is the identity coordinate of the plugin the withdrawal was recorded
	// against.
	Plugin string
	// Severity is the recorded severity: warning, error or retracted.
	Severity string
	// Message is the withdrawal's recorded human-readable message.
	Message string
}

// Notice renders the finding as one line: the plugin, its recorded severity, the
// recorded message, and where the fact came from together with the command that
// refreshes it.
//
// It carries no channel prefix and no policy clause. A front door supplies its own
// prefix, because the prefix belongs to the surface — a terminal diagnostic stream,
// an editor diagnostic, a structured tool payload — while this sentence is the
// fact, which is the same on all of them.
//
// The severity is stated through the shared wording rule rather than spelled here,
// so this sentence and every other notice about a recorded severity describe the
// same value the same way — and so a retraction is never reported as merely
// deprecated, which would understate an exclusion as a discouragement.
//
// It names no moment, and that is a narrower claim than the lockfile's rather than
// an omission: this record carries no wall-clock stamp at all, which is what makes
// an unchanged resolution byte-identical over time as well as across machines. So
// the sentence says where the fact was recorded and how to refresh it, and does not
// imply a currency it cannot substantiate.
func (f DeprecationFinding) Notice() string {
	return fmt.Sprintf("the plugin trusted as %s is %s: %s"+
		" (recorded beside the lockfile at pull; re-run `aggrega pull` to refresh it)",
		f.Plugin, deprecation.Severity(f.Severity).StatedAs(), f.Message)
}

// DeprecationFindings reports every trusted plugin whose recorded artifact carries
// a withdrawal, sorted by plugin coordinate so the surfaced order — and therefore
// which finding a caller's policy acts on first — is deterministic.
//
// It is the single producer of that report. Every surface that says anything about
// a withdrawn verdict plugin reads this one slice, so the set of plugins reported
// is identical on all of them and only the VERDICT differs: a surface producing an
// artifact may refuse on a finding, a surface producing a report enumerates it.
//
// # Why this is a SECOND producer and that is single-sourcing rather than a breach
//
// The lockfile beside this file has its own producer over its own marker-bearing
// rows. The two are deliberately separate, and folding them together would be one
// key doing two jobs. Four mismatches make that concrete, and none of them is a
// difference a shared implementation could parameterise away:
//
//   - SCOPE. This record is per-DIRECTORY — one file beside a Release — while a
//     lockfile is per-Release, so several Releases in one directory share this
//     record and each has a lockfile of its own.
//   - SUBJECT. A lockfile row pins something a build reproduces byte-for-byte. A
//     record here pins a plugin that shapes no rendered byte, which is precisely
//     why its identity is recorded here instead of pinned there.
//   - LIFECYCLE, and it is inverted. A pull regenerates a lockfile wholesale from a
//     freshly computed closure, so a row for something no longer reached is removed
//     by never being carried forward. This record is never regenerated: a plain pull
//     neither rewrites nor removes an entry, and an entry leaves only on an explicit
//     upgrade. A carry-forward step is therefore something the lockfile needs and
//     this record must not have — the marker persists here by not being rewritten.
//   - SEMANTICS, and these are inverted too. An entry here that no configured
//     attachment accounts for HALTS by design, because a dropped gate changes no
//     rendered byte. The lockfile analogue — a row nothing reaches — is benign drift.
//
// A nil receiver, and a record that holds no withdrawal, both yield nothing: where
// there is no record there are no withdrawals either, so a surface that tolerates
// an absent record by design loses nothing by calling this and must not be made to
// guard against it.
//
// # What it cannot reach
//
// A withdrawal is discoverable only where something can record one. A third store
// exists that neither this producer nor the lockfile's covers: the local registry
// index, which holds per-version withdrawal facts for artifacts no lockfile pins and
// no verdict attachment names. So the two producers together are not everything
// knowable about a withdrawal, and nothing here should be read as claiming they are.
func (t *Trust) DeprecationFindings() []DeprecationFinding {
	if t == nil {
		return nil
	}
	findings := make([]DeprecationFinding, 0, len(t.Plugins))
	for _, p := range t.Plugins {
		if p.Plugin == "" || p.Deprecation == nil {
			continue
		}
		findings = append(findings, DeprecationFinding{
			Plugin:   p.Plugin,
			Severity: p.Deprecation.Severity,
			Message:  p.Deprecation.Message,
		})
	}
	if len(findings) == 0 {
		return nil
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Plugin < findings[j].Plugin })
	return findings
}
