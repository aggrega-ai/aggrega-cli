// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

// VerifyConflict names what a configuration asks a verifying run to do beside its
// comparison, which that run cannot also do.
//
// Each value is a request that would come to nothing under a verify, or would move
// the verify off the pin policy it exists to check. Accepting one would leave it
// doing nothing while the answer read as though it had done something — or, for an
// upgrade, would compare the lockfile against a resolution no reconcile performs.
type VerifyConflict int

const (
	// VerifyWithUpgrade is an upgrade asked for beside the comparison. A verify
	// answers whether the reconcile, which honours every pin the lockfile records,
	// would change the lockfile; an upgrade drops pins to re-resolve them, so the
	// pair would compare against a resolution the reconcile never performs.
	VerifyWithUpgrade VerifyConflict = iota + 1

	// VerifyWithDryRun is a dry run asked for beside the comparison. A dry run
	// reports the files a run would write instead of writing them, and a verify
	// writes none of those files, so the dry run would have nothing to report.
	VerifyWithDryRun

	// VerifyWithPinSources is an answer to whether the pin step runs, given beside
	// the comparison — whichever answer it is. The pin step records the pins an
	// entity lock is missing, or halts on them, and it runs only on a run that
	// writes, after the verify branch has returned, so under a verify it would do
	// neither.
	VerifyWithPinSources

	// VerifyWithUpgradeTargets is a set of references named for an upgrade, given
	// beside the comparison. Named references reach the pin decision only through
	// an upgrade, which a verify does not perform, so they would re-resolve nothing.
	VerifyWithUpgradeTargets
)

// VerifyConflictError is the refusal Config.VerifyConflict returns.
//
// A surface recognises it with errors.As and reports With in its own vocabulary —
// a flag on the command line, an argument on the assistant tool, a field on the
// daemon — so the rule has one home here while the sentence a caller reads belongs
// to the surface that caller is reading.
type VerifyConflictError struct {
	// With is what the configuration asked for beside the comparison.
	With VerifyConflict
}

// Error reports the refusal in the resolution's own terms. Every surface in this
// module renders a sentence of its own from With instead; this is what a caller
// that renders nothing prints.
func (e *VerifyConflictError) Error() string {
	switch e.With {
	case VerifyWithUpgrade:
		return "a dependency resolution that only verifies cannot also upgrade: a verify honours every pin the lockfile records, and an upgrade drops pins to re-resolve them"
	case VerifyWithDryRun:
		return "a dependency resolution that only verifies cannot also be a dry run: a verify already writes none of the files a dry run reports"
	case VerifyWithPinSources:
		return "a dependency resolution that only verifies cannot also be told whether to pin sources: a verify writes no entity.lock, so it neither records a pin one is missing nor halts on one"
	case VerifyWithUpgradeTargets:
		return "a dependency resolution that only verifies cannot also name upgrade targets: naming them asks for an upgrade, and a verify re-resolves nothing"
	}
	return "a dependency resolution that only verifies was asked for something it cannot also do"
}

// VerifyConflict reports what this configuration asks a verifying run to do beside
// its comparison, as a *VerifyConflictError, or nil when it asks for nothing of the
// kind — which includes every configuration that does not set Verify, since each
// exclusion is one half of a pair with the verify.
//
// It is the one home of every verify exclusion. Run asks it before it reads
// anything. A surface that does work of its own before it calls Run — resolves a
// workspace, puts a question to a person, tells a caller that work has begun — asks
// it first, ahead of that work, so a request that contradicts itself is refused
// before anything happens on every surface, and no surface carries a copy of the
// rule that could come to disagree with this one.
//
// A configuration carrying more than one conflict is refused for the first in the
// order the constants are declared, so every surface reports the same one.
//
// It reads the request fields alone — Verify, Upgrade, UpgradeTargets, DryRun and
// PinSources — so a caller may ask it of a configuration that holds nothing else yet.
// PinSources is read for presence rather than value: nil is the one state that
// gives no answer. The workspace's pin_sources_at_pull is a setting rather than
// this request's answer, so a verify does not consult it and it is never refused.
func (c Config) VerifyConflict() error {
	if !c.Verify {
		return nil
	}
	switch {
	case c.Upgrade:
		return &VerifyConflictError{With: VerifyWithUpgrade}
	case c.DryRun:
		return &VerifyConflictError{With: VerifyWithDryRun}
	case c.PinSources != nil:
		return &VerifyConflictError{With: VerifyWithPinSources}
	case len(c.UpgradeTargets) > 0:
		return &VerifyConflictError{With: VerifyWithUpgradeTargets}
	}
	return nil
}
