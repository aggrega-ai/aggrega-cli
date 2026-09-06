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
	// The command line. One row, and the group heading is kept for the reason the
	// others are: the rows are grouped by surface, and a surface with one row is
	// still a surface a reader meets in the same sequence the checks report it.
	{SurfaceCLI, "ir-graph",
		"No command emits the serialized graph, and the command layer resolves the same graph to render a " +
			"readable view of a Release. So the resolution is reached and the document is not offered. Is the " +
			"readable view the intended extent of what a command gives, or does the command that already " +
			"resolves the graph owe the document as one of its output shapes?"},

	// The JSON-RPC daemon.
	//
	// The sixteen rows below share one history and are grouped for it. Each was
	// declined on a single reason — that the daemon's only writing path was the
	// artifact set a build renders, so an operation whose product is some other
	// write had no output there. That reason drew the line in the wrong place.
	// What makes a write acceptable is that somebody asked for it, not which
	// artifact it happens to be: a caller who invokes an acquisition, an eviction
	// or a publication has asked, exactly as invoking a build asks for artifacts.
	// What stays out of bounds is a write, or a reach for a registry, performed
	// while validating or inspecting, where nobody asked — and none of these is
	// that.
	//
	// So the reason is gone, and nothing replaces it. Each row states the fork
	// its own capability is left at. They are not collapsed into one row, because
	// the subjects differ — a machine-wide store, a configuration cascade, a
	// registry, a workspace's own authored files — and a single row would assert
	// that one answer settles all sixteen, which is the thing nobody has
	// established.
	//
	// A neighbouring set is deliberately untouched: the capabilities declined
	// because their subject is outside the workspace keep that reason, which
	// holds on its own and was never the retired one.
	{SurfaceServe, "acquire",
		"Pinning a Release's external inputs writes into the workspace the session already holds, and a caller " +
			"invoking it has asked for that write, so the daemon declines it on no property of its own. What is " +
			"unsettled is the reach: the pins are resolved over the network, and every method answered today is " +
			"served from the workspace and what is already cached. Does a session owe an operation that fetches, " +
			"or is fetching the boundary a session does not cross?"},
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
	{SurfaceServe, "pull",
		"Resolving dependencies writes the per-Release lockfile into the workspace and populates a cache, and it " +
			"reaches registries to do both. The lockfile is a document in the workspace the session opened, so " +
			"the subject is not uniformly outside it and no single property settles the capability. Is a " +
			"session's build expected to find its dependencies already resolved — which is what every method " +
			"answered today assumes — or does a session owe the resolution itself?"},
	{SurfaceServe, "cache-clear",
		"Wiping every cache reaches both the compiled-module store, which is shared across every project on the " +
			"machine, and the artifact cache, which sits under the workspace's own configured cache directory. " +
			"So the subject straddles the boundary the neighbouring capabilities are declined on, and a caller " +
			"invoking it has asked for the write. Does a session owe a caller a way to reclaim what its own " +
			"workspace has cached, or is cache maintenance left to the surface a person drives?"},
	{SurfaceServe, "cache-evict",
		"Trimming the compiled-module cache acts on a store shared across every project on the machine, which is " +
			"the property the capabilities declined for sitting outside the workspace rest on — but eviction is " +
			"a write a caller explicitly asks for, and it is the daemon that fills that store, once per guest, " +
			"for every session on the machine. Is the surface that populates it also the surface that owes a way " +
			"to trim it, or is a machine-wide store outside what any session addresses?"},
	{SurfaceServe, "cache-prune",
		"Reclaiming artifact-cache entries no lockfile references acts on the cache directory the workspace " +
			"configures, defaulting to one inside the workspace itself, and decides what to keep by reading that " +
			"workspace's lockfiles. Both inputs are ones a session already holds. Nothing in the daemon declines " +
			"it. Is reclaiming a workspace's own cache a thing a session owes, or does maintenance belong only " +
			"where a person sees what is removed?"},
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
	{SurfaceServe, "registry-add",
		"Registering a registry and its trust policy writes an entry into the configuration cascade — the " +
			"workspace file by default, the global one when asked — and reaches the registry itself when it is " +
			"asked to take that registry's advertised policy. The companion that reports configured registries " +
			"is declined for sitting outside the workspace; this one's default target is inside it. Is the " +
			"trust policy a thing a session owes a caller, given that a session already enforces it?"},
	{SurfaceServe, "registry-remove",
		"Removing a registry entry edits the same configuration cascade the addition writes, and reaches nothing " +
			"else. It is the smaller half of the same fork: whichever way editing the cascade is settled, this " +
			"is that edit with no network reach at all, so settling the addition and leaving this would split " +
			"one decision. Does a session owe edits to the configuration it reports?"},
	{SurfaceServe, "registry-update",
		"Building the local searchable index reaches every configured registry and writes the index the search " +
			"capability reads. The index is not in the workspace, which is why search itself is declined — but " +
			"the reach is the part that is unsettled, since every method answered today is served from the " +
			"workspace and what is already cached. Does a session owe an operation that goes to the network to " +
			"refresh what a later call would read, or is fetching the boundary a session does not cross?"},
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
	{SurfaceMCP, "feature-attach-candidates",
		"The label-matching result exists so that a caller offering attach targets offers the same set the " +
			"compiler would match. Nothing on this surface offers attach targets today, which is why the " +
			"parity half of that reason does not apply here — but a caller proposing an attachment is a " +
			"plausible consumer of the match itself. Is the capability for an editor alone, or does it belong " +
			"here as well?"},
	{SurfaceMCP, "connection-candidates",
		"The same fork the attach-candidates row above records, reached from the other half of the " +
			"authoring question: nothing on this surface proposes a connection, so the parity half of " +
			"that reason does not apply here either — but a caller composing one is a plausible consumer " +
			"of the socket declarations and the target match. The two rows stand or fall together: " +
			"whichever way the attachment half is settled, this half is the same capability for the other " +
			"kind of edge, so settling one and leaving the other would split one decision across two " +
			"surfaces. Is candidacy for an editor alone, or does it belong here as well?"},
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
