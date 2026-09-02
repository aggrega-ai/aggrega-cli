// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package opsurface

import (
	"fmt"
	"sort"
	"strings"
)

// boundLocation names the file the bound is written in, so a finding that
// excuses a pair or refuses a stale row sends the reader to the one place the
// residue lives rather than to whichever surface happened to report it.
const boundLocation = "internal/opsurface/openquestions.go"

// OpenQuestion is one operation on one surface that carries no disposition
// because the answer is not settled.
//
// It is deliberately not a third Disposition value. Both halves of a
// disposition are answers — a name the capability is reachable under, or the
// reason the surface does not carry it — and "not settled" is not an answer.
// Giving it a home inside that vocabulary would let it be read as one, because
// a table entry meaning "not yet" is indistinguishable at a glance from an
// entry meaning "declined on purpose". So it lives out here, and what it
// carries is the question rather than a reason.
//
// Question states what has to be settled, as a question. A reason would assert
// something nobody has established, and an entry that reads as a reason is the
// precise failure the disposition check exists to prevent: an unexamined
// absence wearing the clothes of a deliberate one.
type OpenQuestion struct {
	Surface  Surface
	ID       string
	Question string
}

// OpenQuestions returns the bound: every operation-and-surface pair the
// disposition check does not require an answer for, each carrying the question
// that has to be settled before it can have one.
//
// It is an enumerated bound rather than a predicate, and that is the whole of
// its design. A predicate — skip the analysis capabilities, skip anything the
// command line has no command for — grows silently as the surfaces grow,
// because a capability added tomorrow is excused by a decision nobody took
// about it. A named list cannot do that: a pair is excused only by being
// written here, so the check refuses every pair that is neither disposed of nor
// named, and the residue is readable as data instead of hidden inside a green.
//
// It returns a copy, so a caller reading the bound cannot alter what the checks
// read.
func OpenQuestions() []OpenQuestion {
	return append([]OpenQuestion(nil), openQuestions...)
}

// openQuestions is the bound.
//
// Every row records what was measured about the surface and the capability, the
// fork that measurement left open, and therefore what would settle it. None of
// them records a reason, because a reason is what a disposition carries and
// none of these has one yet.
//
// The rows are grouped by surface and follow the operation set's order within a
// surface, so a reader meets them in the same sequence the checks report them.
var openQuestions = []OpenQuestion{
	// The command line.
	{SurfaceCLI, "ir-graph",
		"No command emits the serialized graph, and the command layer resolves the same graph to render a " +
			"readable view of a Release. So the resolution is reached and the document is not offered. Is the " +
			"readable view the intended extent of what a command gives, or does the command that already " +
			"resolves the graph owe the document as one of its output shapes?"},
	{SurfaceCLI, "explain-error",
		"No command carries this capability at all: it is reachable as a tool and nowhere else. Is a command " +
			"withheld on purpose, or is the absence one nobody has examined? Nothing in the command layer " +
			"records which, and the two answers differ in what the command line then owes."},
	{SurfaceCLI, "scaffold-entity",
		"No command carries this capability at all: it is reachable as a tool and nowhere else. The nearest " +
			"command drafts authoring files from a foreign source description, which is a different capability " +
			"— it converts something that exists rather than starting from a kind's schema. Is a command " +
			"withheld on purpose, or is the absence one nobody has examined?"},

	// The JSON-RPC daemon.
	{SurfaceServe, "config-show",
		"The handshake reports the workspace root, the configuration layer that took precedence, and the " +
			"marker that anchored the root. It does not report the effective settings, nor where each one came " +
			"from, which is what this capability is. Partial exposure is not a ground for declining: are the " +
			"handshake's three fields the intended extent for a session, or does the daemon owe the full report?"},
	{SurfaceServe, "inspect",
		"The daemon answers entity questions addressed at a cursor and reports the workspace's entity index, " +
			"but it resolves no entity by name and reports no dependency closure. Its subject is in the " +
			"workspace, it writes nothing and it reaches no registry, so none of this surface's grounds for " +
			"declining applies. Is by-name resolution left to the surfaces that have it on purpose, or does a " +
			"session owe it?"},
	{SurfaceServe, "lint",
		"The daemon carries the correctness check and not the publishing-conventions check. Its subject is in " +
			"the workspace and it writes nothing, so none of this surface's grounds for declining applies. Are " +
			"publishing conventions a concern of the moment an entity is published, which a session therefore " +
			"does not carry, or is the absence one nobody has examined?"},
	{SurfaceServe, "explain-error",
		"The daemon is the surface that emits the diagnostics this capability expands, and the diagnostic it " +
			"emits carries a machine-matchable code and a message with no expansion beside them. Is a client " +
			"expected to resolve the expansion itself from the code it was handed, or does the surface that " +
			"produced the diagnostic owe the expansion of it?"},

	// The tool protocol.
	{SurfaceMCP, "ir-graph",
		"The build result carries the rendered files and the Release they came from, not the graph the render " +
			"consumed. The graph is built on this surface — the render cannot proceed without it — so it is " +
			"produced and not returned. Is it withheld on purpose, or is the absence one nobody has examined?"},
	{SurfaceMCP, "workspace-entity-index",
		"The read surface serves one catalog entity by its identity and enumerates the schema kinds; it " +
			"enumerates no workspace-local entity, so a caller has to know an identity already in order to ask " +
			"for one. Is enumeration left off this surface on purpose, or does it owe a caller some way to " +
			"discover what a workspace holds?"},
	{SurfaceMCP, "feature-attach-candidates",
		"The label-matching result exists so that a caller offering attach targets offers the same set the " +
			"compiler would match. Nothing on this surface offers attach targets today, which is why the " +
			"parity half of that reason does not apply here — but a caller proposing an attachment is a " +
			"plausible consumer of the match itself. Is the capability for an editor alone, or does it belong " +
			"here as well?"},
	{SurfaceMCP, "release-candidates",
		"Half of this capability's reason plainly does not apply here: the candidate set exists so a caller's " +
			"own Release detection matches the compiler's, and on this surface every call names the Release it " +
			"means, so there is no detection to keep in step. The other half is what is unsettled — a caller " +
			"that does not yet know which Releases a workspace holds has no way to ask. Is discovery left off " +
			"this surface on purpose, or does it owe one?"},
}

// isOpen reports whether the bound excuses a disposition for the given
// operation on the given surface.
func isOpen(bound []OpenQuestion, surface Surface, id string) bool {
	for _, q := range bound {
		if q.Surface == surface && q.ID == id {
			return true
		}
	}
	return false
}

// BoundStatement renders what the bound excuses on one surface and states that
// this surface's disposition check asserts nothing about those pairs.
//
// It reports what it did not cover rather than only a verdict, because a check
// that goes quiet about its own reach is indistinguishable from one that had
// nothing to excuse. The counts are derived from the operation set and the
// bound at the moment of the call, so neither can rot into agreement with a
// number somebody wrote down.
func BoundStatement(surface Surface) string {
	return boundStatement(surface, openQuestions)
}

func boundStatement(surface Surface, bound []OpenQuestion) string {
	var ids []string
	for _, op := range operations {
		if isOpen(bound, surface, op.ID) {
			ids = append(ids, op.ID)
		}
	}
	// A row naming something that is not an operation is a stale bound row, and
	// the check refuses it — but it is still part of what this surface's bound
	// holds, so it is counted here rather than silently dropped from the census
	// the reader is given.
	var unknown []string
	for _, q := range bound {
		if q.Surface != surface {
			continue
		}
		if _, ok := Lookup(q.ID); !ok {
			unknown = append(unknown, q.ID)
		}
	}
	sort.Strings(unknown)
	ids = append(ids, unknown...)

	if len(ids) == 0 {
		return fmt.Sprintf("the bound in %s excuses no operation on %s, so every one of the %d is asserted here",
			boundLocation, surface, len(operations))
	}
	return fmt.Sprintf("the bound in %s excuses %d of %d operations on %s — %s — and this check asserts "+
		"NOTHING about them: each is undecided rather than declined",
		boundLocation, len(ids), len(operations), surface, strings.Join(ids, ", "))
}

// BoundCensus renders the whole bound: how many operation-and-surface pairs it
// holds, across how many surfaces, and that no disposition is asserted for any
// of them.
func BoundCensus() string {
	return boundCensus(openQuestions)
}

func boundCensus(bound []OpenQuestion) string {
	surfaces := map[Surface]bool{}
	for _, q := range bound {
		surfaces[q.Surface] = true
	}
	return fmt.Sprintf("the bound enumerates %d undecided operation-and-surface pair(s) across %d surface(s) "+
		"in %s, over an operation set of %d; no disposition is asserted for any of them",
		len(bound), len(surfaces), boundLocation, len(operations))
}
