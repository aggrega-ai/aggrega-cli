// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// CatalogPassOutcome is what whole-catalog validation found over one pass: the
// pass itself, and either the reason nothing in it was validated or the result of
// validating it.
//
// Checked is false for a pass that checks nothing (see ValidateCatalogPasses);
// such an outcome carries the pass alone. For a checked pass, PinsErr is the
// refusal of the pins its Releases' lockfiles record, in which case nothing in the
// pass was loaded; otherwise LoadErr is ValidateCatalog's error, a catalog that
// could not be loaded; otherwise Result is ValidateCatalog's result.
type CatalogPassOutcome struct {
	Pass    workspaceconfig.CatalogPass
	Checked bool
	PinsErr error
	LoadErr error
	Result  CatalogResult
}

// ValidateCatalogPasses is the one walk of a workspace's catalog passes. It
// returns one outcome per pass, in pass order, and runs the whole-catalog
// correctness pass (ValidateCatalog) for each pass that is checked and whose
// pins are read. Given the same passes and the same pins, which passes are
// checked, in what order, and on which pass a pins refusal or a load failure is
// reported cannot differ between two callers rendering its outcomes.
//
// Every pass is run whatever an earlier one found, since a broken entity in one
// Release's catalog variant says nothing about another's: a pins refusal or a
// load failure is reported on its own pass and the passes after it still run.
//
// A pass run for named Releases that reads no directory checks nothing. The single
// pass a workspace naming no Release runs is checked whatever it reads: over no
// directory its pins are still asked for, and an empty catalog is validated.
//
// pinsFor returns the pins a checked pass reads a base its catalog does not hold
// through — the ones its Releases' lockfiles record — and is asked for no other
// pass. It is the caller's to supply because which Releases a workspace holds is
// decided in a package that itself depends on this one. wsRoot and cacheDir are
// the workspace's root and artifact cache; ctx is the caller's and reaches every
// pass.
//
// each, where it is not nil, is handed every outcome as soon as it is decided,
// before the next pass runs, so a caller can report a pass while the later ones
// are still being checked. The walk itself renders nothing and writes to no
// stream.
func ValidateCatalogPasses(ctx context.Context, wsRoot, cacheDir string, passes []workspaceconfig.CatalogPass, pinsFor func(workspaceconfig.CatalogPass) (catalog.Pins, error), each func(CatalogPassOutcome)) []CatalogPassOutcome {
	outcomes := make([]CatalogPassOutcome, 0, len(passes))
	for _, pass := range passes {
		outcome := CatalogPassOutcome{Pass: pass}
		if len(pass.Dirs) > 0 || !pass.NamesReleases() {
			outcome.Checked = true
			pins, err := pinsFor(pass)
			if err != nil {
				outcome.PinsErr = err
			} else {
				outcome.Result, outcome.LoadErr = ValidateCatalog(ctx, wsRoot, cacheDir, pass.Dirs, pass.Named, pins)
			}
		}
		if each != nil {
			each(outcome)
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}
