// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package opsurface

import (
	"fmt"
	"strings"
)

// Reach states how far an operation's own effects travel, declared once per
// operation and readable before the operation runs.
//
// It is declared, never derived. Nothing here inspects what an operation does
// and nothing reconstructs a class from an identifier, so a capability that is
// renamed, reimplemented or reached under a new name keeps the class its entry
// gives. The class is wrong only when somebody writes the wrong one, which is
// the failure mode a reader can check; a class inferred from a name would be
// right until the name moved.
//
// # What the class covers, and what it does not
//
// The class covers what an operation leaves behind: bytes written, a registry
// mutated, a store every workspace on the machine shares changed. It does not
// cover where a surface puts the ANSWER an operation returns. A render produces
// artifacts as its answer, and a command line that writes them into a directory
// the caller named is delivering that answer rather than widening the
// operation's reach — the same holds for a starter document whose destination is
// the caller's to choose. Read the other way, every operation that answers with
// content would take the class of whichever surface happened to save it, and the
// class would stop being a property of the operation at all.
//
// # Exactly one class, and the narrowing that needs no second one
//
// An entry declares one class: the one the operation reaches when nothing in the
// call widens it. A call may narrow that — a mutation an operator points at the
// workspace's own configuration file instead of the machine-wide one reaches
// less than its entry declares — and a narrowing needs no declaration, because a
// caller bounded by the wider class is bounded by the narrower one too. Widening
// is the direction that would matter, and an operation that widens says so
// beside its own row rather than here.
//
// # The zero value is deliberately not a class
//
// An entry added without a class carries the empty string, which none of the
// constants below spells. So a missing declaration is a finding from CheckReach
// exactly as a misspelled one is, rather than defaulting to whichever class
// happened to sort first — which would make "nobody has decided" indistinguishable
// from "decided, and it is this one".
//
// # Two neighbouring vocabularies spell two of these words, and stay separate
//
// A cache class carries how far its own contents are shared, and a configuration
// write carries how far the file it resolved to reaches. Both spell workspace
// and machine, and each says in its own documentation that its subject is not an
// operation: one is a property of the class, the other of the resolved file, and
// each has two values because two is all its subject can have. Folding either
// into this one would give it two values no cache class and no configuration
// file can hold, and would put a leaf vocabulary downstream of the operation
// set. What they do hold in common is the spelling, so that a reader meets one
// word per scope wherever a scope is reported — and the cache vocabulary's
// agreement with this one is asserted beside that vocabulary rather than left to
// whoever edits one of the two next.
type Reach string

const (
	// ReachNone marks an operation that leaves nothing behind: a read, or a
	// render whose artifacts are the answer it hands back.
	ReachNone Reach = "none"

	// ReachWorkspace marks an operation that writes inside the workspace it was
	// given — resolving dependencies into the lockfile and the cache, generating
	// catalog entities, editing an authored document.
	ReachWorkspace Reach = "workspace"

	// ReachRegistry marks an operation that changes what a registry holds:
	// publishing, signing, or attaching a deprecation marker to what is already
	// published.
	ReachRegistry Reach = "registry"

	// ReachMachine marks an operation that changes what every workspace on the
	// machine shares — the caches, the machine-wide configuration file, the
	// credential store — or that takes a resource of the machine itself, such as
	// a listening address.
	ReachMachine Reach = "machine"
)

// reachClasses is the closed set, in the order a finding lists them: widening,
// so a reader meets them as a scale rather than as an alphabet.
var reachClasses = []Reach{ReachNone, ReachWorkspace, ReachRegistry, ReachMachine}

// Classes returns the closed set of reach classes, widening.
//
// It exists so a surface deciding what a caller may run enumerates the classes
// rather than restating them. A surface with its own list would be a second copy
// of the closed set, and the copy is what is missing a class the day one is
// added — at which point the new class would silently be admitted or refused by
// nothing having considered it.
//
// It returns a copy, so a caller walking the set cannot alter what the check
// above reads.
func Classes() []Reach {
	return append([]Reach(nil), reachClasses...)
}

// isReachClass reports whether r is one of the four.
func isReachClass(r Reach) bool {
	for _, c := range reachClasses {
		if r == c {
			return true
		}
	}
	return false
}

// reachClassList renders the closed set for a finding, so the reader of a
// failure is told what to write rather than sent looking for it.
func reachClassList() string {
	quoted := make([]string, 0, len(reachClasses))
	for _, c := range reachClasses {
		quoted = append(quoted, fmt.Sprintf("%q", string(c)))
	}
	return strings.Join(quoted, ", ")
}

// CheckReach reports every operation whose reach class is absent or is not one
// the closed set holds.
//
// Each finding names the operation, because the reader of a failure has to go
// and write one entry's class in a set of many: a finding reporting only that
// some entry was wrong would hand them the search instead of the repair.
//
// The findings follow the operation set's order, so a failing check reports the
// same text across runs.
func CheckReach() []string {
	return checkReach(operations)
}

// checkReach is the whole of CheckReach with the set supplied rather than read
// from the package.
//
// The split exists so each failure class can be driven against a set built for
// it. The real set declares a class for every operation — which is the property
// the exported form asserts — so a check that could only ever read that set
// would be one nobody had watched reject anything.
func checkReach(ops []Operation) []string {
	var findings []string
	for _, op := range ops {
		if isReachClass(op.Reach) {
			continue
		}
		if op.Reach == "" {
			findings = append(findings, fmt.Sprintf(
				"the %q operation declares no reach class (%s). Declare one of %s beside its identifier: "+
					"the class is what a surface reads, before running anything, to decide whether a caller "+
					"who does not own this machine may run it, and the empty value is not a class.",
				op.ID, op.Summary, reachClassList()))
			continue
		}
		findings = append(findings, fmt.Sprintf(
			"the %q operation declares a reach of %q, which is not a class. The classes are %s. Either the "+
				"value is misspelled, or it names a scope this vocabulary does not hold — in which case the "+
				"vocabulary is what to change, not this one entry.",
			op.ID, string(op.Reach), reachClassList()))
	}
	return findings
}
