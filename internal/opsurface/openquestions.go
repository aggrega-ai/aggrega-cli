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

// pending is the whole of what a row records beyond the pair it names: which
// state the pair is in, and nothing else.
//
// It is a token rather than a sentence on purpose. A row's job is to say that
// the pair is not settled on this surface; anything beyond that is either a
// reason, which is what an availability record carries and what a row must not
// be mistaken for, or an argument about what the answer should be, which has no
// business on a surface that ships.
const pending = "pending"

// UndecidedPair is one operation on one surface whose availability is not
// declared, because its disposition is not settled.
//
// It is deliberately not a third OperationAvailability value. Both halves of an
// availability record are answers — a name the capability is reachable under, or
// the reason the surface does not carry it — and "not settled" is not an answer.
// Giving it a home inside that vocabulary would let it be read as one, because
// a table entry meaning "not yet" is indistinguishable at a glance from an
// entry meaning "declined on purpose". So it lives out here, and what it
// carries is a state rather than a reason.
//
// Keeping it out of that vocabulary matters where the table registers the
// surfaces rather than describing them: an unavailability reason decides that
// nothing is registered, and a row meaning "not yet" would make that decision
// while recording no ruling behind it.
//
// Disposition carries pending and nothing else. A reason would assert something
// nobody has established, and an entry that reads as a reason is the precise
// failure the availability check exists to prevent: an unexamined absence
// wearing the clothes of a deliberate one.
type UndecidedPair struct {
	Surface     Surface
	ID          string
	Disposition string
}

// UndecidedPairs returns the bound: every operation-and-surface pair the
// availability check does not require an answer for, each carrying the token
// that says its disposition is not settled.
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
func UndecidedPairs() []UndecidedPair {
	return append([]UndecidedPair(nil), undecidedPairs...)
}

// undecidedPairs is the bound.
//
// Every row names a surface and an operation and records the one thing the bound
// is for: that the pair is not settled there. None of them records a reason,
// because a reason is what an availability record carries and none of these has
// one.
//
// The rows are grouped by surface and follow the operation set's order within a
// surface, so a reader meets them in the same sequence the checks report them.
var undecidedPairs = []UndecidedPair{
	// The JSON-RPC daemon.
	{SurfaceServe, "config-capture", pending},
	{SurfaceServe, "config-init", pending},
	{SurfaceServe, "import", pending},
	{SurfaceServe, "logout", pending},
	{SurfaceServe, "plugin-add", pending},
	{SurfaceServe, "plugin-gen-catalog", pending},
	{SurfaceServe, "plugin-remove", pending},
	{SurfaceServe, "plugin-sync", pending},
	{SurfaceServe, "explain-error", pending},
}

// isUndecided reports whether the bound excuses an availability declaration for
// the given operation on the given surface.
func isUndecided(bound []UndecidedPair, surface Surface, id string) bool {
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
	return boundStatement(surface, undecidedPairs)
}

func boundStatement(surface Surface, bound []UndecidedPair) string {
	var ids []string
	for _, op := range operations {
		if isUndecided(bound, surface, op.ID) {
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
	return boundCensus(undecidedPairs)
}

func boundCensus(bound []UndecidedPair) string {
	surfaces := map[Surface]bool{}
	for _, q := range bound {
		surfaces[q.Surface] = true
	}
	return fmt.Sprintf("the bound enumerates %d undecided operation-and-surface pair(s) across %d surface(s) "+
		"in %s, over an operation set of %d; no availability is asserted for any of them",
		len(bound), len(surfaces), boundLocation, len(operations))
}
