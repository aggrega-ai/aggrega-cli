// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import "fmt"

// DestinationSupply names the input one particular caller sets a publication's
// destination registry through, in that caller's own vocabulary.
//
// It exists for the same reason the identity ladder's supply hint does: the
// condition — no destination was requested and the workspace names no default —
// is one condition with one home, while what a caller can DO about it differs by
// surface. A command line flag is the right thing to name at a terminal and
// unsayable to a caller that has no command line, so the phrasing travels in as a
// parameter and the condition is not re-decided per surface.
//
// The zero value names no input. The refusal is then the bare statement of what
// is missing, which is true everywhere and unhelpful nowhere.
type DestinationSupply string

// resolveDestination returns the destination registry a publication will use —
// the one the caller requested, else the workspace's default — and refuses when
// there is neither, naming what supply says this caller can set.
//
// It is the single home for that decision. Every surface reaches it rather than
// re-testing the same two values, because two copies of a two-value test agree
// right up until one of them is edited.
func resolveDestination(requested, defaultRegistry string, supply DestinationSupply) (string, error) {
	if registry := firstNonEmpty(requested, defaultRegistry); registry != "" {
		return registry, nil
	}
	if supply == "" {
		return "", fmt.Errorf("no destination registry")
	}
	return "", fmt.Errorf("no destination registry; %s", supply)
}
