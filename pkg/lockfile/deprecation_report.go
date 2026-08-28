// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"fmt"
	"sort"
)

// DeprecationFinding reports one pinned reference carrying a recorded
// retroactive deprecation marker. It is the unit every surface that reports a
// deprecated reference renders, so the wording of that report has one home and
// the surfaces cannot come to say different things about the same fact.
//
// Severity is the recorded severity verbatim, in the ordered set
// warning < error < retracted, so a caller comparing it against a configured
// threshold reads the same string the marker recorded rather than a
// re-classification. Message is the marker's own recorded message.
//
// The observation moment is carried unexported and rendered by Notice, because
// it is a property of the lockfile that recorded the marker rather than of the
// individual finding: one stamp governs every finding a lockfile produces. Only
// DeprecationFindings constructs a value of this type, which is what keeps a
// hand-built finding from reporting a moment its lockfile never recorded.
type DeprecationFinding struct {
	// URI is the pinned reference the marker was recorded against.
	URI string
	// Severity is the recorded severity: warning, error or retracted.
	Severity string
	// Message is the marker's recorded human-readable message.
	Message string

	generatedAt string
}

// Notice renders the finding as one line: the reference, its recorded severity,
// the recorded message, and when the record was observed together with the
// command that refreshes it.
//
// It carries no channel prefix and no policy clause. A front door supplies its
// own prefix, because the prefix belongs to the surface — a terminal diagnostic
// stream, an editor diagnostic, a structured tool payload — while this sentence
// is the fact, which is the same on all of them.
func (f DeprecationFinding) Notice() string {
	return fmt.Sprintf("%s is deprecated (%s): %s%s", f.URI, f.Severity, f.Message, asOfSuffix(f.generatedAt))
}

// DeprecationFindings reports every pinned reference in the lockfile carrying a
// recorded retroactive deprecation marker, sorted by URI so the surfaced order —
// and therefore which finding a caller's policy acts on first — is deterministic
// and independent of the order the sections are enumerated in.
//
// It is the single producer of that report. Every surface that says anything
// about a deprecated reference — the build's diagnostic stream, the validation
// and convention passes, the editor-facing daemon, the AI-native tools — reads
// this one slice, so the set of references reported is identical on all of them
// and only the VERDICT differs: a surface producing an artifact may refuse on a
// finding, a surface producing a report enumerates it. A second producer per
// surface is how a reference came to be reported on one surface and not another,
// with each surface's silence reading as "nothing is deprecated".
//
// # Why the recorded rows ARE the consumed references
//
// The report walks the lockfile's own enumeration of its marker-bearing rows and
// consults no closure, and that is sound rather than a widening: the row set is
// the consumed set by construction. A pull materialises the lockfile from a
// freshly computed closure — it reads no prior lockfile when composing the row
// set — so a row for a reference the Release no longer reaches is removed by
// never being carried forward, with no pruning step that could be missing. The
// only other write path replaces the render-time external-input pins and passes
// every marker-bearing row through untouched. A closure that has since drifted
// from the lockfile is a different condition with its own gate, which reports
// drift in both directions.
//
// The bound on that reasoning, stated rather than glossed: the drift gate runs
// only against a lockfile that pre-existed the build, because a lockfile the
// build itself pulled is taken to match the closure it was computed from. That
// correspondence is between two code paths — the pull's closure computation and
// the build's — which share no symbol, so nothing fails when it stops holding.
// This report does not depend on it: the rows it reads are the rows the pull
// wrote, whichever path wrote them.
//
// # What it cannot reach
//
// A marker is discoverable only where a row can record one. Plugin identities
// pinned outside the lockfile — the linter attachments and the verdict-role
// governance attachments, which are pinned in the trust record beside the
// Release — carry no marker field and no lockfile row, so no report driven by
// these rows can ever mention one. That is a gap in what is recorded, not in
// what is walked here.
//
// A nil receiver, and a lockfile that records no marker, both yield nothing:
// where there is no lockfile there are no recorded markers either, so a surface
// that tolerates an absent lockfile by design loses nothing by calling this and
// must not be made to guard against it.
func (lf *Lockfile) DeprecationFindings() []DeprecationFinding {
	if lf == nil {
		return nil
	}
	rows := lf.VerifiedFactRows()
	findings := make([]DeprecationFinding, 0, len(rows))
	for _, row := range rows {
		rec := row.Deprecation()
		if rec == nil {
			continue
		}
		findings = append(findings, DeprecationFinding{
			URI:         row.URI,
			Severity:    rec.Severity,
			Message:     rec.Message,
			generatedAt: lf.GeneratedAt,
		})
	}
	if len(findings) == 0 {
		return nil
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].URI < findings[j].URI })
	return findings
}

// asOfSuffix renders when a recorded marker was observed — the moment the
// lockfile carrying it was generated — together with the command that refreshes
// it, so a reader can tell a stale record from a fresh one. A deprecation is
// mutable registry state, so the record says what the registry reported at that
// pull and never what it reports now; the suffix exists to stop the message
// implying a currency an offline build cannot provide.
//
// The stamp can only lag the true observation, never lead it: it is set solely
// when a pull generates the lockfile, a re-pull that changes nothing keeps the
// earlier stamp, and every consumer preserves whatever it loaded. So the suffix
// errs toward reporting the record as older than it is, which is the safe
// direction.
//
// generatedAt is the lockfile's own stamp, already RFC 3339 when present; empty
// means the lockfile carries none, which is reported as such rather than
// rendered as a zero moment.
//
// This is deliberately NOT shared with the as-of wording on the index-served
// surfaces (the pull-time deprecation notices and the inspect deprecation
// block). The two report different facts: a different provenance (the local
// registry index versus the lockfile), a different refresh command, and a
// different input (a parsed instant plus a presence flag versus an
// already-formatted string, so no formatting rule is duplicated here). Folding
// them together would take a template with every distinguishing part injected,
// which would single-source nothing.
func asOfSuffix(generatedAt string) string {
	if generatedAt == "" {
		return " (recorded in the lockfile at pull, which carries no generation time; re-run `aggrega pull` to refresh it)"
	}
	return fmt.Sprintf(" (recorded in the lockfile at pull, as of %s; re-run `aggrega pull` to refresh it)", generatedAt)
}
