// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ErrUsesInjectForbidden reports an `inject:` block on a `uses:` edge that the
// socket's declaration does not permit. One sentinel covers both refusals,
// because they are one rule read on two inputs — the socket says which delivery
// mechanisms an edge wired through it may dictate, and the edge named one it does
// not say:
//
//   - the socket declares an `inject:` allowlist and the edge dictates a mechanism
//     that is not a member of it;
//   - the socket declares no allowlist, or an empty one, so it permits no
//     dictation at all and the edge dictates something. An entity whose delivery
//     is opaque declares its socket without `inject:` and the consumer takes
//     whatever the producer's Recipe emits — the Black-Box pattern.
//
// The two carry different message text, which is what tells an author whether to
// widen an allowlist or to stop dictating; the condition they report is the same
// one, so splitting the sentinel would ask a programmatic consumer to match two
// values for one question.
//
// A socket the declaring entity never declared is OUT of scope and reaches no
// refusal here. An ad-hoc socket has no `usesSchema` entry, so it carries no
// allowlist to be outside of and makes no Black-Box promise to keep: what such a
// socket gives up is the declaration's guarantees, not the author's ability to
// route what they invented. The build's other rules still bound what the edge may
// carry.
//
// Per the Aggrega architectural invariant set (Invariant 4): policy violations
// produce Loud Guardrails — the build halts naming the socket, the offending
// mechanism, and what the socket does permit — never a silent drop of the
// dictation.
func ErrUsesInjectForbidden() error { return errUsesInjectForbidden }

var errUsesInjectForbidden = cerrors.Register("uses-inject-forbidden", cerrors.Meta{
	ShortCode: "E0015",
	Message:   "uses-edge inject dictates a delivery mechanism the socket does not permit",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// usesInjectLocator names the surface an edge's inject block was authored on, in
// the author's own terms: the component instance that consumes the edge, the
// attachment instance when the edge came from one, and the deploying document when
// the edge was wired there rather than in the Aggregate. It exists so a refusal
// points at the block to edit instead of at the internal identity of the edge.
func usesInjectLocator(nodeID, originFeatureInstance string, originRelease bool) string {
	switch {
	case originRelease:
		return fmt.Sprintf("Release component %q", nodeID)
	case originFeatureInstance != "":
		return fmt.Sprintf("component %q attachment %q", nodeID, originFeatureInstance)
	default:
		return fmt.Sprintf("component %q", nodeID)
	}
}

// usesInjectViolations checks one edge's dictated delivery mechanisms against the
// allowlist declared on the socket it is wired into, and returns one diagnostic
// per violation. An empty result is the permitted case.
//
// The two facts it reads are the edge's dictated mechanism names, taken from the
// authored inject block via [resolver.InjectMechanismNames], and the socket's
// declaration, taken from [socketEntry] so the declaring entity is resolved once
// for the whole build path.
//
// Both inputs are needed at once, which is what fixes where this runs: the
// authored block belongs to the edge and the allowlist belongs to whichever
// catalog entity owns the socket. Diagnostics come back in the mechanism order the
// author wrote, with custom mechanisms following the core three, so a joined halt
// is byte-identical across runs of the same input.
func usesInjectViolations(
	lookup resolver.CatalogLookup,
	componentType, originFeature, socket, alias, pathPrefix string,
	inject *types.InjectBlock,
) []string {
	mechanisms := resolver.InjectMechanismNames(inject)
	if len(mechanisms) == 0 {
		return nil
	}

	entry, owner, declared := socketEntry(lookup, componentType, originFeature, socket)
	if !declared {
		return nil
	}

	surface := "component type"
	if originFeature != "" {
		surface = "feature type"
	}
	edgePath := fmt.Sprintf("%s.uses[%q][%q].inject", pathPrefix, socket, alias)

	// An absent allowlist and an explicitly empty one are the same declaration:
	// the socket permits no dictation. Reading them apart would make `inject: []`
	// mean something no surface documents.
	if len(entry.Inject) == 0 {
		return []string{fmt.Sprintf(
			"%s: socket %q on %s %q declares no inject allowlist (Black-Box pattern) — delivery dictation is not allowed",
			edgePath, socket, surface, owner)}
	}

	allowed := make(map[string]bool, len(entry.Inject))
	for _, mech := range entry.Inject {
		allowed[mech] = true
	}

	var out []string
	for _, mech := range mechanisms {
		if allowed[mech] {
			continue
		}
		out = append(out, fmt.Sprintf(
			"%s.%s: mechanism %q not in socket %q inject allowlist %v on %s %q",
			edgePath, mech, mech, socket, entry.Inject, surface, owner))
	}
	return out
}

// joinUsesInjectViolations renders every uses-inject refusal collected over the
// edge set as one halt, so an author sees every offending edge in a single build
// rather than fixing them one build at a time. Ordering follows the edge set's
// authoring order, which the topology layer preserves.
func joinUsesInjectViolations(violations []string) error {
	return fmt.Errorf("%w: %s", ErrUsesInjectForbidden(), strings.Join(violations, "; "))
}
