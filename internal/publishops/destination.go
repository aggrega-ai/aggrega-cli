// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

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

// destinationHost returns the registry host a publication publishes to: the
// destination resolveDestination settles on — a requested token resolved from an
// alias or a host through the one alias-or-host resolver, or else the workspace's
// default registry — held to the one reading of a registry host.
//
// The hold is what keeps a destination named for one invocation from escaping
// the policy written for its host. A workspace's default registry is already the
// host it names, in the spelling its trust policy is kept under: resolution has
// resolved an alias written there to its entry's host and refused anything else.
// So it is used as that host and not passed through the resolver again, which
// could read it as a second entry's alias and publish somewhere a pull from the
// same default never reads. A requested destination arrives after resolution, so
// it is resolved and checked here, where it becomes the host every signing
// decision for this publication is taken against. A requested token may still be
// a reference — the resolver reduces it to the host it names — but that host is
// refused rather than rewritten when it is not spelled as a registry host.
//
// Every publishing entry point reaches it, so no surface decides the destination
// a second way.
func destinationHost(wsCfg workspaceconfig.Config, requested string, supply DestinationSupply) (string, error) {
	registry, err := resolveDestination(requested, wsCfg.DefaultRegistry, supply)
	if err != nil {
		return "", err
	}
	if requested != "" {
		if registry, err = wsCfg.ResolveRegistry(requested); err != nil {
			return "", err
		}
	}
	if err := registryhost.Check("the destination registry", registry); err != nil {
		return "", err
	}
	return registry, nil
}
