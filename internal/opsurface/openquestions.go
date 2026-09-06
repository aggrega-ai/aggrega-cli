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

// OpenQuestion is one operation on one surface whose availability is not
// declared because the answer is not settled.
//
// It is deliberately not a third OperationAvailability value. Both halves of an
// availability record are answers — a name the capability is reachable under, or
// the reason the surface does not carry it — and "not settled" is not an answer.
// Giving it a home inside that vocabulary would let it be read as one, because
// a table entry meaning "not yet" is indistinguishable at a glance from an
// entry meaning "declined on purpose". So it lives out here, and what it
// carries is the question rather than a reason.
//
// Keeping it out of that vocabulary matters more now that the table registers
// the surfaces rather than describing them: an unavailability reason decides
// that nothing is registered, and a row meaning "not yet" would make that
// decision while recording no ruling behind it.
//
// Question states what has to be settled, as a question. A reason would assert
// something nobody has established, and an entry that reads as a reason is the
// precise failure the availability check exists to prevent: an unexamined
// absence wearing the clothes of a deliberate one.
type OpenQuestion struct {
	Surface  Surface
	ID       string
	Question string
}

// OpenQuestions returns the bound: every operation-and-surface pair the
// availability check does not require an answer for, each carrying the question
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
// them records a reason, because a reason is what an availability record carries
// and none of these has one yet.
//
// The rows are grouped by surface and follow the operation set's order within a
// surface, so a reader meets them in the same sequence the checks report them.
var openQuestions = []OpenQuestion{
	// The command line holds no undecided pair. Its last one asked whether the
	// readable view was the intended extent of what a command gives, or whether a
	// command that already resolves the graph owed the document too; the answer is
	// that it owes it, and three commands now hand it back.

	// The JSON-RPC daemon.
	//
	// The rows below share one history and are grouped for it. Each was declined
	// on a single reason — that the daemon's only writing path was the artifact
	// set a build renders, so an operation whose product is some other write had
	// no output there. That reason drew the line in the wrong place. What makes a
	// write acceptable is that somebody asked for it, not which artifact it
	// happens to be: a caller who invokes an acquisition, an eviction or a
	// publication has asked, exactly as invoking a build asks for artifacts.
	// What stays out of bounds is a write, or a reach for a registry, performed
	// while validating or inspecting, where nobody asked — and none of these is
	// that.
	//
	// So the reason is gone, and nothing replaces it. Each row states the fork
	// its own capability is left at. They are not collapsed into one row, because
	// the subjects differ — a machine-wide store, a configuration cascade, a
	// registry, a workspace's own authored files — and a single row would assert
	// that one answer settles them all, which is the thing nobody has
	// established.
	//
	// Several of that set have since been settled and are gone from here:
	// resolving a Release's dependencies and pinning the external inputs its
	// render consumes; reporting and reclaiming the caches that serve the work;
	// and registering, dropping and refreshing the registries a workspace
	// reaches. Every one of them was left at some version of the same fork —
	// whether a session owes an operation that writes, or that fetches — and
	// every one was answered the same way, that the rule which survives is
	// explicit invocation rather than which surface asked. Their availability is
	// declared on the surface instead, which is where an answered question
	// belongs, and each response there states the reach of what it changed.
	//
	// The rows left below are the ones that fork on something else. A subject
	// that straddles the boundary, a capability whose product is new authoring
	// files, a machine-wide credential store: none of those is settled by the
	// invocation rule alone, which is why disposing of the rows above says
	// nothing about them.
	{SurfaceServe, "config-capture",
		"Capture writes this project's workspace configuration file and never the global one, so its subject is " +
			"the workspace the session opened, it reaches no registry and it prompts nobody. No property of the " +
			"daemon declines it. Is freezing the resolved configuration a thing a session owes a caller that has " +
			"already asked it what the effective configuration is, or is authoring a workspace's own files " +
			"deliberately left to the surface a person drives?"},
	{SurfaceServe, "import",
		"Drafting authoring files from a foreign source description writes into the workspace the session holds, " +
			"and a caller invoking it has asked for those files. The conversion itself runs in a plugin the " +
			"daemon already loads for other work, so neither the write nor the machinery declines it. Is drafting " +
			"a capability a session owes, or does an operation whose product is new authoring files belong only " +
			"where a person reviews them before they land?"},
	{SurfaceServe, "plugin-add",
		"Adding a plugin searches a registry and records the result in configuration. The configuration half is " +
			"outside the workspace, which is the property the neighbouring rows rest on — but the search half is " +
			"a network reach, and the two halves would be declined for different reasons. Neither has been ruled " +
			"on here. Is this outside a session because its record is machine-wide, or because a session makes no " +
			"network reach at all?"},
	{SurfaceServe, "plugin-gen-catalog",
		"Generating Plugin entities reads a guest module's own metadata and an authored overlay and writes " +
			"catalog entities. The overlay and the entities are in the workspace; the module is named by path and " +
			"may not be. So the subject straddles the boundary the neighbouring rows are declined on, and which " +
			"side governs has not been settled. Does a session owe generation over the workspace's own overlay, " +
			"or is a capability whose input can sit anywhere on disk outside what a session addresses?"},
	{SurfaceServe, "plugin-sync",
		"Synchronising a plugin's machine-owned records writes into that plugin's entity file, which is a " +
			"document in the workspace — the same kind of document every method answered today is anchored to. " +
			"The daemon declines it on nothing. Is bringing an entity file into step with the module it names a " +
			"thing a session owes, or is it left to the surface that installed the module?"},
	{SurfaceServe, "config-init",
		"Seeding a configuration writes the global file shared across every workspace by default, and this " +
			"project's workspace file when asked. The default subject is outside the workspace, which is the " +
			"property the neighbouring rows rest on; the other is a document in the workspace the session " +
			"opened. Which of the two governs has not been settled. Is seeding outside a session because its " +
			"default target is machine-wide, or does a session owe the workspace half of it?"},
	{SurfaceServe, "logout",
		"Removing a stored credential acts on the machine-wide credential store, which is reached without a " +
			"session and is unchanged by which workspace one was opened over — the property the capabilities " +
			"declined for sitting outside the workspace rest on. What is unsettled is whether that property " +
			"alone should decline it now that an explicitly invoked write is no longer objectionable: the " +
			"companion that acquires the credential is declined for prompting a person, and this one prompts " +
			"nobody. Is the machine-wide store outside what a session addresses, or only outside what it may " +
			"reach without being asked?"},
	{SurfaceServe, "plugin-remove",
		"Removing a configured plugin edits the configuration cascade rather than anything in the workspace's " +
			"document set, and the companion that lists resolvable plugins is declined for exactly that. But " +
			"the cascade includes this project's own workspace configuration file, so the subject is not wholly " +
			"outside the workspace, and a caller invoking the removal has asked for the write. Does a session " +
			"owe edits to the cascade it already reports the effective value of, or is editing configuration " +
			"left to the surface a person drives?"},
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

	// The tool protocol holds no undecided pair. Its last two asked whether
	// candidacy — which Features may attach to a node, and what a node may connect
	// to — was for an editor alone; the answer is that both belong here too, and
	// two tools now carry them.
}

// isOpen reports whether the bound excuses an availability declaration for the
// given operation on the given surface.
func isOpen(bound []OpenQuestion, surface Surface, id string) bool {
	for _, q := range bound {
		if q.Surface == surface && q.ID == id {
			return true
		}
	}
	return false
}

// BoundStatement renders what the bound excuses on one surface and states that
// this surface's availability check asserts nothing about those pairs.
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
		"NOTHING about them: each is undecided rather than declared unavailable",
		boundLocation, len(ids), len(operations), surface, strings.Join(ids, ", "))
}

// BoundCensus renders the whole bound: how many operation-and-surface pairs it
// holds, across how many surfaces, and that no availability is asserted for any
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
		"in %s, over an operation set of %d; no availability is asserted for any of them",
		len(bound), len(surfaces), boundLocation, len(operations))
}
